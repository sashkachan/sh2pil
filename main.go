package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// python3 is the interpreter the sibling helpers run under. A build may bake a path in with
// -ldflags -X main.python3=<path>; resolvePython3 prefers that path when it exists and falls
// back to the usual locations, because a binary built on one machine often runs on another.
var python3 = "/usr/bin/python3"

// envFirst returns the first environment variable that is set.  The current name is tried
// first and the name an earlier release used is kept as a fallback, so a machine that still
// has the older dotfiles keeps working while it is migrated.
func envFirst(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// version is set at build time with -ldflags -X main.version=<tag>; "dev" for a plain build.
var version = "dev"

// resolvePython3 returns the first interpreter that exists: $SH2PIL_PYTHON, the baked path,
// the Homebrew and system locations, then PATH.
func resolvePython3() string {
	if env := os.Getenv("SH2PIL_PYTHON"); env != "" {
		return env
	}
	for _, candidate := range []string{python3, "/opt/homebrew/bin/python3", "/usr/local/bin/python3", "/usr/bin/python3"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath("python3"); err == nil {
		return found
	}
	return python3
}

const (
	// previewDelayDebounce is how long the cursor must rest before a transcript is read.
	// Short enough to feel immediate, long enough that holding j does not queue renders.
	previewDelayDebounce = 180 * time.Millisecond
	previewCacheLimit    = 40
)

type model struct {
	helperDir       string
	python          string                       // the interpreter the sibling helpers run under
	editor          string                       // the editor the transcript and project actions open
	fileBrowser     string                       // the tool the alt+f action runs
	gitTool         string                       // the tool the ctrl+g action runs
	toolHosts       map[string]map[string]string // per-host tools from tools.hosts
	mode            string                       // keybind, prompt, or both: how the primary keys act
	triggers        map[string]string            // action -> key or prompt, from the trigger settings
	toolChoices     []string                     // the menu's actions: travel, or the command palette
	toolSections    []string                     // the section heading of each menu row, or "" for none
	toolPos         int                          // the highlighted menu row
	agentChoices    []string                     // the store menu a new chat from a window asks
	agentPos        int                          // the highlighted store row
	harness         string                       // the store a new chat uses
	harnesses       []string                     // the stores every target is read for
	closeOnNavigate bool
	// windowMenu marks the cmd+. dialog: the picker was started for one window that
	// sh2pil-open already resolved, so its menu opens at once, every action asks where it
	// goes, and the dialog closes when the action is done.  windowRow is that window.
	windowMenu bool
	alwaysAsk  bool
	windowRow  session
	// keymap resolves every chord for the state that owns the keyboard, so a binding can
	// move without touching the dispatch.  A nil keymap leaves every key as it came in,
	// which is the default behavior a hand-built test model relies on.
	keymap *keymap
	// targets are the destinations the picker can read, this machine first; current is the
	// one on screen, and data caches each one's read so a switch back is instant.
	targets  []target
	current  int
	data     map[string]targetData
	expanded map[string]bool // which groups are open, keyed by target and path
	// generation counts target switches, so a delayed read whose target has already been left
	// behind is dropped instead of reading a list nobody is looking at.
	generation int
	// view is the list that owns the cursor: the project groups, or the zmx pane.  Both panes
	// are on screen at once, so this is focus and not a mode.
	view string
	// sessions and zmxRows are the rows of the two panes, for the current target.  rebuildRows
	// is the one place that builds them.
	sessions []session
	zmxRows  []session
	// groupCount, sessionCount, inZmxCount, liveCount and waitingCount describe what the target
	// holds once the ignored mode and the search have been applied.  rebuildRows fills them, so the
	// header never walks the rows again and never reports merely collapsed groups as an empty
	// machine.
	groupCount    int
	sessionCount  int
	inZmxCount    int
	liveCount     int
	waitingCount  int     // the chats whose state is blocked: a person must answer them
	statePoll     float64 // seconds between state reads; 0 follows it only on an action
	liveTicks     int     // cheap reads since the last full live read
	attentionSort bool    // lift the rows that wait on a person above the rest of their group
	// The attention queue: which chats wait on a person, which of them have waited since the
	// reader last looked at them, and what the picker remembers each chat doing.  See attention.go.
	notify       string            // off, bell, or terminal: how a chat that starts waiting is announced
	out          io.Writer         // the terminal a notification sequence goes to; main sets os.Stdout
	focused      bool              // whether the terminal says the picker is in front of the reader
	notifierRole bool              // whether this picker is the one of its processes that speaks
	claimedAt    float64           // when this picker took the speaking role: the newest picker speaks
	unread       map[string]bool   // chats that started waiting and have not been visited yet
	seen         map[string]string // this machine's chats: chat -> the state the picker last saw it in
	// A host's chats are remembered apart from this machine's, so one source's pruning can never
	// drop another's memory or unread mark.  See attention.go.
	remoteStates     map[string]liveInfo // server+chat -> what that chat is doing
	remoteSeen       map[string]string   // server+chat -> the state the picker last saw it in
	remoteKnown      map[string]bool     // server+chat -> every chat that host has named
	visited          string              // the row the cursor was last on: the mark an unread row clears on
	showIgnored      bool                // the project pane shows the ignored groups instead of the active ones
	onlyShown        bool                // the lists keep only the rows a window already shows
	live             map[string]liveInfo
	showPrev         bool
	status           string
	pending          string // an action waiting for a yes on a live session
	query            string // the search of the project pane
	searching        bool   // the search field of the active pane is open
	queryCursor      int
	cursor           int
	offset           int
	zmxQuery         string // the search of the zmx pane, which the project search never touches
	zmxQueryCursor   int
	zmxCursor        int
	zmxOffset        int
	width            int
	height           int
	preview          preview
	cache            map[string]preview
	projectFiles     []string
	projectFilesPath string
	projectFilesErr  string
	zmxHistory       []string // the end of the session's own scrollback, for a row with no chat
	zmxHistoryName   string   // the session those lines came from
	zmxHistoryErr    string
	focus            int // paneFocusList or paneFocusPreview
	help             bool
	layout           string // where a new terminal goes: tab, window, or pane
	modal            string // a modal owns the keyboard while it is open: see modalBox
	name             field  // the text field of an open modal
	pruneAge         string // the age the delete-old-sessions question is about
	pruneSessions    int    // the transcripts the prune count came from: -1 before it is read
	pruneZmx         int    // the zmx sessions the prune count came from
	layoutPos        int
	askAction        string
	askSession       session
	askForce         bool
	askLayout        string
	editorLabel      string
	kill             string // the last killed text, which ctrl+y puts back
	forkName         string // the name the pending fork will carry
	pendingID        string // session the pending question is about
	pendingServer    string // SSH destination for a pending remote zmx action
	wrapped          []string
	wrapWidth        int
	wrapID           string
	err              error
}

const (
	paneFocusList = iota
	paneFocusPreview
)

func main() {
	prepareTerminal()

	editor := flag.String("nvim", "", "editor for the transcript action (default: $EDITOR, then nvim)")
	menu := flag.Bool("menu", false,
		"open the next-step menu for one window at once, which is what sh2pil-open window-menu asks")
	menuCWD := flag.String("menu-cwd", "", "that window's project directory, on the window's own host")
	menuServer := flag.String("menu-server", "", "that window's SSH destination; empty means this machine")
	menuStores := flag.String("menu-stores", "",
		"the stores that window's host reports, comma-separated; empty leaves the setting in force")
	harness := flag.String("harness", "", "store for a new chat: pi, opencode, claude, or codex (default: remembered choice)")
	style := flag.String("md-style", envFirst("SH2PIL_MD_STYLE", "PIB_MD_STYLE"),
		"glamour style for the preview: dark, light, notty, or a style file (default: match system appearance)")
	config := flag.String("config", envFirst("SH2PIL_CONFIG", "PIB_CONFIG"),
		"settings file (default: ~/.config/sh2pil/config.yaml, plus config.d/*.yaml overlays)")
	checkConfig := flag.Bool("check-config", false,
		"print the effective configuration and the resolved keybindings, then exit")
	printConfig := flag.Bool("print-config", false, "print the effective configuration, then exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("sh2pil %s\n", version)
		return
	}
	if *config != "" {
		configPathOverride = *config
	}
	settings := loadConfig()
	if *printConfig {
		printConfiguration(os.Stdout, settings)
		return
	}
	if *checkConfig {
		os.Exit(checkConfiguration(os.Stdout, settings))
	}
	if *style != "" {
		markdownStyle = *style
	} else {
		markdownStyle = systemMarkdownStyle()
	}
	if *harness != "" && !isKnownHarness(*harness) {
		fmt.Fprintln(os.Stderr, "sh2pil: --harness must be pi, opencode, claude, or codex")
		os.Exit(2)
	}
	chosen := *editor
	if chosen == "" {
		chosen = settings.Editor
	}
	if chosen == "" {
		chosen = os.Getenv("EDITOR")
	}
	if chosen == "" {
		chosen = "nvim"
	}

	// The helpers are siblings of this binary, because a kitty key-binding child has a
	// minimal PATH with neither /opt/homebrew/bin nor ~/.local/bin in it.
	dir := "."
	if self, err := os.Executable(); err == nil {
		dir = filepath.Dir(self)
	}

	chosenHarness := readHarness()
	if *harness != "" {
		chosenHarness = knownHarness(*harness)
	}
	overrides := map[string]string{}
	for action, value := range settings.Keys {
		overrides[action] = value
	}
	for action, value := range settings.Triggers {
		// A prompt trigger moves one action into the menu and releases its key.  The primary
		// action is the exception: its prompt trigger opens the menu, so its key must stay
		// routed to the dispatch.
		if value != "prompt" || action == "list.resume" {
			continue
		}
		if _, set := overrides[action]; !set {
			overrides[action] = "none"
		}
	}
	harnesses := settings.Harnesses
	if *menu {
		if *menuCWD == "" || !filepath.IsAbs(*menuCWD) {
			fmt.Fprintln(os.Stderr, "sh2pil: --menu needs --menu-cwd, an absolute project directory")
			os.Exit(2)
		}
		harnesses = menuHarnesses(settings.Harnesses, *menuStores)
	}
	app := &model{helperDir: dir, python: resolvePython3(), editor: chosen, fileBrowser: settings.FileBrowser,
		gitTool: settings.GitTool, toolHosts: settings.ToolHosts, mode: settings.Mode, triggers: settings.Triggers,
		harness:   chosenHarness,
		harnesses: harnesses, closeOnNavigate: settings.CloseOnNavigate, view: viewSessions,
		windowMenu: *menu, alwaysAsk: *menu, windowRow: windowMenuRow(*menuCWD, *menuServer),
		statePoll:     settings.StatePoll,
		attentionSort: settings.AttentionSort, notify: settings.Notify,
		out:      os.Stdout,
		showPrev: true, layout: readLayout(), keymap: buildKeymap(overrides),
		live: map[string]liveInfo{}, cache: map[string]preview{},
		unread: map[string]bool{}, seen: map[string]string{},
		remoteStates: map[string]liveInfo{}, remoteSeen: map[string]string{},
		remoteKnown: map[string]bool{}}
	if *menu {
		// An action from the cmd+. dialog opens its own terminal or window, so the dialog is
		// finished by the time the action is under way: leaving it on screen would cover the
		// window it was opened from, which is not what a menu key should do.
		app.closeOnNavigate = true
	}
	program := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sh2pil:", err)
		os.Exit(1)
	}
}

// systemColorScheme follows the macOS appearance, or COLORFGBG on other systems.
func systemColorScheme() string {
	if runtime.GOOS == "darwin" {
		style, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
		if err == nil && strings.EqualFold(strings.TrimSpace(string(style)), "Dark") {
			return "dark"
		}
		return "light"
	}
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if len(parts) > 1 {
		if background, err := strconv.Atoi(parts[len(parts)-1]); err == nil && background > 6 {
			return "light"
		}
	}
	return "dark"
}

func systemMarkdownStyle() string { return systemColorScheme() }

// prepareTerminal keeps a terminal colour query out of the keyboard stream.
//
// Bubble Tea's package init asks the terminal for its background colour with an OSC 11
// query. That question and its answer share the input stream. Set COLORFGBG before Bubble
// Tea starts to prevent the reply from appearing as typed input. On macOS use system appearance.
func prepareTerminal() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	colorFGBG := os.Getenv("COLORFGBG")
	if runtime.GOOS == "darwin" {
		if systemColorScheme() == "light" {
			colorFGBG = "0;15"
		} else {
			colorFGBG = "15;0"
		}
	} else if colorFGBG == "" {
		colorFGBG = "15;0"
	}
	if os.Getenv("COLORFGBG") != colorFGBG {
		path, err := os.Executable()
		if err == nil {
			if err := os.Setenv("COLORFGBG", colorFGBG); err != nil {
				fmt.Fprintln(os.Stderr, "sh2pil: set terminal appearance:", err)
				return
			}
			if err := syscall.Exec(path, os.Args, os.Environ()); err != nil {
				fmt.Fprintln(os.Stderr, "sh2pil: re-exec failed:", err)
			}
		}
	}
	// Drop input the terminal driver already holds, including a reply to a colour query that
	// came back before this process started. The request is spelled per platform in flush_*.go.
	flushInput(fd)
}

