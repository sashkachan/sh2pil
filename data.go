package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// session mirrors one row of `sh2pil-sessions list --json`, and the picker's project and zmx rows are
// built on it too: a project has ProjectOnly set, a zmx session has ZmxOnly set, and a
// session has neither.
type session struct {
	ID      string  `json:"id"`
	Harness string  `json:"harness"`
	Name    string  `json:"name"`
	Project string  `json:"project"`
	CWD     string  `json:"cwd"`
	Alive   bool    `json:"alive"`
	Bytes   int64   `json:"bytes"`
	Mod     float64 `json:"modified"`
	File    string  `json:"file"`
	// Live is the answer a remote host gives about its own pi processes.  A local row leaves
	// it false and is marked from `sh2pil-open live` instead, because that also says which window
	// shows the session.
	Live bool `json:"live"`
	// ProjectOnly marks a project row.  In the grouped list that row is a group header, and
	// Expanded says whether the sessions under it are shown.  Depth is 0 on a header and 1 on
	// a session that sits under one, which is what indents it.
	ProjectOnly bool
	Expanded    bool
	Depth       int
	Count       int // a group header: how many sessions are under it
	Waiting     int // a group header: how many of them are blocked on a person
	Ignored     bool
	// Dim marks a project the query did not answer: filter_hide false keeps it as a dim folded
	// header so the list never hides where the reader is.
	Dim bool
	// A zmx row is one live zmx session.  ID and File are the Pi chat the session carries,
	// when it carries one; ZmxName is the name zmx knows, and the handle an attach uses.  A
	// session row that runs inside a zmx session carries that name too, which is how the
	// session list marks it.
	ZmxOnly bool
	ZmxName string
	Server  string // SSH destination for a remote zmx session; empty means this host
	Clients int
	Current bool // the session the picker itself is running inside
	Command string
}

// zmxSession mirrors one entry of `sh2pil-open zmx-list --json`: a live zmx session, the chat
// its `pi=` label points at, and where that chat's transcript is.  A remote entry carries the
// label but no chat: the picker names it from the session list it read from the same host.
type zmxSession struct {
	Name    string  `json:"name"`
	Chat    string  `json:"chat"`
	Pi      string  `json:"pi"`
	Project string  `json:"project"`
	Command string  `json:"cmd"`
	CWD     string  `json:"cwd"`
	File    string  `json:"file"`
	Clients int     `json:"clients"`
	Created float64 `json:"created"`
	Current bool    `json:"current"`
}

// liveInfo mirrors one entry of `sh2pil-open live --json`: a running pi process that may own
// the session, with the evidence the helper found.
//
// State is what the chat is doing, and it is the one part of this that a record written by an
// older extension does not carry: State is then empty and the row says only that the chat is
// live.  A state is believed only while its clock is fresh, so the helper empties a stale
// working state and keeps what was published in LastState.
type liveInfo struct {
	ID      string `json:"id"`
	CWD     string `json:"cwd"`
	Written int    `json:"written"`
	Pids    []int  `json:"pids"`
	Certain bool   `json:"certain"`
	Owner   string `json:"owner"`

	State     string `json:"state"`      // working, tool, blocked, idle, starting, ended, or empty
	LastState string `json:"last_state"` // the state a stale record was last seen in
	Detail    string `json:"detail"`     // the tool name, or the kind of prompt being answered
	Label     string `json:"label"`      // a blocked prompt's own short label, never its content
	Age       int    `json:"age"`        // seconds the state has lasted
	Stale     bool   `json:"stale"`      // the clock stopped while the chat works: do not trust State

	// Server names the host a remote state came from.  A notification says it, so that the same
	// project name on two machines cannot be read as one chat.  It is never taken from the wire:
	// a record belongs to the machine that wrote it, and only this machine's rows leave it empty.
	Server string `json:"-"`
}

func (l liveInfo) pids() string {
	parts := make([]string, 0, len(l.Pids))
	for _, pid := range l.Pids {
		parts = append(parts, fmt.Sprint(pid))
	}
	return strings.Join(parts, ",")
}

type preview struct {
	id     string
	source []string // the markdown as it came from sh2pil-sessions
	lines  []string // rendered: styled markdown, or the source when rendering is off
	width  int      // the word wrap these lines were rendered for
	scroll int
}

// previewLines is how much of a transcript end the preview asks for.  The reader can scroll
// a little beyond a screenful, and a longer tail costs more to render for no benefit.
const previewLines = 400

// liveMsg carries the running pi processes this machine knows about.
type liveMsg struct{ live map[string]liveInfo }

// stateMsg carries what every registered chat on this machine is doing.  It is the cheap read:
// no process check, no window scan, and no transcript read, so it can run on a clock.
type stateMsg struct{ states map[string]liveInfo }

