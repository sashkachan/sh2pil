package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Colours use the small ANSI palette indexes (6 cyan, 2 green, 3 yellow, 1 red, 8 dim
// grey) so the terminal theme keeps deciding the look, with an adaptive selected-row
// background that stays subtle on light terminals and clear on dark terminals.
var (
	dimSty    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accentSty = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	liveSty   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	// A chat that is waiting on a person is the one row a reader must not have to look for, so it
	// is the only state the picker colours apart from the rest.
	waitSty  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	warnSty  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	goneSty  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	matchSty = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	// filterSty marks the query that is narrowing the list, so an active filter is not taken
	// for an empty target.  The background is adaptive, so the chip reads on light and dark.
	filterSty = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("6")).
			Background(lipgloss.AdaptiveColor{Light: "#d0d0d0", Dark: "#3a3a3a"})
	titleSty = lipgloss.NewStyle().Bold(true)
	// sectionSty heads a group of rows in a menu: the command palette's own headings.
	sectionSty = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	// `sh2pil` sets COLORFGBG from system appearance before Bubble Tea starts, so
	// Lip Gloss can select the right background without an OSC terminal query.
	selSty = lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#e8e8e8", Dark: "#454545"}).
		Bold(true)
	paneSty = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8"))
)

const (
	ageW       = 4
	projW      = 20
	sizeW      = 6
	harnessW   = 8  // the store a row comes from: opencode is the longest name
	zmxW       = 16 // the zmx session name, the handle an attach uses
	serverMaxW = 28 // cap a host name in the target bar so long destinations do not crowd it
	stateW     = 9  // what a chat is doing: "needs you" is the longest word the column says
	zmxTag     = "⚡"
	zmxTagW    = 2 // the mark for a session in a zmx session: a terminal draws it in two cells
	gutterW    = 2 // selection marker plus a space
)

// ---- geometry ---------------------------------------------------------------

func (m *model) chromeLines() int { return 4 } // target bar, header, status, help

// paneHeight is the content height of the left column, borders aside.  The preview pane and
// the two boxes of the left column fill the same area.
func (m *model) paneHeight() int {
	height := m.height - m.chromeLines() - 2 // two border rows
	if height < 3 {
		height = 3
	}
	return height
}

// minProjectsRows is the least the project box may show.  The two boxes of the column fill it
// exactly, so this is the floor the zmx box yields to.
const minProjectsRows = 3

// zmxPaneRows is how many zmx sessions the box under the project list shows, or 0 when there
// are none, or when the column is too short to give both boxes their share and the project
// groups keep the whole column.  It never takes more than a third of the column, so the project
// groups stay the main list, and a window that shrinks takes rows from this box as well instead
// of squeezing the project list down to its floor on its own.
func (m *model) zmxPaneRows() int {
	rows := len(m.zmxRows)
	if rows == 0 {
		return 0
	}
	// The zmx box spends one line on its header and two on its border; what is left of the
	// column must still leave the project box its own floor.
	limit := min((m.paneHeight()-2)/3, m.paneHeight()-2-minProjectsRows-1)
	if limit < 1 {
		return 0
	}
	return min(rows, limit)
}

// zmxPaneHeight is the content height of the zmx box -- its header line and the rows it shows
// -- or 0 when the box is hidden.
func (m *model) zmxPaneHeight() int {
	rows := m.zmxPaneRows()
	if rows == 0 {
		return 0
	}
	return rows + 1
}

// projectsPaneHeight is the content height of the project box: what is left of the column
// once the zmx box and its own border have taken their share.  zmxPaneRows leaves at least
// minProjectsRows, so the two boxes always add up to the column exactly.
func (m *model) projectsPaneHeight() int {
	height := m.paneHeight() - m.zmxPaneHeight()
	if m.zmxPaneHeight() > 0 {
		height -= 2 // the zmx box's border
	}
	return max(minProjectsRows, height)
}

// listHeight is the number of rows the list that owns the cursor can show, which is what page
// movements and the scroll follow.
func (m *model) listHeight() int {
	if m.view == viewZmx {
		if height := m.zmxPaneHeight(); height > 0 {
			return height - 1 // the box header line
		}
		return 0
	}
	return m.projectsPaneHeight()
}

// paneWidths splits the window between the list column and the preview pane.  The list takes a
// little over half, and the preview keeps at least 20 columns while there is room for both.  A
// window too narrow for that gives the preview its floor and the list the remainder, so the two
// boxes never add up to more columns than the terminal has: a frame wider than the window wraps
// and pushes every line below it down.
// minPaneWidth is the narrowest either pane is drawn; below it the columns are simply clipped.
const minPaneWidth = 8

// paneWidths splits the window between the list column and the preview pane.  The list takes a
// little over half and the preview the rest, but never less than minPaneWidth each, and the two
// always add up to exactly the window: a frame one column too wide makes the terminal wrap it,
// which shifts every line below and looks like the layout falling apart.
func (m *model) paneWidths() (int, int) {
	content := m.width - 4 // one border column per side, per pane
	if content < 2*minPaneWidth {
		list := max(1, content/2)
		return list, max(1, content-list)
	}
	list := (m.width * 55 / 100) - 2
	if list < minPaneWidth {
		list = minPaneWidth
	}
	preview := content - list
	if preview < minPaneWidth {
		preview = minPaneWidth
		list = content - preview
	}
	return list, preview
}

// listWidth is the width of the left column.  With the preview hidden the column takes the whole
// window, whatever split it would have used beside it.
func (m *model) listWidth() int {
	if !m.showPrev {
		return max(1, m.width-2)
	}
	list, _ := m.paneWidths()
	return list
}

// previewWidth is the width of the preview pane.  It stays a real width while the pane is hidden,
// because a transcript is still wrapped for the pane it will come back to.
func (m *model) previewWidth() int {
	_, preview := m.paneWidths()
	return preview
}

// ---- target state -----------------------------------------------------------

// currentTarget is the destination on screen.  A model built without targets -- a test, or a
// window that arrives before Init -- reads this machine.
func (m *model) currentTarget() target {
	if m.current < 0 || m.current >= len(m.targets) {
		return target{}
	}
	return m.targets[m.current]
}

func (m *model) targetLabel() string { return m.currentTarget().label() }

// switchTarget moves one step along the target bar.  The walk wraps, so one key pair reaches
// every target, and a single target says why it cannot move.
func (m *model) switchTarget(delta int) tea.Cmd {
	if len(m.targets) < 2 {
		m.status = "only this machine is configured; add hosts to zmx_servers"
		return nil
	}
	return m.showTarget((m.current + delta + len(m.targets)) % len(m.targets))
}

// switchTargetAt moves to the target the number names, counting from one in the order the target
// bar prints: 1 is this machine, and each configured host follows in the order zmx_servers lists
// them.  The number is the one the bar shows, so a key reaches a host without walking past every
// target before it.  A number the bar does not print is no move and says so, rather than landing
// the reader on a target that is not on screen.
func (m *model) switchTargetAt(number int) tea.Cmd {
	if number < 1 || number > len(m.targets) {
		if len(m.targets) < 2 {
			m.status = "only this machine is configured; add hosts to zmx_servers"
		} else {
			m.status = fmt.Sprintf("no target %d: %s", number, plural(len(m.targets), "target"))
		}
		return nil
	}
	if number-1 == m.current {
		// Already here: the target stays as it is.  A reload would drop the cursor and the
		// search the reader is working in, and ctrl+r is the key that reads a target again.
		return nil
	}
	return m.showTarget(number - 1)
}

// showTarget puts a target on screen and schedules its read.  The delay is the point of the
// message: a reader who walks the targets starts one read, not one per switch, and the rows of a
// target that has already answered are on screen before the read even begins.
func (m *model) showTarget(index int) tea.Cmd {
	m.current = index
	m.generation++
	*m.cursorPtr(), *m.offsetPtr() = 0, 0
	m.rebuildRows()
	// A pane the new target cannot fill gives the cursor back to the project list.
	if m.zmxPaneHeight() == 0 {
		m.view = viewSessions
	}
	m.preview, m.wrapped, m.wrapID = preview{}, nil, ""
	target := m.currentTarget()
	if !target.local() && !m.data[target.label()].Connected {
		m.status = "connecting to " + target.Server + "…"
	} else {
		m.status = ""
	}
	return tea.Tick(targetRefreshDelay, func(time.Time) tea.Msg {
		return targetRefreshTickMsg{label: target.label(), generation: m.generation}
	})
}

// paneSpot is one pane of the cursor cycle: the column it lives in, and, for a list, which
// list it shows.
type paneSpot struct {
	focus int
	view  string
}

// panesInOrder lists the panes as they are drawn: the project list, the zmx list under it when
// the target has one, and the transcript preview when it is shown.
func (m *model) panesInOrder() []paneSpot {
	panes := []paneSpot{{focus: paneFocusList, view: viewSessions}}
	if m.zmxPaneHeight() > 0 {
		panes = append(panes, paneSpot{focus: paneFocusList, view: viewZmx})
	}
	if m.showPrev {
		panes = append(panes, paneSpot{focus: paneFocusPreview})
	}
	return panes
}

