package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// zmxRow is one picker row built from `sh2pil-open zmx-list --json`.
func zmxRow() session {
	return session{
		ID: "01a10260", Harness: "pi", Name: "Fix ingress docs", Project: "home-infra",
		CWD: "/Users/me/personal/home-infra", File: "/sessions/2026-10-03_01a10260.jsonl",
		Alive: true, Mod: 1791041092, ZmxOnly: true, ZmxName: "pi-01a1002e-2",
		Clients: 1, Command: "pi --fork 01a1002e --name 'Fix ingress docs'",
	}
}

// sessionInZmx is one picker row for a chat that runs inside a zmx session: the same chat
// as zmxRow, seen from the session list, where the zmx name is a mark and not the row.
func sessionInZmx() session {
	return session{
		ID: "01a10260", Harness: "pi", Name: "Fix ingress docs", Project: "home-infra",
		CWD: "/Users/me/personal/home-infra", File: "/sessions/2026-10-03_01a10260.jsonl",
		Alive: true, Mod: 1791041092, Bytes: 2 * 1024 * 1024, ZmxName: "pi-01a1002e-2",
	}
}

func TestZmxRowsCarryTheChatAndTheHandle(t *testing.T) {
	entries := []zmxSession{
		{Name: "pi-aaa", Chat: "Fix ingress docs", Pi: "aaa", Project: "home-infra",
			CWD: "/project", File: "/sessions/aaa.jsonl", Clients: 1, Created: 100,
			Command: "pi --session aaa", Current: true},
		{Name: "shell", CWD: "/Users/me", Created: 50},
	}
	rows := zmxRows(entries)
	if len(rows) != 2 {
		t.Fatalf("zmxRows returned %d rows, want 2", len(rows))
	}
	first := rows[0]
	if !first.ZmxOnly || first.ZmxName != "pi-aaa" || first.Name != "Fix ingress docs" ||
		first.ID != "aaa" || first.File != "/sessions/aaa.jsonl" || first.Clients != 1 ||
		first.Mod != 100 || first.Command != "pi --session aaa" || !first.Alive {
		t.Fatalf("zmx row = %#v, want the chat, its transcript, and the handle", first)
	}
	// A session with no `pi=` label stands on its own name and directory instead.
	second := rows[1]
	if second.Name != "shell" || second.Project != "me" || second.ID != "" ||
		second.File != "" || second.Clients != 0 {
		t.Fatalf("labelless zmx row = %#v, want its own name and the directory's base", second)
	}
}

func TestRemoteZmxRowsHaveNoLocalChatMetadataAndStayServerScoped(t *testing.T) {
	rows := zmxRows([]zmxSession{{Name: "dev", CWD: "/srv/project", Clients: 1}})
	rows[0].Server = "build-host"
	rows[0].ID, rows[0].File = "", ""
	rows[0].Name = rows[0].ZmxName
	m := model{view: viewZmx, layout: "tab", width: 100, height: 20, zmxRows: rows}
	if rows[0].Server != "build-host" || rows[0].File != "" || rows[0].Name != "dev" {
		t.Fatalf("remote row = %#v, want only zmx session data and server", rows[0])
	}
	if got := ansi.Strip(m.zmxView()); !strings.Contains(got, "dev") {
		t.Fatalf("remote row display = %q, want its session name", got)
	}
	if cmd := m.refreshPreview(); cmd != nil {
		t.Fatal("remote row scheduled a local preview or scrollback read")
	}
	args, _ := m.openArgs("zmx", rows[0], false)
	want := []string{"zmx-switch", "dev", "--cwd", "/srv/project", "--place", "tab",
		"--server", "build-host"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("remote attach args = %#v, want %#v", args, want)
	}
	args, _ = m.openArgs("zmx-new", rows[0], false)
	if want := []string{"zmx-remote-new", "build-host", "/srv/project", "--place", "tab"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("remote new-chat args = %#v, want %#v", args, want)
	}
	if _, cmd := m.startZmxAction("ctrl+a", rows[0]); cmd != nil ||
		!strings.Contains(m.status, "project group") {
		t.Fatalf("ctrl+a on a remote zmx row = status %q, cmd %v; want group guidance",
			m.status, cmd)
	}
	if _, cmd := m.startZmxAction("ctrl+w", rows[0]); cmd != nil ||
		m.pending != "zmx-kill" || m.pendingID != "dev" || m.pendingServer != "build-host" {
		t.Fatalf("remote kill action = pending %q for %q on %q, cmd %v; want confirmation",
			m.pending, m.pendingID, m.pendingServer, cmd)
	}
	if !strings.Contains(m.status, "dev on build-host") {
		t.Fatalf("remote kill question = %q, want session and server", m.status)
	}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil || m.pendingServer != "" || !strings.Contains(m.status, "cancelled") {
		t.Fatalf("remote kill cancellation = status %q, pending server %q, cmd %v",
			m.status, m.pendingServer, cmd)
	}
}

