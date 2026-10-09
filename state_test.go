package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestStateWordsNamesWhatAChatIsDoing pins the state column.  Every word is measured, because a
// column that is one cell short shifts every column after it.
func TestStateWordsNamesWhatAChatIsDoing(t *testing.T) {
	cases := []struct {
		name string
		info liveInfo
		live bool
		want string
	}{
		{"a row nothing runs says nothing", liveInfo{}, false, ""},
		{"a record from an older extension says only that it is live", liveInfo{}, true, "live"},
		{"a stopped clock is not believed", liveInfo{Stale: true, LastState: "tool"}, true, "unknown"},
		{"a tool names itself", liveInfo{State: "tool", Detail: "bash"}, true, "bash"},
		{"a tool with no name is work in progress", liveInfo{State: "tool"}, true, "working"},
		{"a blocking prompt asks for a person", liveInfo{State: "blocked", Detail: "confirm"}, true, "needs you"},
		{"a settled chat is idle", liveInfo{State: "idle"}, true, "idle"},
		{"a chat that just started says so", liveInfo{State: "starting"}, true, "starting"},
		{"a chat that is going away says so", liveInfo{State: "ended"}, true, "ended"},
		{"a long tool name is clipped, not wrapped", liveInfo{State: "tool", Detail: "lib.search.everything"},
			true, "lib.sear…"},
	}
	for _, c := range cases {
		word, _ := stateWords(c.info, c.live)
		if got := strings.TrimSpace(word); got != c.want {
			t.Errorf("%s: stateWords = %q, want %q", c.name, got, c.want)
		}
		if got := displayWidth(word); got != stateW {
			t.Errorf("%s: stateWords is %d columns, want %d", c.name, got, stateW)
		}
	}
}

// TestABlockingPromptIsNotColouredLikeRunningOrSettled pins the one emphasis the picker adds: a
// chat that is waiting on a person must not look like a chat that is merely working, or the row
// a reader must find is the row that hides.  The colours are compared as colours: a test has no
// terminal, so lipgloss renders every style as plain text.
func TestABlockingPromptIsNotColouredLikeRunningOrSettled(t *testing.T) {
	_, waiting := stateWords(liveInfo{State: "blocked"}, true)
	_, running := stateWords(liveInfo{State: "working"}, true)
	_, liveOnly := stateWords(liveInfo{}, true)
	_, settled := stateWords(liveInfo{State: "idle"}, true)
	if waiting.GetForeground() == running.GetForeground() ||
		waiting.GetForeground() == liveOnly.GetForeground() ||
		waiting.GetForeground() == settled.GetForeground() {
		t.Fatal("a blocking prompt is coloured like a running or a settled chat")
	}
	if waiting.GetBold() != true {
		t.Fatal("a blocking prompt is not emphasised")
	}
}

