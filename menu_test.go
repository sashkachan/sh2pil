package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestToolChoicesForEachRow pins the next-step list per row kind.
func TestTravelChoicesForEachRow(t *testing.T) {
	cases := []struct {
		name string
		row  session
		want []string
	}{
		{"project", session{ProjectOnly: true},
			[]string{"new", "project-editor", "shell", "lazygit", "yazi"}},
		{"session", session{},
			[]string{"resume", "editor", "shell", "lazygit", "yazi"}},
		{"zmx", session{ZmxOnly: true},
			[]string{"zmx", "shell", "lazygit", "yazi", "new"}},
	}
	for _, c := range cases {
		if got := travelChoicesFor(c.row); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s choices = %v, want %v", c.name, got, c.want)
		}
	}
	withChat := travelChoicesFor(session{ZmxOnly: true, File: "/tmp/x"})
	if len(withChat) == 0 || withChat[len(withChat)-1] != "editor" {
		t.Errorf("a zmx row with a transcript should offer the editor: %v", withChat)
	}
	// Fork is a command and not travel: it belongs to the palette, so the travel menu for a
	// session must not offer it.
	for _, choice := range travelChoicesFor(session{}) {
		if choice == "fork" {
			t.Error("fork belongs to the command palette, not the travel menu")
		}
	}
}

// TestPromptPrimaryFollowsModeAndTrigger pins what the primary key does.
func TestPromptPrimaryFollowsModeAndTrigger(t *testing.T) {
	if (&model{}).promptPrimary() {
		t.Error("the default mode must run the action directly")
	}
	if !(&model{mode: "prompt"}).promptPrimary() {
		t.Error("mode prompt must open the menu")
	}
	if !(&model{triggers: map[string]string{"list.resume": "prompt"}}).promptPrimary() {
		t.Error("a prompt trigger on the primary action must open the menu")
	}
	if (&model{mode: "prompt", triggers: map[string]string{"list.resume": "key"}}).promptPrimary() {
		t.Error("a key trigger must override mode prompt")
	}
}

// TestChooseToolBeginsTheNamedAction pins that a menu row runs through beginAction and closes
// the menu.
func TestChooseToolBeginsTheNamedAction(t *testing.T) {
	m := &model{toolChoices: []string{"shell", "yazi"}, toolPos: 1,
		askSession: session{Project: "repo", CWD: "/tmp/repo", Alive: true}}
	_, cmd := m.chooseTool(0)
	if cmd == nil {
		t.Fatal("choosing shell returned no command")
	}
	if m.modal != "" || m.toolChoices != nil {
		t.Fatalf("the menu did not close: modal=%q choices=%v", m.modal, m.toolChoices)
	}
}

// TestModeAndTriggerConfig pins the mode and trigger settings, their validation, and defaults.
func TestModeAndTriggerConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "mode: prompt\ntrigger.list.fork: prompt")
	cfg := loadConfig()
	if cfg.Mode != "prompt" || cfg.Triggers["list.fork"] != "prompt" {
		t.Fatalf("mode=%q triggers=%v, want prompt and list.fork", cfg.Mode, cfg.Triggers)
	}
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "mode: sideways")
	if cfg := loadConfig(); cfg.Mode != defaultMode {
		t.Fatalf("a bad mode = %q, want the default %q", cfg.Mode, defaultMode)
	}
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "trigger.list.nope: prompt")
	if cfg := loadConfig(); len(cfg.KeyErrors) == 0 {
		t.Fatal("an unknown trigger action must be an error")
	}
}

// TestWindowMenuRowIsAProjectOnTheWindowHost pins the row the cmd+. dialog acts on: the
// directory the window is in, on the host the window runs on, as a project row.
func TestWindowMenuRowIsAProjectOnTheWindowHost(t *testing.T) {
	row := windowMenuRow("/Users/me/work/buildbox", "")
	if row.Project != "buildbox" || row.CWD != "/Users/me/work/buildbox" || row.Server != "" {
		t.Fatalf("local row = %+v", row)
	}
	if !row.ProjectOnly || !row.Alive {
		t.Fatalf("the row must be a live project: %+v", row)
	}
	want := []string{"new", "project-editor", "shell", "lazygit", "yazi"}
	if choices := travelChoicesFor(row); !reflect.DeepEqual(choices, want) {
		t.Fatalf("the window menu offers %v, want %v", choices, want)
	}
	remote := windowMenuRow("/home/me/buildbox", "user@192.0.2.15")
	if remote.Server != "user@192.0.2.15" || remote.Project != "buildbox" {
		t.Fatalf("remote row = %+v", remote)
	}
	if root := windowMenuRow("/", ""); root.Project != "/" {
		t.Fatalf("a root project = %q, want /", root.Project)
	}
}

