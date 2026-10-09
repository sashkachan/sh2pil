package main

import (
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// queueModel is the smallest picker with an attention queue: one open project whose sessions are
// named by their state map, every one of them live.  A state of "live" means a record from an
// older extension: the chat is live and nothing said what it is doing.
func queueModel(attentionSort bool, states map[string]string) *model {
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := make([]session, 0, len(ids))
	live := make(map[string]liveInfo, len(ids))
	for _, id := range ids {
		rows = append(rows, session{ID: id, Name: id, Project: "repo", CWD: "/tmp/repo", Alive: true})
		info := liveInfo{ID: id, CWD: "/tmp/repo", Certain: true}
		if states[id] != "live" {
			info.State = states[id]
		}
		live[id] = info
	}
	m := &model{view: viewSessions, width: 140, height: 40, attentionSort: attentionSort,
		expanded: map[string]bool{}, live: live, cache: map[string]preview{},
		unread: map[string]bool{}, seen: map[string]string{},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: rows}}}}}
	m.expanded[m.expansionKey("/tmp/repo", "repo")] = true
	m.rebuildRows()
	return m
}

// deliver hands one message to the real Update and runs the command it returns, which is what the
// program itself does with it: a notification is a side effect of that command.
func deliver(t *testing.T, m *model, msg tea.Msg) {
	t.Helper()
	_, cmd := m.Update(msg)
	runBatch(t, cmd)
}

// TestTheAttentionSortLiftsOnlyTheChatsThatWaitOnAPerson pins the sort, and the state it does not
// sort on: a chat blocked on a prompt comes first, and a chat that has settled is left in the order
// the read gave with every other row, because a finished chat is what every chat becomes.  The sort
// is stable, so the two blocked rows keep their own order too.
func TestTheAttentionSortLiftsOnlyTheChatsThatWaitOnAPerson(t *testing.T) {
	states := map[string]string{
		"ses_1": "tool", "ses_2": "idle", "ses_3": "blocked", "ses_4": "live", "ses_5": "idle",
		"ses_6": "blocked",
	}
	m := queueModel(true, states)
	want := []string{"ses_3", "ses_6", "ses_1", "ses_2", "ses_4", "ses_5"}
	if got := sessionIDs(m.sessions); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ranked order = %v, want %v", got, want)
	}
	if len(m.sessions) < 1 || !m.sessions[0].ProjectOnly {
		t.Fatalf("the project header moved: first row = %#v", m.sessions[0])
	}
	if m.sessionCount != len(states) {
		t.Fatalf("sessionCount = %d, want %d", m.sessionCount, len(states))
	}

	// A reader who wants the plain read order gets it, and the queue key still works there.
	plain := queueModel(false, states)
	want = []string{"ses_1", "ses_2", "ses_3", "ses_4", "ses_5", "ses_6"}
	if got := sessionIDs(plain.sessions); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unranked order = %v, want the read order %v", got, want)
	}
}

// TestTheRankingLeavesTheGroupOrderAlone pins the half of the ranking that must not happen: the
// sort runs inside a group, so a project with a blocked chat does not jump above its neighbours.
func TestTheRankingLeavesTheGroupOrderAlone(t *testing.T) {
	m := &model{view: viewSessions, width: 140, height: 40, attentionSort: true,
		expanded: map[string]bool{}, cache: map[string]preview{}, live: map[string]liveInfo{
			"ses_b": {ID: "ses_b", CWD: "/tmp/docs", State: "blocked"},
		},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "docs", CWD: "/tmp/docs", Sessions: []session{
				{ID: "ses_a", Name: "a", Project: "docs", CWD: "/tmp/docs"}}},
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{
				{ID: "ses_b", Name: "b", Project: "repo", CWD: "/tmp/repo"}}}}}}}
	m.rebuildRows()
	headers := make([]string, 0, 2)
	for _, row := range m.sessions {
		if row.ProjectOnly {
			headers = append(headers, row.Project)
		}
	}
	if strings.Join(headers, ",") != "docs,repo" {
		t.Fatalf("group order = %v, want the order the read gave (docs, repo)", headers)
	}
}

