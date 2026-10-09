package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig puts a config file in a fresh home directory and returns nothing; the caller
// reads it through the same HOME.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".config", "sh2pil", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeConfigOverlay puts one config.d overlay in the home the test is already using, so a
// case can prove that a later file wins over the main one.
func writeConfigOverlay(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".config", "sh2pil", "config.d", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadCloseOnNavigate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := readCloseOnNavigate(); got != defaultCloseOnNavigate {
		t.Fatalf("missing config = %t, want default %t", got, defaultCloseOnNavigate)
	}

	writeConfig(t, "")
	if got := readCloseOnNavigate(); got != defaultCloseOnNavigate {
		t.Fatalf("empty config = %t, want default %t", got, defaultCloseOnNavigate)
	}

	writeConfig(t, "close_on_navigate: false\n")
	if got := readCloseOnNavigate(); got {
		t.Fatal("close_on_navigate: false returned true")
	}

	writeConfig(t, "close_on_navigate: true # default\n")
	if got := readCloseOnNavigate(); !got {
		t.Fatal("close_on_navigate: true returned false")
	}
}

// TestStatePollSeconds pins the interval that decides what the state clock is worth: a reader
// who wants the old behaviour sets it to zero, and a value that is not a number of seconds keeps
// the default and says so rather than silently changing the cadence.
func TestStatePollSeconds(t *testing.T) {
	if got := loadConfig().StatePoll; got != defaultStatePoll {
		t.Fatalf("empty config = %g, want the default %g", got, defaultStatePoll)
	}

	writeConfig(t, "state_poll_seconds: 1.5\n")
	if got := loadConfig().StatePoll; got != 1.5 {
		t.Fatalf("state_poll_seconds: 1.5 = %g, want 1.5", got)
	}

	writeConfig(t, "state_poll_seconds: 0\n")
	if got := loadConfig().StatePoll; got != 0 {
		t.Fatalf("state_poll_seconds: 0 = %g, want the clock off", got)
	}

	for _, bad := range []string{"soon", "-1", ""} {
		writeConfig(t, "state_poll_seconds: "+bad+"\n")
		cfg := loadConfig()
		if cfg.StatePoll != defaultStatePoll {
			t.Fatalf("state_poll_seconds: %q = %g, want the default %g", bad, cfg.StatePoll,
				defaultStatePoll)
		}
		if bad == "" {
			continue // an empty value is not a setting at all, so there is nothing to warn about
		}
		if len(cfg.Warnings) == 0 {
			t.Fatalf("state_poll_seconds: %q kept the default without saying so", bad)
		}
	}
}

func TestReadZmxServers(t *testing.T) {
	writeConfig(t, "zmx_servers: build-host, gpu-host build-host # duplicates ignored\n")
	got := readZmxServers()
	if len(got) != 2 || got[0] != "build-host" || got[1] != "gpu-host" {
		t.Fatalf("zmx servers = %#v, want configured unique aliases", got)
	}
	writeConfig(t, "default_target: host-a\n")
	if got := readZmxServers(); len(got) != 0 {
		t.Fatalf("missing zmx_servers = %#v, want none", got)
	}
}

func TestRemotePollTickOnlyRunsWhenServersAreConfigured(t *testing.T) {
	writeConfig(t, "")
	if cmd := remotePollTick(); cmd != nil {
		t.Fatal("remote polling is enabled with no configured servers")
	}
	writeConfig(t, "zmx_servers: host-a\n")
	if cmd := remotePollTick(); cmd == nil {
		t.Fatal("remote polling is disabled with a configured server")
	}
}

// TestReadDefaultTarget pins which host the picker opens on.  Only a destination the config
// actually lists is honoured: a stale alias must not leave the picker on a list nothing can
// read, so anything else gives this machine.
func TestReadDefaultTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := readDefaultTarget(); got != "" {
		t.Fatalf("missing config = %q, want this machine", got)
	}

	writeConfig(t, "close_on_navigate: true\nzmx_servers: build-host, gpu-host\n")
	if got := readDefaultTarget(); got != "" {
		t.Fatalf("no default_target = %q, want this machine", got)
	}

	writeConfig(t, "zmx_servers: build-host, gpu-host\ndefault_target: gpu-host\n")
	if got := readDefaultTarget(); got != "gpu-host" {
		t.Fatalf("default_target: gpu-host = %q, want gpu-host", got)
	}

	writeConfig(t, "zmx_servers: build-host\ndefault_target: Build-Host # the builder\n")
	if got := readDefaultTarget(); got != "build-host" {
		t.Fatalf("default_target with a comment and capitals = %q, want build-host", got)
	}

	writeConfig(t, "zmx_servers: build-host\ndefault_target: local\n")
	if got := readDefaultTarget(); got != "" {
		t.Fatalf("default_target: local = %q, want this machine", got)
	}

	writeConfig(t, "zmx_servers: build-host\ndefault_target: nowhere\n")
	if got := readDefaultTarget(); got != "" {
		t.Fatalf("unknown default_target = %q, want this machine", got)
	}
}

// TestReadTargetsPutsThisMachineFirst pins the target bar order: this machine, then each
// configured host in the order the config lists them.
func TestReadTargetsPutsThisMachineFirst(t *testing.T) {
	writeConfig(t, "zmx_servers: build-host, gpu-host\n")
	targets := readTargets()
	if len(targets) != 3 {
		t.Fatalf("targets = %#v, want this machine and two hosts", targets)
	}
	if !targets[0].local() || targets[0].label() != "local" {
		t.Fatalf("first target = %#v, want this machine", targets[0])
	}
	if targets[1].label() != "build-host" || targets[2].label() != "gpu-host" {
		t.Fatalf("host targets = %#v, want the configured order", targets)
	}
	if got := targetIndex(targets, "gpu-host"); got != 2 {
		t.Fatalf("targetIndex(gpu-host) = %d, want 2", got)
	}
	if got := targetIndex(targets, ""); got != 0 {
		t.Fatalf("targetIndex(this machine) = %d, want 0", got)
	}
	if got := targetIndex(targets, "nowhere"); got != 0 {
		t.Fatalf("targetIndex(unknown) = %d, want 0", got)
	}
}

// TestTargetTableIsStaticallyConfigured is the old guards' subject: a host is a line in the
// config, and nothing about the picker's own state can add or reorder one.
func TestTargetTableIsStaticallyConfigured(t *testing.T) {
	writeConfig(t, "")
	targets := readTargets()
	if len(targets) != 1 || !targets[0].local() {
		t.Fatalf("targets without zmx_servers = %#v, want this machine alone", targets)
	}
}