// TestMenuHarnessesNarrowsToWhatTheTargetHas pins the intersection, and the two cases that
// leave the setting in force: a target that named no store, and a setting none of whose
// stores the target has.
func TestMenuHarnessesNarrowsToWhatTheTargetHas(t *testing.T) {
	configured := []string{"pi", "opencode", "claude", "codex"}
	if got := menuHarnesses(configured, "pi,codex"); !reflect.DeepEqual(got, []string{"pi", "codex"}) {
		t.Fatalf("intersection = %v, want the two named stores", got)
	}
	if got := menuHarnesses(configured, "pi, codex "); !reflect.DeepEqual(got, []string{"pi", "codex"}) {
		t.Fatalf("spaces must not matter: %v", got)
	}
	if got := menuHarnesses(configured, ""); !reflect.DeepEqual(got, configured) {
		t.Fatalf("a target that named no store must leave the setting in force: %v", got)
	}
	if got := menuHarnesses(configured, "opencode"); !reflect.DeepEqual(got, []string{"opencode"}) {
		t.Fatalf("a setting of one store narrows to it: %v", got)
	}
	one := []string{"pi"}
	if got := menuHarnesses(one, "codex"); !reflect.DeepEqual(got, one) {
		t.Fatalf("an empty intersection must leave the setting in force: %v", got)
	}
}

// TestNewChatFromAWindowAsksForTheStore pins that the cmd+. dialog asks which store before it
// asks where the terminal goes, and that the answer travels as the store a new chat uses.
func TestNewChatFromAWindowAsksForTheStore(t *testing.T) {
	m := &model{windowMenu: true, alwaysAsk: true, layout: "tab", modal: "tools",
		harnesses: []string{"pi", "claude"}, toolChoices: []string{"new", "shell"}, toolPos: 0,
		askSession: windowMenuRow("/tmp/repo", "")}
	if _, cmd := m.chooseTool(0); cmd != nil {
		t.Fatal("the store question must not run anything by itself")
	}
	if m.modal != "agents" || !reflect.DeepEqual(m.agentChoices, []string{"pi", "claude"}) {
		t.Fatalf("modal=%q agents=%v, want the store question", m.modal, m.agentChoices)
	}
	if _, cmd := m.chooseAgent(1); cmd != nil {
		t.Fatal("the placement question must not run anything by itself")
	}
	if m.harness != "claude" {
		t.Fatalf("harness = %q, want claude", m.harness)
	}
	if m.modal != "layout" || m.askAction != "new" {
		t.Fatalf("modal=%q action=%q, want the placement question for a new chat",
			m.modal, m.askAction)
	}
}

// TestANewChatOnAHostReusesTheZmxRule pins that a new Pi chat from a window on a host runs in
// a zmx session there, which is the rule the picker already applies to a remote project row.
func TestANewChatOnAHostReusesTheZmxRule(t *testing.T) {
	m := &model{windowMenu: true, alwaysAsk: true, layout: "tab", modal: "agents",
		harnesses: []string{"pi"}, agentChoices: []string{"pi"},
		askSession: windowMenuRow("/home/me/repo", "host")}
	m.chooseAgent(0)
	if m.askAction != "zmx-new" {
		t.Fatalf("action = %q, want zmx-new", m.askAction)
	}
}

// TestTheWindowDialogAlwaysAsksWhereTheTerminalGoes pins that the cmd+. dialog asks for the
// placement whatever the picker remembered, while an ordinary row keeps the remembered one.
func TestTheWindowDialogAlwaysAsksWhereTheTerminalGoes(t *testing.T) {
	dialog := &model{alwaysAsk: true, windowMenu: true, layout: "tab"}
	dialog.beginAction("shell", windowMenuRow("/tmp/repo", ""), false)
	if dialog.modal != "layout" || dialog.askAction != "shell" {
		t.Fatalf("modal=%q action=%q, want the placement question", dialog.modal, dialog.askAction)
	}
	plain := &model{layout: "tab"}
	plain.beginAction("shell", windowMenuRow("/tmp/repo", ""), false)
	if plain.modal != "" {
		t.Fatalf("a remembered placement must not ask: modal=%q", plain.modal)
	}
}