// cyclePane moves the cursor to the next or the previous pane and wraps, so one key pair
// reaches every pane with no memory of which column it is in.  Each list keeps its own cursor
// and its own search, so this is a move and not a reset, and a pane the target cannot fill is
// skipped.
func (m *model) cyclePane(step int) (tea.Model, tea.Cmd) {
	panes := m.panesInOrder()
	at := 0
	for index, pane := range panes {
		if pane.focus == m.focus && (pane.focus == paneFocusPreview || pane.view == m.view) {
			at = index
			break
		}
	}
	next := panes[((at+step)%len(panes)+len(panes))%len(panes)]
	if next.focus == paneFocusPreview {
		m.focus = paneFocusPreview
		m.status = "transcript preview"
		return m, nil
	}
	if next.view != m.view {
		return m.switchList(next.view)
	}
	m.focus = paneFocusList
	m.status = ""
	return m, nil
}

// switchList moves the cursor between the two panes of the left column.  Each pane keeps its
// own cursor and its own search, so this is a move and not a reset.
func (m *model) switchList(next string) (tea.Model, tea.Cmd) {
	if next == m.view {
		return m, nil
	}
	if next == viewZmx && m.zmxPaneHeight() == 0 {
		m.status = "this target has no zmx sessions"
		return m, nil
	}
	m.view = next
	m.focus = paneFocusList
	if next == viewZmx {
		m.status = "zmx sessions"
	} else {
		m.status = "projects and sessions"
	}
	return m, m.refreshPreview()
}

// mergeTarget adopts one target's read.  A read that failed keeps the rows that are already
// there: a row listed a moment ago is more useful than an empty list, and the status line
// carries the reason.  A label that is not on screen is cached and nothing else happens.
func (m *model) mergeTarget(msg targetMsg) {
	data := msg.data
	previous, known := m.data[msg.label]
	if data.Err != "" {
		if known {
			previous.Err, previous.Note = data.Err, data.Note
			m.data[msg.label] = previous
		} else {
			m.data[msg.label] = data
		}
		if msg.label == m.targetLabel() {
			m.status = data.Err
		}
		m.rebuildRows()
		return
	}
	if data.Note != "" {
		m.status = data.Note
	} else if m.status == "reloading" {
		m.status = ""
	}
	m.data[msg.label] = data
	m.rebuildRows()
}

func (m *model) markConnected(label string) {
	data := m.data[label]
	data.Connected, data.Err = true, ""
	m.data[label] = data
}

func (m *model) markDisconnected(label string) {
	data := m.data[label]
	data.Connected = false
	m.data[label] = data
}

func (m *model) setTargetError(label, note string) {
	data := m.data[label]
	data.Connected, data.Err = false, note
	m.data[label] = data
	if label == m.targetLabel() {
		m.status = note
	}
	m.rebuildRows()
}

// toggleGroupAtCursor folds or unfolds the group the cursor is in.  On a header that is the
// header itself; on a session under one it is the nearest header above, because a session row
// carries the directory it runs in and cannot name the group it was grouped under.
func (m *model) toggleGroupAtCursor() {
	if m.view == viewZmx {
		m.status = "the zmx sessions are not grouped; [ and ] move between the panes"
		return
	}
	rows := m.filtered()
	at := *m.cursorPtr()
	if at >= len(rows) {
		at = len(rows) - 1
	}
	for ; at >= 0; at-- {
		if rows[at].ProjectOnly {
			m.toggleGroup(rows[at])
			return
		}
	}
	m.status = "no project group here"
}

// toggleGroup opens or closes one project group and says so.  The state is kept per target and
// per path, so a reload and a revisit both find the groups as they were left.
func (m *model) toggleGroup(header session) {
	if m.expanded == nil {
		m.expanded = map[string]bool{}
	}
	key := m.expansionKey(header.CWD, header.Project)
	m.expanded[key] = !m.expanded[key]
	m.rebuildRows()
	if m.expanded[key] {
		m.status = "opened " + trim(header.Project, 30)
	} else {
		m.status = "closed " + trim(header.Project, 30)
	}
}

// ---- rows -------------------------------------------------------------------

// rebuildRows materializes the rows of the two panes for the target on screen: one header per
// project group, that group's sessions under it when it is open, and the target's zmx
// sessions.  It is the one place that turns target data into rows, so every change that can
// alter what is shown -- a read, an expansion, a target switch, a query, the ignored toggle --
// calls it, and the accessors stay cheap.
func (m *model) rebuildRows() {
	data := m.data[m.targetLabel()]
	rows := make([]session, 0, len(data.Groups))
	groups, sessions, inZmx, live, waiting := 0, 0, 0, 0, 0
	for _, group := range data.Groups {
		if group.Ignored != m.showIgnored {
			continue
		}
		matched, found := m.matchGroup(group)
		if !matched && len(found) == 0 {
			continue
		}
		// The toggle keeps only the rows a window already shows, so a group with none of them is
		// not part of this list at all, and the counts below say what the list holds.
		found = m.visibleSessions(found)
		if m.onlyShown && len(found) == 0 {
			continue
		}
		if !matched && len(found) == 0 {
			continue
		}
		// The rows that wait on a person come first inside their group, and the project list is
		// the only list ranked: a group's own order, and the zmx pane's, are left alone.
		found = m.rankRows(found)
		groups++
		beforeGroup := waiting
		for _, row := range found {
			sessions++
			if row.ZmxName != "" {
				inZmx++
			}
			if info, running := m.rowLive(row); running {
				live++
				if info.State == "blocked" {
					waiting++
				}
			}
		}
		expanded := m.groupExpanded(group, len(found))
		header := groupRow(group, expanded)
		// A closed group hides every session under it, and a group starts closed, so the count a
		// person has to answer belongs on the header too: otherwise the one row worth finding is
		// only findable by opening every project in turn.
		header.Waiting = waiting - beforeGroup
		if m.onlyShown {
			// The header counts what the toggle left, so a count never promises a row that is not
			// in the list.
			header.Count = len(found)
		}
		rows = append(rows, header)
		if !expanded {
			continue
		}
		for _, row := range found {
			row.Depth = 1
			rows = append(rows, row)
		}
	}
	m.sessions = rows
	m.zmxRows = m.visibleZmx(m.matchZmx(data.Zmx))
	m.groupCount, m.sessionCount = groups, sessions
	m.inZmxCount, m.liveCount, m.waitingCount = inZmx, live, waiting
	m.clampCursor()
}

// visibleSessions keeps the sessions a window already shows, when the toggle is on: those are
// the rows the reader can go back to.  A row on another host has no owner to judge by, so it is
// not one of them.
func (m *model) visibleSessions(rows []session) []session {
	if !m.onlyShown {
		return rows
	}
	visible := make([]session, 0, len(rows))
	for _, row := range rows {
		if !m.shown(row) {
			continue
		}
		visible = append(visible, row)
	}
	return visible
}

// visibleZmx keeps the zmx sessions a terminal is attached to, when the toggle is on, which is
// the same thing as a window showing the chat.
func (m *model) visibleZmx(rows []session) []session {
	if !m.onlyShown {
		return rows
	}
	visible := make([]session, 0, len(rows))
	for _, row := range rows {
		if row.Clients == 0 {
			continue
		}
		visible = append(visible, row)
	}
	return visible
}

// zmxPaneKeepsRows reports whether the zmx pane would have rows with its own query empty,
// which is what says whether the pane is there at all.  A query that matched nothing is not
// the pane going away, so it is not asked about here.
func (m *model) zmxPaneKeepsRows() bool {
	rows := m.data[m.targetLabel()].Zmx
	if !m.onlyShown {
		return len(rows) > 0
	}
	for _, row := range rows {
		if row.Clients > 0 {
			return true
		}
	}
	return false
}

// matchGroup applies the project pane's search to one group: whether the project itself
// matches, and which of its sessions do.  An empty query keeps everything.
func (m *model) matchGroup(g group) (bool, []session) {
	query := strings.TrimSpace(m.query)
	if query == "" {
		return true, g.Sessions
	}
	found := make([]session, 0, len(g.Sessions))
	for _, row := range g.Sessions {
		if ok, _ := matchesRow(query, row); ok {
			found = append(found, row)
		}
	}
	return matchProject(query, g.Project, g.CWD), found
}

// matchProject reports whether every term of a query lands in the project's label or its
// path, which is the same rule matchesRow applies to a session.
func matchProject(query, project, cwd string) bool {
	for _, term := range strings.Fields(query) {
		if ok, _ := matchCell(term, project); ok {
			continue
		}
		if ok, _ := matchCell(term, cwd); ok {
			continue
		}
		return false
	}
	return true
}

// matchZmx applies the zmx pane's own search, which never touches the project pane's query.
func (m *model) matchZmx(rows []session) []session {
	query := strings.TrimSpace(m.zmxQuery)
	if query == "" {
		return rows
	}
	out := make([]session, 0, len(rows))
	for _, row := range rows {
		if ok, _ := matchesRow(query, row); ok {
			out = append(out, row)
		}
	}
	return out
}