// A project on a host is a group header like a local one.  ctrl+a starts a chat in it there,
// and the keys that act on the host's files act there too: a project row names a directory on
// that host, so lazygit and yazi open in it.  The remaining project keys are local and say so.
func TestRemoteProjectGroupStartsANewChatThere(t *testing.T) {
	build := func() model {
		m := model{view: viewSessions, layout: "tab", width: 100, height: 20, current: 1,
			targets: []target{{}, {Server: "build-host"}}, expanded: map[string]bool{},
			data: map[string]targetData{"build-host": {Loaded: true,
				Target: target{Server: "build-host"},
				Groups: []group{{Project: "api", CWD: "/srv/api", Server: "build-host"}}}}}
		m.rebuildRows()
		return m
	}
	project := session{Project: "api", CWD: "/srv/api", Alive: true,
		ProjectOnly: true, Server: "build-host"}

	// enter opens the group instead of starting anything.
	m := build()
	if _, cmd := m.startAction("enter"); cmd != nil || m.pending != "" {
		t.Fatalf("enter on a remote group = cmd %v, want only the group opened", cmd)
	}
	if rows := m.filtered(); len(rows) != 1 || !rows[0].Expanded {
		t.Fatalf("rows after enter = %#v, want the group open", rows)
	}
	args, _ := m.openArgs("zmx-new", project, false)
	want := []string{"zmx-remote-new", "build-host", "/srv/api", "--place", "tab"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("new chat args = %#v, want %#v", args, want)
	}
	chat := build()
	if _, cmd := chat.startAction("ctrl+a"); cmd == nil {
		t.Fatal("ctrl+a on a remote project did not start a new chat there")
	}
	// The host's directory is what these keys need, and a project row names one.
	for _, tool := range []struct{ key, action string }{{"ctrl+g", "lazygit"},
		{"alt+f", "yazi"}} {
		fresh := build()
		if _, cmd := fresh.startAction(tool.key); cmd == nil {
			t.Errorf("%s on a remote project = cmd %v, want the tool opened there",
				tool.key, cmd)
		}
		args, _ := fresh.openArgs(tool.action, project, false)
		want := []string{"tool-remote", "build-host", "/srv/api", tool.action, "--place", "tab"}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%s args = %#v, want %#v", tool.key, args, want)
		}
	}
	for _, key := range []string{"ctrl+t", "ctrl+x"} {
		fresh := build()
		if _, cmd := fresh.startAction(key); cmd == nil {
			t.Errorf("%s on a remote project = cmd %v, want a shell opened there", key, cmd)
		}
		args, _ := fresh.openArgs("shell", project, false)
		want := []string{"shell-remote", "build-host", "/srv/api", "--place", "tab"}
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%s args = %#v, want %#v", key, args, want)
		}
	}
	// The project editor opens the host's directory in that host's own editor, and nothing is
	// left on a remote project row that acts here: the four directory keys all reach the host.
	fresh := build()
	if _, cmd := fresh.startAction("alt+e"); cmd != nil || fresh.modal != "editor-title" {
		t.Errorf("alt+e on a remote project = modal %q, cmd %v; want the title prompt",
			fresh.modal, cmd)
	}
	args, _ = fresh.openArgs("project-editor", project, false)
	want = []string{"editor-remote", "build-host", "/srv/api", "--label", "nvim api",
		"--place", "tab"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("alt+e args = %#v, want %#v", args, want)
	}
	// A key that needs a session still says so, and names the host while it does.
	odd := build()
	if _, cmd := odd.startAction("ctrl+f"); cmd != nil {
		t.Errorf("ctrl+f on a remote project = cmd %v, want a note instead", cmd)
	}
	if !strings.Contains(odd.status, "build-host") {
		t.Errorf("ctrl+f status = %q, want the host named", odd.status)
	}
}

