package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// The attention queue.  The state plane says what every chat is doing; this turns that into
// work a person can walk through.  The rows that wait on a person sort to the top of their own
// group, one key visits them in turn, a row that starts waiting is marked until it is visited,
// and the terminal can raise a notification while the picker does not have the reader's attention.
//
// Two rules run through all of it.  A chat needs a person when its state is `blocked`: it is
// stopped on a question and nothing else moves until it is answered.  A chat that is `idle` has
// settled and wants the next instruction, which is not urgent and is not lifted in the grouped
// list; the flat priority list ranks it, and the trade-off is recorded at the rank scale below.
// Nothing here reads a transcript, and nothing here carries prompt text anywhere: a mark and a
// notification name a project and a state, never what was asked.

// The notify modes, which are the accepted values of the notify setting.
const (
	notifyModeOff      = "off"
	notifyModeBell     = "bell"
	notifyModeTerminal = "terminal"
)

// The rank scale.  Only the first rank is lifted in the grouped list, and phase 1 measured why:
// `idle` is what every finished chat is, so lifting it would lift most of the list and the rank
// would stop saying anything.  `blocked` is the one state that needs a person now.  The flat
// priority list makes the opposite bargain -- a reader who asked for it wants every settled chat
// above every running one -- so its ranking reads the whole scale, and accepts that the settled
// rank is long.
const (
	rankWaiting  = iota // blocked on a person: the row this picker exists to find
	rankSettled         // idle: the chat has finished and wants the next instruction
	rankRunning         // a tool is running, or the chat is live and nothing said what it is doing
	rankOrdinary        // ended, gone, not running, or a state nothing here knows
)

// rowRank reads one row's rank for the grouped sort.  Only a chat blocked on a person is lifted,
// which is the rule the grouped list has always kept: every other row keeps the order the read
// gave, whether the row is this machine's or a host's.  A row nothing reports as live is ordinary.
func (m *model) rowRank(s session) int {
	if m.waiting(s) {
		return rankWaiting
	}
	return rankOrdinary
}

// priorityRank reads one row's rank for the flat priority list, from the state the picker already
// holds.  The state ranks the same wherever it came from, so a host's reported state lifts its row
// like a local one and a host that says nothing leaves its rows among the ordinary ones.  A chat
// that is not live has no state to rank, so it is ordinary too.
func (m *model) priorityRank(s session) int {
	info, live := m.rowLive(s)
	if !live {
		return rankOrdinary
	}
	switch {
	case info.State == "blocked":
		return rankWaiting
	case info.State == "idle":
		return rankSettled
	case info.State == "tool":
		return rankRunning
	case info.State == "" && !info.Stale:
		// Live with nothing said about it: a record from an older extension, or a chat that has
		// just come alive.
		return rankRunning
	}
	return rankOrdinary
}

// rankAll orders the flat priority list: what needs a person first, then what has settled, then
// what is running, then everything else.  This is the one place the rank order lives.  Inside one
// rank the reader's own signals decide: an unread row first, then the most recently touched, then
// the project, then the session name.  The sort is stable, so two rows that agree on everything
// keep the order the read gave.
func (m *model) rankAll(rows []session) []session {
	if len(rows) < 2 {
		return rows
	}
	out := make([]session, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if leftRank, rightRank := m.priorityRank(left), m.priorityRank(right); leftRank != rightRank {
			return leftRank < rightRank
		}
		if m.unread[left.ID] != m.unread[right.ID] {
			return m.unread[left.ID]
		}
		if left.Mod != right.Mod {
			return left.Mod > right.Mod
		}
		if left.Project != right.Project {
			return left.Project < right.Project
		}
		return left.Name < right.Name
	})
	return out
}

// rankRows lifts the rows that wait on a person above the rest of their own group.  Only the
// inside of a group is reordered: the groups keep their order, so no project jumps above another
// one.  The sort is stable, so the rows of equal rank -- every row that does not wait on a person
// -- keep the order the read gave, and a copy is returned because the rows handed in may be the
// cached read itself.
func (m *model) rankRows(rows []session) []session {
	if !m.attentionSort || len(rows) < 2 {
		return rows
	}
	out := make([]session, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool { return m.rowRank(out[i]) < m.rowRank(out[j]) })
	return out
}

// waiting reports whether one row is a chat a person has to answer.
func (m *model) waiting(s session) bool {
	info, live := m.rowLive(s)
	return live && info.State == "blocked"
}