// remoteStateMsg carries what one host's registered chats are doing, as that host reported them.
// The records are written on the machine that owns the chat, so the answer is the host's own.  An
// empty answer asks for that host's chats to be forgotten, while a failed read keeps them: a host
// that cannot be reached is not a host whose chats have stopped.
type remoteStateMsg struct {
	server string
	states map[string]liveInfo
	read   bool
}

// liveTickMsg asks for the cheap state read again.
type liveTickMsg struct{}

// liveTickFullEvery counts cheap reads before the full live read runs again, so that a chat which
// has gone away is dropped and a window that has moved is picked up.  At the default interval
// this is the thirty seconds the remote poll already uses.
const liveTickFullEvery = 10

// remoteStateEvery counts state ticks between reads of one host's chats.  A host's read costs the
// ssh round trip and a helper start on that machine, so it runs on a slower clock than this
// machine's own: at the default three second interval this is about nine seconds, which is inside
// the thirty seconds the full read already uses and far outside the cost of the read itself.
const remoteStateEvery = 3

// targetMsg carries one target's read.  A failed read carries only Err, so the rows that are
// already on screen stay where they are.
type targetMsg struct {
	label string
	data  targetData
}

// sshCheckMsg answers whether one host already has an SSH master.  The picker asks that on
// the ordinary helper path, because a terminal handover would repaint the whole screen for a
// question that costs 50 ms.
type sshCheckMsg struct {
	label   string
	server  string
	present bool
}

// connectMsg is the end of an interactive connect, which is where the YubiKey touch and the
// ssh output are on screen.
type connectMsg struct {
	label string
	err   error
}

// forgetMsg is the end of a "forget the master" command: the master is gone, and the target's
// cached connection state has to say so without a refresh that would connect again.
type forgetMsg struct {
	label string
	err   error
}

// targetRefreshTickMsg is the delayed read a target switch schedules.  It carries the target
// and the generation it was made for, so a tick whose target has already been left behind
// starts no read: rotating through the tabs starts one, not one per tab.
type targetRefreshTickMsg struct {
	label      string
	generation int
}

// targetRefreshDelay is how long a target must stay selected before its read starts.  Short
// enough that a real visit feels immediate, long enough that holding tab does not queue a
// read per host.
const targetRefreshDelay = 200 * time.Millisecond

type remotePollTickMsg struct{}
type previewMsg struct{ preview preview }

// previewTickMsg arrives after the cursor has rested, so fast movement through the list
// does not start one transcript render per keystroke.  It carries the session it was
// scheduled for, so a tick whose session is no longer selected is dropped.
type previewTickMsg struct{ id string }
type projectFilesTickMsg struct{ path string }
type projectFilesMsg struct {
	path  string
	files []string
	err   string
}

// zmxHistoryTickMsg arrives after the cursor has rested, and carries the session it was
// scheduled for, so a late tick cannot fill the pane with another session's output.
type zmxHistoryTickMsg struct{ name string }

// zmxHistoryMsg carries the end of one session's own scrollback.  A zmx session that
// carries no chat has no transcript to read, and its terminal is the only picture of it.
type zmxHistoryMsg struct {
	name  string
	lines []string
	err   string
}

const remotePollInterval = 30 * time.Second

func remotePollTick() tea.Cmd {
	if len(readZmxServers()) == 0 {
		return nil
	}
	return tea.Tick(remotePollInterval, func(time.Time) tea.Msg { return remotePollTickMsg{} })
}

type noteMsg struct{ note string }
type actionMsg struct {
	note string
	err  error
}

// interpreter returns the python3 the sibling helpers run under, resolved once at startup.
func (m model) interpreter() string {
	if m.python != "" {
		return m.python
	}
	return resolvePython3()
}

// helper returns the command for one of the sibling python helpers, named with the
// interpreter the build script baked in.
func (m model) helper(name string, args ...string) *exec.Cmd {
	path := filepath.Join(m.helperDir, name)
	return exec.Command(m.interpreter(), append([]string{path}, args...)...)
}

// gitCommand returns git, which is in /usr/bin and therefore always on a kitty child's
// PATH, unlike anything under /opt/homebrew.
func gitCommand(args ...string) *exec.Cmd {
	path := "/usr/bin/git"
	if found, err := exec.LookPath("git"); err == nil {
		path = found
	}
	return exec.Command(path, args...)
}

// gitRoot returns the repository that holds cwd, or cwd itself when it holds none, so
// lazygit opens at the top of the worktree instead of a subdirectory.
func gitRoot(cwd string) string {
	if cwd == "" {
		return cwd
	}
	out, err := gitCommand("-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return cwd
	}
	if root := strings.TrimSpace(string(out)); root != "" {
		return root
	}
	return cwd
}

// ---- targets ----------------------------------------------------------------