// A target that answers is named in the target bar, so a long destination has somewhere to
// live without crowding the session columns.
func TestTargetBarNamesTheDestination(t *testing.T) {
	m := model{view: viewSessions, width: 100, height: 20, current: 1,
		targets: []target{{}, {Server: "user@192.0.2.15"}},
		data: map[string]targetData{"user@192.0.2.15": {Loaded: true,
			Target: target{Server: "user@192.0.2.15"}}}}
	bar := ansi.Strip(m.targetBar())
	if !strings.Contains(bar, "user@192.0.2.15") || !strings.Contains(bar, "local") {
		t.Fatalf("target bar = %q, want both destinations named", bar)
	}
	if got := ansi.StringWidth(m.targetBar()); got > m.width {
		t.Fatalf("target bar measures %d columns, want at most %d", got, m.width)
	}
	// A target whose read failed is marked, so an empty list is explainable at a glance.
	failed := model{view: viewSessions, width: 100, height: 20, current: 0,
		targets: []target{{}, {Server: "user@192.0.2.15"}},
		data:    map[string]targetData{"user@192.0.2.15": {Err: "connection refused"}}}
	if bar := ansi.Strip(failed.targetBar()); !strings.Contains(bar, "✗") {
		t.Fatalf("target bar = %q, want the failed target marked", bar)
	}
}

func TestZmxRowSearchFindsTheHandleAndTheCommand(t *testing.T) {
	row := zmxRow()
	for _, query := range []string{"pi-01a1002e-2", "fork", "home-infra", "Fix ingress"} {
		if ok, _ := matchesRow(query, row); !ok {
			t.Errorf("zmx row did not match %q", query)
		}
	}
	if ok, _ := matchesRow("nothing-here", row); ok {
		t.Error("zmx row matched a query it does not contain")
	}
}

func TestZmxChatsMarkTheSessionThatRunsInsideThem(t *testing.T) {
	entries := []zmxSession{
		{Name: "pi-aaa", Pi: "aaa", Chat: "Fix ingress docs"},
		{Name: "shell"}, // a plain command carries no chat
		{Name: "pi-bbb", Pi: "bbb"},
	}
	chats := zmxChats(entries)
	if len(chats) != 2 || chats["aaa"] != "pi-aaa" || chats["bbb"] != "pi-bbb" {
		t.Fatalf("zmxChats = %#v, want one zmx name per labelled chat", chats)
	}
	if _, ok := chats["shell"]; ok {
		t.Fatal("a zmx session with no `pi=` label marked a session row")
	}
}