// jumpToWaiting moves the cursor to the next chat that waits on a person, and opens the group
// that holds it: a group starts closed, so a queue that skipped closed groups would hide exactly
// the rows it exists to find.  The walk wraps, every waiting row is reached in turn, and the
// status line says so when there is none.  The project list owns the queue, so a jump from the
// zmx pane focuses it first.
func (m *model) jumpToWaiting() (tea.Model, tea.Cmd) {
	m.view = viewSessions
	m.focus = paneFocusList
	// One step per row, plus one for a group this walk opens: the walk is bounded, so a list that
	// changes under it cannot spin.  Step zero is the header under the cursor itself: the selected
	// group is the nearest one, so pressing the key on a closed group opens it and lands on the
	// chat inside rather than skipping past it to the next project.  A session row under the
	// cursor is not "the next one", so step zero never lands on a session row.
	for step := 0; step <= len(m.sessions)+1; step++ {
		rows := m.sessions
		if len(rows) == 0 {
			break
		}
		index := (m.cursor + step) % len(rows)
		row := rows[index]
		if row.ProjectOnly {
			if row.Waiting == 0 || row.Expanded {
				continue
			}
			m.expanded[m.expansionKey(row.CWD, row.Project)] = true
			m.rebuildRows()
			// The rows moved when the group opened, so the first waiting row after the header
			// is found by walking the rebuilt list rather than by the index above.
			for next := index + 1; next < len(m.sessions); next++ {
				if m.sessions[next].ProjectOnly {
					break
				}
				if m.waiting(m.sessions[next]) {
					return m.landOn(next)
				}
			}
			continue
		}
		if step == 0 {
			continue
		}
		if m.waiting(row) {
			return m.landOn(index)
		}
	}
	m.status = "no chat is waiting on you"
	return m, nil
}

// landOn puts the cursor on one row of the project list and reads what the row shows.
func (m *model) landOn(index int) (tea.Model, tea.Cmd) {
	m.cursor = index
	m.clampCursor()
	if name := trim(m.selected().Name, 30); name != "" {
		m.status = "waiting: " + name
	} else {
		m.status = "waiting: " + trim(m.selected().Project, 30)
	}
	return m, m.refreshPreview()
}

// observeStates folds one read into the picker's memory of what each chat was doing, and returns
// the chats that started waiting on a person since the read before it.  seen is that memory for
// the machine this read is about, live answers which of the chats in the read are running, and
// introducing says whether the read is the authority on which chats are live at all.  A chat the
// picker has not seen before is recorded silently: one that was already waiting when the picker
// opened is not news, and neither is one that has just come alive.  The live full read is the one
// that introduces a chat; a cheap read, local or remote, only decorates chats that read already
// found, so a record left behind by a process that died can raise nothing.  The result is ordered
// by id, so one message about several chats names the same project every time.
func observeStates(states map[string]liveInfo, seen map[string]string, introducing bool,
	live func(string) bool) []liveInfo {
	var started []liveInfo
	for id, info := range states {
		if !live(id) && !introducing {
			continue
		}
		previous, known := seen[id]
		if known && previous == info.State {
			continue
		}
		seen[id] = info.State
		if known && (info.State == "blocked" || info.State == "idle") {
			started = append(started, info)
		}
	}
	sort.Slice(started, func(i, j int) bool { return started[i].ID < started[j].ID })
	return started
}

// chatMemory returns this machine's memory of what each chat was doing, ready to be written to.  A
// picker built by hand in a test may not have one yet, and a plain read must never panic over that.
func (m *model) chatMemory() map[string]string {
	if m.seen == nil {
		m.seen = map[string]string{}
	}
	return m.seen
}

// remoteStateKey names one host's chat in the picker's own memory.  Two machines can run projects
// with the same name, so a state is remembered per host and per chat rather than per chat alone.
func remoteStateKey(server, id string) string { return server + "\x00" + id }

// remoteRows is the host's own chats as that host last reported them, by session id.  Two things
// come from here rather than from the state read.  The running flag, because only the host's own
// session list checks its processes.  And the directory, because the state read is the cheap one
// and carries none, while a notification has to name a project: the row the host already answered
// with is where that name is.  A chat the host does not report carries no state, marks nothing,
// and can raise nothing, which is the guard this machine's cheap read has through `m.live`.
func (m *model) remoteRows(server string) map[string]session {
	rows := map[string]session{}
	for _, host := range m.targets {
		if host.Server != server {
			continue
		}
		for _, group := range m.data[host.label()].Groups {
			for _, row := range group.Sessions {
				if row.ID != "" {
					rows[row.ID] = row
				}
			}
		}
	}
	return rows
}