// TestStateDetailNamesThePromptOnlyForABlockedChat pins what the row column has no room for.  The
// label is a dialog's own words about itself, never the content of a prompt.
func TestStateDetailNamesThePromptOnlyForABlockedChat(t *testing.T) {
	cases := []struct {
		name string
		info liveInfo
		want string
	}{
		{"a prompt and its label", liveInfo{State: "blocked", Detail: "confirm", Label: "Allow bash?"},
			" (confirm Allow bash?)"},
		{"a prompt without a label", liveInfo{State: "blocked", Detail: "select"}, " (select)"},
		{"a prompt with neither", liveInfo{State: "blocked"}, ""},
		{"a stale record remembers what it was doing", liveInfo{Stale: true, LastState: "tool"},
			" (last seen tool)"},
		{"a stale record that never said says nothing", liveInfo{Stale: true}, ""},
		{"a running tool says nothing extra", liveInfo{State: "tool", Detail: "bash", Label: "x"}, ""},
		{"a live chat with no state says nothing", liveInfo{}, ""},
	}
	for _, c := range cases {
		if got := stateDetail(c.info); got != c.want {
			t.Errorf("%s: stateDetail = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestABlockedRowAsksForAPersonAndKeepsItsWidth pins the row itself: the word is there, and the
// row still measures exactly the list width, so the columns after it are not shifted.
func TestABlockedRowAsksForAPersonAndKeepsItsWidth(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := model{view: viewSessions, width: 120, height: 20,
		live: map[string]liveInfo{"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm",
			Label: "Gate"}}}
	line := ansi.Strip(m.rowView(row, nil, false))
	if !strings.Contains(line, "needs you") {
		t.Fatalf("row = %q, want it to ask for a person", line)
	}
	if got := ansi.StringWidth(m.rowView(row, nil, false)); got != m.listWidth() {
		t.Fatalf("row measures %d columns, want %d", got, m.listWidth())
	}
	// The word sits where the state column is, after the live flag.
	if !strings.Contains(line, "● needs you") {
		t.Fatalf("row = %q, want the flag and the state together", line)
	}
}

// TestTheRowStillSaysLiveForARecordWithNoState pins the fallback: every chat running today was
// started before the extension published a state, and its row must read exactly as it did.
func TestTheRowStillSaysLiveForARecordWithNoState(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := model{view: viewSessions, width: 120, height: 20,
		live: map[string]liveInfo{"ses_1": {ID: "ses_1", Certain: true}}}
	line := ansi.Strip(m.rowView(row, nil, false))
	if !strings.Contains(line, "● live") {
		t.Fatalf("row = %q, want the old meaning of the column", line)
	}
	if got := ansi.StringWidth(m.rowView(row, nil, false)); got != m.listWidth() {
		t.Fatalf("row measures %d columns, want %d", got, m.listWidth())
	}
}

// TestARemoteRowIsLiveWithoutAState pins what a host can answer: it says whether one of its pi
// processes owns the session, and its state does not travel yet.
func TestARemoteRowIsLiveWithoutAState(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "srv", CWD: "/srv/repo", Alive: true,
		Live: true, Server: "build-host"}
	m := model{view: viewSessions, width: 120, height: 20,
		live: map[string]liveInfo{"ses_1": {ID: "ses_1", State: "blocked"}}}
	info, live := m.rowLive(row)
	if !live || info.State != "" {
		t.Fatalf("remote row state = %q (live %v), want live with no state", info.State, live)
	}
	if line := ansi.Strip(m.rowView(row, nil, false)); !strings.Contains(line, "● live") {
		t.Fatalf("remote row = %q, want the live word and no state", line)
	}
}

// TestTheHeaderCountsTheChatsThatWaitOnAPerson pins the count a reader scans for.  A settled chat
// is not counted: waiting for the next instruction is what a chat does, and counting it would
// make the number mean nothing.
func TestTheHeaderCountsTheChatsThatWaitOnAPerson(t *testing.T) {
	blocked := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	busy := session{ID: "ses_2", Name: "write tests", Project: "repo", CWD: "/tmp/repo", Alive: true}
	settled := session{ID: "ses_3", Name: "ship it", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := model{view: viewSessions, width: 140, height: 24, expanded: map[string]bool{},
		live: map[string]liveInfo{
			"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm"},
			"ses_2": {ID: "ses_2", State: "tool", Detail: "bash"},
			"ses_3": {ID: "ses_3", State: "idle"},
		},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{blocked, busy, settled}}}}}}
	m.rebuildRows()
	if m.waitingCount != 1 {
		t.Fatalf("waitingCount = %d, want 1", m.waitingCount)
	}
	if m.liveCount != 3 {
		t.Fatalf("liveCount = %d, want 3", m.liveCount)
	}
	header := ansi.Strip(m.headerView())
	if !strings.Contains(header, "1 waiting on you") {
		t.Fatalf("header = %q, want it to count the chat that waits", header)
	}
}