func TestSessionRowMarksTheZmxSessionItRunsIn(t *testing.T) {
	plain := session{ID: "01a10000", Harness: "pi", Name: "Docs tidy", Project: "docs",
		CWD: "/project/docs", Alive: true, Mod: 1791041092, Bytes: 4096}
	// The two sessions go through the loader's own path, so the header counts them the way it
	// counts a real target.
	m := model{view: viewSessions, width: 120, height: 20, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "home-infra", CWD: "/srv/home-infra",
				Sessions: []session{sessionInZmx(), plain}}}}}}
	m.rebuildRows()
	m.expanded[m.expansionKey("/srv/home-infra", "home-infra")] = true
	m.rebuildRows()

	lines := strings.Split(ansi.Strip(m.projectsView()), "\n")
	if len(lines) != 3 {
		t.Fatalf("session list = %q, want a group header and the two session rows", lines)
	}
	lines = lines[1:]
	if len(lines) != 2 {
		t.Fatalf("session list = %q, want the two session rows", lines)
	}
	// A session under its group header leaves the project column out: the header above it
	// already names the project, so the name is what the row spends its columns on.
	if !strings.Contains(lines[0], zmxTag) || !strings.Contains(lines[0], "Fix ingress docs") ||
		!strings.Contains(lines[0], "2.0M") {
		t.Fatalf("zmx session row = %q, want the mark and the size beside it", lines[0])
	}
	if strings.Contains(lines[0], "home-infra") {
		t.Fatalf("zmx session row = %q, want no project column under a header", lines[0])
	}
	if header := ansi.Strip(m.headerView()); !strings.Contains(header, "home-infra") &&
		!strings.Contains(ansi.Strip(m.projectsView()), "home-infra") {
		t.Fatal("the project is nowhere: the header must name it")
	}
	if strings.Contains(lines[1], zmxTag) {
		t.Fatalf("plain session row = %q, want no zmx mark", lines[1])
	}
	if !strings.Contains(lines[1], "Docs tidy") || !strings.Contains(lines[1], "4K") {
		t.Fatalf("plain session row = %q, want its own name and size", lines[1])
	}
	// The mark is two cells wide, and the reserved column matches it: a row is then exactly as
	// wide as the picker thinks it is, which is what keeps the pane border straight.
	if got := ansi.StringWidth(lines[0]); got != m.listWidth() {
		t.Fatalf("zmx session row measures %d columns, want %d", got, m.listWidth())
	}
	if got := displayWidth(zmxTag); got != zmxTagW || ansi.StringWidth(zmxTag) != zmxTagW {
		t.Fatalf("the zmx mark %q measures %d columns by this table and %d by the terminal, "+
			"but the row reserves %d", zmxTag, displayWidth(zmxTag), ansi.StringWidth(zmxTag), zmxTagW)
	}
	// The mark keeps its column on both rows, so what sits beside it still lines up.
	column := func(line, name string) int {
		return ansi.StringWidth(line[:strings.Index(line, name)])
	}
	if column(lines[0], "Fix ingress docs") != column(lines[1], "Docs tidy") {
		t.Fatalf("session names start at columns %d and %d; want one column\n%s",
			column(lines[0], "Fix ingress docs"), column(lines[1], "Docs tidy"),
			strings.Join(lines, "\n"))
	}
	if header := ansi.Strip(m.headerView()); !strings.Contains(header, "1 in zmx") {
		t.Fatalf("session header = %q, want the count of the rows that run in zmx", header)
	}
	// The handle zmx knows still finds the session, which is what the mark points at.
	if ok, _ := matchesRow("pi-01a1002e-2", sessionInZmx()); !ok {
		t.Fatal("a session that runs in zmx did not match its zmx handle")
	}
}

// A chat whose client was closed is live with nothing to switch to, so the session list asks
// where the new client goes.  A chat that a window still shows is switched to, with no
// question: the answer is the window it already has.
func TestSessionRowAsksForAPlaceOnlyWhenNoWindowShowsTheChat(t *testing.T) {
	row := sessionInZmx()
	detached := model{view: viewSessions, layout: "ask", width: 120, height: 20,
		sessions: []session{row}, live: map[string]liveInfo{row.ID: {ID: row.ID, Certain: true}}}
	if _, cmd := detached.beginAction("resume", row, false); cmd != nil || detached.modal != "layout" {
		t.Fatalf("detached chat = modal %q, cmd %v; want the placement question",
			detached.modal, cmd)
	}

	shown := model{view: viewSessions, layout: "ask", width: 120, height: 20,
		sessions: []session{row},
		live: map[string]liveInfo{row.ID: {ID: row.ID, Certain: true,
			Owner: "kitty window 12 (zmx " + row.ZmxName + ")"}}}
	if _, cmd := shown.beginAction("resume", row, false); cmd == nil || shown.modal != "" {
		t.Fatalf("shown chat = modal %q, cmd %v; want the switch without a question",
			shown.modal, cmd)
	}

	// A fixed setting is the answer already, so nothing asks in either case.
	fixed := model{view: viewSessions, layout: "tab", sessions: []session{row},
		live: map[string]liveInfo{row.ID: {ID: row.ID, Certain: true}}}
	_, cmd := fixed.beginAction("resume", row, false)
	if cmd == nil || fixed.modal != "" {
		t.Fatalf("fixed placement = modal %q, cmd %v; want the attach", fixed.modal, cmd)
	}
	args, _ := fixed.openArgs("resume", row, false)
	want := []string{"switch", row.CWD, row.ID, "--label", "pi " + row.Name, "--place", "tab"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("detached chat resume args = %#v, want %#v", args, want)
	}
}

