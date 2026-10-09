package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// remoteRow is one picker row for a session on another host: the same fields a local row has,
// with the host that answered in Server and the transcript path that host reported.
func remoteRow() session {
	return session{
		ID: "01a10260", Harness: "pi", Name: "Fix ingress docs", Project: "home-infra",
		CWD:   "/srv/home-infra",
		File:  "/home/user/.pi/agent/sessions/--srv-home-infra--/2026-10-03_01a10260.jsonl",
		Alive: true, Mod: 1791041092, Bytes: 2 * 1024 * 1024, Server: "user@build-host",
	}
}

func localPlainRow() session {
	return session{ID: "01a10000", Harness: "pi", Name: "Docs tidy", Project: "docs",
		CWD: "/project/docs", Alive: true, Mod: 1791041092, Bytes: 4096}
}

// A row of a host keeps the columns a local row has.  The host is named once, in the target
// bar, so no row carries a host column: a target shows only its own rows.
func TestRemoteRowsKeepTheColumnsAligned(t *testing.T) {
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20, current: 1,
		targets:  []target{{}, {Server: "build-host"}},
		sessions: []session{localPlainRow(), remoteRow()}}
	lines := strings.Split(ansi.Strip(m.projectsView()), "\n")
	if len(lines) != 2 {
		t.Fatalf("list = %q, want one local row and one remote row", lines)
	}
	column := func(line, name string) int {
		return ansi.StringWidth(line[:strings.Index(line, name)])
	}
	if column(lines[0], "Docs tidy") != column(lines[1], "Fix ingress docs") {
		t.Fatalf("names start at columns %d and %d; want one column\n%s",
			column(lines[0], "Docs tidy"), column(lines[1], "Fix ingress docs"),
			strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if got := ansi.StringWidth(line); got != m.listWidth() {
			t.Fatalf("row %q measures %d columns, want %d", line, got, m.listWidth())
		}
		if strings.Contains(line, "build-host") || strings.Contains(line, "local") {
			t.Fatalf("row %q carries a host column, want the host named in the target bar", line)
		}
	}
	// The target bar is where the host is named, and it marks the target on screen.
	if bar := ansi.Strip(m.targetBar()); !strings.Contains(bar, "build-host") ||
		!strings.Contains(bar, "local") {
		t.Fatalf("target bar = %q, want both destinations named", bar)
	}
}

func TestRemoteSessionRowSearchFindsItsHost(t *testing.T) {
	for _, query := range []string{"build-host", "home-infra", "Fix ingress"} {
		if ok, _ := matchesRow(query, remoteRow()); !ok {
			t.Errorf("remote row did not match %q", query)
		}
	}
}

// Every action that opens a chat reaches one helper verb, so a resume, a second terminal, a
// fork, and a new chat in the same project differ only in their arguments.
func TestRemoteSessionActionsReachOneVerb(t *testing.T) {
	row := remoteRow()
	m := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20,
		sessions: []session{row}}
	cases := []struct {
		action string
		want   []string
	}{
		{"resume", []string{"session-remote-open", row.Server, row.CWD, row.ID,
			"--harness", "pi", "--place", "tab"}},
		// ctrl+t asks for a terminal of its own, so it keeps its own OS window whatever the
		// placement preference says.
		{"window", []string{"session-remote-open", row.Server, row.CWD, row.ID,
			"--harness", "pi", "--place", "window"}},
		{"new", []string{"session-remote-open", row.Server, row.CWD,
			"--harness", "pi", "--place", "tab"}},
	}
	for _, test := range cases {
		args, note := m.openArgs(test.action, row, false)
		if strings.Join(args, "\x00") != strings.Join(test.want, "\x00") {
			t.Errorf("%s args = %#v, want %#v", test.action, args, test.want)
		}
		if !strings.Contains(note, row.Server) {
			t.Errorf("%s note = %q, want the host it opens on", test.action, note)
		}
	}
	m.forkName = "Fix ingress docs (fork)"
	args, note := m.openArgs("fork", row, false)
	want := []string{"session-remote-open", row.Server, row.CWD, row.ID, "--harness", "pi",
		"--place", "tab", "--fork", "--name", "Fix ingress docs (fork)"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("fork args = %#v, want %#v", args, want)
	}
	if m.forkName != "" || !strings.Contains(note, "forked") {
		t.Fatalf("fork = name %q, note %q; want the name used once", m.forkName, note)
	}
	if args, _ := m.openArgs("resume", row, true); args == nil {
		t.Fatal("the placement question is the caller's, so force must not drop the action")
	}
	// The harness is the one the row came from, not the picker's current choice.
	other := row
	other.Harness = "opencode"
	other.Bytes = 0
	args, _ = m.openArgs("resume", other, false)
	if args[4] != "opencode" && args[5] != "opencode" {
		t.Fatalf("OpenCode remote args = %#v, want its own harness", args)
	}
}

// A remote project is not a session: it still starts a new chat in a remote zmx session, and
// a remote zmx row is still attached to, not resumed over SSH.
func TestRemoteRowsThatAreNotSessionsKeepTheirOwnVerbs(t *testing.T) {
	project := session{Project: "api", CWD: "/srv/api", Alive: true,
		ProjectOnly: true, Server: "build-host"}
	m := model{view: viewSessions, layout: "tab", width: 100, height: 20}
	args, _ := m.openArgs("zmx-new", project, false)
	want := []string{"zmx-remote-new", "build-host", "/srv/api", "--place", "tab"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote project args = %#v, want %#v", args, want)
	}
	zmx := remoteRow()
	zmx.ZmxOnly, zmx.ZmxName = true, "pi-home-infra"
	args, _ = m.openArgs("zmx", zmx, false)
	want = []string{"zmx-switch", "pi-home-infra", "--cwd", zmx.CWD, "--place", "tab",
		"--server", zmx.Server}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote zmx attach args = %#v, want %#v", args, want)
	}
}