// TestAClosedGroupStillSaysThatSomeoneIsWaiting pins the header rather than the row: a group
// starts closed, so its sessions and their state column are hidden.  Without the mark on the
// header, the one row worth finding is only findable by opening every project in turn.
func TestAClosedGroupStillSaysThatSomeoneIsWaiting(t *testing.T) {
	blocked := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	busy := session{ID: "ses_2", Name: "write tests", Project: "repo", CWD: "/tmp/repo", Alive: true}
	quiet := session{ID: "ses_3", Name: "ship it", Project: "docs", CWD: "/tmp/docs", Alive: true}
	m := model{view: viewSessions, width: 140, height: 24, expanded: map[string]bool{},
		live: map[string]liveInfo{
			"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm"},
			"ses_2": {ID: "ses_2", State: "working"},
		},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{blocked, busy}},
			{Project: "docs", CWD: "/tmp/docs", Sessions: []session{quiet}}}}}}
	m.rebuildRows()

	rows := m.filtered()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both group headers", len(rows))
	}
	for _, row := range rows {
		// Both states of the cursor: a header is marked whether or not it is selected, because the
		// cursor starts on the first row, which is the one a reader is most likely to look at.
		for _, selected := range []bool{false, true} {
			line := ansi.Strip(m.rowView(row, nil, selected))
			if got := ansi.StringWidth(m.rowView(row, nil, selected)); got != m.listWidth() {
				t.Fatalf("%s header (selected %v) measures %d columns, want %d", row.Project,
					selected, got, m.listWidth())
			}
			marked := strings.Contains(line, "waiting")
			want := row.Project == "repo"
			if row.Project == "repo" && row.Waiting != 1 {
				t.Fatalf("repo header waiting = %d, want 1", row.Waiting)
			}
			if marked != want {
				t.Fatalf("%s header (selected %v) = %q, waiting mark = %v, want %v",
					row.Project, selected, line, marked, want)
			}
		}
	}
}

// TestALateLiveReadUpdatesTheCounts pins the bug a rendered frame found and no unit test did: the
// rows read the live map while they are drawn, but the header counts are built with the rows, so
// a live read that arrives after them left a header that contradicted its own rows.
func TestALateLiveReadUpdatesTheCounts(t *testing.T) {
	blocked := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := &model{view: viewSessions, width: 140, height: 24, expanded: map[string]bool{},
		live: map[string]liveInfo{}, cache: map[string]preview{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{blocked}}}}}}
	m.rebuildRows()
	if m.waitingCount != 0 {
		t.Fatalf("precondition: waitingCount = %d, want 0 before any live read", m.waitingCount)
	}
	m.Update(liveMsg{live: map[string]liveInfo{
		"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm"}}})
	if m.waitingCount != 1 {
		t.Fatalf("waitingCount after the live read = %d, want 1", m.waitingCount)
	}
	if header := ansi.Strip(m.headerView()); !strings.Contains(header, "1 waiting on you") {
		t.Fatalf("header after the live read = %q, want it to count the chat that waits", header)
	}
	rows := m.filtered()
	if len(rows) != 1 || rows[0].Waiting != 1 {
		t.Fatalf("group headers after the live read = %#v, want one header with one waiting", rows)
	}
}

// stateHelperScript answers the two reads the picker makes: the cheap one, and the full one.
const stateHelperScript = `import json, sys
if sys.argv[1] == "state":
    print(json.dumps([{"id": "ses_1", "state": "blocked", "detail": "confirm",
                       "last_state": "", "label": "Gate", "age": 12, "stale": False}]))
else:
    print(json.dumps([{"id": "ses_1", "certain": True, "state": "tool", "detail": "bash"}]))
`

// runBatch runs a command the way the program does and collects every message it produced, so a
// test can see which read a tick asked for without waiting on the clock it also armed.
func runBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	msgs := make([]tea.Msg, 0, len(batch))
	for _, sub := range batch {
		if sub != nil {
			msgs = append(msgs, sub())
		}
	}
	return msgs
}

func msgKinds(msgs []tea.Msg) []string {
	kinds := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		kinds = append(kinds, fmt.Sprintf("%T", msg))
	}
	return kinds
}