// target is where a list is read from: this machine, or one configured SSH host.  Every read
// takes one, so the local machine and a remote host differ by this value alone and no read
// needs a branch of its own.
type target struct {
	Server string // the SSH destination; empty means this machine
}

func (t target) local() bool { return t.Server == "" }

// label is what the target bar shows and what the cache is keyed on.
func (t target) label() string {
	if t.local() {
		return "local"
	}
	return t.Server
}

// readTargets returns this machine first, then each configured host in the order the config
// lists them, so the target bar reads the same way on every run.
func readTargets() []target {
	servers := readZmxServers()
	targets := make([]target, 0, len(servers)+1)
	targets = append(targets, target{})
	for _, server := range servers {
		targets = append(targets, target{Server: server})
	}
	return targets
}

// targetIndex returns the index of the target the config names, or 0 for this machine, which
// is always first.
func targetIndex(targets []target, wanted string) int {
	for index, t := range targets {
		if t.Server == wanted {
			return index
		}
	}
	return 0
}

// group is one project row and the sessions under it, on one target.  A group with no
// sessions is kept: it is a place to start a new chat.
type group struct {
	Project  string    // the project label the header shows
	CWD      string    // the absolute directory; empty for a session with no known home
	Sessions []session // newest first, as the session store lists them
	Ignored  bool      // local only: the path is in the ignored set
	Server   string    // the host it belongs to; empty means this machine
}

// harnessInfo is what one target answers about one store: whether it is there, and why it is
// not.  A store a target does not have is not an error: its rows simply do not arrive, and the
// header marks it, so the difference between "nothing written here yet" and "nothing installed
// here" is never guessed.
type harnessInfo struct {
	Harness string `json:"harness"`
	Present bool   `json:"present"`
	Reason  string `json:"reason"`
}

// targetData is everything read from one target, and how the read went.  It is cached per
// target label, so a switch back to a host that has already answered is instant.
type targetData struct {
	Target    target
	Groups    []group
	Zmx       []session
	Harnesses []harnessInfo // the stores this target has, as the target itself reports them
	Note      string        // why something is missing, when the read still produced rows
	Err       string        // why the target could not be read at all
	Loaded    bool
	Fetched   time.Time
	Connected bool // the SSH master exists; the local machine is always connected
}

// loadTarget reads one target in a single pass: its projects, its sessions, and its live zmx
// sessions.  The three reads are independent, but they belong to one round trip because a zmx
// row is named from the session rows of the same target: the `pi=` label is a session id on
// the host that answered, and only that host can say what the chat behind it is called.
func (m model) loadTarget(t target) tea.Cmd {
	return func() tea.Msg {
		label := t.label()
		projects, note := m.readProjects(t)
		sessions, failure := m.readSessions(t)
		if failure != "" {
			return targetMsg{label: label, data: targetData{Target: t, Err: failure}}
		}
		rowsByID := make(map[string]session, len(sessions))
		for _, row := range sessions {
			rowsByID[row.ID] = row
		}
		zmx, chats, zmxNote := m.readZmx(t, rowsByID)
		// A chat that runs inside a zmx session is still one row in the project list, and the
		// row says so: the zmx entry's `pi=` label is the chat's own id.
		for index := range sessions {
			if name, ok := chats[sessions[index].ID]; ok {
				sessions[index].ZmxName = name
			}
		}
		return targetMsg{label: label, data: targetData{
			Target: t, Groups: groupSessions(projects, sessions, readIgnoredProjects()),
			Zmx: zmx, Harnesses: m.readHarnessPresence(t),
			Note: joinNotes(note, zmxNote), Loaded: true, Fetched: time.Now(),
			Connected: true}}
	}
}

// readProjects reads one target's project directories.  Both sides answer the same zoxide
// query, so a host without zoxide answers with no projects and its sessions still group.
func (m model) readProjects(t target) ([]session, string) {
	helper, args := "sh2pil-sessions", []string{"projects", "--json"}
	if !t.local() {
		helper, args = "sh2pil-open", []string{"zmx-remote-projects", t.Server, "--json"}
	}
	out, err := m.helper(helper, args...).Output()
	if err != nil {
		return nil, t.label() + " projects: " + remoteFailure(err)
	}
	var paths []string
	if err := json.Unmarshal(out, &paths); err != nil {
		return nil, t.label() + " returned unreadable project data"
	}
	projects := make([]session, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			continue
		}
		projects = append(projects, session{Project: filepath.Base(path), CWD: path,
			Alive: true, ProjectOnly: true, Server: t.Server})
	}
	return projects, ""
}