// The keys that act on this machine's store or directories must not act on a remote row: the
// local sh2pil-sessions is keyed by session id, and the same id can name a session here.  Every key is sent
// through the keyboard path, which is where a rename starts.  Three keys are no longer among
// them: the delete reaches the host (TestCtrlWDeletesLocalAndRemoteTranscripts), and a shell,
// lazygit, and yazi reach it too, because they act on the host's files
// (TestRemoteDirectoryToolsRunOnTheirOwnHost, TestRemoteShellOpensOnItsOwnHost).
func TestRemoteSessionRowRefusesTheLocalOnlyKeys(t *testing.T) {
	row := remoteRow()
	keys := []tea.KeyType{tea.KeyCtrlE, tea.KeyCtrlF}
	for _, key := range keys {
		m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
			sessions: []session{row}, modal: "", pending: "", pendingID: ""}
		_, cmd := m.handleKey(tea.KeyMsg{Type: key})
		if key == tea.KeyCtrlF {
			// Forking is one of the keys that acts on the host: its name prompt then opens a
			// fork there, so the prompt itself is what this key must reach.
			if m.modal != "fork" || cmd != nil {
				t.Errorf("ctrl+f on a remote row = modal %q, cmd %v; want the fork prompt",
					m.modal, cmd)
			}
			continue
		}
		if cmd != nil || m.pending != "" || m.modal != "" {
			t.Errorf("%s on a remote row = modal %q, pending %q, cmd %v; want nothing else",
				key, m.modal, m.pending, cmd)
		}
		if !strings.Contains(m.status, row.Server) {
			t.Errorf("%s status = %q, want the host the session is on", key, m.status)
		}
	}
	// alt+y and ctrl+y are the keys that reach the action table directly.
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}}
	if _, cmd := m.copyAction("alt+y"); cmd != nil ||
		!strings.Contains(m.status, "transcript lives on") {
		t.Fatalf("alt+y on a remote row = status %q, cmd %v; want the remote transcript named",
			m.status, cmd)
	}
	if _, cmd := m.copyAction("ctrl+y"); cmd == nil {
		t.Fatal("ctrl+y on a remote row did not build the command that opens it")
	}
}

// A directory tool opens where its files are.  yazi browses the files a host holds and lazygit
// works on them, so on a row of another host both run there, in that host's own directory, and
// the picker resolves nothing about that path: it cannot see it.  A directory the host has
// reported as gone is refused by name instead of opening the host's home directory by surprise.
func TestRemoteDirectoryToolsRunOnTheirOwnHost(t *testing.T) {
	row := remoteRow()
	m := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20,
		sessions: []session{row}}
	for _, test := range []struct {
		key  string
		tool string
	}{{"lazygit", "lazygit"},
		{"yazi", "yazi"}} {
		args, note := m.openArgs(test.key, row, false)
		want := []string{"tool-remote", row.Server, row.CWD, test.tool, "--place", "tab"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s args = %#v, want %#v", test.key, args, want)
		}
		if !strings.Contains(note, row.Server) || !strings.Contains(note, "home-infra") {
			t.Errorf("%s note = %q, want the tool, its directory, and the host", test.key, note)
		}
	}
	// A project row names a directory on the host, so the same keys open there.
	project := session{Project: "api", CWD: "/srv/api", Alive: true,
		ProjectOnly: true, Server: "build-host"}
	args, _ := m.openArgs("yazi", project, false)
	want := []string{"tool-remote", "build-host", "/srv/api", "yazi", "--place", "tab"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote project yazi args = %#v, want %#v", args, want)
	}
}

// A shell is the host's own, so it opens there, in the directory that host recorded: the work a
// reader starts from a remote row is on that machine, and a terminal opened here would be in the
// wrong place.  It gets no zmx session, here or there, because only a chat is worth keeping
// alive and a zmx session is tied to the chat it runs.
func TestRemoteShellOpensOnItsOwnHost(t *testing.T) {
	row := remoteRow()
	m := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20,
		sessions: []session{row}}
	args, note := m.openArgs("shell", row, false)
	want := []string{"shell-remote", row.Server, row.CWD, "--place", "tab"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote shell args = %#v, want %#v", args, want)
	}
	if !strings.Contains(note, row.Server) || !strings.Contains(note, "home-infra") {
		t.Fatalf("remote shell note = %q, want the directory and the host", note)
	}
	// ctrl+x is the key, and it goes through the keyboard path like every other one.
	key := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}, modal: "", pending: ""}
	if _, cmd := key.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX}); cmd == nil {
		t.Fatal("ctrl+x on a remote row started nothing")
	}
	// A project row names a directory on the host, so its shell opens there as well, and
	// ctrl+t is that same shell in a terminal of its own.
	project := session{Project: "api", CWD: "/srv/api", Alive: true,
		ProjectOnly: true, Server: "build-host"}
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyCtrlX}, {Type: tea.KeyCtrlT}} {
		fresh := model{view: viewSessions, layout: "tab", width: 120, height: 20,
			sessions: []session{project}}
		if _, cmd := fresh.handleKey(msg); cmd == nil {
			t.Errorf("%s on a remote project started nothing", msg)
		}
		args, _ := fresh.openArgs("shell", project, false)
		want := []string{"shell-remote", "build-host", "/srv/api", "--place", "tab"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s args = %#v, want %#v", msg, args, want)
		}
	}
	// A directory the host has reported as gone stops the shell by name: there is nothing
	// there to open, and the host said so when it read the row.
	gone := row
	gone.Alive = false
	quiet := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{gone}}
	if _, cmd := quiet.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX}); cmd != nil {
		t.Fatalf("ctrl+x on a gone directory = cmd %v, want nothing", cmd)
	}
	if !strings.Contains(quiet.status, gone.CWD) || !strings.Contains(quiet.status, gone.Server) {
		t.Fatalf("ctrl+x on a gone directory = status %q, want the directory and the host",
			quiet.status)
	}
	// The helper is told an empty directory then, which means that host's home directory.
	args, _ = m.openArgs("shell", gone, false)
	want = []string{"shell-remote", gone.Server, "", "--place", "tab"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("gone shell args = %#v, want %#v", args, want)
	}
}