// TestTheStateClockIsOffWhenTheSettingIsZero pins the escape hatch: a reader who wants the old
// behaviour sets the interval to zero and gets no clock at all, rather than a slow one.
func TestTheStateClockIsOffWhenTheSettingIsZero(t *testing.T) {
	silent := &model{statePoll: 0}
	if cmd := silent.stateTick(); cmd != nil {
		t.Fatal("a zero interval still armed a clock")
	}
	ticking := &model{statePoll: 3}
	if cmd := ticking.stateTick(); cmd == nil {
		t.Fatal("a positive interval armed no clock")
	}
}

// TestATickReadsTheStateCheaplyAndLivenessSometimes pins the cadence: the state on every tick,
// because it changes on a clock, and the full read only now and then, because ownership does not.
func TestATickReadsTheStateCheaplyAndLivenessSometimes(t *testing.T) {
	helperDir := t.TempDir()
	fakeHelper(t, helperDir, "pib-open", stateHelperScript)
	m := &model{helperDir: helperDir, statePoll: 0.001, view: viewSessions, width: 120, height: 20,
		live:  map[string]liveInfo{"ses_1": {ID: "ses_1", Certain: true}},
		cache: map[string]preview{}, expanded: map[string]bool{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}}}}

	_, cmd := m.Update(liveTickMsg{})
	kinds := msgKinds(runBatch(t, cmd))
	if !slices.Contains(kinds, "main.stateMsg") {
		t.Fatalf("a tick produced %v, want the cheap state read", kinds)
	}
	if slices.Contains(kinds, "main.liveMsg") {
		t.Fatalf("a tick produced %v: every tick paid for the full read", kinds)
	}

	// The full read is the one that drops a chat that has gone away, so it still comes round.
	m.liveTicks = liveTickFullEvery - 1
	_, cmd = m.Update(liveTickMsg{})
	if kinds := msgKinds(runBatch(t, cmd)); !slices.Contains(kinds, "main.liveMsg") {
		t.Fatalf("the last of %d ticks produced %v, want the full live read", liveTickFullEvery, kinds)
	}
}

// TestATickReadsThisMachineAndEachConnectedHost pins what the clock is worth paying for once a
// host's own chats can be followed: this machine is read on every tick whatever target is on
// screen, because its chats need a person whether or not a host is being looked at, and each host
// is read on its own slower cadence and only while its connection is already up.  A clock that
// asks for a connection is a YubiKey touch nobody asked for, so an unconnected host is skipped.
func TestATickReadsThisMachineAndEachConnectedHost(t *testing.T) {
	helperDir := t.TempDir()
	fakeHelper(t, helperDir, "pib-open", stateHelperScript)
	host := target{Server: "build-host"}
	// The host's answer carries one chat it reports as running: a host with none has nothing to
	// decorate, so the clock leaves it alone.
	hostRow := session{ID: "ses_r", Project: "api", CWD: "/srv/api", Live: true, Server: "build-host"}
	connected := func(state bool, rows ...session) map[string]targetData {
		return map[string]targetData{
			"local": {Loaded: true, Target: target{}},
			"build-host": {Loaded: true, Target: host, Connected: state,
				Groups: []group{{Project: "api", CWD: "/srv/api", Sessions: rows}}},
		}
	}
	m := &model{helperDir: helperDir, statePoll: 0.001, view: viewSessions, width: 120, height: 20,
		current: 1, targets: []target{{}, host},
		live: map[string]liveInfo{}, cache: map[string]preview{}, expanded: map[string]bool{},
		data: connected(true, hostRow)}

	// A host on screen does not stop this machine from being read.  Every step builds its own
	// tick: a Bubble Tea command may be run only once, because a tick arms its timer when the
	// command is made rather than when it is run.
	_, first := m.Update(liveTickMsg{})
	kinds := msgKinds(runBatch(t, first))
	if !slices.Contains(kinds, "main.stateMsg") {
		t.Fatalf("a tick with a host on screen produced %v, want this machine read too", kinds)
	}
	// The first tick is not an interval boundary, so no host is read yet.
	if slices.Contains(kinds, "main.remoteStateMsg") {
		t.Fatalf("tick 1 produced %v, want no host read before the interval", kinds)
	}

	// At the interval, each connected host is read and nothing else changes.
	m.liveTicks = remoteStateEvery - 1
	_, cmd := m.Update(liveTickMsg{})
	kinds = msgKinds(runBatch(t, cmd))
	if !slices.Contains(kinds, "main.remoteStateMsg") {
		t.Fatalf("tick %d produced %v, want a host read", remoteStateEvery, kinds)
	}
	if !slices.Contains(kinds, "main.stateMsg") {
		t.Fatalf("tick %d produced %v, want this machine read as well", remoteStateEvery, kinds)
	}

	// A host with nothing running is skipped too: the read could only decorate rows that are not
	// there, and on a host without a helper it would ship the helper's code every cadence.
	m.liveTicks = remoteStateEvery - 1
	m.data = connected(true)
	_, cmd = m.Update(liveTickMsg{})
	if kinds := msgKinds(runBatch(t, cmd)); slices.Contains(kinds, "main.remoteStateMsg") {
		t.Fatalf("a host with no running chat was read: %v", kinds)
	}

	// A host whose master is gone is skipped: a read would need a touch to re-establish it.
	m.liveTicks = remoteStateEvery - 1
	m.data = connected(false, hostRow)
	_, cmd = m.Update(liveTickMsg{})
	if kinds := msgKinds(runBatch(t, cmd)); slices.Contains(kinds, "main.remoteStateMsg") {
		t.Fatalf("an unconnected host was read: %v", kinds)
	}
}