func (m *model) Init() tea.Cmd {
	// Two decisions a notification depends on.  Which picker speaks: the newest one, because that is
	// the one the reader opened last and the one the next key reaches (see notifierRoleFile).  Whether
	// it has the reader's attention: it is assumed not to, until the terminal says otherwise, so a
	// terminal that never answers focus reports lets a chat that needs a person be heard rather than
	// making the notification silently impossible.
	m.claimedAt = float64(time.Now().UnixNano()) / 1e9
	m.refreshNotifierRole()
	m.focused = false
	m.targets = readTargets()
	m.data = map[string]targetData{}
	m.expanded = map[string]bool{}
	m.current = targetIndex(m.targets, readDefaultTarget())
	m.view = viewSessions
	m.rebuildRows()
	if m.windowMenu {
		// The window was resolved before this program started, so there is no list to read, no
		// state to poll, and no notification to raise for a chat nobody is watching here.
		m.openToolMenuFor(m.windowRow)
		return nil
	}
	// One read of this machine, and no SSH connection at all: a remote target connects when it
	// is first visited, which is when the reader is there to answer a YubiKey touch.  The poll
	// timer starts after that first read, so it can never fire before the picker has rows.
	// Focus reporting is asked for first, so a notification knows whether the picker is on
	// screen before any read can raise one.
	return tea.Sequence(focusReport(), m.refreshTarget(m.currentTarget()), m.loadLive(),
		m.stateTick(), remotePollTick())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampPreviewScroll()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.FocusMsg:
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		// The reader is somewhere else, so the picker keeps quiet: the unread mark and the
		// count carry the news until the picker is back in front of them.
		m.focused = false
		return m, nil
	case remotePollTickMsg:
		// The poll refreshes the target on screen, and only when it is connected: a host is
		// read again while it is being watched, never behind the reader's back.
		if m.currentTarget().local() || !m.data[m.targetLabel()].Connected {
			return m, remotePollTick()
		}
		return m, tea.Batch(m.loadTarget(m.currentTarget()), remotePollTick())
	case targetRefreshTickMsg:
		// A tick belongs to the target and the generation it was made for.  Anything else has
		// been left behind by a later switch, and its read would be wasted.
		if msg.label != m.targetLabel() || msg.generation != m.generation {
			return m, nil
		}
		return m, m.refreshTarget(m.currentTarget())
	case sshCheckMsg:
		if msg.label != m.targetLabel() {
			return m, nil
		}
		if msg.present {
			m.markConnected(msg.label)
			return m, m.loadTarget(m.currentTarget())
		}
		return m, m.connectMaster(m.currentTarget())
	case connectMsg:
		if msg.err != nil {
			m.setTargetError(msg.label, "SSH connection to "+msg.label+" failed: "+
				firstLine(msg.err.Error()))
			return m, nil
		}
		m.markConnected(msg.label)
		if msg.label != m.targetLabel() {
			return m, nil
		}
		return m, m.loadTarget(m.currentTarget())
	case targetMsg:
		m.mergeTarget(msg)
		return m, m.refreshPreview()
	case liveMsg:
		// The counts that say a chat is waiting are built with the rows, so a live read that
		// arrives after them has to rebuild them: the state of a row is read at render time, but
		// the header's count is not, and a header that lags its own rows is a lie.
		// This read is also the authority on which chats are live at all, so it is where the
		// memory of a chat that has gone away is dropped.
		started := observeStates(msg.live, m.chatMemory(), true, func(id string) bool {
			_, live := m.live[id]
			return live
		})
		m.forgetGone(msg.live)
		m.live = msg.live
		m.markWaiting(started)
		m.rebuildRows()
		return m, m.announce(started)
	case stateMsg:
		// The chats that started waiting are read before the merge, because one of them is a
		// chat this read has to already believe is live.
		started := observeStates(msg.states, m.chatMemory(), false, func(id string) bool {
			_, live := m.live[id]
			return live
		})
		// Only a chat this machine already reported as live gains a state.  The cheap read does
		// not check the process, so it must never be the thing that makes a row live, and a
		// record left behind by a process that died must not bring its own row back.
		for id, state := range msg.states {
			known, live := m.live[id]
			if !live {
				continue
			}
			known.State, known.LastState = state.State, state.LastState
			known.Detail, known.Label = state.Detail, state.Label
			known.Age, known.Stale = state.Age, state.Stale
			m.live[id] = known
		}
		m.markWaiting(started)
		m.rebuildRows()
		return m, m.announce(started)
	case remoteStateMsg:
		// One host's answer.  The rows it decorates are the ones whose host reported the chat as
		// running, so a record left behind on that host by a process that died decorates nothing
		// and announces nothing.
		started := m.applyRemoteStates(msg)
		m.markWaiting(started)
		m.rebuildRows()
		return m, m.announce(started)
	case liveTickMsg:
		m.liveTicks++
		// One small read per tick keeps the speaking role with the newest picker.
		m.refreshNotifierRole()
		// Every target on the clock is read, whatever target is on screen: this machine's chats
		// need a person whether or not a host is being looked at, and this read is cheap.
		reads := make([]tea.Cmd, 0, len(m.targets)+2)
		if m.liveTicks%liveTickFullEvery == 0 {
			reads = append(reads, m.loadLive())
		} else {
			reads = append(reads, m.loadState())
		}
		// A host is read on its own, slower cadence, and only while its connection is already up.
		// A clock must never be the thing that asks for a YubiKey touch, so a host whose master is
		// gone is skipped until the reader visits it again.
		if m.liveTicks%remoteStateEvery == 0 {
			for _, host := range m.targets {
				if host.local() || !m.data[host.label()].Connected {
					continue
				}
				// A read decorates the chats the host reports as running, so a host that reports
				// none has nothing to say.  Skipping it keeps the clock off a host that would answer
				// with an empty list, and off one whose helper has to be shipped on every read.
				if len(m.remoteRows(host.Server)) == 0 {
					continue
				}
				reads = append(reads, m.loadRemoteState(host))
			}
		}
		return m, tea.Batch(append(reads, m.stateTick())...)
	case projectFilesTickMsg:
		selected := m.selected()
		if !m.showPrev || !selected.ProjectOnly || selected.Server != "" || selected.CWD != msg.path {
			return m, nil
		}
		if m.projectFilesPath == msg.path && m.projectFilesErr == "" {
			return m, nil
		}
		return m, m.fetchProjectFiles(msg.path)
	case projectFilesMsg:
		selected := m.selected()
		if !m.showPrev || !selected.ProjectOnly || selected.Server != "" || selected.CWD != msg.path {
			return m, nil
		}
		m.projectFilesPath, m.projectFiles, m.projectFilesErr = msg.path, msg.files, msg.err
		m.preview.scroll = 0
		return m, nil
	case zmxHistoryTickMsg:
		selected := m.selected()
		if !m.showPrev || !selected.ZmxOnly || selected.Server != "" || selected.ZmxName != msg.name {
			return m, nil
		}
		return m, m.fetchZmxHistory(msg.name)
	case zmxHistoryMsg:
		selected := m.selected()
		if !m.showPrev || !selected.ZmxOnly || selected.Server != "" || selected.ZmxName != msg.name {
			return m, nil
		}
		m.zmxHistoryName, m.zmxHistory, m.zmxHistoryErr = msg.name, msg.lines, msg.err
		m.preview.scroll = 0
		return m, nil
	case previewTickMsg:
		// The cursor moved on, or the preview was hidden again: this read is pointless.
		// The id is enough to tell, so no counter is needed (a counter bumped on a value
		// copy is lost, which silently discards every tick).
		if !m.showPrev || msg.id != m.selectedID() {
			return m, nil
		}
		if cached, ok := m.cache[msg.id]; ok {
			m.usePreview(cached)
			return m, nil
		}
		return m, m.fetchPreview(m.selected())
	case previewMsg:
		m.cache[msg.preview.id] = msg.preview
		if len(m.cache) > previewCacheLimit {
			m.cache = map[string]preview{msg.preview.id: msg.preview}
		}
		if msg.preview.id != m.selectedID() {
			return m, nil // the cursor moved on while this was being read
		}
		m.usePreview(msg.preview)
		return m, nil
	case pruneCheckMsg:
		// The count is the question: nothing is deleted until the reader answers it.
		if msg.err != nil {
			m.status = "could not check the age: " + helperFailure(msg.err, nil)
			return m, nil
		}
		m.pruneAge, m.pruneSessions, m.pruneZmx = msg.age, msg.sessions, msg.zmx
		if msg.sessions+msg.zmx == 0 {
			m.status = "nothing on " + m.targetLabel() + " is older than " +
				ageSummary(mustAge(msg.age))
			return m, nil
		}
		m.modal = "prune-confirm"
		return m, nil
	case noteMsg:
		m.status = msg.note
		return m, nil
	case actionMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.status = msg.note
		return m, tea.Batch(m.refreshTarget(m.currentTarget()), m.loadLive())
	}
	return m, nil
}

// defaultForkName is the name a fork is offered with, so it can be told apart from the
// session it came from.  The modal shows it and lets it be edited before pi starts the fork.
func defaultForkName(s session) string {
	base := strings.TrimSpace(s.Name)
	if base == "" {
		base = "session " + s.ID[:8]
	}
	return base + " (fork)"
}

// namedKeys are the menu chords that name a key rather than a character.
var namedKeys = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "space": tea.KeySpace,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"backspace": tea.KeyBackspace, "delete": tea.KeyDelete,
}