// The project editor opens on the host that holds the directory, in that host's own editor: an
// editor is a machine's tool with its own plugins and clipboard, so this machine names none of
// it.  The title the reader gives the window still travels, because a tab title is read here.
func TestRemoteProjectEditorOpensOnItsOwnHost(t *testing.T) {
	row := remoteRow()
	m := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20,
		sessions: []session{row}}
	args, note := m.openArgs("project-editor", row, false)
	want := []string{"editor-remote", row.Server, row.CWD, "--label", "nvim home-infra",
		"--place", "tab"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote editor args = %#v, want %#v", args, want)
	}
	if !strings.Contains(note, row.Server) {
		t.Fatalf("remote editor note = %q, want the host named", note)
	}
	// The title the modal collected is the one the window gets.
	named := m
	named.editorLabel = "api notes"
	args, _ = named.openArgs("project-editor", row, false)
	if strings.Join(args, "\x00") != strings.Join([]string{"editor-remote", row.Server,
		row.CWD, "--label", "api notes", "--place", "tab"}, "\x00") {
		t.Fatalf("titled remote editor args = %#v, want the title the reader gave", args)
	}
	// alt+e is the key, and it goes through the modal first, which is where the title is
	// asked for; the modal is what the key must reach.
	key := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}}
	if _, cmd := key.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}, Alt: true}); cmd != nil || key.modal != "editor-title" {
		t.Fatalf("alt+e on a remote row = modal %q, cmd %v; want the title prompt", key.modal, cmd)
	}
	// A remote project row hands its directory to its host's editor as well.
	project := session{Project: "api", CWD: "/srv/api", Alive: true,
		ProjectOnly: true, Server: "build-host"}
	fresh := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{project}}
	if _, cmd := fresh.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}, Alt: true}); cmd != nil || fresh.modal != "editor-title" {
		t.Fatalf("alt+e on a remote project = modal %q, cmd %v; want the title prompt",
			fresh.modal, cmd)
	}
	// A directory the host has reported as gone stops it by name.
	gone := row
	gone.Alive = false
	quiet := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{gone}}
	if _, cmd := quiet.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}, Alt: true}); cmd != nil {
		t.Fatalf("alt+e on a gone directory = cmd %v, want nothing", cmd)
	}
	if !strings.Contains(quiet.status, gone.CWD) || !strings.Contains(quiet.status, gone.Server) {
		t.Fatalf("alt+e on a gone directory = status %q, want the directory and the host",
			quiet.status)
	}
}

// A remote project row's new chat goes to that host in whichever store is in force.  Pi is the
// one exception, and not here: only a Pi chat can run inside a zmx session on that host, so a
// Pi new chat becomes `zmx-new`, and a store that has no such session comes through this open
// with no session id.  Getting this wrong asked this machine for a host's directory.
func TestRemoteProjectNewChatReachesTheHost(t *testing.T) {
	project := session{Project: "api", CWD: "/srv/api", Alive: true, ProjectOnly: true,
		Server: "build-host"}
	for _, harness := range []string{"claude", "codex", "opencode"} {
		m := model{view: viewSessions, layout: "tab", harness: harness, width: 120, height: 20}
		args, note := m.openArgs("new", project, false)
		want := []string{"session-remote-open", "build-host", "/srv/api",
			"--harness", harness, "--place", "tab"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s project new args = %#v, want %#v", harness, args, want)
		}
		if !strings.Contains(note, "build-host") {
			t.Errorf("%s project new note = %q, want the host named", harness, note)
		}
		// ctrl+a is the key that has to reach that open, in this store as in any other.
		key := m
		key.sessions = []session{project}
		if _, cmd := key.startAction("ctrl+a"); cmd == nil {
			t.Errorf("ctrl+a on a remote project in the %s store started nothing", harness)
		}
	}
	// ctrl+a is the key, and with the Pi store in force it starts the chat in a zmx session
	// there, which the host owns: the picker still names that host.
	pi := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20,
		sessions: []session{project}}
	if _, cmd := pi.startAction("ctrl+a"); cmd == nil {
		t.Fatal("ctrl+a on a remote project started nothing")
	}
	args, _ := pi.openArgs("zmx-new", project, false)
	if want := []string{"zmx-remote-new", "build-host", "/srv/api", "--place", "tab"}; strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("pi project new args = %#v, want %#v", args, want)
	}
	// beginAction is where every route to a new chat meets, so the Pi project row is turned
	// into `zmx-new` there and not by the key that asked for it.  With the placement question
	// set to ask, the decision is visible as the action the question will run.
	ask := model{view: viewSessions, layout: "ask", harness: "pi", width: 120, height: 20}
	if _, cmd := ask.beginAction("new", project, false); cmd != nil || ask.askAction != "zmx-new" {
		t.Fatalf("pi project new chat = action %q, cmd %v; want the remote zmx verb",
			ask.askAction, cmd)
	}
	if ask.askSession.Server != "build-host" {
		t.Fatalf("pi project new chat = row on %q, want the host it came from", ask.askSession.Server)
	}
	// The Pi open with no session, if anything ever asks for it, is refused rather than run
	// here: that condition is what keeps a host's path away from this machine's own verb.
	if args, note := pi.openArgs("new", project, false); args != nil ||
		!strings.Contains(note, "build-host") {
		t.Fatalf("pi project open-new = args %#v note %q, want a refusal naming the host",
			args, note)
	}
}

