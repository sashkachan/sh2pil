package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestLayoutAskPreference(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := readLayout(); got != "current" {
		t.Fatalf("readLayout() = %q, want the picker's own pane by default", got)
	}
	// The pane the picker runs in is a placement like any other: ctrl+l reaches it and the
	// choice is remembered, or the default could never be kept.
	writeLayout("current")
	if got := readLayout(); got != "current" {
		t.Fatalf("readLayout() = %q, want the remembered placement", got)
	}
	writeLayout("ask")
	if got := readLayout(); got != "ask" {
		t.Fatalf("readLayout() = %q, want ask", got)
	}
	if got := cycleLayout("pane"); got != "current" {
		t.Fatalf("cycleLayout(pane) = %q, want current", got)
	}
	if got := cycleLayout("current"); got != "ask" {
		t.Fatalf("cycleLayout(current) = %q, want ask", got)
	}

	m := model{layout: "ask", width: 80}
	_, cmd := m.beginAction("new", session{ID: "session", CWD: "/tmp", Alive: true}, false)
	if cmd != nil || m.modal != "layout" || m.layoutPos != placementIndex(defaultLayout) {
		t.Fatalf("ask modal = %q, pos %d, cmd %v; want layout modal with the fallback placement selected", m.modal, m.layoutPos, cmd)
	}
	if got := placements[m.layoutPos]; got != "current" {
		t.Fatalf("ask modal opens on %q, want the picker's own pane", got)
	}
	if menu := m.modalBox(); !strings.Contains(menu, "new pane (same window)") ||
		!strings.Contains(menu, "same window / same pane") {
		t.Fatalf("ask menu = %q, want separate new-pane and current-pane options", menu)
	}
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if cmd == nil || m.modal != "" || m.layout != "ask" {
		t.Fatalf("digit choice did not open and preserve ask mode: modal=%q layout=%q", m.modal, m.layout)
	}
	m = model{layout: "ask"}
	_, _ = m.beginAction("new", session{ID: "session", CWD: "/tmp", Alive: true}, false)
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if cmd == nil || m.modal != "" || m.layout != "ask" {
		t.Fatalf("fourth placement choice failed: modal=%q layout=%q cmd=%v", m.modal, m.layout, cmd)
	}
	current := model{layout: "current"}
	currentArgs, _ := current.openArgs("new", session{ID: "session", CWD: "/tmp", Alive: true}, false)
	if got := currentArgs[len(currentArgs)-1]; got != "current" {
		t.Fatalf("same-pane placement = %q, want current", got)
	}

	for _, action := range []string{"resume", "new", "fork"} {
		m := model{layout: "ask"}
		_, cmd := m.beginAction(action, session{ID: "session", CWD: "/tmp", Alive: true}, false)
		if cmd != nil || m.modal != "layout" {
			t.Errorf("%s action: modal=%q, cmd %v; want placement question", action, m.modal, cmd)
		}
	}

	// A live session that a terminal already shows is navigated in place instead of launched,
	// so its tab, pane, or window is preserved and there is nothing to ask.  The placement is
	// the fallback one, because a switch is not a launch.
	shown := liveInfo{ID: "session", Owner: "kitty window 12 (zmx pi-abc)", Certain: true}
	m = model{layout: "ask", live: map[string]liveInfo{"session": shown}}
	active := session{ID: "session", CWD: "/tmp", Alive: true}
	_, cmd = m.beginAction("resume", active, false)
	if cmd == nil || m.modal != "" {
		t.Fatalf("shown session navigation: modal=%q, cmd %v; want direct open without question", m.modal, cmd)
	}
	args, _ := m.openArgs("resume", active, false)
	want := []string{"switch", "/tmp", "session", "--label", "pi session", "--place", "current"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("active session args = %#v, want %#v", args, want)
	}

	// A live session that nothing shows needs a terminal, so the placement question is the
	// reader's only say in where it goes: a detached zmx chat is the usual case, and a live
	// session on an unreachable host is the other.
	m = model{layout: "ask", live: map[string]liveInfo{"session": {}}}
	_, cmd = m.beginAction("resume", active, false)
	if cmd != nil || m.modal != "layout" {
		t.Fatalf("unshown session navigation: modal=%q, cmd %v; want placement question", m.modal, cmd)
	}
	_, cmd = m.chooseLayout(1) // its own OS window
	if cmd == nil || m.modal != "" {
		t.Fatalf("chosen placement: modal=%q, cmd %v; want the launch", m.modal, cmd)
	}

	// A shell, lazygit, or yazi opens a new terminal whenever it runs, so on a session row
	// the placement question is the reader's only say in where it goes, exactly as on a
	// project row.
	for _, action := range []string{"shell", "lazygit", "yazi"} {
		m := model{layout: "ask"}
		_, cmd := m.beginAction(action, active, false)
		if cmd != nil || m.modal != "layout" {
			t.Errorf("%s action: modal=%q, cmd %v; want placement question", action, m.modal, cmd)
		}
		if _, chosen := m.chooseLayout(2); chosen == nil || m.modal != "" { // a new pane
			t.Errorf("%s chosen placement: modal=%q, cmd %v; want the launch", action, m.modal, chosen)
		}
		args, _ := m.openArgs(action, active, false)
		if got := placementArg(args); got != "current" {
			t.Errorf("%s placement without ask = %q, want the fallback placement", action, got)
		}
	}

	// The zmx list asks too: a client that was closed leaves nothing to open beside, so a
	// tool taken from that row lands in a terminal the reader chooses.
	m = model{layout: "ask", view: viewZmx, zmxRows: []session{zmxRow()}}
	_, cmd = m.startAction("alt+f")
	if cmd != nil || m.modal != "layout" {
		t.Fatalf("zmx alt+f: modal=%q, cmd %v; want placement question", m.modal, cmd)
	}

	// ctrl+t asks for nothing: it exists to add a window of its own.
	for _, action := range []string{"window"} {
		m := model{layout: "ask"}
		_, cmd := m.beginAction(action, active, false)
		if cmd == nil || m.modal != "" {
			t.Errorf("%s action: modal=%q, cmd %v; want no placement question", action, m.modal, cmd)
		}
		if action == "window" {
			args, _ := m.openArgs(action, active, false)
			if got := args[len(args)-1]; got != "window" {
				t.Errorf("%s placement = %q, want window", action, got)
			}
		}
	}
}

func TestHarnessPreference(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := readHarness(); got != "pi" {
		t.Fatalf("readHarness() = %q, want pi by default", got)
	}
	writeHarness("opencode")
	if got := readHarness(); got != "opencode" {
		t.Fatalf("readHarness() = %q, want opencode", got)
	}
	if got := cycleHarness("opencode", knownHarnesses); got != "claude" {
		t.Fatalf("cycleHarness(opencode) = %q, want claude", got)
	}
	if got := cycleHarness("codex", knownHarnesses); got != "pi" {
		t.Fatalf("cycleHarness(codex) = %q, want the cycle to wrap to pi", got)
	}
	// A target that does not run a store does not offer it, and a store in force that this target
	// does not have gives way to the first one it does.
	if got := cycleHarness("pi", []string{"pi", "claude"}); got != "claude" {
		t.Fatalf("cycleHarness(pi) over pi+claude = %q, want claude", got)
	}
	if got := cycleHarness("opencode", []string{"pi", "claude"}); got != "pi" {
		t.Fatalf("cycleHarness(opencode) over pi+claude = %q, want pi", got)
	}
	if got := cycleHarness("claude", []string{"pi", "claude"}); got != "pi" {
		t.Fatalf("cycleHarness(claude) over pi+claude = %q, want the cycle to wrap to pi", got)
	}
	if got := knownHarness("unknown"); got != "pi" {
		t.Fatalf("knownHarness(unknown) = %q, want pi", got)
	}
	m := model{harness: "pi"}
	if _, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlB}); m.harness != "opencode" {
		t.Fatalf("ctrl+b selected %q, want opencode", m.harness)
	}
	if got := readHarness(); got != "opencode" {
		t.Fatalf("ctrl+b did not persist the harness: got %q", got)
	}
}