// TestWindowMenuOpensTheMenuAtOnce pins the startup the cmd+. dialog relies on: no read of any
// list, no poll timer, and the menu already open for the window sh2pil-open resolved.
func TestWindowMenuOpensTheMenuAtOnce(t *testing.T) {
	m := &model{windowMenu: true, out: io.Discard, modal: "",
		windowRow: windowMenuRow("/tmp/repo", ""),
		live:      map[string]liveInfo{}, cache: map[string]preview{},
		unread: map[string]bool{}, seen: map[string]string{}}
	if cmd := m.Init(); cmd != nil {
		t.Fatal("the dialog must not start a read or a timer")
	}
	if m.modal != "tools" || m.askSession.Project != "repo" {
		t.Fatalf("modal=%q row=%+v, want the menu open on the window's project", m.modal, m.askSession)
	}
	if len(m.toolChoices) == 0 || m.toolChoices[0] != "new" {
		t.Fatalf("choices = %v, want the project menu", m.toolChoices)
	}
}

// TestTheWindowDialogDrawsTheMenuAndItsTarget pins what the cmd+. key puts on screen: the menu
// for the window's project, the host it applies to, and no list behind it.
func TestTheWindowDialogDrawsTheMenuAndItsTarget(t *testing.T) {
	m := &model{windowMenu: true, width: 80, height: 24, modal: "tools",
		windowRow:   windowMenuRow("/tmp/repo", "host"),
		toolChoices: []string{"new", "project-editor", "shell", "lazygit", "yazi"},
		editor:      "nvim", gitTool: "lazygit", fileBrowser: "yazi"}
	view := m.View()
	for _, want := range []string{"travel", "repo on host", "start a new chat",
		"open the git tool", "open the file browser"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the dialog does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "no sessions") {
		t.Fatalf("the dialog must draw no list behind it:\n%s", view)
	}
}

// TestTheWindowDialogAsksForTheStoreByName pins the store menu a new chat opens.
func TestTheWindowDialogAsksForTheStoreByName(t *testing.T) {
	m := &model{windowMenu: true, width: 80, height: 24, modal: "agents",
		windowRow: windowMenuRow("/tmp/repo", ""), agentChoices: []string{"pi", "claude"}}
	view := m.View()
	for _, want := range []string{"start a new chat with", "Pi", "Claude Code", "esc goes back"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the store menu does not show %q:\n%s", want, view)
		}
	}
}

// TestTheWindowDialogReportsWhatHappened pins the one line the dialog has left once it has
// answered: the action's outcome, and the key that closes it.
func TestTheWindowDialogReportsWhatHappened(t *testing.T) {
	m := &model{windowMenu: true, width: 80, height: 24, status: "opened a shell in repo",
		windowRow: windowMenuRow("/tmp/repo", "")}
	view := m.View()
	if !strings.Contains(view, "opened a shell in repo") || !strings.Contains(view, "any key closes") {
		t.Fatalf("the dialog does not report the action:\n%s", view)
	}
}

// TestEscapeOnAQuestionGoesBackToTheMenu pins the way out of the questions the cmd+. dialog
// asks: escape returns to the menu, and escape on the menu itself closes the dialog.
func TestEscapeOnAQuestionGoesBackToTheMenu(t *testing.T) {
	m := &model{windowMenu: true, alwaysAsk: true, layout: "tab", modal: "tools",
		harnesses: []string{"pi"}, toolChoices: []string{"new", "shell"},
		askSession: windowMenuRow("/tmp/repo", "")}
	m.chooseTool(0)
	if m.modal != "agents" {
		t.Fatalf("modal=%q, want the store question", m.modal)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal != "tools" {
		t.Fatalf("escape on the store question left modal=%q, want the menu", m.modal)
	}
	m.chooseTool(0)
	m.chooseAgent(0)
	if m.modal != "layout" {
		t.Fatalf("modal=%q, want the placement question", m.modal)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal != "tools" {
		t.Fatalf("escape on the placement question left modal=%q, want the menu", m.modal)
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Fatal("escape on the menu must close the dialog")
	}
}