// The next-step menu is the route a reader takes when a key is rerouted or the mode is prompt,
// so a new chat chosen there must reach the host exactly as ctrl+a does.  The menu row is what
// failed: it asked for the `new` verb, which is this machine's own, and the guard refused it.
func TestRemoteProjectMenuNewChatReachesTheHost(t *testing.T) {
	project := session{Project: "api", CWD: "/srv/api", Alive: true, ProjectOnly: true,
		Server: "build-host"}
	for _, harness := range []string{"pi", "claude", "opencode", "codex"} {
		m := model{view: viewSessions, layout: "tab", harness: harness, width: 120, height: 20,
			sessions: []session{project}}
		if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'.'}}); cmd != nil ||
			m.modal != "tools" || len(m.toolChoices) == 0 {
			t.Fatalf("%s: the dot key = modal %q choices %v, want the next-step menu",
				harness, m.modal, m.toolChoices)
		}
		if m.toolChoices[0] != "new" {
			t.Fatalf("%s: first menu row = %q, want the new chat", harness, m.toolChoices[0])
		}
		if _, cmd := m.chooseTool(0); cmd == nil {
			t.Errorf("%s: the menu's new chat started nothing; status %q", harness, m.status)
		}
	}
	// With the placement question in force, the menu route's action is visible after the
	// choice: the Pi store reaches the host through the zmx verb, and the others through the
	// remote open with no session id, which is the same pair ctrl+a produces.
	for _, test := range []struct{ harness, want string }{
		{"pi", "zmx-new"}, {"claude", "new"},
	} {
		m := model{view: viewSessions, layout: "ask", harness: test.harness, width: 120,
			height: 20, sessions: []session{project}}
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'.'}})
		if _, cmd := m.chooseTool(0); cmd != nil || m.askAction != test.want {
			t.Errorf("%s: menu new chat = action %q, cmd %v; want %q",
				test.harness, m.askAction, cmd, test.want)
		}
		if m.askSession.Server != "build-host" {
			t.Errorf("%s: menu new chat = row on %q, want the host",
				test.harness, m.askSession.Server)
		}
	}
}

// Every action a remote row can be asked for either reaches that host or is refused.  A local
// verb given a host's directory is the failure this pins: it fails on a path that is not here,
// or, worse, acts on a different directory that happens to carry the same path on this machine.
func TestNoRemoteRowIsEverGivenALocalVerb(t *testing.T) {
	project := session{Project: "api", CWD: "/srv/api", Alive: true, ProjectOnly: true,
		Server: "build-host"}
	zmx := remoteRow()
	zmx.ZmxOnly, zmx.ZmxName = true, "pi-home-infra"
	rows := map[string]session{"session": remoteRow(), "project": project, "zmx": zmx}
	actions := []string{"resume", "window", "fork", "new", "shell", "lazygit", "yazi",
		"project-editor", "editor", "zmx", "zmx-new"}
	for name, row := range rows {
		for _, action := range actions {
			m := model{view: viewSessions, layout: "tab", harness: "claude", width: 120,
				height: 20, editorLabel: "api notes"}
			args, _ := m.openArgs(action, row, false)
			if args == nil {
				continue
			}
			if !reachesHost(args) {
				t.Errorf("%s row %q was given the local verb %v", name, action, args)
			}
		}
	}
	// The same holds for the Pi store, whose project row has one more remote verb of its own.
	for _, action := range []string{"new", "zmx-new"} {
		m := model{view: viewSessions, layout: "tab", harness: "pi", width: 120, height: 20}
		args, _ := m.openArgs(action, project, false)
		if args != nil && !reachesHost(args) {
			t.Errorf("pi project %q was given the local verb %v", action, args)
		}
	}
}

// The keyboard path is what a reader's key really goes through, so it is what pins that
// ctrl+g and alt+f act on the host, and that a gone directory stops them there.
func TestRemoteDirectoryToolKeysReachTheHost(t *testing.T) {
	row := remoteRow()
	cases := []struct {
		msg  tea.KeyMsg
		tool string
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlG}, "lazygit"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true}, "yazi"},
	}
	for _, test := range cases {
		m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
			sessions: []session{row}, modal: "", pending: ""}
		_, cmd := m.handleKey(test.msg)
		if cmd == nil {
			t.Errorf("%s on a remote row started nothing", test.msg)
		}
	}
	gone := row
	gone.Alive = false
	for _, test := range cases {
		m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
			sessions: []session{gone}}
		if _, cmd := m.handleKey(test.msg); cmd != nil {
			t.Errorf("%s on a row whose directory is gone = cmd %v, want nothing", test.msg, cmd)
		}
		for _, want := range []string{gone.CWD, gone.Server} {
			if !strings.Contains(m.status, want) {
				t.Errorf("%s status = %q, want %q", test.msg, m.status, want)
			}
		}
	}
	// A session whose store recorded no directory is refused in its own words, because there
	// is no path to name.
	nameless := row
	nameless.CWD, nameless.Alive = "", false
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{nameless}}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlG}); cmd != nil ||
		!strings.Contains(m.status, "recorded no project directory") {
		t.Fatalf("ctrl+g on a nameless remote row = status %q, cmd %v", m.status, cmd)
	}
	if line := goneProjectLine(nameless); !strings.Contains(line, nameless.Server) {
		t.Fatalf("goneProjectLine = %q, want the host named", line)
	}
}