// TestConfiguredHarnesses pins the set the picker reads: the config narrows it, a name the
// picker does not know is dropped, and a set that narrows to nothing is read as no narrowing at
// all rather than as an empty picker.
func TestConfiguredHarnesses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := readHarnesses(); !reflect.DeepEqual(got, knownHarnesses) {
		t.Fatalf("readHarnesses() with no config = %#v, want every known store", got)
	}
	write := func(value string) {
		if err := os.MkdirAll(filepath.Join(home, ".config", "sh2pil"), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "default_target: local\nharnesses: " + value + "\n"
		if err := os.WriteFile(filepath.Join(home, ".config", "sh2pil", "config.yaml"),
			[]byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("claude, codex")
	if got := readHarnesses(); !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Fatalf("readHarnesses() = %#v, want claude and codex", got)
	}
	write("codex, bogus, claude")
	if got := readHarnesses(); !reflect.DeepEqual(got, []string{"codex", "claude"}) {
		t.Fatalf("readHarnesses() = %#v, want the known stores of the set, in the configured "+
			"order", got)
	}
	write("")
	if got := readHarnesses(); !reflect.DeepEqual(got, knownHarnesses) {
		t.Fatalf("readHarnesses() over an empty set = %#v, want every known store", got)
	}
}

func TestSlashSearchUsesEmacsEditingWithoutTriggeringListActions(t *testing.T) {
	m := model{query: "alpha beta", queryCursor: len([]rune("alpha beta")), sessions: []session{{ID: "session", Name: "alpha beta"}}}
	key := func(msg tea.KeyMsg) { _, _ = m.handleKey(msg) }

	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.searching {
		t.Fatal("slash did not activate search")
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	if m.queryCursor != len([]rune("alpha ")) {
		t.Fatalf("alt+b cursor = %d, want start of last word %d", m.queryCursor, len([]rune("alpha ")))
	}
	key(tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.query != "beta" || m.pending != "" {
		t.Fatalf("ctrl+w in search: query=%q pending=%q; want kill-word and no list action", m.query, m.pending)
	}
	key(tea.KeyMsg{Type: tea.KeyCtrlY})
	if m.query != "alpha beta" {
		t.Fatalf("ctrl+y query = %q, want restored text", m.query)
	}
	// alt+f is a list action too, so inside the field it must still move by a word.
	key(tea.KeyMsg{Type: tea.KeyCtrlE})
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	if m.queryCursor != len([]rune("alpha ")) {
		t.Fatalf("alt+b in restored text: cursor = %d, want %d", m.queryCursor, len([]rune("alpha ")))
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if m.queryCursor != len([]rune("alpha beta")) {
		t.Fatalf("alt+f cursor = %d, want end of last word %d", m.queryCursor, len([]rune("alpha beta")))
	}
	key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.searching {
		t.Fatal("enter did not leave search active state")
	}
	key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.query != "alpha beta" {
		t.Fatalf("typing outside search changed query to %q", m.query)
	}
}

func TestProjectActionsUseSelectedDirectoryAndPlacement(t *testing.T) {
	project := session{Project: "repo", CWD: "/tmp/repo", Alive: true, ProjectOnly: true}
	m := model{harness: "opencode", layout: "pane", view: viewSessions, editor: "nvim"}
	newArgs, _ := m.openArgs("new", project, false)
	wantNew := []string{"opencode-new", "/tmp/repo", "--label", "opencode repo", "--place", "pane"}
	if !reflect.DeepEqual(newArgs, wantNew) {
		t.Fatalf("project new args = %#v, want %#v", newArgs, wantNew)
	}
	shellArgs, _ := m.openArgs("shell", project, false)
	wantShell := []string{"shell", "/tmp/repo", "--label", "shell repo", "--place", "pane"}
	if !reflect.DeepEqual(shellArgs, wantShell) {
		t.Fatalf("project shell args = %#v, want %#v", shellArgs, wantShell)
	}
	yaziArgs, _ := m.openArgs("yazi", project, false)
	wantYazi := []string{"run-tool", "--label", "yazi repo", "--place", "pane", "/tmp/repo", "yazi"}
	if !reflect.DeepEqual(yaziArgs, wantYazi) {
		t.Fatalf("project yazi args = %#v, want %#v", yaziArgs, wantYazi)
	}
	editorArgs, _ := m.openArgs("project-editor", project, false)
	wantEditor := []string{"editor-dir", "/tmp/repo", "--editor", "nvim", "--label", "nvim repo", "--place", "pane"}
	if !reflect.DeepEqual(editorArgs, wantEditor) {
		t.Fatalf("project editor args = %#v, want %#v", editorArgs, wantEditor)
	}

	selectedSession := session{CWD: "/tmp/repo/subdir", Alive: false}
	editorArgs, _ = m.openArgs("project-editor", selectedSession, false)
	if got := editorArgs[1]; got != selectedSession.CWD {
		t.Fatalf("session editor cwd = %q, want recorded project directory %q", got, selectedSession.CWD)
	}

	m.layout = "ask"
	m.sessions = []session{project}
	_, editorCmd := m.beginAction("project-editor", project, false)
	if editorCmd != nil || m.modal != "layout" || m.askAction != "project-editor" {
		t.Fatalf("project editor: modal=%q action=%q cmd=%v; want placement prompt",
			m.modal, m.askAction, editorCmd)
	}
	_, editorCmd = m.chooseLayout(0)
	if editorCmd != nil || m.modal != "editor-title" || m.name.text != "nvim repo" || m.askLayout != "tab" {
		t.Fatalf("project editor placement: modal=%q title=%q place=%q cmd=%v; want title prompt",
			m.modal, m.name.text, m.askLayout, editorCmd)
	}
	m.name = newField("review auth flow")
	_, editorCmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if editorCmd == nil || m.modal != "" || m.askAction != "" {
		t.Fatalf("project editor title: modal=%q action=%q cmd=%v; want editor launch",
			m.modal, m.askAction, editorCmd)
	}

	m.layout = "pane"
	_, editorCmd = m.beginAction("project-editor", project, false)
	if editorCmd != nil || m.modal != "editor-title" || m.askLayout != "pane" {
		t.Fatalf("fixed-layout editor: modal=%q place=%q cmd=%v; want title prompt without placement prompt",
			m.modal, m.askLayout, editorCmd)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal != "" || m.askAction != "" {
		t.Fatalf("cancelled editor title left modal state: modal=%q action=%q", m.modal, m.askAction)
	}
	m.editorLabel = "review auth flow"
	customTitleArgs, _ := m.openArgs("project-editor", project, false)
	if got := customTitleArgs[5]; got != "review auth flow" {
		t.Fatalf("custom editor title = %q, want prompt text", got)
	}
	m.editorLabel = ""
	m.layout = "ask"
	_, cmd := m.startAction("ctrl+t")
	if cmd != nil || m.modal != "layout" || m.askAction != "shell" {
		t.Fatalf("project ctrl+t: modal=%q action=%q cmd=%v; want placement question for shell",
			m.modal, m.askAction, cmd)
	}

	// alt+f is the file manager on the list and word-forward inside a field, so the list
	// action has to reach sh2pil-open with the picked directory.
	m.layout, m.modal, m.askAction = "pane", "", ""
	_, yaziCmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if yaziCmd == nil || m.modal != "" {
		t.Fatalf("alt+f: modal=%q cmd=%v; want a yazi launch", m.modal, yaziCmd)
	}
}

// TestGroupsExpandAndCollapse pins the shape of the main list: a project is a header, its
// sessions appear under it only when it is opened, and enter moves between the two.
func TestGroupsExpandAndCollapse(t *testing.T) {
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}
	other := session{ID: "ses_2", Name: "tidy", Project: "other", CWD: "/tmp/other/sub"}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}},
			{Project: "other", CWD: "/tmp/other", Sessions: []session{other}},
		}}}}
	m.rebuildRows()

	if rows := m.filtered(); len(rows) != 2 {
		t.Fatalf("collapsed rows = %d, want one header per group", len(rows))
	}
	if head := m.filtered()[0]; !head.ProjectOnly || head.Expanded || head.Count != 1 {
		t.Fatalf("header = %#v, want a closed header holding one session", head)
	}

	// enter on the header opens it and leaves the cursor there.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	rows := m.filtered()
	if len(rows) != 3 || !rows[0].Expanded || rows[1].ID != chat.ID {
		t.Fatalf("opened rows = %#v, want the header and its session", rows)
	}
	if rows[1].Depth != 1 {
		t.Fatalf("session depth = %d, want 1 under a header", rows[1].Depth)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor moved to %d on open, want the header", m.cursor)
	}

	// A second enter closes it again, and the expansion state is what the next read keeps.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if rows := m.filtered(); len(rows) != 2 || rows[0].Expanded {
		t.Fatalf("closed rows = %#v, want two headers", rows)
	}
	if m.expanded[m.expansionKey("/tmp/repo", "repo")] {
		t.Fatal("the group is still marked open after a second enter")
	}
}