func TestZmxRowRendersTheHandleBesideTheChat(t *testing.T) {
	m := model{view: viewZmx, width: 100, height: 20, zmxRows: []session{zmxRow()}}
	rendered := ansi.Strip(m.zmxView())
	if !strings.Contains(rendered, "Fix ingress docs") || !strings.Contains(rendered, "pi-01a1002e-2") {
		t.Fatalf("zmx row = %q, want the chat name and the zmx handle", rendered)
	}
	header := ansi.Strip(m.headerView())
	if !strings.Contains(header, "zmx sessions") || strings.Contains(header, "pi zmx") {
		t.Fatalf("zmx header = %q, want the list named without the harness", header)
	}
	if !strings.Contains(header, "1 attached") {
		t.Fatalf("zmx header = %q, want the attached count", header)
	}
}

func TestZmxEnterAttachesWithThePlacementSetting(t *testing.T) {
	row := zmxRow()
	m := model{view: viewZmx, layout: "pane", zmxRows: []session{row}}
	args, note := m.openArgs("zmx", row, false)
	want := []string{"zmx-switch", "pi-01a1002e-2", "--cwd",
		"/Users/me/personal/home-infra", "--place", "pane"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("zmx attach args = %#v, want %#v", args, want)
	}
	if !strings.Contains(note, "pi-01a1002e-2") {
		t.Fatalf("zmx attach note = %q, want the session name", note)
	}
	m.layout = "ask"
	if _, cmd := m.beginAction("zmx", row, false); cmd != nil || m.modal != "layout" {
		t.Fatalf("ask placement did not open the layout question: modal %q, cmd %v", m.modal, cmd)
	}
}

func TestZmxCtrlTOpensASecondClientInItsOwnWindow(t *testing.T) {
	row := zmxRow()
	m := model{view: viewZmx, layout: "pane", zmxRows: []session{row}}
	_, cmd := m.startZmxAction("ctrl+t", row)
	if cmd == nil {
		t.Fatal("ctrl+t on a zmx row returned no command")
	}
	args, _ := m.openArgs("zmx", row, false) // the layout itself is untouched
	if args[len(args)-1] != "pane" {
		t.Fatalf("ctrl+t changed the placement setting: %#v", args)
	}
}

func TestZmxKillAsksBeforeItActs(t *testing.T) {
	row := zmxRow()
	m := model{view: viewZmx, zmxRows: []session{row}}
	_, cmd := m.startZmxAction("ctrl+w", row)
	if cmd != nil || m.pending != "zmx-kill" || m.pendingID != "pi-01a1002e-2" {
		t.Fatalf("ctrl+w = pending %q for %q, cmd %v; want a question first",
			m.pending, m.pendingID, cmd)
	}
	if !strings.Contains(m.status, "pi-01a1002e-2") {
		t.Fatalf("kill question = %q, want the session name", m.status)
	}
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("answering yes did not run the kill")
	}
	m = model{view: viewZmx, zmxRows: []session{row}}
	m.startZmxAction("ctrl+w", row)
	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if cmd != nil || !strings.Contains(m.status, "cancelled") {
		t.Fatalf("answering no ran something: status %q, cmd %v", m.status, cmd)
	}
}

func TestZmxKeysThatNeedASessionRowSaySo(t *testing.T) {
	row := zmxRow()
	for _, key := range []string{"ctrl+e", "ctrl+f"} {
		m := model{view: viewZmx, zmxRows: []session{row}}
		if _, cmd := m.startZmxAction(key, row); cmd != nil || m.status == "" {
			t.Errorf("%s on a zmx row = status %q, cmd %v; want a note and nothing else",
				key, m.status, cmd)
		}
	}
	// The transcript editor is the menu's action now: the ctrl+o key was retired, so it must
	// reach no action at all on a zmx row.
	retired := model{view: viewZmx, zmxRows: []session{row}}
	if _, cmd := retired.startZmxAction("ctrl+o", row); cmd != nil {
		t.Errorf("ctrl+o on a zmx row = cmd %v; want no action", cmd)
	}
}