// keyMsgForChord rebuilds the key press a chord names, so a command palette row can be run
// the way its own key would run it: the dispatch, and every rule it carries about a running
// session or a row that cannot be renamed, is reached in the one place that already has it.
// A chord that cannot be rebuilt reports false, and the caller says so instead of guessing.
func keyMsgForChord(chord string) (tea.KeyMsg, bool) {
	if rest, ok := strings.CutPrefix(chord, "alt+"); ok {
		if len([]rune(rest)) != 1 {
			return tea.KeyMsg{}, false
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(rest), Alt: true}, true
	}
	if rest, ok := strings.CutPrefix(chord, "ctrl+"); ok {
		runes := []rune(rest)
		if len(runes) != 1 || runes[0] < 'a' || runes[0] > 'z' {
			return tea.KeyMsg{}, false
		}
		// Bubble Tea numbers the control keys from ctrl+a, which is one.
		return tea.KeyMsg{Type: tea.KeyType(int(runes[0]-'a') + 1)}, true
	}
	if keyType, ok := namedKeys[chord]; ok {
		return tea.KeyMsg{Type: keyType}, true
	}
	runes := []rune(chord)
	if len(runes) != 1 {
		return tea.KeyMsg{}, false
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}, true
}

// resolveChord maps a pressed key to the canonical chord of its action in one context.  A
// nil keymap keeps the key as it came in, which is what a test that builds a model by hand
// expects.
func (m *model) resolveChord(context, key string) string {
	if m.keymap == nil {
		return key
	}
	return m.keymap.resolve(context, key)
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// The cmd+. dialog draws no list of its own, so once its menu is closed what is left on
	// screen is the outcome of the action: any key closes the dialog.  A modal -- the store
	// question, or the placement question -- still owns the keyboard above this.
	if m.windowMenu && m.modal == "" {
		return m, tea.Quit
	}

	// Resolve the chord for the state that owns the keyboard, so a rebound key reaches the
	// same case as its default and a released one matches nothing.
	switch {
	case m.help:
		key = m.resolveChord(keyContextHelp, key)
	case m.modal != "":
		key = m.resolveChord(keyContextModal, key)
	case m.searching:
		return m.handleSearchKey(msg, m.resolveChord(keyContextSearch, key))
	case m.pending != "":
		key = m.resolveChord(keyContextConfirm, key)
	default:
		key = m.resolveChord(keyContextList, key)
	}
	if key == blockedChord {
		return m, nil
	}

	// The help box owns the keyboard while it is open.  It closes on its own key, escape, q
	// or enter, and swallows everything else, so a stray key cannot act on a list the reader
	// cannot see.
	if m.help {
		switch key {
		case "?", "esc", "q", "enter", "ctrl+c":
			m.help = false
		}
		return m, nil
	}
	// A modal owns the keyboard while it is open, so '?' and every other letter go into the
	// name instead of the filter or the help box.  The field itself edits with Emacs keys
	// (see field.go); the ones below are the list's, so they must not be reached from here.
	if m.modal != "" {
		// The prune question has no field: it is answered, not edited.  A yes deletes and
		// anything else leaves the stores alone, so it is settled before the field keys below.
		if m.modal == "prune-confirm" {
			switch key {
			case "y", "Y", "enter":
				age := m.pruneAge
				m.modal, m.pruneAge, m.pruneSessions, m.pruneZmx = "", "", 0, 0
				return m, m.runPrune(age)
			case "esc", "n", "ctrl+c", "ctrl+g":
				m.modal, m.pruneAge, m.pruneSessions, m.pruneZmx = "", "", 0, 0
				m.status = "prune cancelled"
			}
			return m, nil
		}
		switch key {
		case "esc", "ctrl+c", "ctrl+g":
			kind := m.modal
			m.modal, m.name, m.toolChoices, m.toolSections = "", field{}, nil, nil
			m.agentChoices = nil
			m.askAction, m.askLayout = "", ""
			switch {
			case kind == "agents" || (m.windowMenu && kind != "tools"):
				// A question asked from a menu goes back to the menu that asked it: the store
				// question, and in the cmd+. dialog every question on the way to an action.
				return m.openToolMenuFor(m.askSession)
			case m.windowMenu:
				// The cmd+. dialog exists for this one question: escape closes it.
				return m, tea.Quit
			case kind == "layout" || kind == "editor-title":
				m.status = "open cancelled"
			case kind == "prune":
				m.status = "prune cancelled"
			default:
				m.status = kind + " cancelled"
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if m.modal == "layout" && key <= "4" {
				return m.chooseLayout(int(key[0] - '1'))
			}
			if m.inMenu() {
				return m.chooseMenu(int(key[0] - '1'))
			}
			if m.modal == "agents" {
				return m.chooseAgent(int(key[0] - '1'))
			}
			m.name.insert(key)
		case "up", "ctrl+p":
			if m.modal == "layout" {
				m.layoutPos = (m.layoutPos + len(placements) - 1) % len(placements)
				return m, nil
			}
			if m.inMenu() && len(m.toolChoices) > 0 {
				m.toolPos = (m.toolPos + len(m.toolChoices) - 1) % len(m.toolChoices)
				return m, nil
			}
			if m.modal == "agents" && len(m.agentChoices) > 0 {
				m.agentPos = (m.agentPos + len(m.agentChoices) - 1) % len(m.agentChoices)
				return m, nil
			}
		case "down", "ctrl+n":
			if m.modal == "layout" {
				m.layoutPos = (m.layoutPos + 1) % len(placements)
				return m, nil
			}
			if m.inMenu() && len(m.toolChoices) > 0 {
				m.toolPos = (m.toolPos + 1) % len(m.toolChoices)
				return m, nil
			}
			if m.modal == "agents" && len(m.agentChoices) > 0 {
				m.agentPos = (m.agentPos + 1) % len(m.agentChoices)
				return m, nil
			}
		case "enter":
			if m.modal == "layout" {
				return m.chooseLayout(m.layoutPos)
			}
			if m.inMenu() {
				return m.chooseMenu(m.toolPos)
			}
			if m.modal == "agents" {
				return m.chooseAgent(m.agentPos)
			}
			name := strings.TrimSpace(m.name.text)
			kind := m.modal
			if kind == "prune" {
				if _, err := parseAge(name); err != nil {
					m.status = err.Error()
					return m, nil
				}
				m.modal, m.name = "", field{}
				m.status = "checking what is older than " + name + "…"
				return m, m.checkPrune(name)
			}
			if kind == "editor-title" {
				if name == "" {
					m.status = "editor title cannot be empty"
					return m, nil
				}
				m.modal, m.name = "", field{}
				return m, m.runEditorAction(name)
			}
			session := m.selected()
			m.modal, m.name = "", field{}
			if kind == "fork" {
				// A fork writes a new transcript, so a live session is no obstacle; its name
				// is what makes the two distinguishable.
				m.forkName = name
				return m.beginAction("fork", session, false)
			}
			if name == "" {
				m.status = "rename cancelled"
				return m, nil
			}
			// No question for a rename: it appends a record through pi, and sh2pil-sessions also
			// retitles the tab.  Deleting is the destructive one and keeps its question.
			return m, m.runPib("renamed to "+trim(name, 40), "rename", session.ID, name)
		// Emacs editing, inside the field only: the list keeps its own keys.
		case "backspace", "ctrl+h":
			m.name.backspace()
		case "delete", "ctrl+d":
			m.name.deleteForward()
		case "ctrl+a", "home":
			m.name.home()
		case "ctrl+e", "end":
			m.name.end()
		case "ctrl+b", "left":
			m.name.move(-1)
		case "ctrl+f", "right":
			m.name.move(1)
		case "alt+b", "alt+left", "ctrl+left":
			m.name.wordLeft()
		case "alt+f", "alt+right", "ctrl+right":
			m.name.wordRight()
		// alt+backspace is ESC DEL in a kitty with macos_option_as_alt; a terminal that
		// sends BS instead spells the same key "alt+ctrl+h".
		case "alt+backspace", "alt+ctrl+h", "ctrl+w":
			m.kill = m.name.killWordBack()
		case "alt+d":
			m.kill = m.name.killWordForward()
		case "ctrl+k":
			m.kill = m.name.killToEnd()
		case "ctrl+u":
			m.kill = m.name.killToStart()
		case "ctrl+y":
			m.name.insert(m.kill)
		default:
			if len(msg.Runes) > 0 && !msg.Alt {
				m.name.insert(string(msg.Runes))
			}
		}
		return m, nil
	}

	// A live-session question owns the keyboard until it is answered.
	if m.pending != "" {
		action, session := m.pending, m.selected()
		wanted, server := m.pendingID, m.pendingServer
		m.pending, m.pendingID, m.pendingServer = "", "", ""
		confirmed := key == "y" || key == "Y"
		switch {
		case action == "zmx-kill":
			if !confirmed {
				m.status = "kill cancelled"
				return m, nil
			}
			args := []string{"zmx-kill", wanted}
			if server != "" {
				args = append(args, "--server", server)
			}
			return m, m.runHelper("sh2pil-open", "killed "+wanted, args...)
		case action == "delete-force" || action == "delete":
			if !confirmed {
				m.status = "delete cancelled"
				return m, nil
			}
			helper, args, note := m.deleteCommand(session)
			return m, m.runHelper(helper, note, args...)
		}
		if confirmed {
			return m.beginAction(action, session, true)
		}
		m.status = "cancelled"
		return m, nil
	}

	switch key {
	case "/":
		m.searching = true
		*m.queryCursorPtr() = len([]rune(*m.queryPtr()))
		m.status = ""
		return m, nil
	case "?":
		m.help = true
		return m, nil
	case "q", "ctrl+c":
		return m, tea.Quit
	case "enter":
		if m.promptPrimary() {
			return m.openToolMenu()
		}
		return m.startAction(key)
	case ".":
		return m.openToolMenu()
	case "alt+p":
		return m.openPalette()
	case "alt+d":
		return m.startPrune()
	case "ctrl+a", "ctrl+t", "ctrl+x", "ctrl+g":
		return m.startAction(key)
	case "ctrl+y", "alt+y":
		return m.copyAction(key)
	case "alt+e", "alt+f":
		return m.startAction(key)
	case "ctrl+e":
		session := m.selected()
		if session.ProjectOnly {
			m.status = "select a session to rename it"
			return m, nil
		}
		if session.Server != "" {
			// Renaming runs the local sh2pil-sessions, which appends to the local store: a session that
			// lives on another host must be renamed there, not here.
			m.status = "renaming a session on " + session.Server + " is not supported here"
			return m, nil
		}
		if harnessOf(session) != "pi" {
			m.status = "renaming " + harnessTitle(session.Harness) + " sessions is not supported"
			return m, nil
		}
		if session.ID == "" {
			// A zmx session whose `pi=` label never arrived carries no chat, so there is no
			// transcript to name: renaming would run sh2pil-sessions against an empty session id.
			m.status = "this zmx session carries no chat; rename acts on a session row"
			return m, nil
		}
		// The modal shows what has been typed, so the status line stops repeating it.
		m.modal, m.name, m.status = "rename", field{}, ""
		return m, nil
	case "ctrl+f":
		session := m.selected()
		if session.ProjectOnly {
			m.status = "select a Pi session to fork it"
			return m, nil
		}
		if session.Harness == "opencode" {
			m.status = "forking OpenCode sessions is not supported by its CLI"
			return m, nil
		}
		if session.ID == "" {
			// Without a `pi=` label there is no chat to fork, and an empty session id would
			// reach pi as a fork of nothing.
			m.status = "this zmx session carries no chat; fork acts on a session row"
			return m, nil
		}
		if session.Harness == "codex" {
			// Codex names a fork itself and takes no name from here, so the fork starts at once
			// instead of asking for a name nothing would carry.
			return m.beginAction("fork", session, false)
		}
		// Forking asks for a name: a fork without one cannot be told from the session it
		// came from.
		m.modal, m.name, m.status = "fork", newField(defaultForkName(session)), ""
		return m, nil
	case "ctrl+w":
		session := m.selected()
		if session.ZmxOnly {
			// A zmx row is a running session, so this key ends it, on its own host when it has
			// one.  The transcript is untouched: the chat's own row in the project pane is where
			// the delete belongs.
			return m.startZmxAction(key, session)
		}
		if session.ProjectOnly {
			if session.Server != "" {
				m.status = "ignored projects are a local list; " + session.Project + " is on " +
					session.Server
				return m, nil
			}
			return m.toggleProjectIgnored()
		}
		if session.Harness == "opencode" {
			m.status = "deleting OpenCode sessions is not supported here"
			return m, nil
		}
		if session.ID == "" {
			m.status = "this row carries no session to delete"
			return m, nil
		}
		// The delete asks first and then removes the transcript, here or on the host that holds
		// it.  There is no trash: a chat that is gone is gone, so the question names the session
		// and says where it is.
		m.pending, m.pendingID, m.pendingServer = "delete", session.ID, session.Server
		label := trim(session.Name, 30)
		if label == "" {
			label = trim(session.ID, 8)
		}
		where := ""
		if session.Server != "" {
			where = " on " + session.Server
		}
		running := session.Live
		if session.Server == "" {
			_, running = m.live[session.ID]
		}
		m.status = "delete " + label + where + "? y/N"
		if running {
			m.status = "● that session is running" + where + " — delete it anyway? y/N"
		}
		return m, nil
	case "alt+i":
		if m.view != viewSessions {
			m.status = "ignored projects are shown in the project pane"
			return m, nil
		}
		if !m.currentTarget().local() {
			// The ignored set is one list in this picker's own state, and a host cannot write
			// it: showing it for a host would hide that host's projects behind a local rule.
			m.status = "ignored projects are a local list; " + m.targetLabel() + " is remote"
			return m, nil
		}
		m.showIgnored = !m.showIgnored
		m.resetCursor()
		m.status = "showing ignored projects"
		if !m.showIgnored {
			m.status = "showing active projects"
		}
		return m, nil
	case "alt+l":
		// A session a window already shows is the one the reader can go back to, and every other
		// row is a chat that is not on screen yet.
		m.onlyShown = !m.onlyShown
		m.resetCursor()
		if m.onlyShown {
			m.status = "showing only the sessions a window already shows"
		} else {
			m.status = "showing every session"
		}
		return m, nil
	case "ctrl+l":
		m.layout = cycleLayout(m.layout)
		writeLayout(m.layout)
		m.status = "new terminals go to " + describeLayout(m.layout)
		return m, nil
	case "ctrl+b":
		m.harness = cycleHarness(m.harness, m.availableHarnesses())
		writeHarness(m.harness)
		m.status = "a new chat uses " + harnessTitle(m.harness) + "; the list shows every store"
		return m, nil
	case "ctrl+v":
		m.showPrev = !m.showPrev
		if !m.showPrev {
			m.focus = paneFocusList
			return m, nil
		}
		return m, m.refreshPreview()
	case "tab":
		// tab folds the group the cursor is in, which is what a list of projects is for; the
		// targets have their own pair, { and }.
		m.toggleGroupAtCursor()
		return m, nil
	case "{", "shift+[":
		return m, m.switchTarget(-1)
	case "}", "shift+]":
		return m, m.switchTarget(1)
	case "alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9":
		// The number is the one the target bar prints, so the same key pair walks the bar and a
		// numbered key reaches a host directly.  alt+0 is left out: ten targets is past the point
		// where a number is easier than { and }.
		return m, m.switchTargetAt(int(key[len(key)-1] - '0'))
	case "h", "left":
		if !m.showPrev {
			m.status = "the preview is hidden; ctrl+v shows it"
			return m, nil
		}
		m.focus = paneFocusList
		return m, nil
	case "l", "right":
		if !m.showPrev {
			m.status = "the preview is hidden; ctrl+v shows it"
			return m, nil
		}
		m.focus = paneFocusPreview
		return m, nil
	case "]":
		return m.cyclePane(1)
	case "[":
		return m.cyclePane(-1)
	case "n":
		// The attention queue's one key: the next chat that is blocked on a person, wrapping.
		return m.jumpToWaiting()
	case "j", "down", "ctrl+n":
		if m.focus == paneFocusPreview {
			m.scrollPreview(1)
			return m, nil
		}
		return m.move(1)
	case "k", "up", "ctrl+p":
		if m.focus == paneFocusPreview {
			m.scrollPreview(-1)
			return m, nil
		}
		return m.move(-1)
	case "g", "home":
		if m.focus == paneFocusPreview {
			m.scrollPreviewTo(0)
			return m, nil
		}
		*m.cursorPtr() = 0
		m.clampCursor()
		return m, m.refreshPreview()
	case "G", "end", "shift+g", "shift+end":
		// Bubble Tea reports a shifted letter as the letter itself, so both spellings are here.
		if m.focus == paneFocusPreview {
			m.scrollPreviewToBottom()
			return m, nil
		}
		*m.cursorPtr() = len(m.filtered()) - 1
		m.clampCursor()
		return m, m.refreshPreview()
	case "pgdown":
		if m.focus == paneFocusPreview {
			m.scrollPreview(m.previewBodyHeight() - 1)
			return m, nil
		}
		return m.move(m.listHeight())
	case "pgup":
		if m.focus == paneFocusPreview {
			m.scrollPreview(-(m.previewBodyHeight() - 1))
			return m, nil
		}
		return m.move(-m.listHeight())
	case "ctrl+d":
		if m.focus == paneFocusPreview {
			m.scrollPreview(m.previewBodyHeight() / 2)
			return m, nil
		}
		return m.move(m.listHeight() / 2)
	case "ctrl+u":
		if m.focus == paneFocusPreview {
			m.scrollPreview(-(m.previewBodyHeight() / 2))
			return m, nil
		}
		return m.move(-(m.listHeight() / 2))
	case "ctrl+r":
		m.status = "reloading"
		if !m.currentTarget().local() {
			// Forget the remembered connection, so this key re-checks the master and connects
			// again when it is gone: a master can be killed, or time out, between polls.
			m.markDisconnected(m.targetLabel())
		}
		return m, tea.Batch(m.refreshTarget(m.currentTarget()), m.loadLive())
	case "alt+r":
		// The forced reconnect: end the master and make a new one, for a connection that is
		// stale rather than gone.  ctrl+r only reconnects when the master is missing.
		host := m.currentTarget()
		if host.local() {
			m.status = "this machine needs no connection"
			return m, nil
		}
		m.markDisconnected(host.label())
		m.status = "recreating the connection to " + host.label()
		return m, m.reconnectMaster(host)
	case "esc":
		if *m.queryPtr() != "" {
			// Clearing the query must rebuild the rows: the list still holds the filtered set
			// otherwise, and the filter looks like it will not go away.
			*m.queryPtr(), *m.queryCursorPtr() = "", 0
			m.rebuildRows()
			return m, m.refreshPreview()
		}
		return m, nil
	}
	return m, nil
}

// handleSearchKey edits the slash-prefixed search field with Emacs/readline keys.  The field
// belongs to the list that owns the cursor, so a query typed in the zmx pane never narrows
// the project pane.
func (m *model) handleSearchKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	if key == blockedChord {
		return m, nil
	}
	f := field{text: *m.queryPtr(), cursor: *m.queryCursorPtr()}
	switch key {
	case "enter":
		m.searching = false
		m.status = ""
	case "esc", "ctrl+g":
		m.searching = false
		*m.queryPtr(), *m.queryCursorPtr() = "", 0
		m.rebuildRows()
		m.clampCursor()
		m.status = ""
		return m, m.refreshPreview()
	case "backspace", "ctrl+h":
		f.backspace()
	case "delete", "ctrl+d":
		f.deleteForward()
	case "ctrl+a", "home":
		f.home()
	case "ctrl+e", "end":
		f.end()
	case "ctrl+b", "left":
		f.move(-1)
	case "ctrl+f", "right":
		f.move(1)
	case "alt+b", "alt+left", "ctrl+left":
		f.wordLeft()
	case "alt+f", "alt+right", "ctrl+right":
		f.wordRight()
	case "ctrl+w", "alt+backspace", "alt+ctrl+h":
		m.kill = f.killWordBack()
	case "alt+d":
		m.kill = f.killWordForward()
	case "ctrl+k":
		m.kill = f.killToEnd()
	case "ctrl+u":
		m.kill = f.killToStart()
	case "ctrl+y":
		f.insert(m.kill)
	default:
		if len(msg.Runes) > 0 && !msg.Alt {
			f.insert(string(msg.Runes))
		}
	}
	*m.queryPtr(), *m.queryCursorPtr() = f.text, f.cursor
	m.rebuildRows()
	m.clampCursor()
	return m, m.refreshPreview()
}