// TestExpansionSurvivesAReload pins that the open groups belong to the rider's state and not
// to the rows: a poll that replaces the data must not close what was opened.
func TestExpansionSurvivesAReload(t *testing.T) {
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}
	groups := []group{{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: groups}}}
	m.rebuildRows()
	m.toggleGroup(m.filtered()[0])

	m.mergeTarget(targetMsg{label: "local", data: targetData{Target: target{},
		Groups: groups, Loaded: true}})
	rows := m.filtered()
	if len(rows) != 2 || !rows[0].Expanded {
		t.Fatalf("rows after a reload = %#v, want the group still open", rows)
	}
}

// TestSearchOpensOnlyForAsLongAsItMatches pins that a query reaches into a closed group
// without taking over the rider's own expansion state.
func TestSearchOpensOnlyForAsLongAsItMatches(t *testing.T) {
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}},
			{Project: "other", CWD: "/tmp/other"},
		}}}}
	m.rebuildRows()

	m.query = "login"
	m.rebuildRows()
	rows := m.filtered()
	if len(rows) != 2 || rows[1].ID != chat.ID {
		t.Fatalf("sorted rows = %#v, want the matching group opened", rows)
	}
	if m.expanded[m.expansionKey("/tmp/repo", "repo")] {
		t.Fatal("a search overwrote the rider's own expansion state")
	}

	// A query that matches nothing anywhere leaves an empty list, and clearing it brings back
	// the closed header untouched.
	m.query = "nothing-here"
	m.rebuildRows()
	if rows := m.filtered(); len(rows) != 0 {
		t.Fatalf("rows for an unmatched query = %#v, want none", rows)
	}
	m.query = ""
	m.rebuildRows()
	if rows := m.filtered(); len(rows) != 2 || rows[0].Expanded {
		t.Fatalf("rows after clearing = %#v, want the closed headers", rows)
	}
}

// TestAProjectOnlyMatchOpensToItsWholeProject pins the three-state match: a group found by
// its own name or path stays folded, and opened it holds every session, so the box is never
// empty.  A session found by the query still opens its own group.
func TestAProjectOnlyMatchOpensToItsWholeProject(t *testing.T) {
	first := session{ID: "ses_1", Name: "fix login"}
	second := session{ID: "ses_2", Name: "add tests"}
	other := session{ID: "ses_3", Name: "css"}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "api", CWD: "/srv/api", Sessions: []session{first, second}},
			{Project: "web", CWD: "/srv/web", Sessions: []session{other}},
		}}}}
	m.rebuildRows()

	m.query = "api"
	m.rebuildRows()
	rows := m.filtered()
	if len(rows) != 1 || !rows[0].ProjectOnly {
		t.Fatalf("project-only match = %#v, want the folded header alone", rows)
	}
	if rows[0].Count != 2 {
		t.Fatalf("header count = %d, want 2", rows[0].Count)
	}

	// Opening it shows every session, and the count still describes the whole project.
	m.toggleGroup(rows[0])
	rows = m.filtered()
	if len(rows) != 3 || rows[1].ID != first.ID || rows[2].ID != second.ID {
		t.Fatalf("opened rows = %#v, want the header and both sessions", rows)
	}
	if rows[0].Count != 2 {
		t.Fatalf("opened header count = %d, want 2", rows[0].Count)
	}

	// A session match opens its own group and shows the matching session.
	m.query = "tests"
	m.rebuildRows()
	rows = m.filtered()
	if len(rows) != 2 || rows[1].ID != second.ID {
		t.Fatalf("session match rows = %#v, want only the matching session", rows)
	}
}

// TestGroupJoinFindsTheInnerProject pins the path rule: the longest project path that holds a
// session wins, so a repository inside another repository lands in the inner one, and a
// session outside every project still gets a group of its own.
func TestGroupJoinFindsTheInnerProject(t *testing.T) {
	projects := []session{
		{Project: "home", CWD: "/work", ProjectOnly: true},
		{Project: "inner", CWD: "/work/inner", ProjectOnly: true},
	}
	sessions := []session{
		{ID: "ses_in", Project: "inner", CWD: "/work/inner/sub"},
		{ID: "ses_out", Project: "home", CWD: "/work/other"},
		{ID: "ses_loose", Project: "scratch", CWD: "/tmp/scratch"},
	}
	groups := groupSessions(projects, sessions, nil)
	if len(groups) != 3 {
		t.Fatalf("groups = %#v, want the two projects and one loose group", groups)
	}
	if len(groups[0].Sessions) != 1 || groups[0].Sessions[0].ID != "ses_out" {
		t.Fatalf("outer project holds %#v, want the session in its own subdirectory", groups[0].Sessions)
	}
	if len(groups[1].Sessions) != 1 || groups[1].Sessions[0].ID != "ses_in" {
		t.Fatalf("inner project holds %#v, want the nested session", groups[1].Sessions)
	}
	if groups[2].Project != "scratch" || len(groups[2].Sessions) != 1 {
		t.Fatalf("loose group = %#v, want its own group named from the session", groups[2])
	}
}

// TestIgnoredGroupsFollowTheIgnoredToggle pins that the ignored set stays a mode of the same
// list: alt+i swaps which groups are shown instead of adding a list beside them.
func TestIgnoredGroupsFollowTheIgnoredToggle(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo"},
			{Project: "old", CWD: "/tmp/old", Ignored: true},
		}}}}
	m.rebuildRows()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}, Alt: true})
	rows := m.filtered()
	if len(rows) != 1 || !rows[0].Ignored || rows[0].Project != "old" {
		t.Fatalf("ignored rows = %#v, want the ignored group alone", rows)
	}
	if header := ansi.Strip(m.headerView()); !strings.Contains(header, "ignored") {
		t.Fatalf("header = %q, want the ignored mode named", ansi.Strip(m.headerView()))
	}
}

// TestGroupHeaderKeepsOnlyTheArrow pins what marks a project row: the expansion arrow, because
// the session rows under it are already indented.  An ignored project keeps the one mark no
// other row has.
func TestGroupHeaderKeepsOnlyTheArrow(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{}}
	closed := ansi.Strip(m.rowView(groupRow(group{Project: "repo", CWD: "/tmp/repo"}, false),
		nil, false))
	if strings.Contains(closed, "◆") || !strings.HasPrefix(closed, " ▸ repo") {
		t.Fatalf("closed group header = %q, want the arrow and the project name alone", closed)
	}
	ignored := ansi.Strip(m.rowView(
		groupRow(group{Project: "old", CWD: "/tmp/old", Ignored: true}, false), nil, false))
	if strings.Contains(ignored, "◆") || !strings.HasPrefix(ignored, " ▸ ◌ old") {
		t.Fatalf("ignored group header = %q, want the ignored mark and the name", ignored)
	}
}