// Deleting a transcript acts on the store that holds it: the local sh2pil-sessions deletes a session here,
// and a session on another host is deleted there by that host's own sh2pil-sessions, which is what resolves
// the id against the sessions that host has.  Both ask first and name what goes.
func TestCtrlWDeletesLocalAndRemoteTranscripts(t *testing.T) {
	here := session{ID: "ses_here", Name: "Fix ingress docs", Project: "repo",
		CWD: "/tmp/repo", Alive: true}
	m := model{view: viewSessions, width: 120, height: 20, sessions: []session{here},
		live: map[string]liveInfo{}}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		m.pending != "delete" || m.pendingID != here.ID || m.pendingServer != "" {
		t.Fatalf("ctrl+w = pending %q for %q on %q, cmd %v; want a delete question",
			m.pending, m.pendingID, m.pendingServer, cmd)
	}
	if !strings.Contains(m.status, "delete") || !strings.Contains(m.status, "Fix ingress docs") {
		t.Fatalf("question = %q, want the session named for deletion", m.status)
	}
	helper, args, note := m.deleteCommand(here)
	if helper != "sh2pil-sessions" || !reflect.DeepEqual(args, []string{"delete", here.ID}) {
		t.Fatalf("local delete = %s %v, want the local sh2pil-sessions", helper, args)
	}
	if !strings.Contains(note, "deleted") {
		t.Fatalf("local delete note = %q, want it named as a delete", note)
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd == nil {
		t.Fatal("answering yes did not run the local delete")
	}

	// A session that is running is carried through as --force, because the question has already
	// been asked and answered.
	running := model{view: viewSessions, width: 120, height: 20, sessions: []session{here},
		live: map[string]liveInfo{here.ID: {ID: here.ID, Owner: "kitty window 12"}}}
	if _, _ = running.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); !strings.Contains(running.status, "running") {
		t.Fatalf("question for a running session = %q, want it named as running", running.status)
	}
	if _, args, _ = running.deleteCommand(here); args[len(args)-1] != "--force" {
		t.Fatalf("running delete args = %v, want --force on the end", args)
	}

	// On another host the delete is that host's, and the question says where the session is.
	row := remoteRow()
	remote := model{view: viewSessions, width: 120, height: 20, sessions: []session{row},
		live: map[string]liveInfo{}}
	if _, cmd := remote.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		remote.pending != "delete" || remote.pendingID != row.ID ||
		remote.pendingServer != row.Server {
		t.Fatalf("ctrl+w on a remote row = pending %q for %q on %q, cmd %v; want the host named",
			remote.pending, remote.pendingID, remote.pendingServer, cmd)
	}
	if !strings.Contains(remote.status, row.Server) {
		t.Fatalf("question = %q, want the host the transcript lives on", remote.status)
	}
	helper, args, note = remote.deleteCommand(row)
	want := []string{"session-remote-delete", row.Server, row.ID, "--harness", "pi"}
	if helper != "sh2pil-open" || !reflect.DeepEqual(args, want) {
		t.Fatalf("remote delete = %s %v, want %v", helper, args, want)
	}
	if !strings.Contains(note, row.Server) {
		t.Fatalf("remote delete note = %q, want the host named", note)
	}
	if _, cmd := remote.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd == nil {
		t.Fatal("answering yes did not run the delete on the host")
	}
}

// A chat that runs inside a zmx session on the host is reached by attaching to that session, not
// by starting a second pi beside it: the process that already writes the transcript keeps it, and
// no question about a second writer is asked.  A live chat with no such session still asks,
// because a new process is the only way into it from here.
func TestRemoteLiveZmxChatAttachesInsteadOfAsking(t *testing.T) {
	chat := remoteRow()
	chat.Live, chat.ZmxName = true, "pi-01a10260"
	attach, ok := remoteAttachRow(chat)
	if !ok {
		t.Fatal("a live chat in a zmx session has no attach row")
	}
	if !attach.ZmxOnly || attach.ZmxName != chat.ZmxName {
		t.Fatalf("attach row = %#v, want the same chat as a zmx row", attach)
	}
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{chat}}
	if _, cmd := m.startAction("enter"); cmd == nil || m.pending != "" {
		t.Fatalf("enter on a live remote chat in zmx = pending %q, cmd %v; want it attached",
			m.pending, cmd)
	}
	args, _ := m.openArgs("zmx", attach, false)
	want := []string{"zmx-switch", chat.ZmxName, "--cwd", chat.CWD, "--place", "tab",
		"--server", chat.Server}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remote attach args = %#v, want %#v", args, want)
	}
	// A second terminal is a second client of the same session, which is what ctrl+t asks for.
	plain := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{chat}}
	if _, cmd := plain.startAction("ctrl+t"); cmd == nil || plain.pending != "" {
		t.Fatalf("ctrl+t on a live remote chat in zmx = pending %q, cmd %v; want a second "+
			"client", plain.pending, cmd)
	}

	// A live chat that no zmx session carries has no local answer, so the question stays.
	bare := remoteRow()
	bare.Live = true
	if _, ok := remoteAttachRow(bare); ok {
		t.Fatal("a live chat with no zmx session offered an attach row")
	}
	ask := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{bare}}
	if _, cmd := ask.startAction("enter"); cmd != nil || ask.pending != "resume" {
		t.Fatalf("enter on a live remote chat with no zmx session = pending %q, cmd %v; want a "+
			"question", ask.pending, cmd)
	}
}