// startZmxAction runs the keys a zmx row understands.
//
// Enter and ctrl+t go to the session itself: the picker hands its terminal over, and a
// second client goes in a window of its own. Local project keys keep working from the
// directory the session was started in, and ctrl+w ends the session and everything it runs.
// A new remote chat starts from the project group of that host. Renaming and forking a chat
// belong to a transcript row, where the transcript is the subject; here the session is.
func (m *model) startZmxAction(key string, row session) (tea.Model, tea.Cmd) {
	if row.Server != "" {
		if key == "ctrl+w" {
			m.pending, m.pendingID, m.pendingServer = "zmx-kill", row.ZmxName, row.Server
			m.status = "kill the zmx session " + row.ZmxName + " on " + row.Server +
				" and everything in it? y/N"
			return m, nil
		}
		if key == "enter" || key == "ctrl+t" {
			if key == "ctrl+t" {
				configured := m.layout
				m.layout = "window"
				cmd := m.runAction("zmx", row, false)
				m.layout = configured
				return m, cmd
			}
			return m.beginAction("zmx", row, false)
		}
		m.status = "remote zmx rows attach and kill; ctrl+a in its project group starts a new chat"
		return m, nil
	}
	switch key {
	case "enter":
		return m.beginAction("zmx", row, false)
	case "ctrl+t":
		// The same key on a session row asks for another terminal; here it is another
		// client, which is only useful in a window of its own.
		configured := m.layout
		m.layout = "window"
		cmd := m.runAction("zmx", row, false)
		m.layout = configured
		return m, cmd
	case "ctrl+x":
		return m.beginAction("shell", row, false)
	case "ctrl+a":
		return m.beginAction("new", row, false)
	case "ctrl+g":
		return m.beginAction("lazygit", row, false)
	case "alt+f":
		return m.beginAction("yazi", row, false)
	case "alt+e":
		return m.beginAction("project-editor", row, false)
	case "ctrl+w":
		m.pending, m.pendingID = "zmx-kill", row.ZmxName
		m.status = "kill the zmx session " + row.ZmxName + " and everything in it? y/N"
		return m, nil
	}
	m.status = "a zmx row attaches, kills, and opens shells; rename and fork act on a session row"
	return m, nil
}