// TestIgnoredProjectsAreLocalOnly pins the guard: the ignored set is this picker's own state,
// so on a host the key explains itself instead of hiding the host's projects.
func TestIgnoredProjectsAreLocalOnly(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		targets: []target{{}, {Server: "build-host"}}, current: 1,
		data: map[string]targetData{"build-host": {Loaded: true, Target: target{Server: "build-host"},
			Groups: []group{{Project: "repo", CWD: "/srv/repo", Server: "build-host"}}}}}
	m.rebuildRows()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}, Alt: true})
	if m.showIgnored {
		t.Fatal("alt+i switched the ignored mode on for a host")
	}
	if !strings.Contains(m.status, "local list") {
		t.Fatalf("status = %q, want the local-only reason", m.status)
	}
}

func TestIgnoredProjectsPersist(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if got := readIgnoredProjects(); len(got) != 0 {
		t.Fatalf("readIgnoredProjects() = %#v, want empty", got)
	}
	want := map[string]bool{"/tmp/repo": true, "/work/repo": true}
	if err := writeIgnoredProjects(want); err != nil {
		t.Fatal(err)
	}
	if got := readIgnoredProjects(); !reflect.DeepEqual(got, want) {
		t.Fatalf("readIgnoredProjects() = %#v, want %#v", got, want)
	}
}

// sessionIDs lists the session ids of a row set with the group headers left out, so a test can
// say which sessions a list shows.
func sessionIDs(rows []session) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ProjectOnly {
			continue
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// TestOnlyShownKeepsTheRowsAWindowShows pins alt+l: the list keeps the sessions a window
// already shows and drops every other row, an attached zmx session counts, a remote row goes
// because its host answers no owner, and a group left with nothing is not in the list at all.
func TestOnlyShownKeepsTheRowsAWindowShows(t *testing.T) {
	open := session{ID: "ses_open", Project: "repo", CWD: "/tmp/repo", Alive: true}
	closed := session{ID: "ses_closed", Project: "repo", CWD: "/tmp/repo", Alive: true}
	remote := session{ID: "ses_remote", Project: "srv", CWD: "/srv/repo", Alive: true,
		Live: true, Server: "build-host"}
	attached := session{ID: "zmx_a", Name: "pi-a", ZmxName: "pi-a", Project: "repo",
		CWD: "/tmp/repo", ZmxOnly: true, Clients: 1}
	free := session{ID: "zmx_b", Name: "pi-b", ZmxName: "pi-b", Project: "repo",
		CWD: "/tmp/repo", ZmxOnly: true}
	m := model{view: viewSessions, width: 100, height: 20,
		expanded: map[string]bool{},
		live:     map[string]liveInfo{"ses_open": {ID: "ses_open", Owner: "kitty window 12"}},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Groups: []group{
				{Project: "repo", CWD: "/tmp/repo", Sessions: []session{open, closed}},
				{Project: "srv", CWD: "/srv/repo", Sessions: []session{remote}},
			},
			Zmx: []session{attached, free}}}}
	m.rebuildRows()
	// Nothing is expanded yet, so the list is two headers and their counts, which say what the
	// groups hold.
	if got, want := len(m.filtered()), 2; got != want {
		t.Fatalf("rows before the toggle = %d, want %d headers", got, want)
	}
	if got := m.filtered()[0].Count; got != 2 {
		t.Fatalf("repo header before the toggle counts %d, want both sessions", got)
	}
	if got := sessionIDs(m.zmxRows); !reflect.DeepEqual(got, []string{"zmx_a", "zmx_b"}) {
		t.Fatalf("zmx rows before the toggle = %v, want both", got)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}, Alt: true})
	if !m.onlyShown {
		t.Fatal("alt+l did not turn the toggle on")
	}
	// One header, holding the one session a window shows, opened because the toggle left it
	// worth looking at; the remote session's group is gone with it.
	rows := m.filtered()
	if len(rows) != 2 || !rows[0].ProjectOnly || rows[0].Project != "repo" || !rows[0].Expanded {
		t.Fatalf("rows with the toggle on = %#v, want the repo header open", rows)
	}
	if rows[0].Count != 1 {
		t.Fatalf("repo header with the toggle on counts %d, want the one session left", rows[0].Count)
	}
	if got := sessionIDs(rows); !reflect.DeepEqual(got, []string{"ses_open"}) {
		t.Fatalf("sessions with the toggle on = %v, want the one a window shows", got)
	}
	if got := sessionIDs(m.zmxRows); !reflect.DeepEqual(got, []string{"zmx_a"}) {
		t.Fatalf("zmx rows with the toggle on = %v, want the attached one", got)
	}
	if m.sessionCount != 1 || m.groupCount != 1 {
		t.Fatalf("counts with the toggle on = %d groups, %d sessions; want one of each",
			m.groupCount, m.sessionCount)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}, Alt: true})
	if m.onlyShown {
		t.Fatal("alt+l did not turn the toggle off")
	}
	rows = m.filtered()
	if len(rows) != 2 || len(sessionIDs(rows)) != 0 {
		t.Fatalf("rows after the toggle = %#v, want the two headers and the reader's own expansion", rows)
	}
	if got := m.filtered()[0].Count; got != 2 {
		t.Fatalf("repo header after the toggle counts %d, want both sessions", got)
	}
	if got := sessionIDs(m.zmxRows); !reflect.DeepEqual(got, []string{"zmx_a", "zmx_b"}) {
		t.Fatalf("zmx rows after the toggle = %v, want both", got)
	}

	// A zmx pane keeps its cursor when the toggle leaves rows in it, and gives the cursor back
	// to the project list when it leaves none, the same way a target with no zmx sessions does.
	kept := model{view: viewZmx, width: 100, height: 20, onlyShown: true, zmxCursor: 0,
		expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Zmx: []session{attached, free}}}}
	kept.rebuildRows()
	if kept.view != viewZmx {
		t.Fatalf("view with an attached zmx session = %q, want the zmx pane", kept.view)
	}
	emptied := model{view: viewZmx, width: 100, height: 20, onlyShown: true, zmxCursor: 0,
		expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Zmx: []session{free}}}}
	emptied.rebuildRows()
	if emptied.view != viewSessions {
		t.Fatalf("view with no attached zmx session = %q, want the project list", emptied.view)
	}
}