// pressKey drives one key through the real dispatch, which is what the key map resolves.
func pressKey(t *testing.T, m *model, key string) {
	t.Helper()
	if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}); cmd != nil {
		// The command is a read of the next row's transcript.  A hand-built model has no helper,
		// so it is dropped; the cursor and the status are what this test is about.
		_ = cmd
	}
}

// TestTheJumpKeyWalksEveryWaitingChatAndOpensItsGroup pins the queue's one key: it reaches each
// chat that is blocked on a person in turn, opens the group that hides it, wraps around, and says
// so when there is none.
func TestTheJumpKeyWalksEveryWaitingChatAndOpensItsGroup(t *testing.T) {
	m := &model{view: viewSessions, width: 140, height: 40, attentionSort: true,
		expanded: map[string]bool{}, cache: map[string]preview{}, unread: map[string]bool{},
		seen: map[string]string{}, live: map[string]liveInfo{
			"ses_b": {ID: "ses_b", CWD: "/tmp/docs", State: "blocked"},
			"ses_d": {ID: "ses_d", CWD: "/tmp/repo", State: "blocked"},
		},
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "docs", CWD: "/tmp/docs", Sessions: []session{
				{ID: "ses_a", Name: "a", Project: "docs", CWD: "/tmp/docs"},
				{ID: "ses_b", Name: "b", Project: "docs", CWD: "/tmp/docs"}}},
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{
				{ID: "ses_c", Name: "c", Project: "repo", CWD: "/tmp/repo"},
				{ID: "ses_d", Name: "d", Project: "repo", CWD: "/tmp/repo"}}}}}}}
	m.rebuildRows()

	// Both groups start closed, so the whole queue is hidden and the cursor sits on a header.
	if got := sessionIDs(m.sessions); len(got) != 0 {
		t.Fatalf("precondition: %v is on screen with every group closed", got)
	}
	var visited []string
	for range 2 {
		for range 2 {
			pressKey(t, m, "n")
			visited = append(visited, m.selected().ID)
		}
	}
	want := []string{"ses_b", "ses_d", "ses_b", "ses_d"}
	if strings.Join(visited, ",") != strings.Join(want, ",") {
		t.Fatalf("the queue visited %v, want %v", visited, want)
	}
	if !m.selected().ProjectOnly && m.selected().Depth != 1 {
		t.Fatalf("the cursor landed on %#v, want a session row inside an opened group", m.selected())
	}
	for _, group := range m.data["local"].Groups {
		key := m.expansionKey(group.CWD, group.Project)
		if !m.expanded[key] {
			t.Fatalf("group %s stayed closed, so its waiting row is still hidden", group.Project)
		}
	}

	// A group with a waiting chat but no reading of it is opened too: the header is the only row
	// on screen, and the cursor ends up on the chat and not on the header.  With nothing waiting at
	// all, the cursor stays where it was and the status line says why.
	empty := queueModel(true, map[string]string{"ses_1": "tool"})
	before := empty.selected()
	pressKey(t, empty, "n")
	if empty.selected().ID != before.ID {
		t.Fatalf("the cursor moved to %#v with nothing waiting", empty.selected())
	}
	if empty.status != "no chat is waiting on you" {
		t.Fatalf("status = %q, want it to say that nothing waits", empty.status)
	}
}

// TestAnUnreadMarkSurvivesAPollAndClearsWhenTheCursorArrives pins the whole life of the mark: it
// appears when a chat starts waiting, it is not repeated by the next read, a poll does not clear
// it, and the cursor arriving on the row is what does.
func TestAnUnreadMarkSurvivesAPollAndClearsWhenTheCursorArrives(t *testing.T) {
	m := queueModel(false, map[string]string{"ses_1": "tool", "ses_2": "working"})
	m.seen = map[string]string{"ses_1": "tool", "ses_2": "working"}
	m.visited = "ses_1"
	m.cursor = 1 // the cursor starts on the first session, not on the header

	m.Update(stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "tool"},
		"ses_2": {ID: "ses_2", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"},
	}})
	if !m.unread["ses_2"] {
		t.Fatal("a chat that became blocked was not marked unread")
	}
	marked := false
	for _, row := range m.sessions {
		if row.ID != "ses_2" {
			continue
		}
		marked = strings.HasPrefix(ansi.Strip(m.rowView(row, nil, false)), "•")
	}
	if !marked {
		t.Fatal("the unread row does not carry the mark")
	}

	// The next poll says the same thing, so it is not news a second time, and the mark stays.
	m.Update(stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "tool"},
		"ses_2": {ID: "ses_2", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"},
	}})
	if !m.unread["ses_2"] {
		t.Fatal("a poll cleared the mark of a row the reader has not visited")
	}

	// The cursor arrives on the row, which is the visit.
	m.cursor = 2
	m.clampCursor()
	if m.unread["ses_2"] {
		t.Fatal("the mark survived the cursor arriving on the row")
	}
}

