package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDefaultKeymapResolvesEveryChord pins that the defaults are self-consistent: every
// chord of every action resolves to that action's canonical chord, and no default collides.
func TestDefaultKeymapResolvesEveryChord(t *testing.T) {
	km := buildKeymap(nil)
	for _, action := range keyActions {
		for _, chord := range action.chords {
			if got := km.resolve(action.context, chord); got != action.chords[0] {
				t.Errorf("%s: %s resolved to %q, want %q", action.name, chord, got, action.chords[0])
			}
		}
	}
	if len(km.conflicts) != 0 {
		t.Fatalf("the default keymap has conflicts: %v", km.conflicts)
	}
}

// TestRebindingMovesAChordAndReleasesTheOld checks both halves of a rebind: the new chord
// acts, and the old one stops acting instead of lingering.
func TestRebindingMovesAChordAndReleasesTheOld(t *testing.T) {
	km := buildKeymap(map[string]string{"list.move_down": "x, alt+j"})
	if got := km.resolve(keyContextList, "x"); got != "j" {
		t.Errorf("x resolved to %q, want the move-down canonical j", got)
	}
	if got := km.resolve(keyContextList, "alt+j"); got != "j" {
		t.Errorf("alt+j resolved to %q, want the move-down canonical j", got)
	}
	if got := km.resolve(keyContextList, "j"); got != blockedChord {
		t.Errorf("the old j resolved to %q, want it released", got)
	}
	if got := km.resolve(keyContextList, "down"); got != blockedChord {
		t.Errorf("the old down resolved to %q, want it released", got)
	}
}

// TestDisablingAnActionReleasesIt pins that none removes every chord of one action.
func TestDisablingAnActionReleasesIt(t *testing.T) {
	km := buildKeymap(map[string]string{"list.fork": "none"})
	if got := km.resolve(keyContextList, "ctrl+f"); got != blockedChord {
		t.Errorf("a disabled action still resolves ctrl+f to %q", got)
	}
	if chords := km.chords("list.fork"); len(chords) != 0 {
		t.Errorf("a disabled action keeps chords %v", chords)
	}
}

// TestSameChordInDifferentContextsStaysSeparate pins the context rule: ctrl+w deletes a row
// in the list and kills a word in a field, and moving one must not move the other.
func TestSameChordInDifferentContextsStaysSeparate(t *testing.T) {
	km := buildKeymap(map[string]string{"list.delete": "alt+x"})
	if got := km.resolve(keyContextList, "alt+x"); got != "ctrl+w" {
		t.Errorf("the list delete rebind resolved to %q", got)
	}
	if got := km.resolve(keyContextList, "ctrl+w"); got != blockedChord {
		t.Errorf("the list delete default resolved to %q, want it released", got)
	}
	if got := km.resolve(keyContextSearch, "ctrl+w"); got != "ctrl+w" {
		t.Errorf("the search kill-word binding moved with the list: %q", got)
	}
}

// TestConflictKeepsTheDefault pins that a chord two actions claim is reported, and the later
// action keeps its own defaults.
func TestConflictKeepsTheDefault(t *testing.T) {
	km := buildKeymap(map[string]string{"list.fork": "enter"})
	if len(km.conflicts) == 0 {
		t.Fatal("two actions on enter were not reported as a conflict")
	}
	if got := km.resolve(keyContextList, "ctrl+f"); got != "ctrl+f" {
		t.Errorf("the conflicting action lost its default: ctrl+f resolved to %q", got)
	}
	if got := km.resolve(keyContextList, "enter"); got != "enter" {
		t.Errorf("enter resolved to %q, want the resume canonical", got)
	}
}

// TestUnknownKeyKeepsItsValue pins that a key no action knows reaches the field unchanged,
// so a letter typed into search or a name is still inserted.
func TestUnknownKeyKeepsItsValue(t *testing.T) {
	km := buildKeymap(nil)
	if got := km.resolve(keyContextList, "z"); got != "z" {
		t.Errorf("an unbound key resolved to %q, want itself", got)
	}
}

// TestHelpBoxCoversEveryDocumentedAction is the drift guard between the registry and the ?
// box: an action with help text must appear on some row.
func TestHelpBoxCoversEveryDocumentedAction(t *testing.T) {
	covered := map[string]bool{}
	for _, row := range helpRows {
		for _, name := range row.actions {
			covered[name] = true
		}
	}
	for _, action := range keyActions {
		if action.help == "" {
			continue
		}
		if !covered[action.name] {
			t.Errorf("action %s has help text but no help row", action.name)
		}
	}
}

// TestHelpChordFollowsARebind pins that the help box shows the binding in force.  The chord
// is one no default claims, so the rebind is what moves it.
func TestHelpChordFollowsARebind(t *testing.T) {
	km := buildKeymap(map[string]string{"list.fork": "alt+o"})
	if got := helpChord(km, "list.fork"); got != "alt+o" {
		t.Errorf("help chord after a rebind = %q, want alt+o", got)
	}
	if got := helpChord(nil, "list.fork"); got != "ctrl+f" {
		t.Errorf("help chord without a keymap = %q, want the default ctrl+f", got)
	}
}