// TestZmxBoxYieldsWhenTheWindowShrinks pins how the left column is split: the zmx box is as
// tall as its sessions need while the window can afford it, and gives rows back as the window
// shrinks, so the project box is not the only one that loses height.  The two boxes still fill
// the column exactly, and the project box keeps its floor while the zmx box is shown at all.
func TestZmxBoxYieldsWhenTheWindowShrinks(t *testing.T) {
	rows := make([]session, 6)
	for index := range rows {
		rows[index] = session{ID: "zmx_1", ZmxOnly: true, Clients: 1}
	}
	tall := model{width: 100, height: 34, zmxRows: rows}
	if got := tall.zmxPaneRows(); got != 6 {
		t.Fatalf("zmx rows in a tall window = %d, want all six", got)
	}

	previous := -1
	for height := 34; height >= 8; height-- {
		m := model{width: 100, height: height, zmxRows: rows}
		got := m.zmxPaneRows()
		if previous >= 0 && got > previous {
			t.Fatalf("window %d: zmx rows = %d, want no more than the %d of a taller window",
				height, got, previous)
		}
		previous = got
		if got > 0 {
			if m.projectsPaneHeight() < minProjectsRows {
				t.Fatalf("window %d: the project box = %d rows, want at least %d",
					height, m.projectsPaneHeight(), minProjectsRows)
			}
			if m.projectsPaneHeight()+m.zmxPaneHeight()+2 != m.paneHeight() {
				t.Fatalf("window %d: the boxes = %d + %d rows, want the column's %d",
					height, m.projectsPaneHeight(), m.zmxPaneHeight(), m.paneHeight())
			}
		}
	}

	// A window that shrinks takes rows from the zmx box and not from the project list alone.
	mid := model{width: 100, height: 24, zmxRows: rows}
	if got := mid.zmxPaneRows(); got >= tall.zmxPaneRows() {
		t.Fatalf("zmx rows in a 24 row window = %d, want fewer than the %d of a tall one",
			got, tall.zmxPaneRows())
	}
	small := model{width: 100, height: 16, zmxRows: rows}
	if got := small.zmxPaneRows(); got >= mid.zmxPaneRows() {
		t.Fatalf("zmx rows in a 16 row window = %d, want fewer than the %d of a 24 row one",
			got, mid.zmxPaneRows())
	}

	// A column too short for both boxes gives every row to the project list.
	short := model{width: 100, height: 10, zmxRows: rows}
	if got := short.zmxPaneRows(); got != 0 {
		t.Fatalf("zmx rows in a very short window = %d, want the box hidden", got)
	}
	if got := short.projectsPaneHeight(); got != short.paneHeight() {
		t.Fatalf("project rows with no zmx box = %d, want the whole column's %d",
			got, short.paneHeight())
	}
}

// TestTargetsAndPanesSwitch pins the moves the two pairs of brackets make: `{` and `}` walk the
// targets, and `[` and `]` walk the panes of the window.
func TestTargetsAndPanesSwitch(t *testing.T) {
	chat := session{ID: "ses_1", Project: "repo", CWD: "/tmp/repo"}
	zmx := session{Name: "pi-repo", ZmxName: "pi-repo", Project: "repo", CWD: "/tmp/repo",
		ZmxOnly: true, Clients: 1}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		targets: []target{{}, {Server: "build-host"}},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Groups: []group{{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}},
			Zmx:    []session{zmx}}}}
	m.rebuildRows()

	// `]` moves into the zmx pane, which keeps its own cursor, and `[` comes back.
	m.zmxCursor = 0
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.view != viewZmx {
		t.Fatalf("] left the view at %q, want the zmx pane", m.view)
	}
	if rows := m.filtered(); len(rows) != 1 || !rows[0].ZmxOnly {
		t.Fatalf("zmx pane rows = %#v, want the zmx session", rows)
	}
	// The cycle wraps, so one key pair reaches the project list again from the last pane.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if m.view != viewSessions {
		t.Fatalf("] wrapped to the view at %q, want the projects pane", m.view)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.view != viewZmx {
		t.Fatalf("[ left the view at %q, want the zmx pane", m.view)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	if m.view != viewSessions {
		t.Fatalf("[ left the view at %q, want the projects pane", m.view)
	}

	// A target switch moves to the host and schedules its read; the rows of a target that has not
	// answered yet are empty, and the pane it cannot fill gives the cursor back.
	cmd := m.switchTarget(1)
	if cmd == nil || m.targetLabel() != "build-host" {
		t.Fatalf("switch: label=%q cmd=%v, want the host and a scheduled read", m.targetLabel(), cmd)
	}
	if m.generation != 1 {
		t.Fatalf("generation = %d, want one switch counted", m.generation)
	}
	if rows := m.filtered(); len(rows) != 0 {
		t.Fatalf("rows before the host answers = %#v, want none", rows)
	}
	if cmd := m.switchTarget(1); cmd == nil || m.targetLabel() != "local" {
		t.Fatalf("the walk wrapped to %q, want this machine", m.targetLabel())
	}
	if m.generation != 2 {
		t.Fatalf("generation = %d, want both switches counted", m.generation)
	}

	// The keys drive the same walk: } goes on, { comes back.
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	if cmd == nil || m.targetLabel() != "build-host" || m.generation != 3 {
		t.Fatalf("}: label=%q generation=%d, want the host and one more switch",
			m.targetLabel(), m.generation)
	}
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'{'}})
	if cmd == nil || m.targetLabel() != "local" || m.generation != 4 {
		t.Fatalf("{}: label=%q generation=%d, want this machine back", m.targetLabel(), m.generation)
	}

	// The numbered keys reach a target by the number the bar prints.  The bar carries those
	// numbers, a number already on screen is no move and no reload, and a number the bar does
	// not print is a message instead of a switch.
	if bar := ansi.Strip(m.targetBar()); !strings.Contains(bar, "1 local") ||
		!strings.Contains(bar, "2 build-host") {
		t.Fatalf("target bar = %q, want every target numbered for the alt keys", bar)
	}
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	if cmd == nil || m.targetLabel() != "build-host" || m.generation != 5 {
		t.Fatalf("alt+2: label=%q generation=%d, want the host and one more switch",
			m.targetLabel(), m.generation)
	}
	if _, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true}); cmd != nil ||
		m.generation != 5 {
		t.Fatalf("alt+2 on the target already on screen: cmd=%v generation=%d, want no move",
			cmd, m.generation)
	}
	if _, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}, Alt: true}); cmd != nil ||
		m.targetLabel() != "build-host" || !strings.Contains(m.status, "no target 3") {
		t.Fatalf("alt+3: label=%q status=%q cmd=%v, want a message and no switch",
			m.targetLabel(), m.status, cmd)
	}
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if cmd == nil || m.targetLabel() != "local" || m.generation != 6 {
		t.Fatalf("alt+1: label=%q generation=%d, want this machine back",
			m.targetLabel(), m.generation)
	}
}

// TestTabFoldsTheGroupUnderTheCursor pins tab: on a header it folds that group, on a session
// under one it folds the group that holds it, and in the zmx pane it says there is nothing to
// fold.
func TestTabFoldsTheGroupUnderTheCursor(t *testing.T) {
	chat := session{ID: "ses_1", Project: "repo", CWD: "/tmp/repo/inner"}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Groups: []group{{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}}}}}
	m.rebuildRows()

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	rows := m.filtered()
	if len(rows) != 2 || !rows[0].Expanded {
		t.Fatalf("tab on the header left %#v, want the group open", rows)
	}
	if !strings.Contains(m.status, "opened repo") {
		t.Fatalf("status = %q, want the group named as opened", m.status)
	}

	// The session under the header carries its own directory, so tab there has to find the
	// group by walking back to the nearest header.
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if got := m.selected().ID; got != "ses_1" {
		t.Fatalf("cursor row = %q, want the session under the header", got)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	rows = m.filtered()
	if len(rows) != 1 || rows[0].Expanded {
		t.Fatalf("tab on the session left %#v, want the group closed", rows)
	}

	m.view = viewZmx
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if !strings.Contains(m.status, "not grouped") {
		t.Fatalf("status in the zmx pane = %q, want the no-group note", m.status)
	}
}

// TestPaneCycleReachesThePreview pins that the cycle includes the preview while it is shown,
// so `]` and `[` reach all three panes and not only the two lists.
func TestPaneCycleReachesThePreview(t *testing.T) {
	chat := session{ID: "ses_1", Project: "repo", CWD: "/tmp/repo"}
	zmx := session{Name: "pi-repo", ZmxName: "pi-repo", Project: "repo", CWD: "/tmp/repo",
		ZmxOnly: true, Clients: 1}
	m := model{view: viewSessions, width: 100, height: 20, showPrev: true,
		expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Groups: []group{{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}},
			Zmx:    []session{zmx}}}}
	m.rebuildRows()
	press := func(r rune) {
		t.Helper()
		_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	press(']')
	if m.view != viewZmx || m.focus != paneFocusList {
		t.Fatalf("] from the projects = (%q, %d), want the zmx pane", m.view, m.focus)
	}
	press(']')
	if m.focus != paneFocusPreview {
		t.Fatalf("] from the zmx pane = (%q, %d), want the preview", m.view, m.focus)
	}
	press(']')
	if m.view != viewSessions || m.focus != paneFocusList {
		t.Fatalf("] from the preview = (%q, %d), want the project list", m.view, m.focus)
	}
	press('[')
	if m.focus != paneFocusPreview {
		t.Fatalf("[ from the projects = (%q, %d), want the preview", m.view, m.focus)
	}
}