// TestAChatAlreadyWaitingIsNotAnnouncedAsNew pins the seed: the first read of a chat is silent, so
// opening the picker on a machine full of blocked chats does not fire a notification for each one.
func TestAChatAlreadyWaitingIsNotAnnouncedAsNew(t *testing.T) {
	var out bytes.Buffer
	m := queueModel(true, map[string]string{"ses_1": "live"})
	m.out, m.notifierRole, m.notify = &out, true, notifyModeTerminal

	m.Update(stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"},
	}})
	if out.Len() != 0 {
		t.Fatalf("the first read raised %q, want silence", out.String())
	}
	if m.unread["ses_1"] {
		t.Fatal("a chat that was already waiting was marked as new")
	}

	// A change the picker watches happen is news.
	deliver(t, m, stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "tool", Detail: "bash"},
	}})
	deliver(t, m, stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"},
	}})
	if got := out.String(); !strings.Contains(got, "\x1b]9;pib: repo needs you\x07") {
		t.Fatalf("notification = %q, want one desktop notification naming the project", got)
	}
	if !m.unread["ses_1"] {
		t.Fatal("the blocked chat was not marked unread")
	}
}

// TestABurstOfChatsMakesOneNoise pins the rate limit: several chats that start waiting in one read
// make one message, and a chat that is not live cannot raise one at all.
func TestABurstOfChatsMakesOneNoise(t *testing.T) {
	var out bytes.Buffer
	m := queueModel(true, map[string]string{"ses_1": "live", "ses_2": "live", "ses_3": "live"})
	m.out, m.notifierRole, m.notify = &out, true, notifyModeTerminal
	m.seen = map[string]string{"ses_1": "working", "ses_2": "working", "ses_3": "working"}

	deliver(t, m, stateMsg{states: map[string]liveInfo{
		"ses_1":     {ID: "ses_1", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"},
		"ses_2":     {ID: "ses_2", CWD: "/tmp/repo", State: "idle"},
		"ses_9":     {ID: "ses_9", CWD: "/tmp/other", State: "blocked"},
		"ses_other": {ID: "ses_other", CWD: "/tmp/repo", State: "blocked"},
	}})
	if got := strings.Count(out.String(), "\x1b]9;"); got != 1 {
		t.Fatalf("a burst raised %d notifications (%q), want one", got, out.String())
	}
	if got := out.String(); !strings.Contains(got, "1 chat settled") ||
		!strings.Contains(got, "repo needs you") {
		t.Fatalf("notification = %q, want one message naming both states", got)
	}
	if m.unread["ses_9"] || m.unread["ses_other"] {
		t.Fatal("a chat nothing reports as live was announced")
	}
}

// TestAFirstFullReadSeedsTheStateSoTheNextChangeIsNews pins the seam a live harness found and no
// unit test did: the full read is the one that introduces a chat, so it is also the one that has to
// record what that chat was doing.  Seeding only the chats a previous read already knew left the
// first change the picker watched looking like a first sighting, and the picker stayed silent.
func TestAFirstFullReadSeedsTheStateSoTheNextChangeIsNews(t *testing.T) {
	var out bytes.Buffer
	chat := session{ID: "ses_1", Name: "fix login", Project: "repo", CWD: "/tmp/repo"}
	m := &model{view: viewSessions, width: 140, height: 40, attentionSort: true,
		expanded: map[string]bool{}, cache: map[string]preview{}, live: map[string]liveInfo{},
		unread: map[string]bool{}, seen: map[string]string{},
		out: &out, notifierRole: true, notify: notifyModeTerminal,
		data: map[string]targetData{"local": {Loaded: true, Target: target{}, Groups: []group{
			{Project: "repo", CWD: "/tmp/repo", Sessions: []session{chat}}}}}}
	m.rebuildRows()

	// The full read is the picker's first sighting of the chat, and that is silent.
	deliver(t, m, liveMsg{live: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", Certain: true, State: "tool"}}})
	if out.Len() != 0 {
		t.Fatalf("the first sighting raised %q, want silence", out.String())
	}

	// The cheap read that follows carries the change, which the picker now has something to
	// compare it with, so it is news.
	deliver(t, m, stateMsg{states: map[string]liveInfo{
		"ses_1": {ID: "ses_1", CWD: "/tmp/repo", State: "blocked", Detail: "confirm"}}})
	if !strings.Contains(out.String(), "pib: repo needs you") {
		t.Fatalf("notification = %q, want the change the picker watched announced", out.String())
	}
	if !m.unread["ses_1"] {
		t.Fatal("the blocked chat was not marked unread")
	}
}

// TestTheNotificationModesAndTheTwoGates pins what the setting buys and the two conditions under
// it: each mode writes the sequence it names, off writes nothing, a picker the reader is looking at
// stays quiet because its mark is already on screen, and a picker that is not the one that speaks
// for the machine says nothing either.
func TestTheNotificationModesAndTheTwoGates(t *testing.T) {
	started := []liveInfo{{ID: "ses_1", CWD: "/tmp/repo", State: "blocked"}}
	cases := []struct {
		name    string
		mode    string
		focused bool
		speaks  bool
		want    string
	}{
		{"terminal, the reader is elsewhere", notifyModeTerminal, false, true,
			"\x1b]9;pib: repo needs you\x07"},
		{"the bell, the reader is elsewhere", notifyModeBell, false, true, "\a"},
		{"off stays quiet", notifyModeOff, false, true, ""},
		{"terminal, the reader is looking at the picker", notifyModeTerminal, true, true, ""},
		{"the bell, the reader is looking", notifyModeBell, true, true, ""},
		{"a picker that is not the speaker", notifyModeTerminal, false, false, ""},
	}
	for _, c := range cases {
		var out bytes.Buffer
		m := &model{notify: c.mode, focused: c.focused, notifierRole: c.speaks, out: &out}
		if cmd := m.announce(started); cmd != nil {
			cmd()
		}
		if out.String() != c.want {
			t.Errorf("%s wrote %q, want %q", c.name, out.String(), c.want)
		}
	}
}

// TestOnlyOnePickerSpeaksForTheMachine pins the role that keeps a chat from being announced once
// per hidden picker: Cmd+B launches a new picker each time and an overtaken one keeps running, so
// the newest picker takes the role, an overtaken one gives it up, and a role whose holder has gone
// is taken over rather than left silent.  The claim time is what makes "newest" decidable for two
// processes that cannot see each other's start times.
func TestOnlyOnePickerSpeaksForTheMachine(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	mine := os.Getpid()

	// Nothing recorded yet: the first picker to run claims the role, with its own claim time.
	first := &model{claimedAt: 100}
	first.refreshNotifierRole()
	if !first.notifierRole {
		t.Fatal("a picker with no recorded speaker did not take the role")
	}
	record, known := readNotifierRecord()
	if !known || record.Pid != mine || record.At != 100 {
		t.Fatalf("record = %#v (known %v), want this pid at its claim time", record, known)
	}

	// A picker that started after this one holds the role, so this one gives it up.  Pid 1 is
	// launchd here and is running, which is all the liveness check asks of it.
	writeNotifierRecord(t, notifierRecord{Pid: 1, At: 200})
	overtaken := &model{claimedAt: 100}
	overtaken.refreshNotifierRole()
	if overtaken.notifierRole {
		t.Fatal("an overtaken picker kept the role")
	}

	// The recorded holder is older than this picker, so this picker is the newest and speaks.
	newest := &model{claimedAt: 300}
	newest.refreshNotifierRole()
	if !newest.notifierRole {
		t.Fatal("the newest picker did not take the role from an older one")
	}
	if record, _ = readNotifierRecord(); record.At != 300 || record.Pid != mine {
		t.Fatalf("record = %#v, want the newest picker's claim", record)
	}

	// The holder has gone: the role is taken over rather than left with a pid that is not running.
	if processAlive(999999) {
		t.Skip("pid 999999 is in use, so this test cannot prove the takeover")
	}
	writeNotifierRecord(t, notifierRecord{Pid: 999999, At: 400})
	orphan := &model{claimedAt: 100}
	orphan.refreshNotifierRole()
	if !orphan.notifierRole {
		t.Fatal("the role was left with a pid that is not running")
	}
	if !processAlive(mine) || processAlive(0) {
		t.Fatal("processAlive does not tell a live pid from an impossible one")
	}
}

// writeNotifierRecord puts one claim in the role file, as another picker would.
func writeNotifierRecord(t *testing.T, record notifierRecord) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notifierRoleFile(), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestANotificationNamesAProjectAndAStateAndNothingElse pins the safety rule: the message carries
// no path, no prompt, no tool argument, and no label.  The project's own name is the one word it
// does carry, which is the same word the row shows.
func TestANotificationNamesAProjectAndAStateAndNothingElse(t *testing.T) {
	blocked := liveInfo{ID: "ses_1", CWD: "/Users/someone/work/atlas", State: "blocked",
		Detail: "confirm", Label: "Allow rm -rf /tmp/secret"}
	settled := liveInfo{ID: "ses_2", CWD: "/", State: "idle"}
	messages := []string{notifyMessage([]liveInfo{blocked}), notifyMessage([]liveInfo{settled}),
		notifyMessage([]liveInfo{blocked, settled})}
	for _, message := range messages {
		for _, forbidden := range []string{"/", "Users", "someone", "work", "Allow", "confirm",
			"rm -rf", "tmp", "secret"} {
			if strings.Contains(message, forbidden) {
				t.Fatalf("notification %q carries %q", message, forbidden)
			}
		}
		if !strings.HasPrefix(message, "pib: ") {
			t.Fatalf("notification %q does not name the program", message)
		}
	}
	if got := messages[0]; got != "pib: atlas needs you" {
		t.Fatalf("message = %q, want the project's own name and the state", got)
	}
	if got := messages[1]; got != "pib: a chat settled" {
		t.Fatalf("message = %q, want a word for a chat with no directory to name", got)
	}
	if got := messages[2]; got != "pib: atlas needs you, 1 chat settled" {
		t.Fatalf("message = %q, want one message naming both chats", got)
	}
	if got := notifyMessage([]liveInfo{{CWD: "/tmp/repo", State: "blocked"},
		{CWD: "/tmp/docs", State: "blocked"}}); got != "pib: 2 chats need you" {
		t.Fatalf("message = %q, want one message about both chats", got)
	}
}

// TestTheQueueSettings pins the two settings: they reach the model, a value that is not one of the
// words keeps the default and says so, and --print-config shows both.
func TestTheQueueSettings(t *testing.T) {
	if cfg := loadConfig(); cfg.AttentionSort != defaultAttentionSort || cfg.Notify != defaultNotifyMode {
		t.Fatalf("empty config = attention_sort %t, notify %q; want %t and %q",
			cfg.AttentionSort, cfg.Notify, defaultAttentionSort, defaultNotifyMode)
	}

	writeConfig(t, "attention_sort: false\nnotify: bell\n")
	cfg := loadConfig()
	if cfg.AttentionSort {
		t.Fatal("attention_sort: false was not read")
	}
	if cfg.Notify != notifyModeBell {
		t.Fatalf("notify = %q, want bell", cfg.Notify)
	}
	var printed strings.Builder
	printConfiguration(&printed, cfg)
	for _, want := range []string{"attention_sort: false", "notify: bell"} {
		if !strings.Contains(printed.String(), want) {
			t.Errorf("--print-config does not show %q", want)
		}
	}

	writeConfig(t, "attention_sort: maybe\nnotify: shout\n")
	cfg = loadConfig()
	if !cfg.AttentionSort || cfg.Notify != defaultNotifyMode {
		t.Fatalf("a bad value changed a setting: %t, %q", cfg.AttentionSort, cfg.Notify)
	}
	warnings := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{"attention_sort: \"maybe\"", "notify: \"shout\""} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings %q do not name %q", warnings, want)
		}
	}
}

// TestTheQueueKeyIsRegistered pins the key itself: the registry binds it, the ? box covers it, and
// the dispatch reaches it (the drift guards in keymap_test.go are the other half).
func TestTheQueueKeyIsRegistered(t *testing.T) {
	km := buildKeymap(nil)
	if got := km.resolve(keyContextList, "n"); got != "n" {
		t.Fatalf("n resolved to %q, want the queue action", got)
	}
	if chords := km.chords("list.next_waiting"); len(chords) != 1 || chords[0] != "n" {
		t.Fatalf("list.next_waiting chords = %v, want n", chords)
	}
	if action, known := keyActionByName("list.next_waiting"); !known || action.help == "" {
		t.Fatal("the queue action is missing from the registry or carries no help text")
	}
}

// TestAHostsWaitingChatIsQueuedAndNamedWithItsHost pins the queue on a host's rows: a chat that
// host reports as blocked is lifted above its neighbour, counted, reached by the queue key, marked
// unread, and announced in a message that names the host, because the same project exists on more
// than one machine.
func TestAHostsWaitingChatIsQueuedAndNamedWithItsHost(t *testing.T) {
	m := hostStateModel()
	m.current = 1 // the host's target is the one on screen
	m.expanded[m.expansionKey("/srv/api", "api")] = true
	m.rebuildRows()
	var out bytes.Buffer
	m.out, m.notifierRole, m.notify = &out, true, notifyModeTerminal
	// The picker watched this chat work, so going blocked is news rather than a first sighting.
	m.remoteSeen[remoteStateKey("build-host", "ses_remote")] = "working"

	deliver(t, m, remoteStateMsg{server: "build-host", read: true, states: map[string]liveInfo{
		"ses_busy":   {ID: "ses_busy", CWD: "/srv/api", State: "tool", Detail: "bash"},
		"ses_remote": {ID: "ses_remote", CWD: "/srv/api", State: "blocked", Detail: "confirm"}}})

	if order := sessionIDs(m.sessions); strings.Join(order, ",") != "ses_remote,ses_busy" {
		t.Fatalf("the host's rows = %v, want the blocked chat lifted", order)
	}
	if m.waitingCount != 1 {
		t.Fatalf("waitingCount = %d, want the host's blocked chat counted", m.waitingCount)
	}
	if !m.unread["ses_remote"] {
		t.Fatal("the host's blocked chat was not marked unread")
	}
	pressKey(t, m, "n")
	if got := m.selected().ID; got != "ses_remote" {
		t.Fatalf("the queue key landed on %q, want the host's blocked chat", got)
	}
	if got := out.String(); !strings.Contains(got, "pib: api on build-host needs you") {
		t.Fatalf("notification = %q, want the project and the host named", got)
	}
	// The local row is not part of the host's answer, and its own state is untouched.
	if _, live := m.rowLive(m.data["local"].Groups[0].Sessions[0]); !live {
		t.Fatal("this machine's chat stopped being live")
	}
}

// TestAHostMessageNamesTheHostOnlyWhenThereIsOne pins the message rule: a local chat reads as it
// always did, and a host's chat adds the destination and no path.
func TestAHostMessageNamesTheHostOnlyWhenThereIsOne(t *testing.T) {
	local := notifyMessage([]liveInfo{{ID: "a", CWD: "/Users/someone/atlas", State: "blocked"}})
	if local != "pib: atlas needs you" {
		t.Fatalf("local message = %q, want the project alone", local)
	}
	remote := notifyMessage([]liveInfo{{ID: "a", CWD: "/home/someone/atlas", State: "idle",
		Server: "user@192.0.2.15"}})
	if remote != "pib: atlas on user@192.0.2.15 settled" {
		t.Fatalf("remote message = %q, want the project and the host", remote)
	}
	if strings.Contains(remote, "/") {
		t.Fatalf("remote message %q carries a path", remote)
	}
}