// TestACheapStateNeverMakesARowLive pins the split of responsibility: the cheap read does not
// check the process, so it may only decorate a chat the full read already believes.  Otherwise a
// record left behind by a process that died would bring its own row back.
func TestACheapStateNeverMakesARowLive(t *testing.T) {
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := &model{view: viewSessions, width: 120, height: 20, expanded: map[string]bool{},
		live: map[string]liveInfo{"ses_1": {ID: "ses_1", Certain: true, Owner: "kitty window 12"}},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}}}}}
	m.rebuildRows()

	m.Update(stateMsg{states: map[string]liveInfo{
		"ses_gone": {ID: "ses_gone", State: "blocked", Detail: "confirm"}}})
	if _, live := m.live["ses_gone"]; live {
		t.Fatal("a state read brought a chat back that nothing reports as live")
	}

	m.Update(stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm", Label: "Gate", Age: 12}}})
	got, live := m.live["ses_1"]
	if !live || got.State != "blocked" || got.Detail != "confirm" || got.Age != 12 {
		t.Fatalf("merged state = %#v, want the state of the chat that is live", got)
	}
	if got.Owner != "kitty window 12" {
		t.Fatalf("owner = %q, want the window the full read found to survive", got.Owner)
	}
	if m.waitingCount != 1 {
		t.Fatalf("waitingCount = %d, want 1 after the state arrived", m.waitingCount)
	}
}

// TestThePreviewBadgeNamesTheStateAndThePrompt pins the roomier half of the same information: the
// preview says which dialog is blocking, which the row column has no room for.
func TestThePreviewBadgeNamesTheStateAndThePrompt(t *testing.T) {
	row := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo", Alive: true}
	m := model{view: viewSessions, width: 200, height: 24, showPrev: true,
		sessions: []session{row},
		live: map[string]liveInfo{"ses_1": {ID: "ses_1", State: "blocked", Detail: "confirm",
			Label: "Allow bash?", Owner: "kitty window 12"}},
		cache: map[string]preview{}}
	badge := ansi.Strip(m.previewView())
	if !strings.Contains(badge, "needs you (confirm Allow bash?)") {
		t.Fatalf("preview badge = %q, want the state and the prompt named", badge)
	}
	if !strings.Contains(badge, "in kitty") {
		t.Fatalf("preview badge = %q, want the window that shows it", badge)
	}

	// A stale record says what it was last seen doing, so the reader is not left guessing.
	m.live["ses_1"] = liveInfo{ID: "ses_1", Stale: true, LastState: "tool"}
	if badge := ansi.Strip(m.previewView()); !strings.Contains(badge, "unknown (last seen tool)") {
		t.Fatalf("preview badge for a stale record = %q, want the last state named", badge)
	}

	// A prompt that has waited a while says so, because the row column has no room for it.
	m.live["ses_1"] = liveInfo{ID: "ses_1", State: "blocked", Detail: "confirm", Age: 240}
	if badge := ansi.Strip(m.previewView()); !strings.Contains(badge, "needs you (confirm) for 4m") {
		t.Fatalf("preview badge for a long wait = %q, want the wait named", badge)
	}
}