// TestFooterShowsOnlyTheHelpHint pins that the footer never lists bindings: the full list is
// the ? box, which keeps the footer two lines on any window.
func TestFooterShowsOnlyTheHelpHint(t *testing.T) {
	m := model{view: viewSessions, width: 80, height: 20}
	footer := m.footerView()
	if strings.Contains(footer, "ctrl+") || !strings.Contains(footer, "? help") {
		t.Fatalf("footer exposes bindings other than help: %q", footer)
	}
	if lines := strings.Split(footer, "\n"); len(lines) != 2 {
		t.Fatalf("footer = %q, want a status line and the help hint", footer)
	}
}

func TestProjectIgnoreCanBeToggledAndRestored(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := session{Project: "repo", CWD: "/tmp/repo", Alive: true, ProjectOnly: true}
	m := model{view: viewSessions, sessions: []session{project}}
	_, cmd := m.toggleProjectIgnored()
	if cmd == nil || !readIgnoredProjects()[project.CWD] {
		t.Fatal("ignoring project did not persist its path")
	}

	project.Ignored = true
	m.sessions = []session{project}
	m.showIgnored = true
	_, cmd = m.toggleProjectIgnored()
	if cmd == nil || readIgnoredProjects()[project.CWD] {
		t.Fatal("restoring project did not remove its ignored entry")
	}
	if m.showIgnored {
		t.Fatal("restoring a project should return to the active project list")
	}
}

func TestIgnoringProjectPreservesCursorPosition(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	groups := []group{
		{Project: "one", CWD: "/tmp/one"},
		{Project: "two", CWD: "/tmp/two"},
		{Project: "three", CWD: "/tmp/three"},
	}
	m := model{view: viewSessions, cursor: 1, offset: 1, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: groups}}}
	m.rebuildRows()
	_, cmd := m.toggleProjectIgnored()
	if cmd == nil {
		t.Fatal("ignoring project returned no reload command")
	}
	groups[1].Ignored = true
	m.mergeTarget(targetMsg{label: "local", data: targetData{Target: target{}, Loaded: true,
		Groups: groups}})
	if m.cursor != 1 {
		t.Fatalf("cursor moved to %d after ignore; want same row index 1", m.cursor)
	}
	if got := m.selected().Project; got != "three" {
		t.Fatalf("selected %q after ignore, want the next group three", got)
	}
}

func TestProjectPreviewLoadsAndScrollsFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := model{}.fetchProjectFiles(root)().(projectFilesMsg)
	if msg.err != "" {
		t.Fatalf("fetchProjectFiles error: %s", msg.err)
	}
	if !reflect.DeepEqual(msg.files, []string{"README.md", "cmd/"}) {
		t.Fatalf("project files = %#v", msg.files)
	}

	project := session{Project: "repo", CWD: root, ProjectOnly: true}
	m := model{view: viewSessions, sessions: []session{project}, width: 90, height: 14,
		projectFilesPath: root, projectFiles: msg.files}
	if rendered := ansi.Strip(m.previewView()); !strings.Contains(rendered, "README.md") ||
		!strings.Contains(rendered, "cmd/") {
		t.Fatalf("project preview is missing its directory entries: %q", rendered)
	}
	if limit := m.previewScrollLimit(); limit != 0 {
		t.Fatalf("short project file list scroll limit = %d, want 0", limit)
	}
	m.projectFiles = make([]string, 20)
	m.scrollPreviewToBottom()
	if m.preview.scroll != m.previewScrollLimit() {
		t.Fatalf("file list scroll = %d, want bottom %d", m.preview.scroll, m.previewScrollLimit())
	}
}

func TestOpenArgsUseHarness(t *testing.T) {
	m := model{harness: "opencode", layout: "tab"}
	newArgs, _ := m.openArgs("new", session{Project: "project", CWD: "/tmp/project", Alive: true}, false)
	wantNew := []string{"opencode-new", "/tmp/project", "--label", "opencode project", "--place", "tab"}
	if !reflect.DeepEqual(newArgs, wantNew) {
		t.Fatalf("new OpenCode args = %#v, want %#v", newArgs, wantNew)
	}
	resumeArgs, _ := m.openArgs("resume", session{
		Harness: "opencode", ID: "ses_example", Name: "example",
		CWD: "/tmp/project", Alive: true,
	}, false)
	wantResume := []string{"opencode", "/tmp/project", "ses_example", "--label", "opencode example", "--place", "tab"}
	if !reflect.DeepEqual(resumeArgs, wantResume) {
		t.Fatalf("resume OpenCode args = %#v, want %#v", resumeArgs, wantResume)
	}
}

// TestFrameFillsTheWindowExactly pins the arithmetic that keeps the frame stable: the target
// bar, the header, the two boxes of the left column, the preview, and the footer must add up to
// exactly the window height, and no line may be wider than the window.  A frame one line too
// tall, or one column too wide, makes the terminal scroll: everything below shifts and the rest
// of the screen looks corrupted.
func TestFrameFillsTheWindowExactly(t *testing.T) {
	groups := make([]group, 0, 6)
	for index := 0; index < 6; index++ {
		sessions := make([]session, 0, 4)
		for inner := 0; inner < 4; inner++ {
			sessions = append(sessions, session{
				ID:      fmt.Sprintf("ses_%d_%d", index, inner),
				Name:    fmt.Sprintf("a long session name that has to be clipped %d", inner),
				Project: fmt.Sprintf("project-%d", index), CWD: fmt.Sprintf("/work/project-%d", index),
				Alive: true, Bytes: 2 * 1024 * 1024, Mod: 1791041092, Depth: 1,
			})
		}
		groups = append(groups, group{Project: fmt.Sprintf("project-%d", index),
			CWD: fmt.Sprintf("/work/project-%d", index), Sessions: sessions})
	}
	zmx := []session{{Name: "pi-project-0", ZmxName: "pi-project-0", Project: "project-0",
		CWD: "/work/project-0", ZmxOnly: true, Clients: 1, Alive: true, Mod: 1791041092}}

	sizes := [][2]int{{150, 34}, {120, 30}, {200, 50}, {80, 24}, {70, 20}, {60, 15}, {44, 18}, {30, 12}, {20, 10}}
	for _, size := range sizes {
		for _, view := range []string{viewSessions, viewZmx} {
			for _, showPrev := range []bool{true, false} {
				for _, zmxRows := range [][]session{nil, zmx} {
					m := model{view: view, width: size[0], height: size[1],
						showPrev: showPrev, expanded: map[string]bool{}, zmxRows: zmxRows,
						live: map[string]liveInfo{},
						data: map[string]targetData{"local": {Loaded: true, Target: target{},
							Groups: groups, Zmx: zmxRows}}}
					m.cursor, m.zmxCursor = 3, 0
					m.rebuildRows()
					m.cursor, m.zmxCursor = 3, 0
					m.rebuildRows()
					frame := m.View()
					where := fmt.Sprintf("%dx%d view=%s preview=%t zmx=%d",
						size[0], size[1], view, showPrev, len(zmxRows))
					lines := strings.Split(frame, "\n")
					if len(lines) != size[1] {
						t.Errorf("%s: frame is %d lines, want %d", where, len(lines), size[1])
					}
					for index, line := range lines {
						if got := ansi.StringWidth(line); got > size[0] {
							t.Errorf("%s: line %d is %d columns, want at most %d: %q",
								where, index, got, size[0],
								strings.TrimRight(ansi.Strip(line), " "))
						}
					}
				}
			}
		}
	}
}