func TestZmxPreviewWaitsForATranscriptItHas(t *testing.T) {
	row := zmxRow()
	m := model{view: viewZmx, width: 100, height: 20, showPrev: true, zmxRows: []session{row},
		cache: map[string]preview{}}
	if cmd := m.refreshPreview(); cmd == nil {
		t.Fatal("a zmx row with a transcript scheduled no preview read")
	}
}

// A session that carries no chat has no transcript, so the pane falls back to the session's
// own scrollback: the terminal is the only picture of a shell or a detached job.
func TestZmxRowWithoutAChatPreviewsItsOwnScrollback(t *testing.T) {
	bare := session{ZmxOnly: true, ZmxName: "shell", CWD: "/tmp", Clients: 0}
	m := model{view: viewZmx, width: 100, height: 20, showPrev: true,
		zmxRows: []session{bare}}
	if cmd := m.refreshPreview(); cmd == nil {
		t.Fatal("a zmx row with no chat scheduled no scrollback read")
	}
	m.zmxHistoryName = "shell"
	m.zmxHistory = []string{"$ ls", "README.md  main.go", "$ "}
	rendered := ansi.Strip(m.previewView())
	for _, want := range []string{"shell", "detached", "/tmp", "README.md  main.go"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("scrollback preview = %q, want %q", rendered, want)
		}
	}
	// The row has a command of its own only when it runs one, and the hint line is gone.
	if strings.Contains(rendered, "no Pi transcript") {
		t.Errorf("scrollback preview = %q, want the output instead of the facts filler", rendered)
	}
	if limit := m.previewScrollLimit(); limit != 0 {
		t.Fatalf("three lines in a taller pane scroll limit = %d, want 0", limit)
	}
}

func TestZmxScrollbackScrollsWhenItIsLongerThanThePane(t *testing.T) {
	bare := session{ZmxOnly: true, ZmxName: "build", CWD: "/tmp", Clients: 1}
	lines := make([]string, 0, 120)
	for i := 0; i < 120; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	m := model{view: viewZmx, width: 100, height: 20, showPrev: true,
		zmxRows: []session{bare}, zmxHistoryName: "build", zmxHistory: lines}
	if limit := m.previewScrollLimit(); limit <= 0 {
		t.Fatalf("scroll limit = %d, want room to scroll a long scrollback", limit)
	}
	m.preview.scroll = len(lines)
	m.clampPreviewScroll()
	if m.preview.scroll != m.previewScrollLimit() {
		t.Fatalf("scrolled to %d, want the limit %d", m.preview.scroll, m.previewScrollLimit())
	}
	rendered := ansi.Strip(m.previewView())
	if !strings.Contains(rendered, "line 119") {
		t.Errorf("scrolled preview = %q, want the end of the scrollback", rendered)
	}
	if !strings.Contains(rendered, "attached (1)") {
		t.Errorf("scrolled preview = %q, want the attached state", rendered)
	}
}

// A read that lands after the cursor moved on must not overwrite the pane.
func TestZmxScrollbackIgnoresALateRead(t *testing.T) {
	selected := session{ZmxOnly: true, ZmxName: "shell", CWD: "/tmp"}
	m := model{view: viewZmx, width: 100, height: 20, showPrev: true,
		zmxRows: []session{selected}}
	m.Update(zmxHistoryMsg{name: "other", lines: []string{"stale"}})
	if m.zmxHistoryName != "" || len(m.zmxHistory) != 0 {
		t.Fatalf("late read was used: %#v", m.zmxHistory)
	}
	m.Update(zmxHistoryMsg{name: "shell", lines: []string{"fresh"}})
	if m.zmxHistoryName != "shell" || len(m.zmxHistory) != 1 {
		t.Fatalf("read for the selected session was dropped: %#v", m.zmxHistory)
	}
}