// hostRow finds one of the host's rows by session id, in the group the host answered with.
func hostRow(t *testing.T, m *model, id string) session {
	t.Helper()
	for _, group := range m.data["build-host"].Groups {
		for _, row := range group.Sessions {
			if row.ID == id {
				return row
			}
		}
	}
	t.Fatalf("no row %s in the host's answer", id)
	return session{}
}

// hostStateModel is the smallest picker with one host on it: this machine's chat, and two of a
// host's chats whose rows say the host reports them as running.  The busy one comes first in the
// read's order, so a ranking test can say which one moved.
func hostStateModel() *model {
	local := session{ID: "ses_local", Name: "local work", Project: "repo", CWD: "/tmp/repo",
		Alive: true}
	busy := session{ID: "ses_busy", Name: "other work", Project: "api", CWD: "/srv/api",
		Alive: true, Live: true, Server: "build-host"}
	remote := session{ID: "ses_remote", Name: "api work", Project: "api", CWD: "/srv/api",
		Alive: true, Live: true, Server: "build-host"}
	return &model{view: viewSessions, width: 140, height: 24, statePoll: 3, attentionSort: true,
		current: 0, targets: []target{{}, {Server: "build-host"}},
		expanded: map[string]bool{}, cache: map[string]preview{},
		live:   map[string]liveInfo{"ses_local": {ID: "ses_local", Certain: true, State: "tool"}},
		unread: map[string]bool{}, seen: map[string]string{},
		remoteStates: map[string]liveInfo{}, remoteSeen: map[string]string{},
		data: map[string]targetData{
			"local": {Loaded: true, Target: target{}, Groups: []group{
				{Project: "repo", CWD: "/tmp/repo", Sessions: []session{local}}}},
			"build-host": {Loaded: true, Connected: true, Target: target{Server: "build-host"},
				Groups: []group{{Project: "api", CWD: "/srv/api",
					Sessions: []session{busy, remote}}}},
		}}
}

// TestAHostStateDecoratesThatHostsRowsAndNoOthers pins the merge: a host's answer reaches the rows
// whose host reported them as running, and nothing about this machine moves.
func TestAHostStateDecoratesThatHostsRowsAndNoOthers(t *testing.T) {
	m := hostStateModel()
	local := m.data["local"].Groups[0].Sessions[0]
	remote := hostRow(t, m, "ses_remote")

	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_remote": {ID: "ses_remote", CWD: "/srv/api", State: "blocked", Detail: "confirm",
			Label: "Gate", Age: 4}}})

	info, live := m.rowLive(remote)
	if !live || info.State != "blocked" || info.Detail != "confirm" || info.Age != 4 {
		t.Fatalf("the host's row = %#v (live %v), want its host's state", info, live)
	}
	if info.Server != "build-host" {
		t.Fatalf("the host's row lost its host: %#v", info)
	}
	other, otherLive := m.rowLive(local)
	if !otherLive || other.State != "tool" || other.Server != "" {
		t.Fatalf("this machine's row = %#v (live %v), want it untouched", other, otherLive)
	}
}