// TestNestedRowFillsTheListExactly pins the width of a session that sits under a group header:
// the indent comes out of the name, never out of the row's own last column.  A row two columns
// too wide loses its size to the clip, and the columns beside it stop lining up.
func TestNestedRowFillsTheListExactly(t *testing.T) {
	chat := session{ID: "ses_1", Harness: "pi", Name: "fix login", Project: "repo",
		CWD: "/work/repo", Alive: true, Bytes: 2 * 1024 * 1024, Mod: 1791041092, Depth: 1}
	m := model{view: viewSessions, width: 120, height: 20}
	for _, selected := range []bool{false, true} {
		line := m.rowView(chat, nil, selected)
		if got := ansi.StringWidth(line); got != m.listWidth() {
			t.Errorf("selected=%t: row measures %d columns, want %d: %q",
				selected, got, m.listWidth(), ansi.Strip(line))
		}
		if !strings.Contains(ansi.Strip(line), "2.0M") {
			t.Errorf("selected=%t: row lost its size to the clip: %q", selected, ansi.Strip(line))
		}
		// The project column belongs to the header above, so the row leaves it out.
		if strings.Contains(ansi.Strip(line), "repo") {
			t.Errorf("selected=%t: nested row repeats its project: %q", selected, ansi.Strip(line))
		}
	}
	// A row without a header keeps its project column, and is still exactly as wide.
	top := chat
	top.Depth = 0
	for _, selected := range []bool{false, true} {
		line := m.rowView(top, nil, selected)
		if got := ansi.StringWidth(line); got != m.listWidth() {
			t.Errorf("top row selected=%t measures %d columns, want %d", selected, got, m.listWidth())
		}
		if !strings.Contains(ansi.Strip(line), "repo") {
			t.Errorf("top row selected=%t lost its project column: %q", selected, ansi.Strip(line))
		}
	}
}

// TestHeaderMarksEveryStoreAndTheOneThatIsMissing pins the header's store flags: every store the
// picker reads is named, a store the target does not have is marked, and a store the target did
// not answer about is not called missing.  The stores a target has are the ones ctrl+b may
// select there.
func TestHeaderMarksEveryStoreAndTheOneThatIsMissing(t *testing.T) {
	m := model{view: viewSessions, width: 120, height: 20, harness: "claude",
		harnesses: []string{"pi", "claude", "codex"},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Harnesses: []harnessInfo{
			{Harness: "pi", Present: true},
			{Harness: "claude", Present: true},
			{Harness: "codex", Reason: "no rollout store at /Users/x/.codex/sessions"},
		}}}}
	header := ansi.Strip(m.headerView())
	for _, store := range []string{"pi", "claude", "codex"} {
		if !strings.Contains(header, store) {
			t.Fatalf("header = %q, want %q named", header, store)
		}
	}
	if answered, present := m.harnessMark("codex"); !answered || present {
		t.Fatalf("codex = answered %t, present %t; want an answered store that is absent",
			answered, present)
	}
	if answered, _ := m.harnessMark("opencode"); answered {
		t.Fatal("a store the target never mentioned read as answered")
	}
	if got := m.availableHarnesses(); !reflect.DeepEqual(got, []string{"pi", "claude"}) {
		t.Fatalf("availableHarnesses() = %#v, want the stores this target has", got)
	}
}

// TestSessionRowNamesTheStore pins the store column: a row says which store wrote it, in a
// column of its own, so a list that merges several stores stays readable.
func TestSessionRowNamesTheStore(t *testing.T) {
	m := model{view: viewSessions, width: 120, height: 20}
	row := session{ID: "ses_9", Harness: "codex", Name: "purge the service", Project: "manifests",
		CWD: "/work/manifests", Alive: true, Bytes: 4096, Mod: 1791041092}
	line := ansi.Strip(m.rowView(row, nil, false))
	if !strings.Contains(line, "codex") {
		t.Fatalf("row = %q, want the store that wrote it", line)
	}
	if got := ansi.StringWidth(m.rowView(row, nil, false)); got != m.listWidth() {
		t.Fatalf("row measures %d columns, want %d", got, m.listWidth())
	}
	row.Harness = "claude"
	if line := ansi.Strip(m.rowView(row, nil, false)); !strings.Contains(line, "claude") {
		t.Fatalf("row = %q, want the store that wrote it", line)
	}
}

// The Claude Code store answers the same action keys as the other two, with its own command
// line: a resume takes the session id, or the transcript path when the project directory is
// gone, and a fork is that command with --fork-session and the name the picker asked for.
func TestClaudeHarnessArgs(t *testing.T) {
	m := model{harness: "claude", layout: "tab"}
	newArgs, _ := m.openArgs("new", session{Project: "project", CWD: "/tmp/project", Alive: true}, false)
	wantNew := []string{"claude-new", "/tmp/project", "--label", "claude project", "--place", "tab"}
	if !reflect.DeepEqual(newArgs, wantNew) {
		t.Fatalf("new Claude Code args = %#v, want %#v", newArgs, wantNew)
	}
	row := session{Harness: "claude", ID: "11111111-2222-3333-4444-555555555555",
		File: "/tmp/store/11111111-2222-3333-4444-555555555555.jsonl",
		Name: "wire the harness in", CWD: "/tmp/project", Alive: true}
	resumeArgs, _ := m.openArgs("resume", row, false)
	wantResume := []string{"claude", "/tmp/project", row.ID, "--label",
		"claude wire the harness in", "--place", "tab"}
	if !reflect.DeepEqual(resumeArgs, wantResume) {
		t.Fatalf("resume Claude Code args = %#v, want %#v", resumeArgs, wantResume)
	}
	// A gone project directory still resumes, from the transcript path rather than the id.
	gone := row
	gone.Alive = false
	deadArgs, _ := m.openArgs("resume", gone, false)
	if !strings.Contains(strings.Join(deadArgs, " "), row.File) {
		t.Fatalf("resume of a gone project = %#v, want the transcript path", deadArgs)
	}
	fork := m
	fork.forkName = "forked chat"
	forkArgs, _ := fork.openArgs("fork", row, false)
	wantFork := []string{"claude", "/tmp/project", row.ID, "--label", "claude forked chat",
		"--place", "tab", "--fork", "--name", "forked chat"}
	if !reflect.DeepEqual(forkArgs, wantFork) {
		t.Fatalf("fork Claude Code args = %#v, want %#v", forkArgs, wantFork)
	}
}

// A Claude Code row is deleted and previewed through its own store, and it cannot be renamed:
// only pi writes a session name into its transcript.
func TestClaudeRowNamesItsStore(t *testing.T) {
	row := session{Harness: "claude", ID: "11111111-2222-3333-4444-555555555555",
		File: "/tmp/store/11111111-2222-3333-4444-555555555555.jsonl",
		Name: "chat", CWD: "/tmp/project", Alive: true}
	m := model{view: viewSessions, width: 120, height: 20, showPrev: true,
		sessions: []session{row}, live: map[string]liveInfo{}, cache: map[string]preview{}}
	helper, args, _ := m.deleteCommand(row)
	if helper != "sh2pil-sessions" || !reflect.DeepEqual(args, []string{"delete", row.ID, "--harness", "claude"}) {
		t.Fatalf("Claude Code delete = %s %v, want the local sh2pil-sessions with the store named", helper, args)
	}
	line := strings.Join(m.previewCommand(row).Args, " ")
	if !strings.Contains(line, "show --harness claude "+row.ID) {
		t.Fatalf("Claude Code preview = %q, want sh2pil-sessions show for its own store", line)
	}
	if _, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlE}); !strings.Contains(m.status, "Claude Code") {
		t.Fatalf("ctrl+e on a Claude Code row = %q, want it refused by name", m.status)
	}
}