// groupRow is the header of one group: a project row that says whether its sessions are open
// and how many it holds.
func groupRow(g group, expanded bool) session {
	return session{Project: g.Project, CWD: g.CWD, Alive: true, ProjectOnly: true,
		Ignored: g.Ignored, Expanded: expanded, Count: len(g.Sessions), Server: g.Server}
}

// groupExpanded reports whether a group's sessions are shown: the reader opened it, or the
// search found something inside it.  The reader's own state is never overwritten, so a query
// that ends puts the list back the way it was.
func (m *model) groupExpanded(g group, matched int) bool {
	if m.expanded[m.expansionKey(g.CWD, g.Project)] {
		return true
	}
	if m.onlyShown {
		// The toggle leaves only the rows worth looking at, so a group that survived it opens.
		return matched > 0
	}
	return strings.TrimSpace(m.query) != "" && matched > 0
}

// expansionKey identifies one group in the expansion state.  The target is part of it, so a
// group opened on one host does not open its namesake on another, and the path is preferred
// to the label because a label alone is not unique.
func (m *model) expansionKey(cwd, project string) string {
	key := project
	if cwd != "" {
		key = filepath.Clean(cwd)
	}
	return m.targetLabel() + "\x00" + key
}

// ---- navigation -------------------------------------------------------------

// filtered returns the rows of the list that owns the cursor.  rebuildRows has already applied
// the grouping, the expansion, and the search, so this is a plain accessor and the action path
// and the render path read the same rows.
func (m *model) filtered() []session {
	if m.view == viewZmx {
		return m.zmxRows
	}
	return m.sessions
}

// cursorPtr, offsetPtr, queryPtr and queryCursorPtr point at the state of the list that owns
// the cursor.  Each pane keeps its own, so moving between them does not lose a position.
func (m *model) cursorPtr() *int {
	if m.view == viewZmx {
		return &m.zmxCursor
	}
	return &m.cursor
}

func (m *model) offsetPtr() *int {
	if m.view == viewZmx {
		return &m.zmxOffset
	}
	return &m.offset
}

func (m *model) queryPtr() *string {
	if m.view == viewZmx {
		return &m.zmxQuery
	}
	return &m.query
}

func (m *model) queryCursorPtr() *int {
	if m.view == viewZmx {
		return &m.zmxQueryCursor
	}
	return &m.queryCursor
}

// resetCursor puts the cursor of the list that owns it back at the top and drops that list's
// filter, which is what a change of what the list shows does.
func (m *model) resetCursor() {
	*m.cursorPtr(), *m.offsetPtr() = 0, 0
	*m.queryPtr(), *m.queryCursorPtr() = "", 0
	m.rebuildRows()
}

func (m *model) selected() session {
	rows := m.filtered()
	if len(rows) == 0 {
		return session{}
	}
	cursor := *m.cursorPtr()
	if cursor >= len(rows) {
		return rows[len(rows)-1]
	}
	if cursor < 0 {
		return rows[0]
	}
	return rows[cursor]
}

func (m *model) selectedID() string { return m.selected().ID }

func (m *model) clampCursor() {
	cursor := m.cursorPtr()
	rows := len(m.filtered())
	switch {
	case rows == 0:
		*cursor = 0
	case *cursor >= rows:
		*cursor = rows - 1
	case *cursor < 0:
		*cursor = 0
	}
	// A pane that is not there gives the cursor back to the project list, so the selection never
	// points at a pane that is not drawn: the target has no zmx sessions at all, or the toggle
	// keeps only the attached ones and none is attached.
	if m.view == viewZmx && m.zmxPaneHeight() == 0 && !m.zmxPaneKeepsRows() {
		m.view = viewSessions
		m.clampCursor()
		return
	}
	m.scrollToCursor()
	// A row is visited when the cursor arrives on it, not merely because it is under the cursor
	// while a read rebuilds the list: this is what clears an unread mark, so a poll must not.
	if id := m.selected().ID; id != m.visited {
		m.visited = id
		delete(m.unread, id)
	}
}

func (m *model) scrollToCursor() {
	cursor, offset := m.cursorPtr(), m.offsetPtr()
	height := m.listHeight()
	if height < 1 {
		height = 1
	}
	if *cursor < *offset {
		*offset = *cursor
	}
	if *cursor >= *offset+height {
		*offset = *cursor - height + 1
	}
	if *offset < 0 {
		*offset = 0
	}
}

func (m *model) move(delta int) (tea.Model, tea.Cmd) {
	*m.cursorPtr() += delta
	m.clampCursor()
	return m, m.refreshPreview()
}

// maxPreviewScroll is the number of lines above the last screen of preview text.
// previewBodyHeight is the number of lines the preview spends on transcript text, after its
// two header lines.
func (m *model) previewBodyHeight() int {
	height := m.paneHeight() - 2
	if height < 1 {
		return 1
	}
	return height
}

// previewWrapped returns the transcript as lines ready for the pane, and remembers the
// result so that scrolling or repainting does not render the same text again.
//
// The transcript is markdown, so the pane renders it: headings, lists, and fenced code come
// out styled, and the word wrap that glamour applies is what the scroll positions count in.
func (m *model) previewWrapped() []string {
	width := m.previewWidth()
	if m.wrapID == m.preview.id && m.wrapWidth == width && m.wrapped != nil {
		return m.wrapped
	}
	// One column of slack: glamour wraps to this width and the pane still has its border.
	rendered := m.preview.lines
	if m.preview.width != width-1 && m.preview.source != nil {
		// The pane changed width, so the word wrap changed with it and the lines are
		// re-rendered from the markdown, not from the already styled text.
		rendered = renderMarkdown(strings.Join(m.preview.source, "\n"), width-1)
		m.preview.lines, m.preview.width = rendered, width-1
	}
	if rendered != nil {
		rendered = append(rendered, "")
	}
	m.wrapped, m.wrapID, m.wrapWidth = rendered, m.preview.id, width
	return rendered
}

func (m *model) previewScrollLimit() int {
	selected := m.selected()
	lineCount := 0
	switch {
	case selected.ProjectOnly:
		if m.projectFilesPath == selected.CWD && m.projectFilesErr == "" {
			lineCount = len(m.projectFiles)
		}
	case selected.ZmxOnly && selected.File == "":
		// The session's own scrollback scrolls; the header above it does not.
		lineCount = len(m.zmxHistory)
	default:
		lineCount = len(m.previewWrapped())
	}
	return max(0, lineCount-m.previewBodyHeight())
}

func (m *model) clampPreviewScroll() {
	limit := m.previewScrollLimit()
	if m.preview.scroll > limit {
		m.preview.scroll = limit
	}
	if m.preview.scroll < 0 {
		m.preview.scroll = 0
	}
}

// refreshPreview starts the timer that reads a transcript once movement stops.  A read that
// is already in flight, or a tick that arrives late, is dropped by the id it carries.
func (m *model) refreshPreview() tea.Cmd {
	selected := m.selected()
	if selected.Server != "" {
		// A remote row is read through its own host, so the pane shows the same tail a local
		// row would.  A zmx row that carries no chat has nothing to read there: its pane
		// explains what the session is instead.
		if selected.ID == "" || !m.showPrev {
			return nil
		}
		return m.schedulePreview(previewDelayDebounce)
	}
	if selected.ZmxOnly && selected.File == "" {
		if !m.showPrev {
			return nil
		}
		// A session with no chat has no transcript to read.  Its own scrollback is the picture
		// of it, and a read that is already in the pane for this session needs no repeat.
		if m.zmxHistoryName == selected.ZmxName && m.zmxHistoryErr == "" {
			return nil
		}
		return tea.Tick(previewDelayDebounce,
			func(time.Time) tea.Msg { return zmxHistoryTickMsg{name: selected.ZmxName} })
	}
	if selected.ProjectOnly {
		if !m.showPrev || (m.projectFilesPath == selected.CWD && m.projectFilesErr == "") {
			return nil
		}
		m.preview.scroll = 0
		return tea.Tick(previewDelayDebounce,
			func(time.Time) tea.Msg { return projectFilesTickMsg{path: selected.CWD} })
	}
	return m.schedulePreview(previewDelayDebounce)
}

// usePreview adopts a freshly read transcript and shows its end, which is where a reader
// starts before scrolling back.
//
// The preview helpers mutate through a pointer and return nothing on purpose: a call inside
// `return m, m.helper()` would evaluate the model before the call, so the change would be
// thrown away with the temporary copy.
func (m *model) usePreview(p preview) {
	m.preview = p
	m.wrapped, m.wrapID, m.wrapWidth = nil, "", 0
	m.preview.scroll = 0
	m.clampPreviewScroll()
	m.preview.scroll = m.maxPreviewScroll()
}

func (m *model) maxPreviewScroll() int { return m.previewScrollLimit() }

func (m *model) scrollPreview(lines int) {
	m.preview.scroll += lines
	m.clampPreviewScroll()
}

func (m *model) scrollPreviewTo(line int) {
	m.preview.scroll = line
	m.clampPreviewScroll()
}

func (m *model) scrollPreviewToBottom() {
	m.preview.scroll = m.maxPreviewScroll()
	m.clampPreviewScroll()
}