// A remote session always opens, because no window on another host can be switched to.  When
// the host reports that its own pi already owns the session, the reader is asked first: a
// second pi on one transcript interleaves writes to it.
func TestRemoteSessionAsksBeforeASecondWriter(t *testing.T) {
	row := remoteRow()
	row.Live = true
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}}
	if _, cmd := m.startAction("enter"); cmd != nil || m.pending != "resume" ||
		m.pendingServer != row.Server || m.pendingID != row.ID {
		t.Fatalf("enter on a live remote row = pending %q/%q/%q, cmd %v; want a question",
			m.pending, m.pendingID, m.pendingServer, cmd)
	}
	if !strings.Contains(m.status, "second") || !strings.Contains(m.status, row.Server) {
		t.Fatalf("question = %q, want the host and the second writer", m.status)
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd == nil {
		t.Fatal("answering yes did not open the session")
	}
	m = model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}}
	m.startAction("ctrl+t")
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}); cmd != nil ||
		!strings.Contains(m.status, "cancelled") {
		t.Fatalf("answering no ran something: status %q, cmd %v", m.status, cmd)
	}
	// A session that host says nothing runs opens without a question.
	idle := remoteRow()
	m = model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{idle}}
	if _, cmd := m.startAction("enter"); cmd == nil || m.pending != "" {
		t.Fatalf("enter on an idle remote row = pending %q, cmd %v; want it opened",
			m.pending, cmd)
	}
}

// A host that is this machine answers with the same session ids, so the local live map must
// not mark the remote rows or claim one of their windows.
func TestRemoteRowIgnoresTheLocalLiveMap(t *testing.T) {
	row := remoteRow()
	local := row
	local.Server = ""
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row, local},
		live: map[string]liveInfo{row.ID: {ID: row.ID, Certain: true,
			Owner: "kitty window 3"}}}
	if _, live := m.rowLive(row); live {
		t.Fatal("the local live map marked a remote row")
	}
	if m.shown(row) {
		t.Fatal("a remote row was reported as shown by a local window")
	}
	if info, live := m.rowLive(local); !live || info.Owner != "kitty window 3" {
		t.Fatalf("local row live = %#v/%v, want the local window", info, live)
	}
	lines := strings.Split(ansi.Strip(m.projectsView()), "\n")
	if strings.Contains(lines[0], "●") {
		t.Fatalf("remote row = %q, want no local live mark", lines[0])
	}
	if info, live := m.rowLive(row); live && info.Owner != "" {
		t.Fatalf("remote live info = %#v, want no window claim", info)
	}
}

// A fork from a remote row keeps the prompt's name and reaches the host: a fork writes a
// transcript of its own there, so a session that host already runs is no obstacle.
func TestRemoteForkTakesTheNameThePromptOffered(t *testing.T) {
	row := remoteRow()
	row.Live = true
	m := model{view: viewSessions, layout: "tab", width: 120, height: 20,
		sessions: []session{row}, cache: map[string]preview{}}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF}); cmd != nil || m.modal != "fork" {
		t.Fatalf("ctrl+f = modal %q, cmd %v; want the fork prompt", m.modal, cmd)
	}
	if m.name.text != "Fix ingress docs (fork)" {
		t.Fatalf("fork prompt = %q, want the name it offers", m.name.text)
	}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.modal != "" || m.forkName != "" || m.pending != "" {
		t.Fatalf("enter = modal %q, fork %q, pending %q, cmd %v; want the fork started",
			m.modal, m.forkName, m.pending, cmd)
	}
}

// The preview reads a remote transcript on its own host, through the same `sh2pil-sessions show` the
// OpenCode preview uses, and reports the path as the remote one it is.
func TestRemoteRowPreviewReadsTheTranscriptOnItsHost(t *testing.T) {
	row := remoteRow()
	m := model{view: viewSessions, width: 120, height: 20, showPrev: true,
		sessions: []session{row}, cache: map[string]preview{}}
	command := m.previewCommand(row)
	line := strings.Join(command.Args, " ")
	for _, want := range []string{"session-remote-show", row.Server, row.ID, "--harness", "pi",
		"--tail", "--file " + row.File} {
		if !strings.Contains(line, want) {
			t.Errorf("remote preview command = %q, want %q", line, want)
		}
	}
	// A row without a transcript path (an OpenCode session, or a chat whose file is gone)
	// leaves the path out: the read then resolves the session on its host.
	pathless := row
	pathless.File = ""
	if got := strings.Join(m.previewCommand(pathless).Args, " "); strings.Contains(got, "--file") {
		t.Errorf("remote preview command = %q, want no path for a session without one", got)
	}
	if cmd := m.refreshPreview(); cmd == nil {
		t.Fatal("a remote row scheduled no preview read")
	}
	rendered := ansi.Strip(m.previewView())
	if !strings.Contains(rendered, row.Server+":"+row.CWD) {
		t.Fatalf("remote preview = %q, want the host and the remote path", rendered)
	}
	// A zmx row that carries no chat has no transcript to read on that host either.
	bare := session{Server: row.Server, ZmxOnly: true, ZmxName: "shell", CWD: "/srv"}
	m.sessions = []session{bare}
	if cmd := m.refreshPreview(); cmd != nil {
		t.Fatal("a remote zmx row with no chat scheduled a transcript read")
	}
}