// goneProjectLine names the directory a row cannot open.  A host reports a session whose
// recorded directory is no longer there as not alive, and one whose store recorded no
// directory at all the same way, and those two read differently to a person.
func goneProjectLine(s session) string {
	if s.CWD == "" {
		return "this session recorded no project directory on " + s.Server
	}
	return "the project directory " + s.CWD + " is gone on " + s.Server
}

// remoteAttachRow returns the zmx row that carries a remote session's chat, and whether the row
// has one at all.  A chat that a zmx session on that host runs is reached by attaching to that
// session: the process that already writes the transcript keeps it, and the terminal becomes a
// second client instead of a second writer.  That is what the local `switch` does, and the
// `pi=` handle on the row is what makes it possible here.  A chat with no such session has no
// local answer: no window on another host can be switched to, so opening it means a new process
// there, and a session that already runs has to be asked about first.
func remoteAttachRow(row session) (session, bool) {
	if row.ZmxName == "" || !row.Live {
		return session{}, false
	}
	attach := row
	attach.ZmxOnly = true
	return attach, true
}

// startRemoteSessionAction runs the keys a remote session row understands.  A remote row acts
// on its own host: a resume, a second terminal, a fork, a new chat in the same project, and the
// delete of its transcript all reach that host through one helper.  Rename, the transcript
// editor, and the local project shells say where the session is instead.
func (m *model) startRemoteSessionAction(key string, row session) (tea.Model, tea.Cmd) {
	switch key {
	case "enter", "ctrl+t", "ctrl+a", "ctrl+f":
		name := actionName(key)
		// A chat that runs inside a zmx session on that host is reached by attaching to that
		// session, so the reader is not asked about a second writer they never asked for.
		if attach, ok := remoteAttachRow(row); ok && (name == "resume" || name == "window") {
			if name == "window" {
				// ctrl+t asks for a terminal of its own, and a second client is one.
				configured := m.layout
				m.layout = "window"
				cmd := m.runAction("zmx", attach, false)
				m.layout = configured
				return m, cmd
			}
			return m.beginAction("zmx", attach, false)
		}
		if row.Live && (name == "resume" || name == "window") {
			m.pending, m.pendingID, m.pendingServer = name, row.ID, row.Server
			m.status = "● " + row.Server + " runs this session — open a second one? y/N"
			return m, nil
		}
		return m.beginAction(name, row, false)
	case "ctrl+x":
		// A shell is the host's own, run there in the host's directory: the work a reader
		// starts from this row is on that machine, so a terminal here would be in the wrong
		// place.  It gets no zmx session, because only a chat is worth keeping alive and a
		// zmx session is tied to the chat it runs.
		if !row.Alive {
			m.status = goneProjectLine(row)
			return m, nil
		}
		return m.beginAction("shell", row, false)
	case "ctrl+g", "alt+f":
		// The directory is the host's, so the tool runs there: yazi and lazygit act on the
		// files they show, and a host's files are on that host.  A directory the host has
		// already reported as gone has nothing to open, and the host said so when it read
		// the row, so there is nothing to ask it a second time.
		if !row.Alive {
			m.status = goneProjectLine(row)
			return m, nil
		}
		return m.beginAction(actionName(key), row, false)
	case "alt+e":
		// The project editor opens the host's own directory in the host's own editor, for the
		// same reason a shell does: an editor is a machine's tool with its own configuration,
		// and the files are there.
		if !row.Alive {
			m.status = goneProjectLine(row)
			return m, nil
		}
		return m.beginAction("project-editor", row, false)
	case "ctrl+e":
		m.status = "renaming a session on " + row.Server + " is not supported here"
		return m, nil
	}
	m.status = "a remote session resumes, forks, opens new terminals, and deletes its transcript there; rename is left to that host"
	return m, nil
}

func (m *model) toggleProjectIgnored() (tea.Model, tea.Cmd) {
	project := m.selected()
	if !project.ProjectOnly {
		return m, nil
	}
	ignored := readIgnoredProjects()
	path := filepath.Clean(project.CWD)
	if project.Ignored {
		delete(ignored, path)
		m.showIgnored = false
		m.status = "restored project " + project.Project
	} else {
		ignored[path] = true
		m.status = "ignored project " + project.Project
	}
	if err := writeIgnoredProjects(ignored); err != nil {
		m.status = "could not save ignored projects: " + err.Error()
		return m, nil
	}
	// The list is this machine's: the ignored set lives in the picker's own state, so only
	// the local target is read again.
	return m, m.loadTarget(target{})
}

// deleteCommand is the helper call that removes one transcript: the local sh2pil-sessions for a session
// here, and the host's own sh2pil-sessions for a session that lives there, where the id has to be resolved
// against that host's store.  A running session is carried through as --force, because the
// question has already been asked and answered.
func (m *model) deleteCommand(session session) (helper string, args []string, note string) {
	if session.Server != "" {
		harness := session.Harness
		if harness == "" {
			harness = "pi"
		}
		args = []string{"session-remote-delete", session.Server, session.ID, "--harness", harness}
		if session.Live {
			args = append(args, "--force")
		}
		return "sh2pil-open", args, "deleted the transcript on " + session.Server
	}
	args = []string{"delete", session.ID}
	if harnessOf(session) != "pi" {
		// The store each harness writes is its own, so the delete has to name it.
		args = append(args, "--harness", harnessOf(session))
	}
	if _, live := m.live[session.ID]; live {
		args = append(args, "--force")
	}
	return "sh2pil-sessions", args, "deleted the transcript"
}

// startAction runs one of the action keys, asking first when a pi process may already own
// the session. Resuming a live session means two writers on one transcript; the
// exact process-to-session record lets us ask before that happens.
func (m *model) startAction(key string) (tea.Model, tea.Cmd) {
	session := m.selected()
	if session.ZmxOnly {
		return m.startZmxAction(key, session)
	}
	name := actionName(key)
	if session.ProjectOnly {
		// A project row is a group header.  enter opens and closes it, because the group is
		// where the sessions are; the keys that start something in the project keep working
		// from the header, so a project with no sessions is still a place to start one.
		if key == "enter" {
			m.toggleGroup(session)
			return m, nil
		}
		if session.Ignored {
			m.status = "restore this project before opening it"
			return m, nil
		}
		if session.Server != "" {
			switch key {
			case "ctrl+a":
				// Which verb a new chat on that host uses is beginAction's decision, so this key,
				// the next-step menu, and a placement answer all reach the same one.
				return m.beginAction("new", session, false)
			case "ctrl+g", "alt+f":
				// A project row names a directory on the host, so the directory tools open
				// there, exactly as they do on one of that host's session rows.
				return m.beginAction(actionName(key), session, false)
			case "ctrl+t", "ctrl+x":
				// A project row's own shell opens on the host too, and ctrl+t is that shell in
				// a terminal of its own, exactly as it is on a local project row.
				return m.beginAction("shell", session, false)
			case "alt+e":
				// The project itself is the host's directory, so its editor opens there too.
				return m.beginAction("project-editor", session, false)
			}
			m.status = "that action needs a session; this project is on " + session.Server
			return m, nil
		}
		switch key {
		case "ctrl+a":
			name = "new"
		case "ctrl+t", "ctrl+x":
			name = "shell"
		case "ctrl+g":
			name = "lazygit"
		case "alt+f":
			name = "yazi"
		case "alt+e":
			name = "project-editor"
		default:
			m.status = "that action needs a session; choose a project action instead"
			return m, nil
		}
		return m.beginAction(name, session, false)
	}
	if session.Server != "" {
		return m.startRemoteSessionAction(key, session)
	}
	if name == "project-editor" {
		return m.beginAction(name, session, false)
	}
	if session.ID == "" {
		return m, nil
	}
	if info, live := m.live[session.ID]; live && needsLiveGuard(name) {
		m.pending = name
		when := fmt.Sprintf("%ds", info.Written)
		if info.Written >= 60 {
			when = fmt.Sprintf("%dm", info.Written/60)
		}
		where := "somewhere this cannot reach"
		if info.Owner != "" {
			where = info.Owner
		}
		m.status = fmt.Sprintf("● pi pid %s wrote %s ago, in %s — open a second one? y/N",
			info.pids(), when, where)
		return m, nil
	}
	return m.beginAction(name, session, false)
}

func defaultEditorTitle(session session) string {
	project := session.Project
	if project == "" {
		project = filepath.Base(session.CWD)
	}
	return "nvim " + project
}

func (m *model) askForEditorTitle(session session, force bool, place string) (tea.Model, tea.Cmd) {
	m.modal, m.name = "editor-title", newField(defaultEditorTitle(session))
	m.askAction, m.askSession, m.askForce, m.askLayout = "project-editor", session, force, place
	m.status = ""
	return m, nil
}

func (m *model) beginAction(name string, session session, force bool) (tea.Model, tea.Cmd) {
	// A new chat on a remote project row runs inside a zmx session on that host when the Pi
	// store is in force: only a Pi chat can, and `zmx-new` is the verb that starts one there.
	// The choice is made here because every route to a new chat meets here -- ctrl+a, the
	// next-step menu, a trigger that moves the action into the menu, and the answer to the
	// placement question.  A choice made by the key alone left the menu asking this machine
	// for a host's directory, which the guard below then refused.
	if name == "new" && session.Server != "" && session.ProjectOnly && m.harness == "pi" {
		name = "zmx-new"
	}
	if name == "project-editor" {
		if m.layout == "ask" || m.alwaysAsk {
			// The menu opens on the placement the picker falls back to, so enter takes it.
			m.modal, m.layoutPos = "layout", placementIndex(defaultLayout)
			m.askAction, m.askSession, m.askForce = name, session, force
			m.status = ""
			return m, nil
		}
		return m.askForEditorTitle(session, force, m.layout)
	}
	// Ask only when a new terminal may be needed.  A live session that a terminal already
	// shows is navigated in place by `sh2pil-open switch`, so its tab, pane, or window is
	// preserved and there is nothing to choose.  A session with no terminal to switch to --
	// a chat in a zmx session whose client was closed, or one running where this cannot
	// reach -- needs a new one, and then the placement question is the reader's only say in
	// where it goes.
	// A shell, lazygit, or yazi always opens a new terminal, so it asks on a session or a
	// zmx row too, not only on a project row.
	asksPlacement := name == "resume" || name == "new" || name == "fork" || name == "zmx" ||
		name == "zmx-new" || name == "shell" || name == "lazygit" || name == "yazi"
	if (m.layout == "ask" || m.alwaysAsk) && asksPlacement && !(name == "resume" && m.shown(session)) {
		// The menu opens on the placement the picker falls back to, so enter takes it.
		m.modal, m.layoutPos = "layout", placementIndex(defaultLayout)
		m.askAction, m.askSession, m.askForce = name, session, force
		m.status = ""
		return m, nil
	}
	return m, m.runAction(name, session, force)
}

// shown reports whether a terminal already shows this session, which is what makes a resume
// a switch instead of a launch.  `sh2pil-open live` answers with the window that holds the
// session, so an entry with no owner is one nothing shows: the client of a zmx session was
// closed, or the pi process runs somewhere this cannot reach.
func (m *model) shown(session session) bool {
	info, live := m.rowLive(session)
	return live && info.Owner != ""
}

func (m *model) chooseLayout(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(placements) {
		return m, nil
	}
	name, session, force := m.askAction, m.askSession, m.askForce
	if name == "project-editor" {
		return m.askForEditorTitle(session, force, placements[index])
	}
	m.modal, m.askAction = "", ""
	configured := m.layout
	m.layout = placements[index]
	cmd := m.runAction(name, session, force)
	m.layout = configured
	return m, cmd
}