// applyRemoteStates folds one host's answer into the picker's memory and returns the chats that
// started waiting on a person since that host was read before.  A read that failed changes
// nothing: a host that cannot be reached is not a host whose chats have stopped.
//
// This host's memory is keyed the way its states are, so one host's pruning can never reach
// another host's chats or this machine's.
func (m *model) applyRemoteStates(msg remoteStateMsg) []liveInfo {
	if !msg.read {
		return nil
	}
	if m.remoteStates == nil {
		m.remoteStates = map[string]liveInfo{}
	}
	if m.remoteSeen == nil {
		m.remoteSeen = map[string]string{}
	}
	if m.remoteKnown == nil {
		m.remoteKnown = map[string]bool{}
	}
	prefix := msg.server + "\x00"
	rows := m.remoteRows(msg.server)
	keyed := make(map[string]liveInfo, len(msg.states))
	for id, info := range msg.states {
		// The host is named on the entry itself, so a notification about a chat on another
		// machine cannot read as one about this machine's project of the same name.  The directory
		// comes from the row, because the state read does not carry one.
		info.Server = msg.server
		if row, known := rows[id]; known {
			info.CWD = row.CWD
		}
		keyed[prefix+id] = info
	}
	liveKey := func(key string) bool {
		row, known := rows[strings.TrimPrefix(key, prefix)]
		return known && row.Live
	}
	started := observeStates(keyed, m.remoteSeen, false, liveKey)
	m.forgetRemote(msg.server, msg.states)
	for key := range keyed {
		m.remoteKnown[key] = true
	}
	// Only a chat the host reports as running keeps its state, which is the same rule this
	// machine's cheap read follows through `m.live`: a record left behind by a process that died
	// decorates no row, marks nothing, and outlives nothing.
	for key, info := range keyed {
		if liveKey(key) {
			m.remoteStates[key] = info
		}
	}
	return started
}

// forgetRemote drops what one host has stopped reporting at all.  The answer is what the host
// just said, not what the picker remembers, so the ids this host has named are what decides: a
// chat it no longer names leaves no state, no memory, and no unread mark behind, and one that
// comes back is a first sighting rather than news.
//
// Only keys with this host's own prefix are touched, so another host's chats and this machine's
// are out of reach.
func (m *model) forgetRemote(server string, states map[string]liveInfo) {
	prefix := server + "\x00"
	for key := range m.remoteKnown {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		id := strings.TrimPrefix(key, prefix)
		if _, present := states[id]; present {
			continue
		}
		delete(m.remoteKnown, key)
		delete(m.remoteStates, key)
		delete(m.remoteSeen, key)
		delete(m.unread, id)
	}
}

// forgetGone drops the memory of this machine's chats that are no longer live, which the full read
// is the authority on.  Only the ids this machine's own reads recorded are touched, so a host's
// chats keep their memory and their marks.
func (m *model) forgetGone(live map[string]liveInfo) {
	for id := range m.seen {
		if _, present := live[id]; present {
			continue
		}
		delete(m.seen, id)
		delete(m.unread, id)
	}
}

// markWaiting marks the chats that just started waiting, so their rows say "this changed since
// you last looked".  The mark is presentation only: it changes no order and writes no state.
func (m *model) markWaiting(started []liveInfo) {
	if len(started) == 0 {
		return
	}
	if m.unread == nil {
		m.unread = map[string]bool{}
	}
	for _, info := range started {
		m.unread[info.ID] = true
	}
}

// announce is the notification a read earns: one message for every chat that started waiting in
// that read, or nothing at all when the reader asked for quiet, when the picker has the reader's
// attention already, when this picker is not the one that speaks, or when nothing started waiting.
// One message per read is also the rate limit: a burst of chats that settle together makes one
// noise, not five.
//
// The notification is for a reader who is somewhere else.  A picker that is in front of the reader
// says nothing, because the mark and the state column are already there to be read; a picker that
// does not know whether it is in front says nothing either, for the same reason.  What it never
// does is decide not to be heard: see focusReport and the note on Init.
func (m *model) announce(started []liveInfo) tea.Cmd {
	if len(started) == 0 || m.notify == notifyModeOff || m.focused || !m.notifierRole {
		return nil
	}
	message := notifyMessage(started)
	if message == "" {
		return nil
	}
	mode, out := m.notify, m.out
	return func() tea.Msg {
		writeNotification(out, mode, message)
		return nil
	}
}

// notifyMessage says what started waiting, by project, state, and host where there is one.  It
// never carries prompt text, a tool argument, a transcript excerpt, or a path: the project is the
// base name of the chat's directory, which is the word the row itself shows, and the host is
// named because the same project exists on more than one machine.
func notifyMessage(started []liveInfo) string {
	blocked, settled := 0, 0
	blockedFirst, settledFirst := liveInfo{}, liveInfo{}
	for _, info := range started {
		switch info.State {
		case "blocked":
			blocked++
			if blockedFirst.ID == "" {
				blockedFirst = info
			}
		case "idle":
			settled++
			if settledFirst.ID == "" {
				settledFirst = info
			}
		}
	}
	blockedWhere := projectName(blockedFirst.CWD) + onHost(blockedFirst.Server)
	settledWhere := projectName(settledFirst.CWD) + onHost(settledFirst.Server)
	switch {
	case blocked == 1 && settled == 0:
		return "sh2pil-sessions: " + blockedWhere + " needs you"
	case blocked == 1:
		return fmt.Sprintf("sh2pil-sessions: %s needs you, %s settled", blockedWhere, plural(settled, "chat"))
	case blocked > 1 && settled == 0:
		return "sh2pil-sessions: " + plural(blocked, "chat") + " need you"
	case blocked > 1:
		return fmt.Sprintf("sh2pil-sessions: %s need you, %s settled", plural(blocked, "chat"),
			plural(settled, "chat"))
	case settled == 1:
		return "sh2pil-sessions: " + settledWhere + " settled"
	default:
		return "sh2pil-sessions: " + plural(settled, "chat") + " settled"
	}
}