// ---- text fitting -----------------------------------------------------------

// runeWidth estimates how many terminal columns a rune takes: two for the wide ranges
// (CJK, most emoji), none for combining marks.  The Python helpers have the same function
// for the same reason: a session name with an emoji must not shift a column.
func runeWidth(r rune) int {
	switch {
	case r == 0 || r < 32:
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && (r <= 0x115F || r == 0x2329 || r == 0x232A ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) || (r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE30 && r <= 0xFE6F) || (r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) || (r >= 0x1F300 && r <= 0x1FAFF) ||
		(r >= 0x20000 && r <= 0x3FFFD)),
		wideEmoji(r):
		return 2
	}
	return 1
}

// wideEmoji reports the marks a terminal draws in two cells although the East Asian table
// alone would not say so: the ones Unicode gives an emoji presentation by default, like the
// lightning bolt that marks a zmx session.  The list is the emoji-presentation set of
// unicode.org/Public/emoji outside the wide blocks already listed above, and this check and
// the terminal's own table must agree, or a row shifts by one column.
func wideEmoji(r rune) bool {
	switch {
	case r >= 0x231A && r <= 0x231B, r >= 0x23E9 && r <= 0x23EC, r == 0x23F0,
		r == 0x23F3, r >= 0x25FD && r <= 0x25FE, r >= 0x2614 && r <= 0x2615,
		r >= 0x2648 && r <= 0x2653, r == 0x267F, r == 0x2693, r == 0x26A1,
		r >= 0x26AA && r <= 0x26AB, r >= 0x26BD && r <= 0x26BE,
		r >= 0x26C4 && r <= 0x26C5, r == 0x26CE, r == 0x26D4, r == 0x26EA,
		r >= 0x26F2 && r <= 0x26F3, r == 0x26F5, r == 0x26FA, r == 0x26FD,
		r == 0x2705, r >= 0x270A && r <= 0x270B, r == 0x2728, r == 0x274C,
		r == 0x274E, r >= 0x2753 && r <= 0x2755, r == 0x2757,
		r >= 0x2795 && r <= 0x2797, r == 0x27B0, r == 0x27BF,
		r >= 0x2B1B && r <= 0x2B1C, r == 0x2B50, r == 0x2B55,
		r == 0x1F004, r == 0x1F0CF, r == 0x1F18E, r >= 0x1F191 && r <= 0x1F19A,
		r >= 0x1F1E6 && r <= 0x1F1FF, r == 0x1F201, r == 0x1F21A, r == 0x1F22F,
		r >= 0x1F232 && r <= 0x1F236, r >= 0x1F238 && r <= 0x1F23A,
		r >= 0x1F250 && r <= 0x1F251:
		return true
	}
	return false
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeWidth(r)
	}
	return width
}

// fit pads or clips plain text to exactly width columns.
func fit(text string, width int) string {
	if displayWidth(text) > width {
		clipped := make([]rune, 0, width)
		used := 0
		for _, r := range text {
			if used+runeWidth(r) > width-1 {
				break
			}
			clipped = append(clipped, r)
			used += runeWidth(r)
		}
		text = string(clipped) + "…"
	}
	return text + strings.Repeat(" ", max(0, width-displayWidth(text)))
}

func fitRight(text string, width int) string {
	if displayWidth(text) >= width {
		return fit(text, width)
	}
	return strings.Repeat(" ", width-displayWidth(text)) + text
}