// promptPrimary reports whether the primary key opens the next-step menu instead of running
// the row's action.  The mode sets it, and a trigger on the primary action overrides the mode.
func (m *model) promptPrimary() bool {
	if value, set := m.triggers["list.resume"]; set {
		return value == "prompt"
	}
	return m.mode == "prompt"
}

// openToolMenu opens the numbered menu of the things the selected row can do.
func (m *model) openToolMenu() (tea.Model, tea.Cmd) {
	return m.openToolMenuFor(m.selected())
}

// openToolMenuFor opens that menu for one row.  The cmd+. dialog passes the row sh2pil-open
// resolved for its window, because that row is not on any list this picker read.
func (m *model) openToolMenuFor(selected session) (tea.Model, tea.Cmd) {
	choices := travelChoicesFor(selected)
	if len(choices) == 0 {
		m.status = "nothing opens from this row; choose a project or a session"
		return m, nil
	}
	m.askSession = selected
	m.toolChoices, m.toolSections = choices, nil
	m.toolPos, m.modal, m.status = 0, "tools", ""
	return m, nil
}

// chooseTool runs the action the menu row names.
func (m *model) chooseTool(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.toolChoices) {
		return m, nil
	}
	name, chosen := m.toolChoices[index], m.askSession
	m.modal, m.toolChoices, m.toolSections, m.toolPos = "", nil, nil, 0
	if name == "new" && m.windowMenu {
		// A chat started from a window asks which store first: the picker's remembered store
		// answers a different question, which is which store the list reads.
		return m.openAgentMenu(chosen)
	}
	return m.beginAction(name, chosen, false)
}

// openAgentMenu opens the store question a new chat from a window asks.
func (m *model) openAgentMenu(row session) (tea.Model, tea.Cmd) {
	if len(m.harnesses) == 0 {
		m.status = "no chat store is available here"
		return m, nil
	}
	m.askSession = row
	m.agentChoices = append([]string{}, m.harnesses...)
	m.agentPos, m.modal, m.status = 0, "agents", ""
	return m, nil
}

// chooseAgent starts the new chat in the store the reader picked.
func (m *model) chooseAgent(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.agentChoices) {
		return m, nil
	}
	harness, row := m.agentChoices[index], m.askSession
	m.modal, m.agentChoices, m.agentPos = "", nil, 0
	m.harness = harness
	return m.beginAction("new", row, false)
}

// windowMenuRow turns the target sh2pil-open resolved for one kitty window into the row its
// menu acts on.  It is a project row: the menu offers what one can do in a directory, and
// the actions that open a chat, a tool, or a shell all start from that directory.
func windowMenuRow(cwd, server string) session {
	project := filepath.Base(cwd)
	if project == "" || project == "." || project == string(filepath.Separator) {
		project = cwd
	}
	return session{Project: project, CWD: cwd, Server: server, Alive: true, ProjectOnly: true}
}

// menuHarnesses keeps the stores the picker reads and one target has.  A target that named no
// store leaves the setting in force, and so does an intersection that keeps nothing: a read
// that failed is not evidence that a store is gone, and a menu with no store in it could not
// start a chat at all.
func menuHarnesses(configured []string, present string) []string {
	if strings.TrimSpace(present) == "" {
		return configured
	}
	has := map[string]bool{}
	for _, name := range strings.Split(present, ",") {
		has[strings.TrimSpace(name)] = true
	}
	kept := make([]string, 0, len(configured))
	for _, name := range configured {
		if has[name] {
			kept = append(kept, name)
		}
	}
	if len(kept) == 0 {
		return configured
	}
	return kept
}

// travelChoicesFor is the travel menu for one row: the ways into the project space the row
// belongs to, in the order a reader reaches for them.  The dot key opens it, and so does the
// cmd+. dialog, because a window's menu is about getting somewhere in its own directory.
func travelChoicesFor(s session) []string {
	switch {
	case s.ZmxOnly:
		choices := []string{"zmx", "shell", "lazygit", "yazi", "new"}
		if s.File != "" {
			choices = append(choices, "editor")
		}
		return choices
	case s.ProjectOnly:
		return []string{"new", "project-editor", "shell", "lazygit", "yazi"}
	default:
		return []string{"resume", "editor", "shell", "lazygit", "yazi"}
	}
}

// paletteSections is the command palette, in the order it is read: the heading, then the
// bound actions under it.  The delete interface comes first, because a reader who opened the
// palette to clean up should not have to walk past the other commands to find it.
var paletteSections = []struct {
	section string
	actions []string
}{
	{"delete", []string{"list.prune", "list.delete"}},
	{"session", []string{"list.rename", "list.fork", "list.copy_resume", "list.copy_path"}},
	{"view", []string{"list.live_only", "list.preview_toggle"}},
	{"target", []string{"list.refresh", "list.reconnect"}},
	{"picker", []string{"list.help", "list.quit"}},
}

// paletteEntriesFor is the command palette for one row: the bound actions that are not
// travel, each under the heading of its section.  An action the reader unbound contributes
// no row, so the palette never offers something that would do nothing.
func (m *model) paletteEntriesFor(s session) ([]string, []string) {
	var actions, sections []string
	for _, group := range paletteSections {
		for _, action := range group.actions {
			if len(m.chordsOf(action)) == 0 {
				continue
			}
			actions = append(actions, action)
			sections = append(sections, group.section)
		}
	}
	return actions, sections
}

// chordsOf is the chords in force for one action: none when the reader unbound it, and none
// when there is no keymap at all, which is what a hand-built test model has.
func (m *model) chordsOf(name string) []string {
	if m.keymap == nil {
		return nil
	}
	return m.keymap.chords(name)
}

// paletteLabel is what one palette row says for the row under the cursor.  The label follows
// that row, because one bound action deletes a transcript, ends a zmx session, or ignores a
// project depending on what is selected.  The help box carries the long form of the same
// actions; a menu row is read at a glance and stays short.
func (m *model) paletteLabel(action string, s session) string {
	switch action {
	case "list.prune":
		return "delete old sessions…"
	case "list.delete":
		switch {
		case s.ZmxOnly:
			return "end this zmx session"
		case s.ProjectOnly:
			return "ignore or restore this project"
		}
		return "delete this session"
	case "list.rename":
		return "rename this session"
	case "list.fork":
		return "fork this session"
	case "list.copy_resume":
		return "copy the resume or attach command"
	case "list.copy_path":
		return "copy the transcript path"
	case "list.live_only":
		return "show only the sessions a window shows"
	case "list.preview_toggle":
		return "show or hide the preview"
	case "list.refresh":
		return "read this target again"
	case "list.reconnect":
		return "end the SSH master and make a new one"
	case "list.help":
		return "the key list"
	case "list.quit":
		return "quit the picker"
	}
	return action
}

// menuRowLabel names one menu row: the travel menu names the way into a project space, and
// the palette names the command for the row it was opened on.
func (m *model) menuRowLabel(action string) string {
	if m.modal == "palette" {
		return m.paletteLabel(action, m.askSession)
	}
	return m.toolLabel(action)
}

// inMenu reports whether a menu owns the keyboard: the travel menu, or the command palette.
func (m *model) inMenu() bool {
	return m.modal == "tools" || m.modal == "palette"
}

// chooseMenu runs the row at one index of whichever menu is open.
func (m *model) chooseMenu(index int) (tea.Model, tea.Cmd) {
	if m.modal == "palette" {
		return m.choosePalette(index)
	}
	return m.chooseTool(index)
}

// openPalette opens the command palette for the selected row.
func (m *model) openPalette() (tea.Model, tea.Cmd) {
	selected := m.selected()
	actions, sections := m.paletteEntriesFor(selected)
	if len(actions) == 0 {
		m.status = "the command palette is empty: every command is unbound"
		return m, nil
	}
	m.askSession = selected
	m.toolChoices, m.toolSections = actions, sections
	m.toolPos, m.modal, m.status = 0, "palette", ""
	return m, nil
}

// choosePalette runs one palette command.  A command is a bound action, so the row is run
// the same way its key would run it: the action's canonical chord goes back through the
// dispatch, and the rules about a running session, a remote row, or a row that cannot be
// renamed stay in the one place that already knows them.
func (m *model) choosePalette(index int) (tea.Model, tea.Cmd) {
	if index < 0 || index >= len(m.toolChoices) {
		return m, nil
	}
	name := m.toolChoices[index]
	m.modal, m.toolChoices, m.toolSections, m.toolPos = "", nil, nil, 0
	chords := m.chordsOf(name)
	if len(chords) == 0 {
		m.status = "that command has no key bound; set key." + name + " to run it"
		return m, nil
	}
	msg, ok := keyMsgForChord(chords[0])
	if !ok {
		m.status = "cannot run " + chords[0] + " from the palette"
		return m, nil
	}
	return m.handleKey(msg)
}

// startPrune opens the age field of the delete-old-sessions command.  It acts on the target on
// screen and on nothing else: a host is read and cleaned on its own, so one key can never
// delete on a machine the reader is not looking at.
func (m *model) startPrune() (tea.Model, tea.Cmd) {
	m.modal, m.name, m.status = "prune", newField(defaultPruneAge), ""
	m.pruneAge, m.pruneSessions, m.pruneZmx = "", -1, 0
	return m, nil
}

// pruneHelper is the helper, and its arguments, for one prune of the target on screen: the
// stores this picker reads, and the zmx sessions of the same target.  A store the picker is
// not configured for is left alone, and an empty setting means every store this build knows.
//
// A host is pruned over the connection it already has, because the store that holds a
// transcript is the one that removes it: the age is resolved against the rows that host
// holds, and this side never guesses at a path it cannot see.
func (m *model) pruneHelper(age string, deletes bool) (string, []string) {
	stores := strings.Join(m.harnesses, ",")
	if stores == "" {
		stores = "all"
	}
	// The same flags in both places, and only the command in front of them differs: the local
	// sh2pil-sessions is asked for JSON, and sh2pil-open only ever relays the host's JSON, so it takes no such
	// flag and no subcommand word of its own.  One flag list is what keeps the two in step.
	flags := []string{"--older-than", age, "--harness", stores, "--zmx"}
	if deletes {
		flags = append(flags, "--yes")
	}
	if server := m.currentTarget().Server; server != "" {
		return "sh2pil-open", append([]string{"session-remote-prune", server}, flags...)
	}
	return "sh2pil-sessions", append([]string{"prune", "--json"}, flags...)
}

// pruneCount is what sh2pil-sessions prune answers: the selection when it is only read, and the two
// counts of what was actually removed when it is a delete.
type pruneCount struct {
	Sessions        []json.RawMessage `json:"sessions"`
	Zmx             []json.RawMessage `json:"zmx"`
	DeletedSessions int               `json:"deleted_sessions"`
	DeletedZmx      int               `json:"deleted_zmx"`
}

// pruneCheckMsg is the answer to the read that runs before the question: how much an age
// would remove, so the reader is asked about a number and not a hope.
type pruneCheckMsg struct {
	age      string
	sessions int
	zmx      int
	err      error
}

// helperFailure is the one line to report when a helper failed and its stdout was kept for
// parsing: the helper's own words on stderr, then what it wrote to stdout, then the error.
func helperFailure(err error, out []byte) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		if line := lastLine(string(exit.Stderr)); line != "" {
			return line
		}
	}
	if line := firstLine(string(out)); line != "" {
		return line
	}
	return firstLine(err.Error())
}