// readSessions reads one target's session rows from every store the picker was told to read.
// One call reads them all, so a target costs one round trip and a row from any store lands
// beside the others with the store that produced it on the row itself.  A store that the target
// does not have contributes no rows instead of failing the read: that is what makes a machine
// running three of the four stores look like itself.  A remote row also carries the host's own
// answer about its pi processes, which is what lets the picker ask before opening a second
// writer there.
func (m model) readSessions(t target) ([]session, string) {
	stores := strings.Join(m.enabledHarnesses(), ",")
	var out []byte
	var err error
	if t.local() {
		out, err = m.helper("sh2pil-sessions", "list", "--json", "--harness", stores).Output()
	} else {
		out, err = m.helper("sh2pil-open", "session-remote-list", t.Server,
			"--harness", stores, "--live", "--json").Output()
	}
	if err != nil {
		return nil, remoteFailure(err)
	}
	var rows []session
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, t.label() + " returned data this UI cannot read"
	}
	for index := range rows {
		rows[index].Server = t.Server
		rows[index].Harness = harnessOf(rows[index])
	}
	return rows, ""
}

// readHarnessPresence asks one target which stores it has, so the header can mark the stores
// that are missing there instead of drawing a column that never fills.  A target that cannot
// answer reports nothing, which the header draws as unknown rather than absent: a read that
// failed is not evidence that a store is gone.
func (m model) readHarnessPresence(t target) []harnessInfo {
	var out []byte
	var err error
	if t.local() {
		out, err = m.helper("sh2pil-sessions", "harnesses", "--json").Output()
	} else {
		out, err = m.helper("sh2pil-open", "harnesses-remote", t.Server, "--json").Output()
	}
	if err != nil {
		return nil
	}
	var stores []harnessInfo
	if err := json.Unmarshal(out, &stores); err != nil {
		return nil
	}
	return stores
}

// readZmx reads one target's live zmx sessions, and the map from a Pi session id to the zmx
// session that carries it.  A remote entry carries its `pi=` label but no chat of its own, so
// it is named from the session rows read from the same host.
func (m model) readZmx(t target, rowsByID map[string]session) ([]session, map[string]string, string) {
	var out []byte
	var err error
	if t.local() {
		out, err = m.helper("sh2pil-open", "zmx-list", "--json").Output()
	} else {
		out, err = m.helper("sh2pil-open", "zmx-remote-list", t.Server, "--json").Output()
	}
	if err != nil {
		if t.local() {
			// A machine without zmx answers with no rows, and says nothing about it.
			return nil, nil, "the zmx list needs sh2pil-open zmx-list"
		}
		return nil, nil, t.label() + " zmx: " + remoteFailure(err)
	}
	var entries []zmxSession
	if err := json.Unmarshal(out, &entries); err != nil {
		if t.local() {
			return nil, nil, "sh2pil-open zmx-list returned data this UI cannot read"
		}
		return nil, nil, t.label() + " returned unreadable zmx data"
	}
	rows := zmxRows(entries)
	if !t.local() {
		rows = remoteZmxRows(t.Server, entries, rowsByID)
	}
	return rows, zmxChats(entries), ""
}

// ---- grouping ---------------------------------------------------------------

// groupSessions joins one target's project list to its session rows.  A session belongs to
// the longest project path that contains its recorded directory, so a repository inside
// another repository lands in the inner one.  A session that matches no project forms a group
// of its own, keyed by its own directory, so a missing zoxide entry hides nothing.
func groupSessions(projects, sessions []session, ignored map[string]bool) []group {
	groups := make([]group, 0, len(projects)+1)
	at := make(map[string]int, len(projects))
	for _, project := range projects {
		key := filepath.Clean(project.CWD)
		if _, seen := at[key]; seen {
			continue
		}
		at[key] = len(groups)
		groups = append(groups, group{Project: project.Project, CWD: project.CWD,
			Ignored: ignored[key], Server: project.Server})
	}
	// The groups the project list does not cover follow it, in the order the store lists them,
	// which is newest session first.
	loose := make([]group, 0)
	looseAt := make(map[string]int)
	for _, row := range sessions {
		if index, ok := findGroup(at, row.CWD); ok {
			groups[index].Sessions = append(groups[index].Sessions, row)
			continue
		}
		key := filepath.Clean(row.CWD)
		if row.CWD == "" {
			key = "session:" + row.ID
		}
		index, seen := looseAt[key]
		if !seen {
			index = len(loose)
			looseAt[key] = index
			loose = append(loose, group{Project: groupLabel(row), CWD: row.CWD,
				Server: row.Server})
		}
		loose[index].Sessions = append(loose[index].Sessions, row)
	}
	return append(groups, loose...)
}

// findGroup returns the index of the longest project path that contains cwd, walking up the
// directory tree so nested projects resolve to the inner one.
func findGroup(at map[string]int, cwd string) (int, bool) {
	if cwd == "" {
		return 0, false
	}
	path := filepath.Clean(cwd)
	for {
		if index, ok := at[path]; ok {
			return index, true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, false
		}
		path = parent
	}
}