// A zmx session on another host is named by the chat its `pi=` label points at, which only
// that host can resolve: the same read that lists its sessions names its zmx rows too.
func TestRemoteZmxRowsTakeTheChatTheirHostReports(t *testing.T) {
	entries := []zmxSession{
		{Name: "pi-aaa", Pi: "aaa", Created: 200, CWD: "/srv/api", Current: true},
		{Name: "pi-bbb", Pi: "bbb", Created: 100, CWD: "/srv/api"},
		{Name: "shell", Created: 50, CWD: "/srv/api"},
	}
	chats := map[string]session{
		"aaa": {ID: "aaa", Name: "Fix ingress docs", File: "/srv/api/sessions/aaa.jsonl"},
		"bbb": {ID: "bbb"},
	}
	rows := remoteZmxRows("build-host", entries, chats)
	if len(rows) != 3 {
		t.Fatalf("remote zmx rows = %#v, want one row per session", rows)
	}
	named := rows[0]
	if named.Name != "Fix ingress docs" || named.File != "/srv/api/sessions/aaa.jsonl" ||
		named.ID != "aaa" || named.Server != "build-host" || named.Current {
		t.Fatalf("named remote zmx row = %#v, want the chat its label points at", named)
	}
	// An unnamed chat leaves the row on the name zmx knows, which is the only name it has.
	unnamed := rows[1]
	if unnamed.Name != "pi-bbb" || unnamed.ID != "bbb" || unnamed.File != "" {
		t.Fatalf("unnamed remote zmx row = %#v, want the zmx name and no transcript", unnamed)
	}
	// A session with no label, or one whose chat that host no longer has, stands alone.
	bare := rows[2]
	if bare.Name != "shell" || bare.ID != "" || bare.File != "" {
		t.Fatalf("labelless remote zmx row = %#v, want its own name only", bare)
	}
	list := model{view: viewZmx, width: 100, height: 20}
	if !bare.ZmxOnly || !strings.Contains(ansi.Strip(list.rowView(bare, nil, true)), "shell") {
		t.Fatalf("labelless remote zmx row = %#v, want its own name drawn", bare)
	}
}

// fakeHelper writes a helper the loader can run: sh2pil calls its siblings through the
// interpreter the build script baked in, so a test helper is a Python script in a directory
// of its own.
func fakeHelper(t *testing.T, directory, name, script string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// localList is what the fake `sh2pil-sessions` answers a local read with: one session and the zoxide
// projects the picker offers beside it.
const localPibScript = `import json, sys
if sys.argv[1] == "projects":
    print(json.dumps(["/project/docs"]))
    sys.exit(0)
print(json.dumps([{
    "id": "local1", "harness": "pi", "name": "Local chat", "project": "docs",
    "cwd": "/project/docs", "alive": True, "live": False, "bytes": 2048,
    "modified": 1791041092, "file": "/sessions/local1.jsonl"}]))
`

// remotePibOpenScript answers for two hosts: one with a session its zmx session carries, and
// one that cannot say anything at all.
const remotePibOpenScript = `import json, sys
command, server = sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else ""
if command == "live":
    print("[]"); sys.exit(0)
if server == "bad-host":
    print("sh2pil-sessions is not installed on this host (PATH and ~/.local/bin)", file=sys.stderr); sys.exit(127)
if command == "zmx-remote-projects":
    print(json.dumps(["/srv/api"]))
    sys.exit(0)
if command == "session-remote-list":
    print(json.dumps([{
        "id": "aaa", "harness": "pi", "name": "Remote ingress docs", "project": "api",
        "cwd": "/srv/api", "alive": True, "live": True, "bytes": 4096,
        "modified": 1791041092, "file": "/srv/api/sessions/aaa.jsonl"}]))
elif command == "zmx-remote-list":
    print(json.dumps([
        {"name": "pi-aaa", "chat": "", "pi": "aaa", "project": "api", "cmd": "pi",
         "cwd": "/srv/api", "file": "", "clients": 1, "created": 200, "current": True},
        {"name": "shell", "chat": "", "pi": "", "project": "api", "cmd": "",
         "cwd": "/srv/api", "file": "", "clients": 0, "created": 100, "current": False}]))
elif command == "zmx-list":
    print("[]")
`

// A failure the reader has to act on is the line that explains it: ssh writes its identity
// noise first, and a Python helper ends with the error that stopped it.
func TestRemoteFailureReportsTheLineThatExplainsIt(t *testing.T) {
	stderr := "sign_and_send_pubkey: signing failed for ED25519-SK\"/key\": device not found\n" +
		"user@build-host: Permission denied (publickey,password,keyboard-interactive).\n"
	if got := remoteFailure(&exec.ExitError{Stderr: []byte(stderr)}); !strings.Contains(got, "Permission denied") {
		t.Fatalf("failure = %q, want the line that says why the connection failed", got)
	}
	missing := &exec.ExitError{Stderr: []byte("sh2pil-sessions is not installed on this host (PATH and ~/.local/bin)\n")}
	if got := remoteFailure(missing); got != "sh2pil-sessions is not installed on this host (PATH and ~/.local/bin)" {
		t.Fatalf("failure = %q, want the helper's own line", got)
	}
	if got := remoteFailure(errors.New("exit status 1")); got != "exit status 1" {
		t.Fatalf("failure with no stderr = %q, want the error itself", got)
	}
	if got := remoteFailure(&exec.ExitError{Stderr: []byte("  \n\n")}); got != "" {
		t.Fatalf("failure with blank stderr = %q, want an empty line", got)
	}
}

// loadTarget is the one loader: the local machine and every host differ by the target alone.
// A host is asked for its own sessions and its own zmx sessions, and the rows of a host that
// could not answer are reported in the note while the rest of the read still arrives.
func TestLoadTargetReadsOneHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".config", "sh2pil")
	if err := os.MkdirAll(settings, 0o755); err != nil {
		t.Fatalf("create the settings directory: %v", err)
	}
	config := "zmx_servers: build-host, bad-host\n"
	if err := os.WriteFile(filepath.Join(settings, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write the shared config: %v", err)
	}
	helperDir := t.TempDir()
	fakeHelper(t, helperDir, "sh2pil-sessions", localPibScript)
	fakeHelper(t, helperDir, "sh2pil-open", remotePibOpenScript)

	m := model{helperDir: helperDir, harness: "pi"}
	message, ok := m.loadTarget(target{Server: "build-host"})().(targetMsg)
	if !ok {
		t.Fatal("the target read returned something other than a target message")
	}
	if message.label != "build-host" || message.data.Err != "" {
		t.Fatalf("host read = %#v, want it to succeed and name the host", message)
	}
	// The host's project list and its session row are joined into one group.
	if len(message.data.Groups) != 1 {
		t.Fatalf("groups = %#v, want the one project it reported", message.data.Groups)
	}
	group := message.data.Groups[0]
	if group.Project != "api" || group.CWD != "/srv/api" || group.Server != "build-host" {
		t.Fatalf("group = %#v, want the host's project", group)
	}
	if len(group.Sessions) != 1 {
		t.Fatalf("group sessions = %#v, want the host's session under it", group.Sessions)
	}
	if row := group.Sessions[0]; row.ID != "aaa" || row.Server != "build-host" || !row.Live ||
		row.Name != "Remote ingress docs" || row.File != "/srv/api/sessions/aaa.jsonl" {
		t.Fatalf("remote row = %#v, want the host's own row with its host named", row)
	}

	// The host's session list is what names a zmx row it carries; a session with no label
	// stands on the zmx name instead.
	if len(message.data.Zmx) != 2 {
		t.Fatalf("zmx rows = %#v, want the two sessions the host reports", message.data.Zmx)
	}
	named := message.data.Zmx[0]
	if named.Name != "Remote ingress docs" || named.File != "/srv/api/sessions/aaa.jsonl" ||
		named.ID != "aaa" || named.Server != "build-host" || named.Current {
		t.Fatalf("remote zmx row = %#v, want the chat its host named", named)
	}
	if bare := message.data.Zmx[1]; bare.Name != "shell" || bare.ID != "" || bare.File != "" {
		t.Fatalf("labelless remote zmx row = %#v, want its own name only", bare)
	}
	// The session that runs inside a zmx session is marked from the same read.
	if group.Sessions[0].ZmxName != "pi-aaa" {
		t.Fatalf("session zmx mark = %q, want the session that carries it",
			group.Sessions[0].ZmxName)
	}

	// A host that cannot answer reports why, and nothing else is invented for it.
	failed, _ := m.loadTarget(target{Server: "bad-host"})().(targetMsg)
	if failed.label != "bad-host" || failed.data.Err == "" {
		t.Fatalf("host that could not answer = %#v, want a failure", failed)
	}
	if !strings.Contains(failed.data.Err, "sh2pil-sessions is not installed") {
		t.Fatalf("failure = %q, want the helper's own line", failed.data.Err)
	}
}