// checkPrune asks sh2pil-sessions what an age selects, without deleting anything, so the reader is asked
// about a number instead of a hope.
func (m *model) checkPrune(age string) tea.Cmd {
	helper, args := m.pruneHelper(age, false)
	command := m.helper(helper, args...)
	return func() tea.Msg {
		out, err := command.Output()
		if err != nil {
			return pruneCheckMsg{age: age, err: err}
		}
		var selected pruneCount
		if err := json.Unmarshal(out, &selected); err != nil {
			return pruneCheckMsg{age: age, err: err}
		}
		return pruneCheckMsg{age: age, sessions: len(selected.Sessions), zmx: len(selected.Zmx)}
	}
}

// runPrune deletes what the reader confirmed.
func (m *model) runPrune(age string) tea.Cmd {
	helper, args := m.pruneHelper(age, true)
	command := m.helper(helper, args...)
	return func() tea.Msg {
		out, err := command.Output()
		if err != nil {
			return noteMsg{note: "prune failed: " + helperFailure(err, out)}
		}
		var done pruneCount
		if err := json.Unmarshal(out, &done); err != nil {
			return noteMsg{note: "prune failed: " + err.Error()}
		}
		return actionMsg{note: fmt.Sprintf("deleted %s and %s older than %s",
			plural(done.DeletedSessions, "session"), plural(done.DeletedZmx, "zmx session"),
			ageSummary(mustAge(age)))}
	}
}

// mustAge reads an age that was already accepted by the field, so a parse cannot fail here.
func mustAge(raw string) time.Duration {
	age, err := parseAge(raw)
	if err != nil {
		return 0
	}
	return age
}

// toolLabel names one menu action for a reader, using the configured tools where they apply.
func (m *model) toolLabel(name string) string {
	switch name {
	case "resume":
		return "open or resume the session"
	case "new":
		return "start a new chat"
	case "project-editor":
		return "open the project in " + firstField(m.editor, "nvim")
	case "editor":
		return "open the transcript in " + firstField(m.editor, "nvim")
	case "fork":
		return "fork the session"
	case "shell":
		return "open a login shell"
	case "lazygit":
		return "open the git tool (" + firstField(m.gitTool, defaultGitTool) + ")"
	case "yazi":
		return "open the file browser (" + firstField(m.fileBrowser, defaultFileBrowser) + ")"
	case "zmx":
		return "attach to the zmx session"
	}
	return name
}

func (m *model) runEditorAction(title string) tea.Cmd {
	configured := m.layout
	m.layout = m.askLayout
	m.editorLabel = title
	cmd := m.runAction("project-editor", m.askSession, m.askForce)
	m.layout, m.editorLabel = configured, ""
	m.askAction, m.askLayout = "", ""
	return cmd
}

func (m *model) copyAction(key string) (tea.Model, tea.Cmd) {
	session := m.selected()
	if session.ZmxOnly {
		if key == "alt+y" {
			m.status = "a zmx session has no transcript path; ctrl+y copies the attach command"
			return m, nil
		}
		args := []string{"zmx-copy", session.ZmxName}
		if session.Server != "" {
			args = append(args, "--server", session.Server)
		}
		command := m.helper("sh2pil-open", args...)
		return m, func() tea.Msg {
			out, err := command.Output()
			if err != nil {
				return noteMsg{note: "copy failed: " + err.Error()}
			}
			return noteMsg{note: "copied the attach command: " + firstLine(string(out))}
		}
	}
	if session.Server != "" {
		if key == "alt+y" {
			m.status = "the transcript lives on " + session.Server +
				"; ctrl+y copies the command that opens it"
			return m, nil
		}
		if session.ID == "" {
			return m, nil
		}
		// The copied line is one the reader can paste here: it opens the same remote session
		// in a terminal of its own, with the host and the directory already in it.
		args := []string{"session-remote-copy", session.Server, session.ID,
			"--harness", harnessOf(session)}
		if session.Alive {
			args = append(args, "--cwd", session.CWD)
		}
		command := m.helper("sh2pil-open", args...)
		return m, func() tea.Msg {
			out, err := command.Output()
			if err != nil {
				return noteMsg{note: "copy failed: " + remoteFailure(err)}
			}
			return noteMsg{note: "copied the command that opens it on " + session.Server +
				": " + firstLine(string(out))}
		}
	}
	if session.ID == "" {
		return m, nil
	}
	args := []string{"copy", "--harness", harnessOf(session), session.ID}
	if key == "alt+y" {
		args = append(args, "--path")
	}
	command := m.helper("sh2pil-sessions", args...)
	kind := "resume command"
	if key == "alt+y" {
		kind = "transcript path"
	}
	return m, func() tea.Msg {
		out, err := command.Output()
		if err != nil {
			return noteMsg{note: "copy failed: " + err.Error()}
		}
		return noteMsg{note: "copied the " + kind + ": " + firstLine(string(out))}
	}
}

// runPib runs a sh2pil-sessions subcommand off the UI path and reports what it said.
//
// Rename and delete live in sh2pil-sessions, not here: they must work the same from the command line and
// this UI, and the rules about a running session belong in one place.
func (m *model) runPib(note string, args ...string) tea.Cmd {
	return m.runHelper("sh2pil-sessions", note, args...)
}

// runHelper runs one subcommand of one helper and reports what it said.  The zmx keys need
// the same treatment as the session keys, and their verbs live in sh2pil-open.
func (m *model) runHelper(helper, note string, args ...string) tea.Cmd {
	command := m.helper(helper, args...)
	mail := note
	return func() tea.Msg {
		out, err := command.CombinedOutput()
		if err != nil {
			line := firstLine(string(out))
			if line == "" {
				line = err.Error()
			}
			return noteMsg{note: args[0] + " failed: " + line}
		}
		return actionMsg{note: mail + " — " + firstLine(string(out))}
	}
}

// runAction hands the work to sh2pil-open.  Bubble Tea releases the terminal for the child,
// which the editor action needs and the launcher does not mind.
func (m *model) runAction(name string, session session, force bool) tea.Cmd {
	args, note := m.openArgs(name, session, force)
	if args == nil {
		// Nothing runs, but the reason is worth a sentence: a key that does nothing at all
		// reads as a broken key, and this is where a refusal for this row is explained.
		if note != "" {
			m.status = note
		}
		return nil
	}
	// The status the reader sees should say which of the two things happened, and this UI
	// already knows where the session runs from the live check.
	if name == "project-editor" && session.Server == "" {
		note = "opened the project editor"
	}
	if name == "resume" {
		if session.Server != "" {
			note = "opened the session on " + session.Server
		} else if session.Harness == "opencode" {
			note = "opened the OpenCode session"
		} else if session.Harness == "claude" {
			note = "opened the Claude Code session"
		} else if session.Harness == "codex" {
			note = "opened the Codex session"
		} else if info, live := m.live[session.ID]; live && info.Owner != "" {
			note = "switched to " + info.Owner
		} else {
			note = "started pi in " + describeLayout(m.layout)
		}
	}
	helper := "sh2pil-open"
	if name == "editor" && harnessOf(session) != "pi" {
		helper = "sh2pil-sessions"
	}
	command := m.helper(helper, args...)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if err != nil {
			return actionMsg{err: fmt.Errorf("%s failed: %v", name, err)}
		}
		if m.closeOnNavigate {
			return tea.Quit()
		}
		return actionMsg{note: note}
	})
}

// reachesHost reports whether one helper call is aimed at the host a row came from.  A verb that
// names a destination does that by itself, and a zmx verb is told which server to use.
func reachesHost(args []string) bool {
	if len(args) == 0 || args[0] == "" {
		return false
	}
	if strings.Contains(args[0], "-remote") {
		return true
	}
	for _, arg := range args[1:] {
		if arg == "--server" {
			return true
		}
	}
	return false
}

// openArgs turns one action and one row into the helper call that runs it.  A row that came from
// a host never gets a call that would run here: its directory is on that host, where a local
// command would fail on a missing path or, far worse, act on a different path that happens to
// exist on this machine under the same name.  The one place this is enforced is here, so a key
// that is added or rerouted later cannot leak a host's path to a local verb.
func (m *model) openArgs(action string, session session, force bool) ([]string, string) {
	args, note := m.openArgsFor(action, session, force)
	if args != nil && session.Server != "" && !reachesHost(args) {
		return nil, "that action runs on this machine; this row is on " + session.Server
	}
	return args, note
}