// groupLabel names a group the project list did not cover, so a session outside every zoxide
// entry still reads as a project rather than as an unnamed row.
func groupLabel(row session) string {
	if row.Project != "" {
		return row.Project
	}
	if row.CWD != "" {
		return filepath.Base(row.CWD)
	}
	return "no project"
}

// joinNotes keeps the reasons a read was incomplete in one line, with no empty parts.
func joinNotes(notes ...string) string {
	kept := make([]string, 0, len(notes))
	for _, note := range notes {
		if strings.TrimSpace(note) != "" {
			kept = append(kept, note)
		}
	}
	return strings.Join(kept, "; ")
}

// ---- connection -------------------------------------------------------------

// checkMaster asks whether a host already has an SSH master.  It runs on the ordinary helper
// path, off the terminal: a handover would release and restore the screen for a question that
// costs 50 ms, which the reader would see as a flicker on every rotation.
func (m model) checkMaster(t target) tea.Cmd {
	label := t.label()
	return func() tea.Msg {
		// --check exits 0 when the master exists and 1 when it does not.
		err := m.helper("sh2pil-open", "zmx-connect", "--check", t.Server).Run()
		return sshCheckMsg{label: label, server: t.Server, present: err == nil}
	}
}

// connectMaster establishes the master for one host, with the UI suspended so the YubiKey
// touch and the ssh output are on screen.  The helper retries the two-YubiKey failure itself,
// so one suspension covers every attempt and the reader sees each one as it happens.
func (m model) connectMaster(t target) tea.Cmd {
	label := t.label()
	command := m.helper("sh2pil-open", "zmx-connect", "--no-wait", t.Server)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return connectMsg{label: label, err: err}
	})
}

// reconnectMaster ends the host's SSH master and establishes a new one, with the UI suspended
// so the YubiKey touch and the ssh output are on screen.  It is the forced form of
// connectMaster: ctrl+r reconnects only when the master is gone, this rebuilds it either way.
func (m model) reconnectMaster(t target) tea.Cmd {
	label := t.label()
	command := m.helper("sh2pil-open", "zmx-connect", "--restart", "--no-wait", t.Server)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return connectMsg{label: label, err: err}
	})
}

// forgetMaster ends the host's SSH master and stops there.  The next read or action makes a
// new one when it needs it, so a reader who wanted the old master gone does not pay for a new
// touch immediately.
func (m model) forgetMaster(t target) tea.Cmd {
	label := t.label()
	command := m.helper("sh2pil-open", "zmx-connect", "--forget", "--no-wait", t.Server)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return forgetMsg{label: label, err: err}
	})
}

// refreshTarget runs one target's read, connecting first when it is remote and has no master
// yet.  The local machine never connects: nothing about it needs the network.
func (m model) refreshTarget(t target) tea.Cmd {
	if t.local() || m.data[t.label()].Connected {
		return m.loadTarget(t)
	}
	return m.checkMaster(t)
}

// harnessOf names the store a row comes from.  A row that does not say is a Pi session:
// Pi was the only store before OpenCode joined it, and sh2pil-sessions writes the field on every row it
// produces.
func harnessOf(s session) string {
	if s.Harness == "" {
		return "pi"
	}
	return s.Harness
}

// remoteFailure turns a failed helper run into the one line worth showing.  The explanation is
// usually the last line the helper wrote: ssh puts its identity noise above the line that says
// why the connection failed, and a Python helper ends with the error that stopped it.
func remoteFailure(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return lastLine(string(exitErr.Stderr))
	}
	return firstLine(err.Error())
}

// lastLine returns the last line of text that is not blank, so a failure that wrote several
// lines reports the one that explains it.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

// remoteZmxRows merges one host's zmx sessions with the chat list read from the same host.
// A row that carries a chat is the row a local zmx session carries: the chat names it, and
// its transcript is the one the host reported, so the preview pane reads it there.  A session
// with no label, or one whose chat the host no longer has, stands on the zmx name instead.
// The arrow zmx prints is dropped: it marks the session the caller runs inside, and this
// process is not inside anything on that host.
func remoteZmxRows(server string, entries []zmxSession, chats map[string]session) []session {
	rows := make([]session, 0, len(entries))
	for _, entry := range entries {
		row := zmxSessionRow(entry)
		row.Server, row.Current = server, false
		chat, ok := chats[row.ID]
		if !ok {
			row.ID, row.File, row.Name = "", "", row.ZmxName
		} else {
			// An unnamed chat keeps the zmx name it came in with, which is the only name it
			// has; a named one names the row, exactly as a local zmx row does.
			if chat.Name != "" {
				row.Name = chat.Name
			}
			row.File = chat.File
		}
		rows = append(rows, row)
	}
	return rows
}

