package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestRunToolArgsUsesTheConfiguredTool pins that a configured directory tool reaches the
// helper as a command with its own arguments.
func TestRunToolArgsUsesTheConfiguredTool(t *testing.T) {
	m := &model{}
	args, note := m.runToolArgs("jj --no-pager", defaultGitTool, "/tmp/repo", "tab")
	want := []string{"run-tool", "--label", "jj repo", "--place", "tab", "/tmp/repo",
		"jj", "--no-pager"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("runToolArgs = %#v, want %#v", args, want)
	}
	if note != "opened jj in repo" {
		t.Fatalf("note = %q, want opened jj in repo", note)
	}
}

// TestRunToolArgsFallsBackToTheDefault pins that a model without a configured tool still
// produces the default command, which is what a hand-built test model and a first run need.
func TestRunToolArgsFallsBackToTheDefault(t *testing.T) {
	m := &model{}
	args, _ := m.runToolArgs("", defaultFileBrowser, "/tmp/repo", "tab")
	if len(args) == 0 || args[len(args)-1] != "yazi" {
		t.Fatalf("runToolArgs fallback = %#v, want the default yazi last", args)
	}
}

// TestToolConfigKeys pins the tool settings and their defaults.
func TestToolConfigKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "file_browser: ranger\ngit_tool: jj")
	cfg := loadConfig()
	if cfg.FileBrowser != "ranger" || cfg.GitTool != "jj" {
		t.Fatalf("tools = %q and %q, want ranger and jj", cfg.FileBrowser, cfg.GitTool)
	}
	if cfg.Editor != "" {
		t.Fatalf("editor = %q, want the empty default", cfg.Editor)
	}
	t.Setenv("HOME", t.TempDir())
	cfg = loadConfig()
	if cfg.FileBrowser != defaultFileBrowser || cfg.GitTool != defaultGitTool {
		t.Fatalf("default tools = %q and %q", cfg.FileBrowser, cfg.GitTool)
	}
}

// TestNestedToolConfig pins the nested tools section: every global key, the per-host map,
// and the dotted names inspection sees.
func TestNestedToolConfig(t *testing.T) {
	writeConfig(t, `
tools:
  editor: code
  file_browser: ranger
  git_tool: jj
  shell: zsh
  hosts:
    build-host:
      editor: vim
      file_browser: lf
      git_tool: tig
    gpu-host:
      file_browser: mc
`)
	cfg := loadConfig()
	if cfg.Editor != "code" || cfg.FileBrowser != "ranger" || cfg.GitTool != "jj" ||
		cfg.Shell != "zsh" {
		t.Fatalf("nested tools = %q, %q, %q, %q", cfg.Editor, cfg.FileBrowser, cfg.GitTool,
			cfg.Shell)
	}
	build := cfg.ToolHosts["build-host"]
	if build["editor"] != "vim" || build["file_browser"] != "lf" || build["git_tool"] != "tig" {
		t.Fatalf("build-host tools = %#v", build)
	}
	if got := cfg.ToolHosts["gpu-host"]["file_browser"]; got != "mc" {
		t.Fatalf("gpu-host file_browser = %q, want mc", got)
	}
	for name, want := range map[string]string{
		"tools.file_browser":                  "ranger",
		"tools.hosts.build-host.file_browser": "lf",
		"tools.hosts.gpu-host.file_browser":   "mc",
	} {
		if got := cfg.Values[name]; got != want {
			t.Errorf("Values[%q] = %q, want %q", name, got, want)
		}
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", cfg.Warnings)
	}
}

// TestToolFormsLastValueWins pins the merge rule: a flat key and a nested key are one
// setting, and the last value in the chain wins whichever form carries it.
func TestToolFormsLastValueWins(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"nested after flat", "file_browser: ranger\ntools:\n  file_browser: yazi\n", "yazi"},
		{"flat after nested", "tools:\n  file_browser: ranger\nfile_browser: yazi\n", "yazi"},
	} {
		writeConfig(t, test.body)
		if got := loadConfig().FileBrowser; got != test.want {
			t.Errorf("%s: file_browser = %q, want %q", test.name, got, test.want)
		}
	}
	writeConfig(t, "tools:\n  file_browser: ranger\n")
	writeConfigOverlayFile(t, "90-local.yaml", "file_browser: yazi\n")
	if got := loadConfig().FileBrowser; got != "yazi" {
		t.Fatalf("overlay flat key = %q, want yazi", got)
	}
}

// TestToolsAfterHostsKeepTheirLevel pins the YAML shape: a shallower line closes the host
// section, so a global tool written after hosts is still a global tool.
func TestToolsAfterHostsKeepTheirLevel(t *testing.T) {
	writeConfig(t, "tools:\n  hosts:\n    build-host:\n      file_browser: lf\n  git_tool: jj\n")
	cfg := loadConfig()
	if cfg.GitTool != "jj" {
		t.Fatalf("git_tool after hosts = %q, want jj", cfg.GitTool)
	}
	if cfg.FileBrowser != defaultFileBrowser {
		t.Fatalf("file_browser = %q, want the default", cfg.FileBrowser)
	}
	if got := cfg.ToolHosts["build-host"]["file_browser"]; got != "lf" {
		t.Fatalf("build-host file_browser = %q, want lf", got)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", cfg.Warnings)
	}
}

// TestToolConfigWarnings pins that a typo and a bad indent are reported and dropped
// instead of silently changing a tool.
func TestToolConfigWarnings(t *testing.T) {
	writeConfig(t, "tools:\n  file_browsr: lf\n")
	cfg := loadConfig()
	if cfg.FileBrowser != defaultFileBrowser {
		t.Fatalf("file_browser = %q, want the default", cfg.FileBrowser)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "tools.file_browsr") {
		t.Fatalf("warnings = %#v, want one about tools.file_browsr", cfg.Warnings)
	}

	writeConfig(t, "tools:\n   file_browser: yazi\n")
	cfg = loadConfig()
	if cfg.FileBrowser != defaultFileBrowser || len(cfg.Warnings) == 0 {
		t.Fatalf("odd indent: file_browser = %q, warnings = %#v", cfg.FileBrowser, cfg.Warnings)
	}
}