func TestZmxViewReportsWhyTheListIsEmpty(t *testing.T) {
	m := model{view: viewZmx, width: 80, data: map[string]targetData{"local": {
		Loaded: true, Target: target{}, Note: "the zmx list needs sh2pil-open zmx-list"}}}
	if got := ansi.Strip(m.zmxView()); !strings.Contains(got, "needs sh2pil-open zmx-list") {
		t.Fatalf("empty zmx list = %q, want the helper's note", got)
	}
	m.data["local"] = targetData{Loaded: true, Target: target{}}
	if got := ansi.Strip(m.projectsView()); !strings.Contains(got, "no projects found") {
		t.Fatalf("empty project list = %q, want a plain empty-list line", got)
	}
}

// The zmx kill the help text promises has to be reachable from the keyboard: this key does not
// travel through startAction, where a zmx row would find startZmxAction, so it is routed here.
func TestZmxCtrlWKillsTheSessionFromTheKeyboard(t *testing.T) {
	row := zmxRow()
	m := model{view: viewZmx, zmxRows: []session{row}}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		m.pending != "zmx-kill" || m.pendingID != row.ZmxName {
		t.Fatalf("ctrl+w = pending %q for %q, cmd %v; want the zmx session to end",
			m.pending, m.pendingID, cmd)
	}
	if !strings.Contains(m.status, row.ZmxName) || !strings.Contains(m.status, "kill") {
		t.Fatalf("question = %q, want the session named in a kill question", m.status)
	}
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}); cmd == nil {
		t.Fatal("answering yes did not end the session")
	}
	// The same chat seen as a session row still deletes its transcript instead: the zmx row
	// ends the session, the session row owns the transcript.
	sessionRow := sessionInZmx()
	plain := model{view: viewSessions, sessions: []session{sessionRow}}
	if _, cmd := plain.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		plain.pending != "delete" || plain.pendingID != sessionRow.ID {
		t.Fatalf("ctrl+w on a session row = pending %q for %q, cmd %v; want the delete question",
			plain.pending, plain.pendingID, cmd)
	}
	// A session that carries no chat is ended by its own name, which the row does have.
	bare := session{ZmxOnly: true, ZmxName: "shell", CWD: "/tmp"}
	labelless := model{view: viewZmx, zmxRows: []session{bare}}
	if _, cmd := labelless.handleKey(tea.KeyMsg{Type: tea.KeyCtrlW}); cmd != nil ||
		labelless.pending != "zmx-kill" || labelless.pendingID != "shell" {
		t.Fatalf("ctrl+w on a labelless zmx row = pending %q for %q, cmd %v; want the kill",
			labelless.pending, labelless.pendingID, cmd)
	}
}

// A zmx session whose `pi=` label never arrived has no chat, so there is nothing to rename or
// fork: the keys say so instead of running sh2pil-sessions and pi against an empty session id.
func TestZmxRowWithNoChatRefusesRenameAndFork(t *testing.T) {
	bare := session{ZmxOnly: true, ZmxName: "shell", CWD: "/tmp"}
	for _, key := range []tea.KeyType{tea.KeyCtrlE, tea.KeyCtrlF} {
		m := model{view: viewZmx, zmxRows: []session{bare}}
		if _, cmd := m.handleKey(tea.KeyMsg{Type: key}); cmd != nil || m.modal != "" {
			t.Errorf("%s on a labelless zmx row = modal %q, cmd %v; want a note",
				key, m.modal, cmd)
		}
		if !strings.Contains(m.status, "no chat") {
			t.Errorf("%s status = %q, want the missing chat named", key, m.status)
		}
	}
	// A chat the session does carry stays renameable and forkable from its zmx row.
	row := zmxRow()
	rename := model{view: viewZmx, zmxRows: []session{row}}
	if _, cmd := rename.handleKey(tea.KeyMsg{Type: tea.KeyCtrlE}); cmd != nil || rename.modal != "rename" {
		t.Fatalf("ctrl+e on a labelled zmx row = modal %q, cmd %v; want the rename prompt",
			rename.modal, cmd)
	}
	fork := model{view: viewZmx, zmxRows: []session{row}}
	if _, cmd := fork.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF}); cmd != nil || fork.modal != "fork" {
		t.Fatalf("ctrl+f on a labelled zmx row = modal %q, cmd %v; want the fork prompt",
			fork.modal, cmd)
	}
}