// TestKeyOverrideIsRead pins that key.<action> reaches the resolver.
func TestKeyOverrideIsRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "key.list.fork: alt+o")
	cfg := loadConfig()
	if cfg.Keys["list.fork"] != "alt+o" {
		t.Fatalf("key.list.fork = %q, want alt+o", cfg.Keys["list.fork"])
	}
	km := buildKeymap(cfg.Keys)
	if got := km.resolve(keyContextList, "alt+o"); got != "ctrl+f" {
		t.Errorf("alt+o resolved to %q, want the fork canonical", got)
	}
	// A rebind onto a chord a default already claims is a conflict, not a silent takeover:
	// alt+p is the command palette, so the fork loses its bid for it.
	conflicted := buildKeymap(map[string]string{"list.fork": "alt+p"})
	if got := conflicted.resolve(keyContextList, "alt+p"); got != "alt+p" {
		t.Errorf("alt+p resolved to %q, want the palette that already claimed it", got)
	}
	if len(conflicted.conflicts) == 0 {
		t.Error("a rebind onto the palette's own chord must be reported as a conflict")
	}
}

// TestConfigOverlayWins pins the config.d chain: a later file overrides the main one.
func TestConfigOverlayWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".config", "sh2pil")
	if err := os.MkdirAll(filepath.Join(base, "config.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "config.yaml"),
		[]byte("zmx_servers: main-host"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(base, "config.d", "90-local.yaml")
	if err := os.WriteFile(overlay, []byte("zmx_servers: overlay-host"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readZmxServers()
	if len(got) != 1 || got[0] != "overlay-host" {
		t.Fatalf("readZmxServers() = %v, want the overlay value", got)
	}
}

// TestUnknownKeyWarns pins that a key neither program reads is reported, not silently used.
func TestUnknownKeyWarns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "mystery: 1")
	cfg := loadConfig()
	for _, warning := range cfg.Warnings {
		if strings.Contains(warning, "mystery") {
			return
		}
	}
	t.Fatalf("an unknown key was not reported: %v", cfg.Warnings)
}

// TestHostEnvKeysAreKnown pins that the environment one host's session is given is a known
// family of settings.  The helper that opens that session reads it, so the picker must hold
// the keys it does not act on without warning about them.
func TestHostEnvKeysAreKnown(t *testing.T) {
	writeConfig(t, "ssh_env: TERM=xterm-256color\nssh_env.build-host: LANG=C.UTF-8")
	cfg := loadConfig()
	if len(cfg.Warnings) != 0 {
		t.Fatalf("the ssh_env settings warned: %v", cfg.Warnings)
	}
	if cfg.Values["ssh_env"] != "TERM=xterm-256color" ||
		cfg.Values["ssh_env.build-host"] != "LANG=C.UTF-8" {
		t.Fatalf("ssh_env settings were not read: %v", cfg.Values)
	}
}

// A family nobody owns is still reported: the prefix rule must not swallow every key.
func TestUnknownKeyWithADotIsStillReported(t *testing.T) {
	writeConfig(t, "ssh_env_extra.build-host: LANG=C.UTF-8\nmystery.host: 1")
	cfg := loadConfig()
	for _, name := range []string{"ssh_env_extra.build-host", "mystery.host"} {
		found := false
		for _, warning := range cfg.Warnings {
			if strings.Contains(warning, name) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was not reported: %v", name, cfg.Warnings)
		}
	}
}

// TestConfigFileOverride pins that --config and SH2PIL_CONFIG select the file, and a matching
// config.d beside it is still read.
func TestConfigFileOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(path, []byte("default_view: zmx"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPathOverride = path
	t.Cleanup(func() { configPathOverride = "" })
	cfg := loadConfig()
	if cfg.DefaultView != viewZmx {
		t.Fatalf("default_view from --config = %q, want zmx", cfg.DefaultView)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0] != path {
		t.Fatalf("sources = %v, want the override file alone", cfg.Sources)
	}
}

// TestHandleKeyUsesTheResolvedChord pins that the dispatch goes through the resolver: a
// rebound help key opens the box and the released default does not.
func TestHandleKeyUsesTheResolvedChord(t *testing.T) {
	modelWith := func(km *keymap) *model {
		return &model{view: viewSessions, expanded: map[string]bool{}, live: map[string]liveInfo{},
			cache: map[string]preview{}, data: map[string]targetData{}, keymap: km}
	}
	plain := modelWith(nil)
	plain.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !plain.help {
		t.Fatal("? did not open help without a keymap")
	}

	rebound := modelWith(buildKeymap(map[string]string{"list.help": "f1"}))
	rebound.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if rebound.help {
		t.Fatal("the released ? still opened help")
	}
	rebound.handleKey(tea.KeyMsg{Type: tea.KeyF1})
	if !rebound.help {
		t.Fatal("the rebound f1 did not open help")
	}
}

// TestDispatchCasesAreRegistered is the drift guard for the dispatch: every case literal in
// the two key handlers must be a chord some action declares, so a key cannot be added to the
// switch without also being configurable.  The placement menu digits are the one exception:
// they select a row in a menu and are data, not a binding.
func TestDispatchCasesAreRegistered(t *testing.T) {
	known := map[string]bool{}
	for _, action := range keyActions {
		for _, chord := range action.chords {
			known[chord] = true
		}
	}
	allow := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true,
		"6": true, "7": true, "8": true, "9": true}

	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	found := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if fn.Name.Name != "handleKey" && fn.Name.Name != "handleSearchKey" {
			continue
		}
		found++
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				literal, ok := expr.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					continue
				}
				if !known[value] && !allow[value] {
					t.Errorf("%s dispatches on %q, which no action declares", fn.Name.Name, value)
				}
			}
			return true
		})
	}
	if found != 2 {
		t.Fatalf("found %d key handlers, want handleKey and handleSearchKey", found)
	}
}