// zmxRows turns the zmx sessions into rows the two panes already know how to draw: the chat
// names the row when the session carries one, and the zmx name stays as the handle.
func zmxRows(entries []zmxSession) []session {
	rows := make([]session, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, zmxSessionRow(entry))
	}
	return rows
}

func zmxSessionRow(entry zmxSession) session {
	name := entry.Chat
	if name == "" {
		name = entry.Name
	}
	project := entry.Project
	if project == "" && entry.CWD != "" {
		project = filepath.Base(entry.CWD)
	}
	return session{
		ID: entry.Pi, Harness: "pi", Name: name, Project: project, CWD: entry.CWD,
		File: entry.File, Alive: true, Mod: entry.Created, ZmxOnly: true,
		ZmxName: entry.Name, Clients: entry.Clients, Current: entry.Current,
		Command: entry.Command,
	}
}

// zmxChats maps each Pi session id to the zmx session that carries it, through the `pi=`
// label zmx holds.  A zmx session started as a plain command carries no label and maps
// nothing, and a chat whose transcript was deleted still labels its id.
func zmxChats(entries []zmxSession) map[string]string {
	chats := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Pi != "" && entry.Name != "" {
			chats[entry.Pi] = entry.Name
		}
	}
	return chats
}

func (m model) loadLive() tea.Cmd {
	return func() tea.Msg {
		out, err := m.helper("sh2pil-open", "live", "--json").Output()
		if err != nil {
			// The guard inside sh2pil-open still asks, so a failure here only costs the
			// marker and the early warning.
			return liveMsg{map[string]liveInfo{}}
		}
		var entries []liveInfo
		if err := json.Unmarshal(out, &entries); err != nil {
			return liveMsg{map[string]liveInfo{}}
		}
		live := make(map[string]liveInfo, len(entries))
		for _, entry := range entries {
			live[entry.ID] = entry
		}
		return liveMsg{live}
	}
}

// loadState reads what every registered chat is doing, and nothing else about it.  The state
// changes on a clock while ownership changes rarely, so this is what the picker follows between
// the full reads; a failure costs the state and nothing else, because the rows it would decorate
// are already on screen.
func (m model) loadState() tea.Cmd {
	return func() tea.Msg {
		out, err := m.helper("sh2pil-open", "state", "--json").Output()
		if err != nil {
			return stateMsg{map[string]liveInfo{}}
		}
		var entries []liveInfo
		if err := json.Unmarshal(out, &entries); err != nil {
			return stateMsg{map[string]liveInfo{}}
		}
		states := make(map[string]liveInfo, len(entries))
		for _, entry := range entries {
			states[entry.ID] = entry
		}
		return stateMsg{states}
	}
}

// stateTick schedules the next cheap read, or nothing at all when the clock is off.
func (m *model) stateTick() tea.Cmd {
	if m.statePoll <= 0 {
		return nil
	}
	interval := time.Duration(m.statePoll * float64(time.Second))
	return tea.Tick(interval, func(time.Time) tea.Msg { return liveTickMsg{} })
}

// loadRemoteState reads what every registered chat on one host is doing, and nothing else about
// it.  A host that cannot be reached answers nothing, and its rows keep the state they already
// have: the read costs one cadence and never a connection the reader did not ask for.
func (m model) loadRemoteState(host target) tea.Cmd {
	server := host.Server
	return func() tea.Msg {
		out, err := m.helper("sh2pil-open", "state-remote", server, "--json").Output()
		if err != nil {
			return remoteStateMsg{server: server}
		}
		var entries []liveInfo
		if err := json.Unmarshal(out, &entries); err != nil {
			return remoteStateMsg{server: server}
		}
		states := make(map[string]liveInfo, len(entries))
		for _, entry := range entries {
			states[entry.ID] = entry
		}
		return remoteStateMsg{server: server, states: states, read: true}
	}
}

// schedulePreview waits for the cursor to rest before reading a transcript.  Rendering one
// costs a subprocess and up to 0.12 s on the largest sessions, so a burst of movement must
// collapse into a single read.
func (m model) schedulePreview(after time.Duration) tea.Cmd {
	id := m.selectedID()
	if id == "" || !m.showPrev {
		return nil
	}
	if _, cached := m.cache[id]; cached {
		return func() tea.Msg { return previewTickMsg{id: id} }
	}
	return tea.Tick(after, func(time.Time) tea.Msg { return previewTickMsg{id: id} })
}

func (m model) fetchProjectFiles(path string) tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(path)
		if err != nil {
			return projectFilesMsg{path: path, err: err.Error()}
		}
		files := make([]string, 0, len(entries))
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			files = append(files, name)
		}
		return projectFilesMsg{path: path, files: files}
	}
}

// zmxHistoryLines is how much of a session's scrollback the preview asks for: several
// screenfuls, so the reader can scroll back, and cheap enough to re-read after every move.
const zmxHistoryLines = 200