// placementArg returns the placement a generated argv carries, wherever it sits: the named
// terminal verbs put it last, and run-tool puts the command after it.
func placementArg(args []string) string {
	for index, arg := range args {
		if arg == "--place" && index+1 < len(args) {
			return args[index+1]
		}
	}
	if len(args) > 0 {
		return args[len(args)-1]
	}
	return ""
}

// TestEscClearsTheFilterFromTheList pins the bug the reader hit: after enter keeps a filter,
// esc in the list must clear it and rebuild the rows, not only the query.
func TestEscClearsTheFilterFromTheList(t *testing.T) {
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		live: map[string]liveInfo{}, cache: map[string]preview{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}},
			{Project: "other", CWD: "/tmp/other"},
		}}}}
	m.query = "nothing-here"
	m.rebuildRows()
	if rows := m.filtered(); len(rows) != 0 {
		t.Fatalf("precondition: rows for the filter = %#v, want none", rows)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.query != "" {
		t.Fatalf("esc left the query %q", m.query)
	}
	if rows := m.filtered(); len(rows) != 2 {
		t.Fatalf("esc left %d rows, want the two headers back", len(rows))
	}
}

// TestFooterShowsTheActiveFilter pins that a kept filter is visible in the footer, not only in
// the header, so it cannot be mistaken for an empty target.
func TestFooterShowsTheActiveFilter(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, query: "login",
		expanded: map[string]bool{}, live: map[string]liveInfo{}, cache: map[string]preview{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{
				{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}}},
		}}}}
	m.rebuildRows()
	if footer := m.footerView(); !strings.Contains(footer, "filter: login") {
		t.Fatalf("footer does not show the active filter: %q", footer)
	}
}

// TestOneQueryDrivesBothPanes pins the shared filter: one query narrows the project list and
// the zmx pane together, and the footer badge reports both counts.
func TestOneQueryDrivesBothPanes(t *testing.T) {
	chat := session{ID: "ses_1", Name: "api login", Project: "repo", CWD: "/tmp/repo"}
	zmx := session{ID: "ses_2", Name: "api agent", ZmxOnly: true, ZmxName: "pi-api", Project: "api"}
	m := model{view: viewSessions, width: 120, height: 20,
		expanded: map[string]bool{}, live: map[string]liveInfo{}, cache: map[string]preview{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{},
			Groups: []group{{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}},
			Zmx:    []session{zmx}}}}
	m.query = "api"
	m.rebuildRows()
	if rows := m.filtered(); len(rows) != 2 || rows[1].ID != chat.ID {
		t.Fatalf("project pane rows = %#v, want the matched session under its header", rows)
	}
	if len(m.zmxRows) != 1 || m.zmxRows[0].ZmxName != "pi-api" {
		t.Fatalf("zmx rows = %#v, want the same query applied there", m.zmxRows)
	}
	footer := m.footerView()
	if !strings.Contains(footer, "filter: api") || !strings.Contains(footer, "1 session match") ||
		!strings.Contains(footer, "1 zmx match") {
		t.Fatalf("footer = %q, want the query and both counts", footer)
	}
	m.query = "nothing-here"
	m.rebuildRows()
	if len(m.zmxRows) != 0 {
		t.Fatalf("zmx rows for an unmatched query = %#v, want none", m.zmxRows)
	}
}

// TestHeaderHighlightsTheProjectMatch pins that the match marks reach the project cell, not
// only the session name.
func TestHeaderHighlightsTheProjectMatch(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "api", CWD: "/srv/api", Sessions: []session{{ID: "ses_1", Name: "login"}}},
		}}}}
	m.query = "api"
	m.rebuildRows()
	view := m.projectsView()
	if !strings.Contains(view, matchSty.Render("a")) || !strings.Contains(view, matchSty.Render("p")) {
		t.Fatalf("the project header carries no match marks: %q", view)
	}
}

// TestGroupsRankByBestMatch pins the ranking: a prefix match sorts above a match inside a
// word when a query is active.
func TestGroupsRankByBestMatch(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "my-api-tool", CWD: "/srv/my-api-tool"},
			{Project: "api", CWD: "/srv/api"},
		}}}}
	m.query = "api"
	m.rebuildRows()
	rows := m.filtered()
	if len(rows) != 2 || rows[0].Project != "api" {
		t.Fatalf("ranked groups = %#v, want the prefix match first", rows)
	}
}

// TestScopedTermsRestrictTheField pins the prefixes: @ the project, # the session name, ~ the
// path, and host: the host.  A bare term still searches every field.
func TestScopedTermsRestrictTheField(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "api", CWD: "/srv/api",
		Server: "build-host"}
	for _, test := range []struct {
		query string
		want  bool
	}{
		{"@api", true},
		{"@login", false},
		{"#login", true},
		{"#api", false},
		{"~/srv", true},
		{"~nomatch", false},
		{"host:build", true},
		{"host:other", false},
		{"api login", true},
	} {
		if ok, _ := matchesRow(test.query, row); ok != test.want {
			t.Errorf("matchesRow(%q) = %t, want %t", test.query, ok, test.want)
		}
	}
}

// TestFilterHideFalseKeepsDimHeaders pins the option: every project stays on screen, and one
// the query did not answer is a dim, folded header instead of a missing row.
func TestFilterHideFalseKeepsDimHeaders(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, expanded: map[string]bool{},
		filterAll: true,
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{{ID: "ses_1", Name: "fix login"}}},
			{Project: "other", CWD: "/tmp/other", Sessions: []session{{ID: "ses_2", Name: "lint"}}},
		}}}}
	m.query = "login"
	m.rebuildRows()
	rows := m.filtered()
	if len(rows) != 3 {
		t.Fatalf("rows = %#v, want the matching project and the dim header", rows)
	}
	if rows[0].Dim || rows[1].Dim || !rows[2].Dim || rows[2].Project != "other" {
		t.Fatalf("dim flags = %#v, want only the unmatched project dim", rows)
	}
	if !strings.Contains(m.projectsView(), "other") {
		t.Fatal("the dim header is not drawn")
	}
	// With the default, the same model hides it.
	m.filterAll = false
	m.rebuildRows()
	if rows := m.filtered(); len(rows) != 2 {
		t.Fatalf("rows with filter_hide = %#v, want the matching project alone", rows)
	}
}

// TestFilterKeepRestoresPerTargetQueries pins the option: the query belongs to the target it
// was typed on, and returning to that target brings it back.
func TestFilterKeepRestoresPerTargetQueries(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, filterKeep: true,
		keptQueries: map[string]string{}, expanded: map[string]bool{},
		targets: []target{{}, {Server: "build-host"}},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}},
			"build-host": {Loaded: true, Target: target{Server: "build-host"}}}}
	m.query = "login"
	m.showTarget(1)
	if m.query != "" {
		t.Fatalf("query after the switch = %q, want the new target's empty query", m.query)
	}
	m.query = "api"
	m.showTarget(0)
	if m.query != "login" {
		t.Fatalf("query restored = %q, want login", m.query)
	}
}

// TestFilterFieldsRestrictBareTerms pins filter_fields: a bare term reads only the configured
// fields, while a scoped term still names its own.
func TestFilterFieldsRestrictBareTerms(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "api", CWD: "/srv/api"}
	if ok, _ := matchesRowFields("srv", row, []string{"name"}); ok {
		t.Fatal("a bare term matched a field outside the configured set")
	}
	if ok, _ := matchesRowFields("srv", row, []string{"cwd"}); !ok {
		t.Fatal("a bare term did not match its configured field")
	}
	if ok, _ := matchesRowFields("~srv", row, []string{"name"}); !ok {
		t.Fatal("a scoped term was dropped with the configured field set")
	}
}