// wrap breaks one line into display lines of at most width columns, on spaces when it can.
func wrap(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	if displayWidth(text) <= width {
		return []string{text}
	}
	var lines []string
	current := ""
	for _, word := range strings.Fields(text) {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if displayWidth(candidate) <= width {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
		}
		if displayWidth(word) > width {
			lines = append(lines, fit(word, width))
			current = ""
			continue
		}
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// highlight marks the runes a query landed on, so the reason a row matches is visible.
func highlight(text string, marks []int) string {
	if len(marks) == 0 {
		return text
	}
	wanted := make(map[int]bool, len(marks))
	for _, index := range marks {
		wanted[index] = true
	}
	var out strings.Builder
	for index, r := range []rune(text) {
		if wanted[index] {
			out.WriteString(matchSty.Render(string(r)))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// ---- views ------------------------------------------------------------------

// targetBar names the destinations the picker can read and marks the one on screen, so the
// scope of the list below it is never in doubt.  A target that has not answered is dim, a
// target whose read failed carries a cross, and the active one is filled.  Each name carries
// its number, which is the key that reaches it (alt+1 is this machine), so the bar is where the
// reader learns which number is which host.
func (m *model) targetBar() string {
	parts := make([]string, 0, len(m.targets))
	for index, t := range m.targets {
		data := m.data[t.label()]
		label := fmt.Sprintf("%d %s", index+1, t.label())
		text := "  " + label
		switch {
		case index == m.current:
			text = "▸ " + label
		case data.Err != "":
			text = "✗ " + label
		}
		switch {
		case index == m.current:
			parts = append(parts, selSty.Render(text))
		case data.Err != "":
			parts = append(parts, goneSty.Render(text))
		default:
			parts = append(parts, dimSty.Render(text))
		}
	}
	hint := dimSty.Render("{ } targets · alt+1…9")
	bar := " " + strings.Join(parts, " ")
	gap := m.width - displayWidth(ansi.Strip(bar)) - displayWidth(ansi.Strip(hint)) - 1
	if gap < 1 {
		return bar
	}
	return bar + strings.Repeat(" ", gap) + hint
}

// headerView counts what the target on screen holds.  The counts belong to the target the target
// bar named above them, and they say what it holds rather than what is open: a collapsed list
// must not read as an empty machine.  The stores this target is read for come first, so which
// machine answered for which store is never in doubt.
func (m *model) headerView() string {
	attached := 0
	for _, row := range m.zmxRows {
		if row.Clients > 0 {
			attached++
		}
	}
	title := m.harnessFlags()
	stats := fmt.Sprintf("%s · %s", plural(m.groupCount, "group"),
		plural(m.sessionCount, "session"))
	if m.view == viewZmx {
		title = titleSty.Render("zmx sessions")
		stats = plural(len(m.zmxRows), "zmx session")
	}
	if m.inZmxCount > 0 {
		stats += " · " + accentSty.Render(fmt.Sprintf("%d in zmx", m.inZmxCount))
	}
	if attached > 0 {
		stats += " · " + liveSty.Render(fmt.Sprintf("%d attached", attached))
	}
	if m.waitingCount > 0 {
		stats += " · " + waitSty.Render(fmt.Sprintf("%d waiting on you", m.waitingCount))
	}
	if m.liveCount > 0 {
		stats += " · " + liveSty.Render(fmt.Sprintf("%d live", m.liveCount))
	}
	if query := strings.TrimSpace(*m.queryPtr()); query != "" {
		stats += " · /" + accentSty.Render(trim(query, 30))
	}
	if m.showIgnored {
		stats += " · " + dimSty.Render("ignored")
	}
	left := titleSty.Render(title)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(stats) - 2
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + stats
}

// harnessMark reports what the target on screen said about one store: whether it answered at
// all, and whether it has that store.  An unanswered store is not an absent one.
func (m *model) harnessMark(harness string) (answered, present bool) {
	for _, info := range m.data[m.targetLabel()].Harnesses {
		if info.Harness == harness {
			return true, info.Present
		}
	}
	return false, false
}

// harnessFlags names the stores the target on screen is read for, in the order the picker reads
// them.  A store in force for a new chat is bold, a store the target does not have is red, and a
// store the target did not answer about is dim: a read that failed is not the same claim as a
// store that is not installed, and only one of the two is worth acting on.
func (m *model) harnessFlags() string {
	parts := make([]string, 0, len(m.harnesses))
	for _, harness := range m.enabledHarnesses() {
		answered, present := m.harnessMark(harness)
		switch {
		case !answered:
			parts = append(parts, dimSty.Render(harness))
		case !present:
			parts = append(parts, goneSty.Render(harness))
		case harness == m.harness:
			parts = append(parts, titleSty.Render(harness))
		default:
			parts = append(parts, accentSty.Render(harness))
		}
	}
	return strings.Join(parts, " ")
}

// enabledHarnesses is the stores every target is read for: the configured set, or every known
// store when the config named none.  A picker handed no set reads the machine rather than
// nothing, which keeps a stray empty setting from opening an empty list.
func (m *model) enabledHarnesses() []string {
	if len(m.harnesses) == 0 {
		return knownHarnesses
	}
	return m.harnesses
}

// availableHarnesses returns the stores the target on screen can answer for: the configured set,
// narrowed to the stores that target reported.  A target that reported nothing leaves the
// configured set alone, because a read that failed is not evidence that a store is gone, and
// ctrl+b must never skip a store for a reason the machine never gave.
func (m *model) availableHarnesses() []string {
	configured := m.enabledHarnesses()
	reported := m.data[m.targetLabel()].Harnesses
	if len(reported) == 0 {
		return configured
	}
	present := map[string]bool{}
	for _, info := range reported {
		if info.Present {
			present[info.Harness] = true
		}
	}
	available := make([]string, 0, len(configured))
	for _, harness := range configured {
		if present[harness] {
			available = append(available, harness)
		}
	}
	if len(available) == 0 {
		return configured
	}
	return available
}

func (m *model) rowLive(s session) (liveInfo, bool) {
	if s.Server != "" {
		// A host answers liveness in its own session list, which the row carries, and its state in
		// the read that asks for it.  The two must not be confused: the cheap state read checks no
		// process on either machine, so only the host's own list may say a chat is running.
		info, known := m.remoteStates[remoteStateKey(s.Server, s.ID)]
		if !known {
			info = liveInfo{}
		}
		info.ID, info.Server = s.ID, s.Server
		return info, s.Live
	}
	info, live := m.live[s.ID]
	return info, live
}

// stateWords is what the state column says about one chat, and how it is coloured.  A chat that
// is waiting on a person is the one row a reader must not have to look for, so it carries the
// only word no other state uses.  A record written by an older extension carries no state, and
// the column then says only that the chat is live, which is what it meant before.
func stateWords(info liveInfo, live bool) (string, lipgloss.Style) {
	if !live {
		return fit("", stateW), dimSty
	}
	word, style := "live", liveSty
	switch info.State {
	case "":
		if info.Stale {
			word, style = "unknown", dimSty
		}
	case "tool":
		word = info.Detail
		if word == "" {
			word = "working"
		}
	case "blocked":
		word, style = "needs you", waitSty
	case "idle", "ended":
		word, style = info.State, dimSty
	default:
		word = info.State
	}
	return fit(word, stateW), style
}

// stateDetail is what the row column has no room for: the dialog a chat is blocked on and its
// own short label, or the state a stale record was last seen in.  The label is the dialog's own
// words about itself, never the content of a prompt.
func stateDetail(info liveInfo) string {
	if info.State == "blocked" {
		detail := strings.TrimSpace(info.Detail)
		if info.Label != "" {
			detail = strings.TrimSpace(detail + " " + info.Label)
		}
		if detail != "" {
			return " (" + detail + ")"
		}
	}
	if info.State == "" && info.Stale && info.LastState != "" {
		return " (last seen " + info.LastState + ")"
	}
	return ""
}

func (m *model) rowView(s session, marks map[string][]int, selected bool) string {
	width := m.listWidth()
	marker := " "
	switch {
	case selected:
		marker = "▌"
	case m.unread[s.ID]:
		// An unread mark takes the cell the selection marker would use, so a row keeps its width
		// and every column after it stays put: it says this chat started waiting since the reader
		// last looked at it.  The selected row does not need it, because the cursor on the row is
		// what clears it.
		marker = "•"
	}
	if s.ProjectOnly {
		// A group header carries the project, its directory, and what is under it, so a closed
		// group still says what it holds.
		arrow := "▸"
		if s.Expanded {
			arrow = "▾"
		}
		// The arrow alone marks a group header: a child session is indented, so a second mark
		// would only repeat it.  An ignored project keeps its mark, which no other row has.
		mark, label := "", s.Project
		if s.Ignored {
			mark, label = "◌ ", s.Project+"  (ignored)"
		}
		hold := "no sessions"
		if s.Count > 0 {
			hold = plural(s.Count, "session")
		}
		head := fmt.Sprintf("%s%s %s%s  %s  ·  %s", marker, arrow, mark, label, s.CWD, hold)
		// The header is the only row a closed group shows, and a group starts closed, so the count
		// a person has to answer belongs here as well: otherwise the one row worth finding is only
		// findable by opening every project in turn.  The mark is part of the plain line, so a
		// selected header carries it like any other row, and the head gives up exactly the width
		// of the mark rather than letting the row overflow.
		waitMark := ""
		if s.Waiting > 0 {
			waitMark = "  ·  • waiting"
		}
		headWidth := max(1, width-displayWidth(waitMark))
		line := fit(head, headWidth) + waitMark
		if selected {
			return selSty.Render(line)
		}
		if s.Ignored {
			return dimSty.Render(line)
		}
		if waitMark != "" {
			return accentSty.Render(fit(head, headWidth)) + waitSty.Render(waitMark)
		}
		return accentSty.Render(line)
	}
	if s.ZmxOnly {
		return m.zmxRowView(s, marks, selected, marker, width)
	}
	return m.sessionRowView(s, marks, selected, marker, width)
}

// zmxRowView draws one live zmx session: its state, its age, the chat it carries, its project,
// and the handle an attach uses, which is the name zmx knows and not the chat's own name.
func (m *model) zmxRowView(s session, marks map[string][]int, selected bool, marker string, width int) string {
	nameW := max(8, width-(gutterW+1+ageW+4+projW+2+zmxW))
	flag := "○"
	if s.Clients > 0 {
		flag = "●"
	}
	if s.Current {
		// zmx marks the session the caller runs inside with an arrow, which the picker does when
		// it is opened from inside a chat.
		flag = "→"
	}
	age := fitRight(ageOf(s), ageW)
	name := fit(s.Name, nameW)
	project := fit(s.Project, projW)
	handle := fit(s.ZmxName, zmxW)
	plain := fit(marker+flag+" "+age+"  "+name+"  "+project+"  "+handle, width)
	if selected {
		return selSty.Render(plain)
	}
	styledFlag := flag
	if s.Clients > 0 {
		styledFlag = liveSty.Render(flag)
	}
	return marker + styledFlag + " " + dimSty.Render(age) + "  " +
		highlight(name, marks["name"]) + "  " + dimSty.Render(project) + "  " +
		accentSty.Render(highlight(handle, marks["zmx"]))
}

// sessionRowView draws one session: the age, the name, the project when it shows, the state,
// the zmx mark, and the size at the right end.  A session under a group header is indented, and
// the project column is dropped there because the header above it already names the project.
func (m *model) sessionRowView(s session, marks map[string][]int, selected bool, marker string, width int) string {
	if s.Depth > 0 {
		marker += strings.Repeat("  ", s.Depth)
	}
	// The row spends its right end on the state, the zmx mark, the store the row came from, and
	// the size, and the rest on the age, the name, and the project.  The indent the nesting adds
	// belongs in the fixed width too, or the row overflows by exactly that much and its last
	// column is clipped away.
	//
	// One cell for the selection marker, the age, the gap that follows it, the gap between the
	// project and the marks, the live flag, the state word, the mark with the gaps between them,
	// the store, and the size.
	fixed := 1 + ageW + 2 + 2 + 1 + 1 + stateW + 1 + zmxTagW + 2 + harnessW + 2 + sizeW + 2*s.Depth
	project := ""
	if s.Depth == 0 {
		fixed += projW + 2
		project = s.Project
		if !s.Alive {
			project += " (gone)"
		}
		project = fit(project, projW)
	}
	nameW := max(8, width-fixed)
	info, live := m.rowLive(s)
	flag := " "
	if live {
		flag = "●"
	}
	state, stateStyle := stateWords(info, live)
	// The zmx column keeps its width on every row, so the columns beside it line up; only a
	// session that runs inside a zmx session carries the mark, and the zmx pane has its handle.
	tag := strings.Repeat(" ", zmxTagW)
	if s.ZmxName != "" {
		tag = zmxTag
	}
	age := fitRight(ageOf(s), ageW)
	name := fit(s.Name, nameW)
	sizeText := humanSize(s.Bytes)
	if s.Harness == "opencode" {
		sizeText = "—"
	}
	size := fitRight(sizeText, sizeW)
	// The store column keeps its width on every row, so the columns beside it line up and a row
	// can be read as "this store wrote it" without looking further left.
	harness := fitRight(harnessOf(s), harnessW)
	head := marker + age + "  " + name
	if project != "" {
		head += "  " + project
	}
	plain := fit(head+"  "+flag+" "+state+" "+tag+"  "+harness+"  "+size, width)
	if selected {
		// One style for the whole row: nesting a styled cell inside would reset the bar after
		// every cell, so the selected row carries no per-cell colours.
		return selSty.Render(plain)
	}
	styledFlag := flag
	if live {
		styledFlag = stateStyle.Render(flag)
	}
	styledTag := tag
	if s.ZmxName != "" {
		styledTag = accentSty.Render(tag)
	}
	middle := "  " + highlight(name, marks["name"])
	if project != "" {
		switch {
		case !s.Alive:
			middle += "  " + goneSty.Render(project)
		case marks["project"] != nil:
			middle += "  " + accentSty.Render(project)
		default:
			middle += "  " + dimSty.Render(project)
		}
	}
	return marker + dimSty.Render(age) + middle + "  " + styledFlag + " " +
		stateStyle.Render(state) + " " + styledTag + "  " +
		dimSty.Render(harness) + "  " + dimSty.Render(size)
}

// projectsView is the content of the project box: one header per group, and the sessions of
// every group the reader has opened.
func (m *model) projectsView() string {
	cursor := -1
	if m.view == viewSessions {
		cursor = m.cursor
	}
	rows := m.sessions
	if len(rows) == 0 {
		return dimSty.Render(m.emptyProjectsNote())
	}
	height := m.projectsPaneHeight()
	lines := make([]string, 0, height)
	last := min(len(rows), m.offset+height)
	for index := m.offset; index < last; index++ {
		_, marks := matchesRow(m.query, rows[index])
		lines = append(lines, m.rowView(rows[index], marks, index == cursor))
	}
	return strings.Join(lines, "\n")
}

// emptyProjectsNote explains an empty project box: a filter that matched nothing, a target
// that has not answered yet, and a target with no projects all look the same without it.
func (m *model) emptyProjectsNote() string {
	if strings.TrimSpace(m.query) != "" {
		return "nothing matches " + trim(m.query, 40)
	}
	if m.onlyShown {
		return "no session has a window open · alt+l shows every session"
	}
	if m.showIgnored {
		return "no ignored projects"
	}
	data := m.data[m.targetLabel()]
	if !data.Loaded {
		return "reading " + m.targetLabel() + "…"
	}
	if data.Note != "" {
		return data.Note
	}
	return "no projects found in zoxide"
}

// zmxView is the zmx box: its own header line, and the live sessions of the same target.  The
// box is drawn only when the target has any, so a target that runs no zmx looks like one list.
func (m *model) zmxView() string {
	rows := m.zmxRows
	if len(rows) == 0 {
		if note := m.data[m.targetLabel()].Note; note != "" {
			return dimSty.Render(note)
		}
		return dimSty.Render("no zmx sessions")
	}
	cursor := -1
	if m.view == viewZmx {
		cursor = m.zmxCursor
	}
	head := accentSty.Render(zmxTag) + " " + titleSty.Render("zmx sessions") + " " +
		dimSty.Render(fmt.Sprintf("(%d)", len(rows)))
	if query := strings.TrimSpace(m.zmxQuery); query != "" {
		head += " " + dimSty.Render("/"+trim(query, 20))
	}
	height := m.zmxPaneHeight() - 1
	lines := make([]string, 0, max(0, height))
	last := min(len(rows), m.zmxOffset+height)
	for index := m.zmxOffset; index < last; index++ {
		_, marks := matchesRow(m.zmxQuery, rows[index])
		lines = append(lines, m.rowView(rows[index], marks, index == cursor))
	}
	return head + "\n" + strings.Join(lines, "\n")
}

func (m *model) previewView() string {
	s := m.selected()
	if s.ZmxOnly && s.File == "" {
		return m.zmxSessionView(s)
	}
	if s.ProjectOnly && s.Server != "" {
		return titleSty.Render(trim(s.Project, m.previewWidth())) + "\n" +
			dimSty.Render(fit(s.Server+":"+s.CWD, m.previewWidth())) + "\n\n" +
			dimSty.Render("Enter opens this group; ctrl+a starts a new chat here.")
	}
	if s.ProjectOnly {
		title := titleSty.Render(trim(s.Project, m.previewWidth()))
		if m.projectFilesPath != s.CWD {
			return title + "\n" + dimSty.Render(fit(s.CWD, m.previewWidth())) + "\n" +
				dimSty.Render("reading project files…")
		}
		if m.projectFilesErr != "" {
			return title + "\n" + dimSty.Render(fit(s.CWD, m.previewWidth())) + "\n" +
				goneSty.Render("cannot list files: "+m.projectFilesErr)
		}
		header := title + " " + dimSty.Render(fmt.Sprintf("%d files", len(m.projectFiles))) + "\n" +
			dimSty.Render(fit(s.CWD, m.previewWidth()))
		if len(m.projectFiles) == 0 {
			return header + "\n" + dimSty.Render("empty directory")
		}
		start := min(m.preview.scroll, len(m.projectFiles))
		end := min(len(m.projectFiles), start+m.previewBodyHeight())
		lines := make([]string, 0, m.previewBodyHeight())
		for _, file := range m.projectFiles[start:end] {
			lines = append(lines, fit("  "+file, m.previewWidth()))
		}
		for len(lines) < m.previewBodyHeight() {
			lines = append(lines, "")
		}
		return header + "\n" + strings.Join(lines, "\n")
	}
	title := titleSty.Render(trim(s.Name, m.previewWidth()))
	if info, live := m.rowLive(s); live {
		word, style := stateWords(info, live)
		badge := "● " + strings.TrimSpace(word) + stateDetail(info)
		if info.State == "blocked" && info.Age > 0 {
			// A prompt that has waited a while is worth saying out loud, and the preview is the
			// one place with the room for it.
			badge += " for " + humanAge(float64(info.Age))
		}
		if info.Owner != "" {
			badge += " in " + info.Owner
		} else if s.Server != "" {
			badge += " on " + s.Server
		}
		title += " " + style.Render(trim(badge, m.previewWidth()/2))
	}
	// A remote transcript is not on this machine, and its path is reported as the remote one
	// it is: the pane never shows a path that looks local and is not.
	origin := s.CWD
	if s.Server != "" {
		origin = s.Server + ":" + s.CWD
	}
	if m.preview.id == "" {
		return title + "\n" + dimSty.Render(fit(origin, m.previewWidth())) + "\n" +
			dimSty.Render("reading the transcript…")
	}
	wrapped := m.previewWrapped()
	height := m.previewBodyHeight()
	start := m.preview.scroll
	end := min(len(wrapped), start+height)
	if end < start {
		end = start
	}
	where := "all"
	if len(wrapped) > height {
		first, last := start+1, end
		where = fmt.Sprintf("%d-%d of %d", first, last, len(wrapped))
		if m.focus != paneFocusPreview && start == m.maxPreviewScroll() {
			where += " (end)"
		}
	}
	head := title + " " + dimSty.Render(where) + "\n" +
		dimSty.Render(fit(origin, m.previewWidth()))
	visible := wrapped[start:end]
	for len(visible) < height {
		visible = append(visible, "")
	}
	return head + "\n" + strings.Join(visible, "\n")
}

// zmxSessionView shows a zmx session that carries no chat: what it is, and what it last
// printed.  There the terminal is the whole picture -- a shell, a job still running, a
// detached client -- so the pane shows the session's own scrollback, which is also what an
// attach would put on screen.  The pib-live extension adds the `pi=` label once pi knows the
// session id, and the row turns into a chat row with its transcript then.
func (m *model) zmxSessionView(s session) string {
	width := m.previewWidth()
	if s.Server != "" {
		return titleSty.Render(trim(s.ZmxName, width)) + " " + accentSty.Render("on "+s.Server) + "\n" +
			dimSty.Render(fit(s.CWD, width)) + "\n\n" + dimSty.Render("Enter attaches over SSH")
	}
	state := "detached"
	if s.Clients > 0 {
		state = fmt.Sprintf("attached (%d)", s.Clients)
	}
	head := []string{
		titleSty.Render(trim(s.ZmxName, width)) + " " + liveSty.Render(state),
		dimSty.Render(fit(s.CWD, width)),
	}
	if s.Command != "" {
		head = append(head, dimSty.Render(fit("runs: "+s.Command, width)))
	}
	head = append(head, "")
	if m.zmxHistoryName != s.ZmxName {
		return strings.Join(append(head,
			dimSty.Render("reading the session's scrollback...")), "\n")
	}
	if m.zmxHistoryErr != "" {
		return strings.Join(append(head,
			goneSty.Render("cannot read the scrollback: "+m.zmxHistoryErr)), "\n")
	}
	if len(m.zmxHistory) == 0 {
		return strings.Join(append(head, dimSty.Render("nothing printed yet")), "\n")
	}
	height := m.previewBodyHeight()
	start := min(m.preview.scroll, len(m.zmxHistory))
	end := min(len(m.zmxHistory), start+height)
	visible := make([]string, 0, height)
	for _, line := range m.zmxHistory[start:end] {
		visible = append(visible, ansi.Strip(fit(line, width)))
	}
	for len(visible) < height {
		visible = append(visible, "")
	}
	return strings.Join(append(head, visible...), "\n")
}

func (m *model) footerView() string {
	status := m.status
	if m.searching {
		// The minibuffer carries the query as a chip, so the text being typed is unmistakable.
		status = filterSty.Render(" /"+m.query+" ") + "  " +
			dimSty.Render("enter keeps it · esc clears")
	}
	if status == "" {
		switch {
		case m.focus == paneFocusPreview:
			status = "transcript preview"
		case strings.TrimSpace(*m.queryPtr()) != "":
			status = filterSty.Render(" filter: "+
				trim(strings.TrimSpace(*m.queryPtr()), 40)+" ") + "  " +
				plural(len(m.filtered()), "match") + " · esc clears"
		case m.view == viewZmx:
			status = "zmx sessions · enter attaches or focuses · [ ] move between the panes"
		case m.showIgnored:
			status = "ignored projects · alt+i shows the active ones"
		case m.onlyShown:
			status = "only sessions a window already shows · alt+l shows the rest"
		default:
			status = "projects and sessions from " + strings.Join(m.enabledHarnesses(), ", ") +
				" · enter opens a group"
		}
	}
	return fit(status, m.width) + "\n" + dimSty.Render(fit("? help", m.width))
}

// clip fits content to a box: at most height lines, and each at most width columns.
//
// This is deliberately done on the content and not with lipgloss's MaxWidth, which truncates
// the rendered block *including its border* -- the pane then lost its right edge and its rows
// ran into the pane beside it.  A row wider than the window also makes the terminal wrap it,
// which shifts every line below and looks like the layout falling apart.
func clip(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[index] = ansi.Truncate(line, width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// helpRow is one line of the ? box.  actions name the registry entries the line covers, so
// the key column follows a rebound chord; label is the text for a line that describes
// behavior rather than one binding.
type helpRow struct {
	actions []string
	label   string
	does    string
}

// helpRows is what the ? box shows.  doc.go lists the same keys for someone reading the
// source; this is the copy a reader reaches for while the program is open.
var helpRows = []helpRow{
	{actions: []string{"list.group_toggle"}, does: "open or close the project group the cursor is in: on a session, its own group"},
	{actions: []string{"list.target_prev", "list.target_next"}, does: "previous and next target: this machine, then each host in zmx_servers"},
	{label: "alt+1 …9", does: "jump to the target the bar numbers: 1 is this machine, then each host in turn"},
	{actions: []string{"list.pane_next", "list.pane_prev"}, does: "move between the panes: the project list, the zmx list, and the preview"},
	{actions: []string{"list.focus_list", "list.focus_preview"}, does: "move between the list column and the transcript preview"},
	{actions: []string{"list.resume"}, does: "open or close a project group; on a session, open or resume it"},
	{actions: []string{"list.ignore_toggle"}, does: "show the ignored projects instead of the active ones (this machine only)"},
	{actions: []string{"list.live_only"}, does: "show only the sessions a window already shows (an attached zmx session counts)"},
	{actions: []string{"list.delete"}, does: "delete the transcript (it asks first), ignore a project, or kill a zmx session"},
	{actions: []string{"list.new"}, does: "a new chat in the selected project, in the store in force (on a host, that host starts it)"},
	{label: "remote rows", does: "a session on another host opens over SSH there, and so do its delete, a shell, lazygit, yazi, and the project editor (that host's $EDITOR, or nvim); rename and the transcript stay local"},
	{actions: []string{"list.store_cycle"}, does: "cycle the store a new chat uses, over the stores this target has (the list shows every configured store)"},
	{actions: []string{"list.window"}, does: "a new kitty OS window (project rows open a shell there, a row in a zmx session gets a client)"},
	{actions: []string{"list.shell"}, does: "a login shell in the project"},
	{actions: []string{"list.lazygit"}, does: "lazygit at the top of the repository"},
	{actions: []string{"list.yazi"}, does: "yazi in the project directory"},
	{actions: []string{"list.project_editor"}, does: "ask for a tab title, then open the selected directory in $EDITOR"},
	{actions: []string{"list.rename"}, does: "rename a Pi session (pi writes the record; OpenCode, Claude Code, and Codex cannot)"},
	{actions: []string{"list.fork"}, does: "fork the selected session (the name is asked for except on Codex)"},
	{actions: []string{"list.copy_resume", "list.copy_path"}, does: "copy the resume command, or the zmx attach command (alt+y copies Pi transcript paths only)"},
	{actions: []string{"list.next_waiting"}, does: "the next chat that is blocked on you, opening its group (the rows that wait sort to the top of their group)"},
	{actions: []string{"list.move_down", "list.move_up"}, does: "next and previous row, or scroll the focused pane"},
	{actions: []string{"list.half_down", "list.half_up", "list.page_down", "list.page_up"}, does: "page through the focused pane"},
	{actions: []string{"list.top", "list.bottom"}, does: "first and last, in the focused pane"},
	{actions: []string{"list.search", "list.clear_query"}, does: "search the focused list: projects and sessions, or zmx names; enter keeps the filter, esc clears"},
	{actions: []string{"search.kill_word_back", "search.kill_word_forward", "search.kill_to_end", "search.kill_to_start"}, does: "kill word or to line end/start in search"},
	{actions: []string{"search.yank"}, does: "yank the last killed text in search"},
	{actions: []string{"list.preview_toggle"}, does: "show or hide the preview pane"},
	{actions: []string{"list.placement_cycle"}, does: "cycle placement: tab, its own window, new pane, the picker's own pane, or ask each time"},
	{actions: []string{"list.refresh"}, does: "read the current target again, connecting when its master is gone"},
	{actions: []string{"list.reconnect"}, does: "end the SSH master for the target on screen and make a new one"},
	{actions: []string{"list.menu"}, does: "travel: the ways into the selected project or session"},
	{actions: []string{"list.palette"}, does: "the command palette: the commands that are not travel"},
	{actions: []string{"list.prune"}, does: "delete every session older than an age, after showing the count"},
	{actions: []string{"list.help"}, does: "this box"},
	{actions: []string{"list.quit"}, does: "quit (closes this box first)"},
}

// helpKeyColumn is the key text of one help row, taken from the bindings in force so the box
// follows a rebound action.  A row that describes behavior rather than one binding keeps its
// own label.
func (m *model) helpKeyColumn(row helpRow) string {
	if len(row.actions) == 0 {
		return row.label
	}
	parts := make([]string, 0, len(row.actions))
	for _, name := range row.actions {
		parts = append(parts, helpChord(m.keymap, name))
	}
	return strings.Join(parts, " ")
}

// helpChord is the first chord in force for an action, or its default when no keymap is set
// (a hand-built test model).  An action the configuration disabled shows as none.
func helpChord(km *keymap, name string) string {
	if km != nil {
		if chords := km.chords(name); len(chords) > 0 {
			return chords[0]
		}
		if _, known := km.effective[name]; known {
			return "none"
		}
	}
	for _, action := range keyActions {
		if action.name == name && len(action.chords) > 0 {
			return action.chords[0]
		}
	}
	return name
}

// overlayAtTop draws box over the top of base, centred, keeping whatever base showed to the
// left and right of it.  A terminal has no compositor, so a modal is composition: the covered
// lines are spliced, and the pane borders beside the box stay visible.
func overlayAtTop(base, box string, width int) string {
	baseLines := strings.Split(base, "\n")
	for index, line := range strings.Split(box, "\n") {
		if index >= len(baseLines) {
			break
		}
		boxWidth := ansi.StringWidth(line)
		pad := (width - boxWidth) / 2
		if pad < 0 {
			pad = 0
		}
		left := ansi.Truncate(baseLines[index], pad, "")
		if have := ansi.StringWidth(left); have < pad {
			left += strings.Repeat(" ", pad-have)
		}
		right := ansi.TruncateLeft(baseLines[index], pad+boxWidth, "")
		baseLines[index] = left + line + right
	}
	return strings.Join(baseLines, "\n")
}

// modalWidth is the width every dialog box is drawn at: two thirds of the terminal, with a
// floor a menu needs and a margin that keeps the box inside the screen.
func (m *model) modalWidth() int {
	width := m.width * 2 / 3
	if width < 46 {
		width = 46
	}
	if width > m.width-4 {
		width = m.width - 4
	}
	return width
}

// dialogTitle is a dialog's title line, the target the cmd+. dialog acts on when there is one,
// and the blank line under them.  A menu drawn over a window says which project and host it
// applies to, because the reader cannot see the list it did not come from.
func (m *model) dialogTitle(title string) []string {
	lines := []string{titleSty.Render(title)}
	if m.windowMenu {
		context := m.windowRow.Project
		if m.windowRow.Server != "" {
			context += " on " + m.windowRow.Server
		}
		lines = append(lines, dimSty.Render(trim(context, m.modalWidth()-4)))
	}
	return append(lines, "")
}

// dialogBox draws one dialog, and the line the dialog has to report when there is one: the
// cmd+. dialog has no other place to show why an action did not happen.
func (m *model) dialogBox(lines []string, width int) string {
	if m.windowMenu && m.status != "" {
		lines = append(lines, "", dimSty.Render(trim(m.status, width-4)))
	}
	return paneSty.BorderForeground(lipgloss.Color("6")).Padding(0, 2).Width(width).
		Render(strings.Join(lines, "\n"))
}

// windowDialog is what the cmd+. key draws: the menu for the window it was opened from, or,
// once the menu is answered, the one line that says what happened.
func (m *model) windowDialog() string {
	if m.modal != "" {
		return m.modalBox()
	}
	message := m.status
	if message == "" {
		message = "nothing to do"
	}
	return m.dialogBox([]string{titleSty.Render("sh2pil"), "",
		dimSty.Render(trim(message, m.modalWidth()-4)), "",
		dimSty.Render("any key closes")}, m.modalWidth())
}

// modalBox is the rename or fork prompt: the session it applies to, the field with a cursor,
// and what the keys do.  A long field scrolls rather than overflows, so the end of a name
// stays visible while it is typed.
func (m *model) modalBox() string {
	width := m.modalWidth()
	session := m.selected()
	title, hint := "rename session", "enter saves · esc cancels"
	if m.modal == "fork" {
		title, hint = "fork session", "enter forks into a new session · esc cancels"
	}
	if m.modal == "editor-title" {
		title, hint = "editor tab title", "enter opens Neovim · esc cancels"
	}
	if m.modal == "layout" {
		title, hint = "open where?", "1/2/3/4 opens now · ctrl+n/p or arrows move · enter selects · esc cancels"
		lines := []string{titleSty.Render(title), ""}
		for index, layout := range placements {
			line := fmt.Sprintf("%d  %s", index+1, describeLayout(layout))
			if index == m.layoutPos {
				line = selSty.Render("› " + line)
			} else {
				line = "  " + line
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", dimSty.Render(hint))
		return paneSty.BorderForeground(lipgloss.Color("6")).Padding(0, 2).Width(width).
			Render(strings.Join(lines, "\n"))
	}
	if m.inMenu() {
		title, hint := "travel", "1-9 opens now · ctrl+n/p moves · enter selects · esc cancels"
		if m.modal == "palette" {
			title, hint = "command palette",
				"type to filter · ctrl+n/p moves · enter runs · ctrl+s saves · esc clears or closes"
		}
		if m.windowMenu {
			// This menu is the whole dialog: escape closes it instead of stepping back into a
			// picker that is not on screen.
			hint = "1-9 opens now · ctrl+n/p or arrows move · enter selects · esc closes"
		}
		lines := m.dialogTitle(title)
		if m.modal == "palette" {
			lines = append(lines, dimSty.Render("filter ")+accentSty.Render(m.nameLine(width-8)))
		}
		section := ""
		for index, name := range m.toolChoices {
			if index < len(m.toolSections) && m.toolSections[index] != section {
				section = m.toolSections[index]
				if section != "" {
					// The first heading follows the title's own blank line; every later one
					// gets a blank line of its own, which is what separates the groups.
					if index > 0 {
						lines = append(lines, "")
					}
					lines = append(lines, sectionSty.Render(strings.ToUpper(section)))
				}
			}
			line := fmt.Sprintf("%d  %s", index+1, trim(m.menuRowLabel(name), width-6))
			if index == m.toolPos {
				line = selSty.Render("› " + line)
			} else {
				line = "  " + line
			}
			lines = append(lines, line)
		}
		if m.modal == "palette" && len(m.toolChoices) == 0 {
			lines = append(lines, "", dimSty.Render(trim("no command matches "+m.name.text, width-4)))
		}
		lines = append(lines, "", dimSty.Render(hint))
		return m.dialogBox(lines, width)
	}
	if m.modal == "prune" {
		lines := []string{
			titleSty.Render("delete old sessions"),
			"",
			dimSty.Render(trim("older than an age such as 1d, 3h, 10m, or 1d3h10m", width-4)),
			"",
			accentSty.Render(m.nameLine(width)),
			"",
			dimSty.Render("enter shows what it would delete · esc cancels"),
		}
		return m.dialogBox(lines, width)
	}
	if m.modal == "prune-confirm" {
		lines := []string{
			titleSty.Render("delete old sessions"),
			"",
			dimSty.Render(trim("older than "+ageSummary(mustAge(m.pruneAge))+
				" on "+m.targetLabel(), width-4)),
			"",
			accentSty.Render(pruneQuestion(m.pruneSessions, m.pruneZmx)),
			"",
			dimSty.Render("y deletes · esc cancels"),
		}
		return m.dialogBox(lines, width)
	}
	if m.modal == "agents" {
		title, hint := "start a new chat with", "1-9 starts now · ctrl+n/p or arrows move · enter selects · esc goes back"
		lines := m.dialogTitle(title)
		for index, name := range m.agentChoices {
			line := fmt.Sprintf("%d  %s", index+1, harnessTitle(name))
			if index == m.agentPos {
				line = selSty.Render("› " + line)
			} else {
				line = "  " + line
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", dimSty.Render(hint))
		return m.dialogBox(lines, width)
	}
	context := session.Name
	if m.modal == "editor-title" {
		context = session.Project
		if context == "" {
			context = filepath.Base(session.CWD)
		}
		context = "editor in " + context
	}
	lines := []string{
		titleSty.Render(title),
		"",
		dimSty.Render(trim(context, width-4)),
		"",
		accentSty.Render(m.nameLine(width)),
	}
	if m.modal == "rename" {
		if _, live := m.live[session.ID]; live {
			lines = append(lines, "",
				dimSty.Render("it is running: its tab and window are renamed too"))
		}
	}
	lines = append(lines, "", dimSty.Render(hint), dimSty.Render("emacs keys edit the name"))
	return paneSty.BorderForeground(lipgloss.Color("6")).Padding(0, 2).Width(width).
		Render(strings.Join(lines, "\n"))
}

// pruneQuestion is the line the confirmation is really about: how much of each kind the age
// selects, counted, so nothing is deleted on an approximate idea of what is there.
func pruneQuestion(sessions, zmx int) string {
	parts := []string{plural(sessions, "transcript")}
	if zmx > 0 {
		parts = append(parts, plural(zmx, "zmx session"))
	}
	return strings.Join(parts, " and ") + " will be deleted"
}

// nameLine is the modal's field with its cursor in it.  A name longer than the box scrolls:
// columns are dropped from the left, marked with "…", so the cursor is never scrolled off
// the edge while the name is edited.
func (m *model) nameLine(width int) string {
	const cursor = "▏"
	runes := m.name.runes()
	at := clampInt(m.name.cursor, 0, len(runes))
	before, after := runes[:at], runes[at:]
	limit := width - 4
	if limit < 1 {
		limit = 1
	}
	line := string(before) + cursor + string(after)
	if ansi.StringWidth(line) <= limit {
		return line
	}
	for len(before) > 0 {
		before = before[1:]
		line = "…" + string(before) + cursor + string(after)
		if ansi.StringWidth(line) <= limit {
			return line
		}
	}
	// The cursor is at the left edge and the tail alone is too wide: clip the tail instead.
	return cursor + ansi.Truncate(string(after), limit-1, "")
}

// helpView is the box itself: one row per key, keys padded so the descriptions line up.
func (m *model) helpView() string {
	width := 0
	for _, row := range helpRows {
		if w := displayWidth(m.helpKeyColumn(row)); w > width {
			width = w
		}
	}
	lines := []string{titleSty.Render("keys"), ""}
	for _, row := range helpRows {
		lines = append(lines, accentSty.Render(fit(m.helpKeyColumn(row), width))+"  "+row.does)
	}
	return paneSty.BorderForeground(lipgloss.Color("6")).Padding(1, 3).
		Render(strings.Join(lines, "\n"))
}

func (m *model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading sessions…"
	}
	if m.windowMenu {
		// The cmd+. dialog covers the window it was opened from with the menu alone: it acts on
		// that window, and the list behind it would belong to no target the reader chose.
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.windowDialog())
	}
	// Each pane is clipped to its own box before they are joined.  Without that, a single long
	// token in a rendered transcript (a path, a URL, a base64 blob) makes that row wider than
	// the window, and the terminal then wraps it: the frame grows taller than the screen,
	// everything below shifts, and the list beside it looks corrupted.
	panes := m.leftColumn()
	if m.showPrev {
		previewPane := paneSty
		if m.focus == paneFocusPreview {
			previewPane = previewPane.BorderForeground(lipgloss.Color("6"))
		}
		panes = lipgloss.JoinHorizontal(lipgloss.Top, panes,
			previewPane.Width(m.previewWidth()).Height(m.paneHeight()).
				Render(clip(m.previewView(), m.previewWidth(), m.paneHeight())))
	}
	// The help box takes the place of the panes, centred in exactly their area, so the frame
	// keeps its size and the chrome stays where it is.
	middle := panes
	switch {
	case m.help:
		middle = lipgloss.Place(m.width, m.paneHeight()+2, lipgloss.Center, lipgloss.Center,
			m.helpView())
	case m.modal != "":
		// The modal sits at the top centre, over the list, so the reader can still see the
		// row it applies to.
		middle = overlayAtTop(middle, m.modalBox(), m.width)
	}
	// Clip the chrome as well: a long project name in the header, or a long legend, would
	// otherwise wrap and shift the panes below it.
	return strings.Join([]string{
		clip(m.targetBar(), m.width, 1),
		clip(m.headerView(), m.width, 1),
		middle,
		clip(m.footerView(), m.width, m.chromeLines()-2),
	}, "\n")
}

// leftColumn stacks the two boxes of the left column: the project groups, and the live zmx
// sessions of the same target under them.  The boxes fill exactly the area of one pane, so the
// column and the preview stay the same height.
func (m *model) leftColumn() string {
	projectsPane := paneSty
	if m.focus == paneFocusList && m.view == viewSessions {
		projectsPane = projectsPane.BorderForeground(lipgloss.Color("6"))
	}
	box := projectsPane.Width(m.listWidth()).Height(m.projectsPaneHeight()).
		Render(clip(m.projectsView(), m.listWidth(), m.projectsPaneHeight()))
	if m.zmxPaneHeight() == 0 {
		return box
	}
	zmxPane := paneSty
	if m.focus == paneFocusList && m.view == viewZmx {
		zmxPane = zmxPane.BorderForeground(lipgloss.Color("6"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, box,
		zmxPane.Width(m.listWidth()).Height(m.zmxPaneHeight()).
			Render(clip(m.zmxView(), m.listWidth(), m.zmxPaneHeight())))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// plural renders a count with its noun, so a list never says "1 sessions".
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