// fetchZmxHistory reads the end of a session's own scrollback through sh2pil-open, which is the
// only place that knows where zmx lives: a kitty child's PATH does not hold it.
//
// This runs in a goroutine, so the subprocess stays off the UI path.
func (m model) fetchZmxHistory(name string) tea.Cmd {
	return func() tea.Msg {
		out, err := m.helper("sh2pil-open", "zmx-history", name,
			"--lines", strconv.Itoa(zmxHistoryLines)).Output()
		if err != nil {
			return zmxHistoryMsg{name: name, err: err.Error()}
		}
		text := strings.TrimRight(string(out), "\n")
		if text == "" {
			return zmxHistoryMsg{name: name}
		}
		return zmxHistoryMsg{name: name, lines: strings.Split(text, "\n")}
	}
}

// previewCommand is the read that fills the preview pane for one row.  A remote transcript is
// read on the host that holds it, through the same reader the f4 binding uses, and a local
// OpenCode or Claude Code one through the local sh2pil-sessions, which is the same helper with a server in
// front of it; only a local Pi transcript is read straight from sh2pil-last, which is the fastest
// path and the one the preview pane uses most.
func (m model) previewCommand(s session) *exec.Cmd {
	if s.Server != "" {
		args := []string{"session-remote-show", s.Server, s.ID,
			"--harness", harnessOf(s), "--tail", strconv.Itoa(previewLines)}
		if s.File != "" && harnessOf(s) == "pi" {
			// The path the host reported keeps a Pi read to one transcript instead of a scan
			// of its whole store, which is what the local preview does too.  The other
			// stores read through their own `sh2pil-sessions show`, so the path would not help there.
			args = append(args, "--file", s.File)
		}
		return m.helper("sh2pil-open", args...)
	}
	if harnessOf(s) != "pi" {
		return m.helper("sh2pil-sessions", "show", "--harness", harnessOf(s), s.ID,
			"--tail", strconv.Itoa(previewLines))
	}
	return exec.Command(m.interpreter(),
		filepath.Join(m.helperDir, "sh2pil-last"), "--session", s.File, "--full",
		"--tail", strconv.Itoa(previewLines))
}

// fetchPreview reads the tail of one transcript through the same renderer the f4 binding
// uses, so headings and truncation look identical, and renders the markdown here.
//
// This runs in a goroutine, so the glamour render and the subprocess both stay off the UI
// path; only a resize re-renders inline, because then the word wrap changes.
func (m model) fetchPreview(s session) tea.Cmd {
	// The preview wants a rendered tail and nothing else, so the read goes straight to the
	// store that holds the transcript: sh2pil-last for a local Pi chat, and `sh2pil-sessions show` for the
	// two stores that are not a local file.
	width := m.previewWidth() - 1
	command := m.previewCommand(s)
	id := s.ID
	return func() tea.Msg {
		out, err := command.Output()
		if err != nil {
			return previewMsg{preview{id: id, lines: []string{"preview failed: " + remoteFailure(err)}}}
		}
		text := strings.TrimRight(string(out), "\n")
		if text == "" {
			return previewMsg{preview{id: id, width: width}}
		}
		source := strings.Split(text, "\n")
		return previewMsg{preview{id: id, source: source,
			lines: renderMarkdown(text, width), width: width}}
	}
}

func humanAge(seconds float64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", int(seconds))
	case seconds < 3600:
		return fmt.Sprintf("%dm", int(seconds/60))
	case seconds < 86400:
		return fmt.Sprintf("%dh", int(seconds/3600))
	default:
		return fmt.Sprintf("%dd", int(seconds/86400))
	}
}

func humanSize(bytes int64) string {
	if bytes < 1024*1024 {
		return fmt.Sprintf("%dK", bytes/1024)
	}
	return fmt.Sprintf("%.1fM", float64(bytes)/(1024*1024))
}

func ageOf(s session) string {
	return humanAge(time.Since(time.Unix(0, int64(s.Mod*1e9))).Seconds())
}

// matchCell reports whether query is a case-insensitive subsequence of text, and which
// runes of text it landed on, so the match can be highlighted where it is shown.
func matchCell(query, text string) (bool, []int) {
	if query == "" {
		return true, nil
	}
	needle := []rune(strings.ToLower(query))
	haystack := []rune(strings.ToLower(text))
	idx := make([]int, 0, len(needle))
	position := 0
	for _, want := range needle {
		found := -1
		for i := position; i < len(haystack); i++ {
			if haystack[i] == want {
				found = i
				break
			}
		}
		if found < 0 {
			return false, nil
		}
		idx = append(idx, found)
		position = found + 1
	}
	return true, idx
}