func (m *model) openArgsFor(action string, session session, force bool) ([]string, string) {
	// "ask" is a UI preference, not a placement accepted by sh2pil-open. Actions that
	// do not ask (including focusing a live session) use the placement in force.
	place := m.layout
	if action == "window" {
		// ctrl+t exists to add a terminal of its own, so it asks for its own OS window
		// whatever the placement preference says, and it never asks a question.
		place = "window"
	} else if place == "ask" {
		place = defaultLayout
	}
	// A remote row names a directory on its own host, so nothing here may replace it with a
	// local one: sh2pil-open builds the SSH terminal for it instead.
	cwd := session.CWD
	if action != "project-editor" && (!session.Alive || cwd == "") {
		cwd = os.Getenv("HOME")
	}
	// The label is the visible tab title, and sh2pil-open matches a window title against the head
	// of the session name to find the terminal that already runs a session, so the full name
	// goes in, untruncated.
	title := session.Name
	if title == "" {
		title = trim(session.ID, 8)
	}
	if action == "fork" && session.Harness == "opencode" {
		return nil, "forking OpenCode sessions is not supported by its CLI"
	}
	if action == "fork" && m.forkName != "" {
		// A fork is a different session: its own name belongs in the title, not the name of
		// the session it was forked from.
		title = m.forkName
	}
	label := session.Harness + " " + title
	if session.Harness == "" {
		label = "pi " + title
	}

	switch action {
	case "resume", "window", "fork", "new":
		// A remote session row opens a terminal on its own host.  A remote project row's new
		// chat is that same open with no session id, for every store but Pi: only a Pi chat
		// can run inside a zmx session there, and beginAction sends that one to `zmx-new`
		// before it gets here.  The condition keeps the Pi case out all the same, so a caller
		// that asks for it directly is refused by the guard in openArgs rather than handed
		// this machine's own verb.
		if session.Server != "" && !session.ZmxOnly &&
			(!session.ProjectOnly || (action == "new" && m.harness != "pi")) {
			return m.remoteOpenArgs(action, session, place)
		}
	}

	// A directory tool opens where its files are: yazi browses the files a host holds and
	// lazygit works on them, so on a row of another host both run there, in that host's own
	// directory.  Nothing here resolves a path for them: yazi browses the directory itself,
	// and lazygit is left to climb to the repository on the host, which is the only machine
	// that can answer where that repository starts.
	if session.Server != "" && (action == "lazygit" || action == "yazi") {
		return m.remoteToolArgs(action, session, place)
	}

	// A shell is the host's own, in the host's directory: the same rule as a directory tool,
	// and the reason a shell opened here would be in the wrong place.  Nothing is checked
	// about it beyond that directory, because the host is the one that has the shell.
	if session.Server != "" && action == "shell" {
		return m.remoteShellArgs(session, place)
	}

	// The project editor opens on the host for the same reason, and it is the host's editor:
	// this machine's is at a path that host does not have, and an editor is a machine's own
	// tool with its own plugins and its own clipboard.
	if session.Server != "" && action == "project-editor" {
		return m.remoteEditorArgs(session, place)
	}

	switch action {
	case "zmx":
		// The zmx session already runs in its own directory and has its own size, so the
		// client only needs somewhere to start and a place to go.
		args := []string{"zmx-switch", session.ZmxName, "--cwd", cwd, "--place", place}
		if session.Server != "" {
			args = append(args, "--server", session.Server)
		}
		return args, "went to the zmx session " + session.ZmxName
	case "zmx-new":
		return []string{"zmx-remote-new", session.Server, cwd, "--place", place},
			"started a new remote Pi chat"
	case "project-editor":
		label := m.editorLabel
		if label == "" {
			label = defaultEditorTitle(session)
		}
		return []string{"editor-dir", cwd, "--editor", m.editor,
			"--label", label, "--place", place}, "opened the project in the editor"
	case "resume", "window", "fork":
		if session.Harness == "claude" {
			// Claude Code resumes by session id or by transcript path from any directory, and
			// a fork is the same command with --fork-session.  It keeps no process record, so
			// there is no terminal to switch to and no second writer to ask about.
			target := session.ID
			if !session.Alive && session.File != "" {
				target = session.File
			}
			args := []string{"claude", cwd, target, "--label", label, "--place", place}
			if action == "fork" {
				args = append(args, "--fork")
				if m.forkName != "" {
					args = append(args, "--name", m.forkName)
					m.forkName = ""
				}
				return args, "forked " + trim(session.Name, 30)
			}
			return args, "opened the Claude Code session"
		}
		if session.Harness == "opencode" {
			if action == "fork" {
				return nil, "forking OpenCode sessions is not supported by its CLI"
			}
			if !session.Alive {
				cwd = os.Getenv("HOME")
			}
			return []string{"opencode", cwd, session.ID, "--label", label,
				"--place", place}, "opened OpenCode session"
		}
		if session.Harness == "codex" {
			// Codex resumes a session id from any directory, and forks the same way; it names a
			// fork itself, so no name travels from here.
			args := []string{"codex", cwd, session.ID, "--label", label, "--place", place}
			if action == "fork" {
				args = append(args, "--fork")
				m.forkName = ""
				return args, "forked " + trim(session.Name, 30)
			}
			return args, "opened the Codex session"
		}
		target := session.ID
		if !session.Alive {
			// A gone project directory still resumes by transcript path.
			target = session.File
		}
		// `switch` brings a running session forward and resumes only when nothing runs,
		// because a second pi on one transcript interleaves writes.
		verb := "pi"
		if action == "resume" {
			verb = "switch"
		}
		// One placement for both, from the setting ctrl+l cycles: opening a session and
		// forking it must not disagree about where the terminal goes.
		args := []string{verb, cwd, target, "--label", label, "--place", place}
		switch action {
		case "fork":
			args = append(args, "--fork")
			if m.forkName != "" {
				args = append(args, "--name", m.forkName)
				m.forkName = ""
			}
		case "window":
			// Deliberately a second terminal, so no --force: sh2pil-open asks its own question.
		default:
			if force {
				// We asked the question here already, so sh2pil-open must not ask again.
				args = append(args, "--force")
			}
		}
		if action == "fork" {
			return args, "forked " + trim(session.Name, 30)
		}
		return args, "opened the session"
	case "shell":
		return []string{"shell", cwd, "--label", "shell " + session.Project,
			"--place", place}, "opened a shell in " + session.Project
	case "new":
		if m.harness == "opencode" {
			return []string{"opencode-new", cwd, "--label", "opencode " + session.Project,
				"--place", place}, "started a new OpenCode session in " + filepath.Base(cwd)
		}
		if m.harness == "claude" {
			return []string{"claude-new", cwd, "--label", "claude " + session.Project,
				"--place", place}, "started a new Claude Code session in " + filepath.Base(cwd)
		}
		if m.harness == "codex" {
			return []string{"codex-new", cwd, "--label", "codex " + session.Project,
				"--place", place}, "started a new Codex session in " + filepath.Base(cwd)
		}
		// A new session carries no transcript forward, which is the difference from resume
		// and fork: pi starts one in the project the picked session works in.  No label is
		// passed, because pi generates the new session's name and titles its own window from
		// it; a label here would freeze a copy of that name in the tab bar.
		project := filepath.Base(cwd)
		return []string{"new", cwd, "--place", place},
			"started a new session in " + project
	case "lazygit":
		// The git tool is a setting.  lazygit alone climbs to the repository top, because it
		// needs the whole worktree; another tool is given the same directory.
		return m.runToolArgs(m.gitTool, defaultGitTool, gitRoot(cwd), place)
	case "yazi":
		// The file browser is a setting too, and it opens at the project directory itself.
		return m.runToolArgs(m.fileBrowser, defaultFileBrowser, cwd, place)
	case "editor":
		if harnessOf(session) != "pi" {
			return []string{"show", "--harness", harnessOf(session), session.ID,
				"--nvim", m.editor}, "closed the transcript view"
		}
		return []string{"editor", session.File, "--editor", m.editor},
			"closed the transcript view"
	}
	return nil, ""
}

// remoteOpenArgs is what sh2pil-open needs to run one session action on another host.  Every
// action that opens a chat goes through one verb, so a resume, a fork, a second terminal, and
// a new chat in the same project differ only in their arguments.  The directory is the remote
// one as that host recorded it, and an empty one is a project that is gone there: the remote
// shell then starts in its own home directory and pi resumes from the transcript path.
func (m *model) remoteOpenArgs(action string, s session, place string) ([]string, string) {
	switch action {
	case "resume", "window", "new", "fork":
	default:
		return nil, "that action opens a terminal on this machine; this session is on " + s.Server
	}
	cwd := s.CWD
	if !s.Alive {
		cwd = ""
	}
	// A new chat uses the store in force, not the store of the row it started from: a project
	// header carries no store of its own, and a session row's store is a different claim.
	harness := harnessOf(s)
	if action == "new" {
		harness = m.harness
	}
	args := []string{"session-remote-open", s.Server, cwd}
	if action != "new" {
		args = append(args, s.ID)
	}
	args = append(args, "--harness", harness, "--place", place)
	switch action {
	case "fork":
		args = append(args, "--fork")
		if m.forkName != "" {
			args = append(args, "--name", m.forkName)
			m.forkName = ""
		}
		return args, "forked " + trim(s.Name, 30) + " on " + s.Server
	case "new":
		project := s.Project
		if project == "" {
			project = filepath.Base(s.CWD)
		}
		return args, "started a new chat in " + project + " on " + s.Server
	}
	return args, "opened the session on " + s.Server
}

// remoteShellArgs is what sh2pil-open needs to open a login shell on another host.  The shell
// starts in the directory that host recorded, so the reader lands where the work is; a
// directory the host has reported as gone sends an empty one, which means that host's home.
// The label is left to sh2pil-open, which names the shell and the directory it opens in.
func (m *model) remoteShellArgs(s session, place string) ([]string, string) {
	cwd := s.CWD
	if !s.Alive {
		cwd = ""
	}
	project := filepath.Base(cwd)
	if project == "" || project == "." {
		project = s.Project
	}
	return []string{"shell-remote", s.Server, cwd, "--place", place},
		"opened a shell in " + project + " on " + s.Server
}

// remoteEditorArgs is what sh2pil-open needs to open one project on another host in that host's
// own editor.  The host's `$EDITOR` is the default, and nvim when it has neither; a per-host
// setting under tools.hosts names one when the reader wants a specific editor there, and only
// then does a name travel.  The label is the title the reader gave the window, or the same
// default a local project editor is offered.
func (m *model) remoteEditorArgs(s session, place string) ([]string, string) {
	cwd := s.CWD
	if !s.Alive {
		cwd = ""
	}
	label := m.editorLabel
	if label == "" {
		label = defaultEditorTitle(s)
	}
	args := []string{"editor-remote", s.Server, cwd}
	if editor := m.hostTool(s.Server, "editor"); editor != "" {
		args = append(args, "--editor", editor)
	}
	args = append(args, "--label", label, "--place", place)
	return args, "opened the project in the editor on " + s.Server
}

// remoteToolArgs is what sh2pil-open needs to run one directory tool on another host.  The
// tool is the host's own: the reader's per-host setting names one when they want a specific
// command there, and otherwise the helper chooses the host's default, so the tool this
// machine is configured with never travels.  The directory is the one that host recorded, and
// an empty one is a directory the host has reported as gone: the tool then starts in that
// host's home directory, which is what an empty directory means to the helper.  The label is
// left to sh2pil-open, which names the tool and the directory it opens in, the same way the
// local verbs are named.
// runToolArgs is the command that runs a configured directory tool in a terminal.  The tool
// is a setting, so jj or another file browser works without a code change; the first word is
// the command and the rest are its arguments.
func (m *model) runToolArgs(tool, fallback, cwd, place string) ([]string, string) {
	fields := strings.Fields(tool)
	if len(fields) == 0 {
		fields = strings.Fields(fallback)
	}
	if len(fields) == 0 {
		return nil, "the configured tool is empty"
	}
	name := filepath.Base(fields[0])
	args := []string{"run-tool", "--label", name + " " + filepath.Base(cwd),
		"--place", place, cwd}
	args = append(args, fields...)
	return args, "opened " + name + " in " + filepath.Base(cwd)
}

// firstField is the command name of a configured tool, for a host that may have its own
// arguments for it: only the name travels.
func firstField(value, fallback string) string {
	if fields := strings.Fields(value); len(fields) > 0 {
		return fields[0]
	}
	return fallback
}

func (m *model) remoteToolArgs(action string, s session, place string) ([]string, string) {
	kind, description := "file_browser", "the file browser"
	if action == "lazygit" {
		kind, description = "git_tool", "the git tool"
	}
	cwd := s.CWD
	if !s.Alive {
		cwd = ""
	}
	project := filepath.Base(cwd)
	if project == "" || project == "." {
		project = s.Project
	}
	args := []string{"tool-remote", s.Server, cwd}
	tool := m.hostTool(s.Server, kind)
	if tool != "" {
		args = append(args, tool)
		description = tool
	} else {
		args = append(args, "--kind", kind)
	}
	args = append(args, "--place", place)
	return args, "opened " + description + " in " + project + " on " + s.Server
}

// hostTool returns the tool the reader set for one host, or "" when the host keeps its own
// default.  The kind is a tools key: editor, file_browser, or git_tool.
func (m *model) hostTool(host, kind string) string {
	if host == "" || m.toolHosts == nil {
		return ""
	}
	return strings.TrimSpace(m.toolHosts[host][kind])
}

func actionName(key string) string {
	switch key {
	case "enter":
		return "resume"
	case "ctrl+a":
		return "new"
	case "ctrl+t":
		return "window"
	case "ctrl+x":
		return "shell"
	case "ctrl+g":
		return "lazygit"
	case "alt+f":
		return "yazi"
	case "alt+e":
		return "project-editor"
	case "ctrl+f":
		return "fork"
	}
	return ""
}

// needsLiveGuard reports whether an action knowingly starts a second writer.
//
// Opening a session does not: it switches to the terminal that already runs it, and only
// resumes when nothing runs.  Asking for a new window is the one action that adds a writer
// on purpose, so it is the one that asks.  A new session writes a transcript of its own, so
// it adds no second writer to the picked one and has nothing to ask about.
func needsLiveGuard(action string) bool {
	return action == "window"
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

func trim(text string, limit int) string {
	if limit < 1 {
		return ""
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "…"
}