// TestAHostRecordForAChatTheHostDoesNotReportIsIgnored pins the guard the cheap local read has
// too: a state may decorate only a chat the host itself lists as running, so a record left behind
// by a process that died on that host announces nothing and marks nothing.
func TestAHostRecordForAChatTheHostDoesNotReportIsIgnored(t *testing.T) {
	m := hostStateModel()
	var out bytes.Buffer
	m.out, m.notifierRole, m.notify = &out, true, notifyModeTerminal

	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_dead": {ID: "ses_dead", CWD: "/srv/api", State: "blocked", Detail: "confirm"}}})

	if _, known := m.remoteStates[remoteStateKey("build-host", "ses_dead")]; known {
		t.Fatal("a record for a chat the host does not report was kept as a state")
	}
	if m.unread["ses_dead"] {
		t.Fatal("a record for a chat the host does not report was marked unread")
	}
	if m.remoteSeen[remoteStateKey("build-host", "ses_dead")] != "" {
		t.Fatal("a chat the host does not report as running was remembered as news")
	}
	if out.Len() != 0 {
		t.Fatalf("a dead chat's record raised %q", out.String())
	}
	rows := m.filtered()
	if len(rows) != 1 || rows[0].Waiting != 0 {
		t.Fatalf("the header counted a chat that is not live: %#v", rows)
	}
}

// TestAReadThatFailsKeepsWhatTheHostSaidLast pins the cost of a host that cannot be reached: its
// rows keep the state they had, because a host that is unreachable is not a host whose chats have
// stopped.
func TestAReadThatFailsKeepsWhatTheHostSaidLast(t *testing.T) {
	m := hostStateModel()
	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_remote": {ID: "ses_remote", CWD: "/srv/api", State: "idle"}}})
	m.Update(remoteStateMsg{server: "build-host"})
	info, live := m.rowLive(hostRow(t, m, "ses_remote"))
	if !live || info.State != "idle" {
		t.Fatalf("a failed read changed the host's row: %#v (live %v)", info, live)
	}
}

// TestAHostThatStopsReportingAChatForgetsIt pins the pruning, and its limit: a chat the host no
// longer reports leaves no state, no memory, and no mark behind, while the other host and this
// machine keep everything they had.
func TestAHostThatStopsReportingAChatForgetsIt(t *testing.T) {
	m := hostStateModel()
	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_remote": {ID: "ses_remote", State: "blocked"},
		"ses_gone":   {ID: "ses_gone", State: "idle"}}})
	m.unread["ses_gone"], m.unread["ses_local"] = true, true

	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_remote": {ID: "ses_remote", State: "blocked"}}})

	if _, known := m.remoteStates[remoteStateKey("build-host", "ses_gone")]; known {
		t.Fatal("a chat the host no longer reports kept its state")
	}
	if m.remoteSeen[remoteStateKey("build-host", "ses_gone")] != "" {
		t.Fatal("a chat the host no longer reports kept its memory")
	}
	if m.unread["ses_gone"] {
		t.Fatal("a chat the host no longer reports kept its mark")
	}
	if !m.unread["ses_local"] {
		t.Fatal("a host's read dropped this machine's mark")
	}
	if m.remoteSeen[remoteStateKey("build-host", "ses_remote")] != "blocked" {
		t.Fatalf("the host's remaining chat lost its memory: %v", m.remoteSeen)
	}
}

// TestThisMachinesFullReadDoesNotForgetAHostsChats pins the two memories apart: the full local
// read drops the chats this machine is no longer running, and it must not touch a host's chats,
// which it knows nothing about.
func TestThisMachinesFullReadDoesNotForgetAHostsChats(t *testing.T) {
	m := hostStateModel()
	m.Update(remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_remote": {ID: "ses_remote", State: "idle"}}})
	m.unread["ses_remote"] = true

	m.Update(liveMsg{live: map[string]liveInfo{"ses_local": {ID: "ses_local", Certain: true}}})

	if m.remoteSeen[remoteStateKey("build-host", "ses_remote")] != "idle" {
		t.Fatalf("a local read forgot a host's chat: %v", m.remoteSeen)
	}
	if !m.unread["ses_remote"] {
		t.Fatal("a local read dropped a host's unread mark")
	}
	if _, known := m.remoteStates[remoteStateKey("build-host", "ses_remote")]; !known {
		t.Fatal("a local read dropped a host's state")
	}
}