// matchScore ranks one query against one field: an exact match beats a prefix, a prefix beats
// a match that starts a word, that beats a match inside a word, and any substring beats a
// subsequence.  It returns -1 when there is no match.
func matchScore(query, text string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(text)
	if q == "" {
		return 0
	}
	if t == q {
		return 0
	}
	if strings.HasPrefix(t, q) {
		return 1
	}
	if at := strings.Index(t, q); at >= 0 {
		if isWordStart(t, at) {
			return 2 + at
		}
		return 100 + at
	}
	if ok, _ := matchCell(q, t); ok {
		return 1000 + len(t)
	}
	return -1
}

// isWordStart reports whether the rune at one byte position begins a word in text.
func isWordStart(text string, at int) bool {
	if at <= 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:at])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous)
}

// groupMatchScore is how well one group answers a query: the best score of its header and its
// sessions' rows.  A group with no match at all scores above every match, so it sorts last.
func groupMatchScore(query string, g group) int {
	return groupMatchScoreFields(query, g, searchFields)
}

// groupMatchScoreFields is groupMatchScore over one field set.
func groupMatchScoreFields(query string, g group, fields []string) int {
	best := -1
	for _, s := range g.Sessions {
		if score := rowMatchScore(query, s, fields); score >= 0 && (best < 0 || score < best) {
			best = score
		}
	}
	header := session{Project: g.Project, CWD: g.CWD, Server: g.Server}
	if score := rowMatchScore(query, header, fields); score >= 0 && (best < 0 || score < best) {
		best = score
	}
	if best < 0 {
		return 1 << 30
	}
	return best
}

// rowMatchScore ranks one query against one row: the worst best-term of its terms, or -1 when
// any term does not match.  It is the ranking form of matchesRowFields, scoped terms included.
func rowMatchScore(query string, s session, fields []string) int {
	values := map[string]string{"name": s.Name, "project": s.Project, "cwd": s.CWD,
		"zmx": s.ZmxName, "cmd": s.Command, "server": s.Server}
	best := 0
	for _, term := range strings.Fields(query) {
		scope, value := scopedTerm(term)
		if value == "" {
			continue
		}
		termBest := -1
		searched := fields
		if scope != "" {
			searched = []string{scope}
		}
		for _, field := range searched {
			text, known := values[field]
			if !known {
				continue
			}
			if score := matchScore(value, text); score >= 0 && (termBest < 0 || score < termBest) {
				termBest = score
			}
		}
		if termBest < 0 {
			return -1
		}
		if termBest > best {
			best = termBest
		}
	}
	return best
}

// searchFields is every field a bare filter term searches, in the order the marks are read.
// A configured filter_fields narrows this set.
var searchFields = []string{"name", "project", "cwd", "zmx", "cmd", "server"}

// scopedTerm splits one query term into the field it restricts itself to, if any: `@` names
// the project, `#` the session name, `~` the path, and `host:` the host.  A bare term is not
// restricted, and an empty scope means every field.
func scopedTerm(term string) (field, value string) {
	switch {
	case strings.HasPrefix(term, "@"):
		return "project", strings.TrimPrefix(term, "@")
	case strings.HasPrefix(term, "#"):
		return "name", strings.TrimPrefix(term, "#")
	case strings.HasPrefix(term, "~"):
		return "cwd", strings.TrimPrefix(term, "~")
	case strings.HasPrefix(term, "host:"):
		return "server", strings.TrimPrefix(term, "host:")
	}
	return "", term
}

// matchesRow applies every whitespace-separated term to the fields a reader would search:
// the name, the project, the directory, the host the row runs on, and, for a zmx session, the
// handle zmx knows and the command it runs.  A term may land in any of them, unless a prefix
// restricts it to one.  matchesRowFields is the same with a configured field set.
func matchesRow(query string, s session) (bool, map[string][]int) {
	return matchesRowFields(query, s, searchFields)
}

// matchesRowFields is matchesRow over one field set: a bare term searches only those fields,
// and a scoped term still searches its own field, which the set does not take away.
func matchesRowFields(query string, s session, fields []string) (bool, map[string][]int) {
	values := map[string]string{"name": s.Name, "project": s.Project, "cwd": s.CWD,
		"zmx": s.ZmxName, "cmd": s.Command, "server": s.Server}
	marks := map[string][]int{}
	for _, term := range strings.Fields(query) {
		scope, value := scopedTerm(term)
		if value == "" {
			continue
		}
		searched := fields
		if scope != "" {
			searched = []string{scope}
		}
		hit := false
		for _, field := range searched {
			text, known := values[field]
			if !known {
				continue
			}
			if ok, idx := matchCell(value, text); ok {
				hit = true
				if len(idx) > 0 {
					marks[field] = idx
				}
			}
		}
		if !hit {
			return false, nil
		}
	}
	return true, marks
}