// onHost names the machine a message is about, and says nothing when the chat is on this one.  A
// destination is not a path: it is the alias the reader typed, and without it two projects with
// one name would read as one chat.
func onHost(server string) string {
	if server == "" {
		return ""
	}
	return " on " + server
}

// projectName is what a chat is called in a notification: the base name of its directory, and a
// plain word when there is none to name.  It is the same word the row's project column shows.
func projectName(cwd string) string {
	name := filepath.Base(strings.TrimSpace(cwd))
	switch name {
	case "", ".", string(filepath.Separator):
		return "a chat"
	}
	return name
}

// writeNotification writes one escape sequence for the terminal to raise a notification.  It goes
// straight to the terminal because Bubble Tea's own print path does nothing while the alternate
// screen is active, and it shells out to nothing: a notification that cost a subprocess would be
// too expensive to raise on a poll.  The sequence is self-terminating, so a frame painted around
// it is not swallowed.
func writeNotification(out io.Writer, mode, message string) {
	if out == nil {
		return
	}
	switch mode {
	case notifyModeBell:
		// The bell, which kitty is configured to keep silent here: it lights the tab instead.
		fmt.Fprint(out, "\a")
	case notifyModeTerminal:
		// OSC 9 is the desktop notification a terminal raises for the program inside it.
		fmt.Fprint(out, "\x1b]9;"+message+"\x07")
	}
}

// focusReport asks the terminal to say when the picker gains and loses focus, which is the closest
// thing to "the picker is on screen" that a terminal offers.  A terminal that never answers leaves
// the picker believing it does not have the reader's attention, so it speaks up: the reader is told
// about a chat that needs them, which is the point, rather than nothing being said at all.
func focusReport() tea.Cmd {
	return func() tea.Msg { return tea.EnableReportFocus() }
}

// notifierRoleFile is where the picker records which of its own processes speaks for this machine.
// Cmd+B launches a new overlay every time and a picker that has been overtaken keeps running
// hidden, so several pickers can watch the same chat.  Without one speaker between them, a chat
// that starts waiting would be announced once per hidden picker.
func notifierRoleFile() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "sh2pil", "notifier")
}

// notifierRecord is what the pickers agree on: which of them speaks, and when it took the role.
// The claim time is what makes "newest" decidable between two processes that cannot see each
// other's start times, and it is fixed for the life of a picker, so two pickers reading the same
// file always reach the same answer about which of them speaks.
type notifierRecord struct {
	Pid int     `json:"pid"`
	At  float64 `json:"at"`
}

func readNotifierRecord() (notifierRecord, bool) {
	data, err := os.ReadFile(notifierRoleFile())
	if err != nil {
		return notifierRecord{}, false
	}
	var record notifierRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Pid <= 0 {
		return notifierRecord{}, false
	}
	return record, true
}

// claimNotifierRole writes this process into the record.  The newest picker is the one the reader
// opened last and the one the next key reaches, so it is the one that speaks.
func (m *model) claimNotifierRole() bool {
	path := notifierRoleFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	record, err := json.Marshal(notifierRecord{Pid: os.Getpid(), At: m.claimedAt})
	if err != nil {
		return false
	}
	return os.WriteFile(path, append(record, '\n'), 0o644) == nil
}

// refreshNotifierRole keeps the role honest while the picker runs.  A picker that a newer one has
// replaced stops speaking; a picker whose predecessor is gone takes the role over, so a machine
// left with one old hidden picker is still told when a chat starts waiting.  It costs one small
// read per state clock tick, and nothing while the picker is closed.
func (m *model) refreshNotifierRole() {
	record, known := readNotifierRecord()
	switch {
	case known && record.Pid == os.Getpid():
		m.notifierRole = true
	case known && record.At > m.claimedAt && processAlive(record.Pid):
		m.notifierRole = false
	default:
		m.notifierRole = m.claimNotifierRole()
	}
}

// processAlive reports whether a process with this pid exists.  A signal that cannot be delivered
// to a process that is there still proves the process is there, which is why a permission error
// counts as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