// TestLoadTargetReadsThisMachine pins the local half of the same loader: no host is named on
// any row, and the target is this machine.
func TestLoadTargetReadsThisMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := filepath.Join(home, ".config", "sh2pil")
	if err := os.MkdirAll(settings, 0o755); err != nil {
		t.Fatalf("create the settings directory: %v", err)
	}
	helperDir := t.TempDir()
	fakeHelper(t, helperDir, "sh2pil-sessions", localPibScript)
	fakeHelper(t, helperDir, "sh2pil-open", remotePibOpenScript)

	m := model{helperDir: helperDir, harness: "pi"}
	message, ok := m.loadTarget(target{})().(targetMsg)
	if !ok || message.label != "local" || message.data.Err != "" {
		t.Fatalf("local read = %#v, want it to succeed and name this machine", message)
	}
	if len(message.data.Groups) != 1 || message.data.Groups[0].Project != "docs" {
		t.Fatalf("groups = %#v, want the local project", message.data.Groups)
	}
	row := message.data.Groups[0].Sessions[0]
	if row.ID != "local1" || row.Server != "" {
		t.Fatalf("local row = %#v, want the local session with no host", row)
	}
}

// The rows a host reports are the picker's own row type, so its answer reaches the list the
// same way the local store does, harness field and all.
func TestRemoteSessionRowsSurviveTheJSONRoundTrip(t *testing.T) {
	body := `[{"id":"aaa","harness":"opencode","name":"Docs","project":"api","cwd":"/srv/api",
	          "alive":false,"live":false,"bytes":0,"modified":1791041092,"file":""}]`
	var rows []session
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("unmarshal a host's session row: %v", err)
	}
	if len(rows) != 1 || rows[0].Harness != "opencode" || rows[0].Alive || rows[0].Bytes != 0 {
		t.Fatalf("rows = %#v, want the harness and state the host reported", rows)
	}
}

// Killing a remote zmx session is a host-side act, so the question names that host and the
// confirmation carries it to the helper.
func TestRemoteZmxCtrlWKillsOnItsOwnHost(t *testing.T) {
	row := remoteRow()
	row.ZmxOnly, row.ZmxName = true, "pi-home-infra"
	row.ID, row.File, row.Name = "", "", "pi-home-infra"
	m := model{view: viewZmx, zmxRows: []session{row}}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		m.pending != "zmx-kill" || m.pendingID != "pi-home-infra" ||
		m.pendingServer != row.Server {
		t.Fatalf("ctrl+w = pending %q for %q on %q, cmd %v; want a question naming the host",
			m.pending, m.pendingID, m.pendingServer, cmd)
	}
	if !strings.Contains(m.status, row.Server) {
		t.Fatalf("question = %q, want the host the session runs on", m.status)
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}); cmd != nil ||
		m.pendingServer != "" {
		t.Fatalf("answering no ran something: status %q, cmd %v", m.status, cmd)
	}
}
