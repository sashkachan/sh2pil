package main

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func holdsAll(haystack []string, needles ...string) bool {
	for _, needle := range needles {
		found := false
		for _, item := range haystack {
			if item == needle {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// paletteRows splits the entries the way the tests reason about them: the names, and the
// section each one belongs to.
func paletteRows(entries []paletteEntry) ([]string, []string) {
	actions := make([]string, 0, len(entries))
	sections := make([]string, 0, len(entries))
	for _, entry := range entries {
		actions = append(actions, entry.name)
		sections = append(sections, entry.section)
	}
	return actions, sections
}

// TestPalettePutsTheDeleteInterfaceFirst pins the one ordering promise the palette makes: the
// command a reader opened it for is the first row, under its own heading.
func TestPalettePutsTheDeleteInterfaceFirst(t *testing.T) {
	m := &model{keymap: buildKeymap(nil)}
	actions, sections := paletteRows(m.paletteEntriesFor(session{}))
	if len(actions) == 0 {
		t.Fatal("the palette is empty")
	}
	if actions[0] != "list.prune" || sections[0] != "delete" {
		t.Fatalf("first row = %q under %q, want the delete interface under delete",
			actions[0], sections[0])
	}
	if len(sections) != len(actions) {
		t.Fatalf("%d sections for %d rows", len(sections), len(actions))
	}
	// The rows are grouped in the order the sections are read, and every row belongs to one.
	var order []string
	for index, section := range sections {
		if section == "" {
			t.Fatalf("row %q carries no section", actions[index])
		}
		if len(order) == 0 || order[len(order)-1] != section {
			order = append(order, section)
		}
	}
	want := []string{"delete", "session", "view", "target", "picker"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("the sections are %v, want %v", order, want)
	}
	// The box draws a heading once per group, however many rows the group holds.
	m.width, m.height, m.modal = 100, 40, "palette"
	m.toolChoices, m.toolSections = actions, sections
	view := m.View()
	for _, heading := range want {
		if count := strings.Count(view, strings.ToUpper(heading)); count != 1 {
			t.Errorf("the heading %q is drawn %d times, want once:\n%s", heading, count, view)
		}
	}
	if !holdsAll(actions, "list.delete", "list.rename", "list.fork", "list.copy_resume",
		"list.quit") {
		t.Fatalf("the palette omits commands it must offer: %v", actions)
	}
	// Travel is not a command: the place to go is the dot menu, and offering it twice would
	// make the two menus the same menu.
	for _, action := range actions {
		if action == "resume" || action == "shell" || action == "yazi" || action == "lazygit" {
			t.Fatalf("travel action %q belongs to the travel menu, not the palette", action)
		}
	}
}

// TestPaletteSkipsAnUnboundCommand pins that a released key removes the command from the
// palette instead of leaving a row that would do nothing.
func TestPaletteSkipsAnUnboundCommand(t *testing.T) {
	m := &model{keymap: buildKeymap(map[string]string{"list.fork": "none"})}
	actions, _ := paletteRows(m.paletteEntriesFor(session{}))
	if holdsAll(actions, "list.fork") {
		t.Errorf("an unbound command is still offered: %v", actions)
	}
	if !holdsAll(actions, "list.rename") {
		t.Errorf("the other commands went with it: %v", actions)
	}
}

// TestPaletteLabelFollowsTheRow pins the row-dependent wording: one bound key destroys
// different things depending on what the cursor is on.
func TestPaletteLabelFollowsTheRow(t *testing.T) {
	m := &model{}
	cases := []struct {
		row  session
		want string
	}{
		{session{}, "delete this session"},
		{session{ZmxOnly: true}, "end this zmx session"},
		{session{ProjectOnly: true}, "ignore or restore this project"},
	}
	for _, c := range cases {
		if got := m.paletteLabel("list.delete", c.row); got != c.want {
			t.Errorf("delete on %+v = %q, want %q", c.row, got, c.want)
		}
	}
	if got := m.paletteLabel("list.prune", session{}); got != "delete old sessions…" {
		t.Errorf("the prune row = %q, want the delete interface", got)
	}
}

// TestKeyMsgForChordRebuildsTheKey pins the bridge from a binding back to a key press, which
// is what lets a palette row run through the dispatch its own key uses.
func TestKeyMsgForChordRebuildsTheKey(t *testing.T) {
	for chord, want := range map[string]string{
		"ctrl+w": "ctrl+w", "ctrl+v": "ctrl+v", "alt+p": "alt+p", "alt+d": "alt+d",
		"q": "q", "?": "?", "enter": "enter", "esc": "esc", "tab": "tab",
	} {
		msg, ok := keyMsgForChord(chord)
		if !ok {
			t.Fatalf("keyMsgForChord(%q) refused a chord a default uses", chord)
		}
		if got := msg.String(); got != want {
			t.Errorf("keyMsgForChord(%q) = %q, want %q", chord, got, want)
		}
	}
	for _, chord := range []string{"", "f5", "alt+pgup", "ctrl+alt+x", "ctrl+1", "shift+g"} {
		if _, ok := keyMsgForChord(chord); ok {
			t.Errorf("keyMsgForChord(%q) built a key it cannot know", chord)
		}
	}
}

// TestEveryPaletteRowCanBeRun pins that the palette only offers what it can run: a row whose
// chord cannot be rebuilt would be a menu entry that fails on the reader.
func TestEveryPaletteRowCanBeRun(t *testing.T) {
	m := &model{keymap: buildKeymap(nil)}
	actions, _ := paletteRows(m.paletteEntriesFor(session{}))
	for _, action := range actions {
		if isPaletteMeta(action) {
			// A meta row has no chord of its own; its dispatch is checked elsewhere.
			continue
		}
		chords := m.chordsOf(action)
		if len(chords) == 0 {
			t.Errorf("%s is offered with no key", action)
			continue
		}
		if _, ok := keyMsgForChord(chords[0]); !ok {
			t.Errorf("%s is offered but its chord %q cannot be rebuilt", action, chords[0])
		}
	}
}

// TestPaletteRunsTheBoundAction pins the dispatch: choosing a row does what its key does.
func TestPaletteRunsTheBoundAction(t *testing.T) {
	m := &model{keymap: buildKeymap(nil), showPrev: true}
	actions, _ := paletteRows(m.paletteEntriesFor(session{}))
	index := -1
	for at, action := range actions {
		if action == "list.preview_toggle" {
			index = at
		}
	}
	if index < 0 {
		t.Fatal("the palette does not offer the preview toggle")
	}
	m.modal, m.toolChoices, m.toolSections = "palette", actions, make([]string, len(actions))
	if _, _ = m.choosePalette(index); m.showPrev {
		t.Error("the palette row did not run the action its key runs")
	}
	if m.modal != "" {
		t.Errorf("the palette stayed open: modal=%q", m.modal)
	}
}

// TestPruneFollowsTheTargetOnScreen pins that the delete acts on one target and no other: a
// host's store is the host's own, so the age is resolved there over the connection it already
// has, and this side never reaches a machine the reader is not looking at.
func TestPruneFollowsTheTargetOnScreen(t *testing.T) {
	m := &model{targets: []target{{Server: "user@192.0.2.15"}}, harnesses: []string{"pi"}}
	if _, cmd := m.startPrune(); cmd != nil || m.modal != "prune" {
		t.Fatalf("startPrune on a host: modal=%q cmd=%v, want the age field", m.modal, cmd)
	}
	// The exact list is pinned, because the two commands are different programs: sh2pil-open's
	// session-remote-prune takes no subcommand word and no --json (it only relays the host's
	// own JSON), and a local token sent to it is refused by argparse before it runs.
	helper, args := m.pruneHelper("1d", true)
	wantRemote := []string{"session-remote-prune", "user@192.0.2.15",
		"--older-than", "1d", "--harness", "pi", "--zmx", "--yes"}
	if helper != "sh2pil-open" || !reflect.DeepEqual(args, wantRemote) {
		t.Fatalf("the remote prune is %q %v, want %q %v", helper, args, "sh2pil-open", wantRemote)
	}

	// This machine's own stores are read and deleted by the local sh2pil-sessions, which is asked for JSON.
	wantLocal := []string{"prune", "--json", "--older-than", "1d3h", "--harness", "pi", "--zmx"}
	if helper, args := (&model{harnesses: []string{"pi"}}).pruneHelper("1d3h", false); helper != "sh2pil-sessions" ||
		!reflect.DeepEqual(args, wantLocal) {
		t.Errorf("the local prune is %q %v, want %q %v", helper, args, "sh2pil-sessions", wantLocal)
	}
}

// TestPruneAsksForTheAgeThenCounts pins the two steps of the interface: the age field, then a
// question about a number.
func TestPruneAsksForTheAgeThenCounts(t *testing.T) {
	m := &model{helperDir: "/tmp/helpers", width: 100, height: 40}
	if _, cmd := m.startPrune(); cmd != nil || m.modal != "prune" {
		t.Fatalf("startPrune: modal=%q cmd=%v, want the age field", m.modal, cmd)
	}
	if m.name.text != defaultPruneAge {
		t.Errorf("the field opens on %q, want %q", m.name.text, defaultPruneAge)
	}
	if m.pruneSessions != -1 {
		t.Errorf("pruneSessions = %d, want -1 until the read answers", m.pruneSessions)
	}

	// A typo is refused in the field, before a helper is asked to run.
	m.name = newField("7")
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.modal != "prune" {
		t.Fatalf("a bad age left the field: modal=%q status=%q", m.modal, m.status)
	}
	if !strings.Contains(m.status, "unit") {
		t.Errorf("the refusal does not explain the unit: %q", m.status)
	}

	// A good age closes the field and starts the read.
	m.name = newField("1d3h")
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.modal != "" || cmd == nil {
		t.Fatalf("a good age: modal=%q cmd=%v, want a read", m.modal, cmd)
	}

	// Nothing old enough is not a question.
	_, _ = m.Update(pruneCheckMsg{age: "1d3h"})
	if m.modal != "" {
		t.Fatalf("an empty selection still asks: modal=%q", m.modal)
	}
	for _, want := range []string{"nothing on local", "older than 1d 3h"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("an empty selection says %q, want %q", m.status, want)
		}
	}

	// Anything old enough is a question about the count.
	_, _ = m.Update(pruneCheckMsg{age: "1d3h", sessions: 12, zmx: 4})
	if m.modal != "prune-confirm" {
		t.Fatalf("modal=%q, want the confirmation", m.modal)
	}
	view := m.View()
	for _, want := range []string{"delete old sessions", "12 transcripts", "4 zmx sessions",
		"older than 1d 3h", "y deletes"} {
		if !strings.Contains(view, want) {
			t.Errorf("the confirmation does not show %q:\n%s", want, view)
		}
	}
}

// TestPruneConfirmAnswersTheQuestion pins the two answers: yes deletes, and anything else
// leaves the stores alone.
func TestPruneConfirmAnswersTheQuestion(t *testing.T) {
	m := &model{modal: "prune-confirm", pruneAge: "7d", pruneSessions: 3, pruneZmx: 0}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.modal != "" || m.pruneAge != "" || m.pruneSessions != 0 {
		t.Fatalf("escape left the question armed: modal=%q age=%q sessions=%d",
			m.modal, m.pruneAge, m.pruneSessions)
	}
	if m.status != "prune cancelled" {
		t.Errorf("escape says %q, want the cancellation", m.status)
	}

	m = &model{modal: "prune-confirm", pruneAge: "7d", pruneSessions: 3, pruneZmx: 1,
		helperDir: "/tmp/helpers"}
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("a yes did not start the delete")
	}
	if m.modal != "" || m.pruneAge != "" {
		t.Errorf("the question is still armed: modal=%q age=%q", m.modal, m.pruneAge)
	}
}

// TestPruneReadsTheConfiguredStores pins that the delete covers the stores this picker is set
// to read, and that the read that comes before the question is not a delete.
func TestPruneReadsTheConfiguredStores(t *testing.T) {
	m := &model{harnesses: []string{"pi", "claude"}}
	_, read := m.pruneHelper("1d3h", false)
	joined := strings.Join(read, " ")
	if !strings.Contains(joined, "--harness pi,claude") || !strings.Contains(joined, "--zmx") {
		t.Errorf("the read is %q", joined)
	}
	if strings.Contains(joined, "--yes") {
		t.Errorf("a read must not delete: %q", joined)
	}
	_, deletes := m.pruneHelper("1d3h", true)
	if !strings.Contains(strings.Join(deletes, " "), "--yes") {
		t.Errorf("the delete is %q, want --yes", deletes)
	}
	// No configured stores is every known store, so an unconfigured picker still cleans.
	_, all := (&model{}).pruneHelper("1d", false)
	if !strings.Contains(strings.Join(all, " "), "--harness all") {
		t.Errorf("an unconfigured picker reads %q, want all stores", all)
	}
}

// TestPaletteOpeners pins the three ways in: alt+p, ctrl+k, and the colon, all landing on the
// same action, so rebinding one moves all three.
func TestPaletteOpeners(t *testing.T) {
	km := buildKeymap(nil)
	for _, chord := range []string{"alt+p", "ctrl+k", ":"} {
		if got := km.resolve(keyContextList, chord); got != "alt+p" {
			t.Errorf("resolve(%q) = %q, want alt+p", chord, got)
		}
	}
}

// TestPaletteFilterNarrowsAndRanks pins the filter: a query keeps only matching rows, and a
// subsequence is a match of last resort.
func TestPaletteFilterNarrowsAndRanks(t *testing.T) {
	m := &model{keymap: buildKeymap(nil)}
	m.paletteAll = m.paletteEntriesFor(session{})
	m.name = field{text: "reconnect"}
	m.refilterPalette()
	if len(m.toolChoices) == 0 || m.toolChoices[0] != "list.reconnect" {
		t.Fatalf("filter reconnect = %#v, want list.reconnect first", m.toolChoices)
	}
	m.name = field{text: "rld"}
	m.refilterPalette()
	if !holdsAll(m.toolChoices, "meta.reload_config") {
		t.Fatalf("subsequence rld = %#v, want the reload row", m.toolChoices)
	}
	m.name = field{text: ""}
	m.refilterPalette()
	if len(m.toolChoices) != len(m.paletteAll) {
		t.Fatalf("clearing the filter left %d of %d rows", len(m.toolChoices), len(m.paletteAll))
	}
}

// TestPaletteEscapeClearsTheFilterFirst pins the two-step escape: the first press keeps the
// palette and drops the query, the second closes it.
func TestPaletteEscapeClearsTheFilterFirst(t *testing.T) {
	m := &model{keymap: buildKeymap(nil), modal: "palette"}
	m.paletteAll = m.paletteEntriesFor(session{})
	m.name = field{text: "recon"}
	m.refilterPalette()
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*model)
	if m.modal != "palette" || m.name.text != "" {
		t.Fatalf("first esc = modal %q, filter %q; want the palette open and the filter cleared",
			m.modal, m.name.text)
	}
	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*model)
	if m.modal != "" {
		t.Fatalf("second esc = modal %q, want closed", m.modal)
	}
}

// TestPaletteValueRowsCycleInPlace pins the mode row: enter changes the session value and
// leaves the palette open, because ctrl+s is what persists it.
func TestPaletteValueRowsCycleInPlace(t *testing.T) {
	m := &model{keymap: buildKeymap(nil), modal: "palette", mode: "keybind"}
	m.paletteAll = m.paletteEntriesFor(session{})
	m.refilterPalette()
	index := -1
	for at, name := range m.toolChoices {
		if name == paletteMetaMode {
			index = at
		}
	}
	if index < 0 {
		t.Fatal("the palette has no mode row")
	}
	updated, _ := m.choosePalette(index)
	m = updated.(*model)
	if m.mode != "prompt" {
		t.Fatalf("mode after the row = %q, want prompt", m.mode)
	}
	if m.paletteChanged != "mode=prompt" {
		t.Fatalf("paletteChanged = %q, want mode=prompt", m.paletteChanged)
	}
	if m.modal != "palette" {
		t.Fatalf("the value row closed the palette; it must stay for ctrl+s")
	}
}

// TestPaletteOffersForgetOnlyForAHost pins that the master row appears only where there is a
// master to forget: this machine has none.
func TestPaletteOffersForgetOnlyForAHost(t *testing.T) {
	local := &model{keymap: buildKeymap(nil)}
	if actions, _ := paletteRows(local.paletteEntriesFor(session{})); holdsAll(actions, paletteMetaForget) {
		t.Fatalf("this machine is offered a master to forget: %v", actions)
	}
	remote := &model{keymap: buildKeymap(nil), targets: []target{{Server: "build-host"}}}
	actions, _ := paletteRows(remote.paletteEntriesFor(session{}))
	if !holdsAll(actions, paletteMetaForget) {
		t.Fatalf("a host is not offered its master: %v", actions)
	}
}

// TestPaletteReloadReReadsTheConfig pins the reload row: the settings the model holds follow
// the file chain again.
func TestPaletteReloadReReadsTheConfig(t *testing.T) {
	writeConfig(t, "mode: prompt\n")
	m := &model{keymap: buildKeymap(nil), mode: "keybind"}
	note := m.reloadConfig()
	if m.mode != "prompt" {
		t.Fatalf("mode after reload = %q, want prompt", m.mode)
	}
	if !strings.Contains(note, "reloaded") {
		t.Fatalf("reload note = %q", note)
	}
}

// TestPaletteDoctorLineNamesTheBuildAndTheConfig pins the one-line check.
func TestPaletteDoctorLineNamesTheBuildAndTheConfig(t *testing.T) {
	writeConfig(t, "")
	m := &model{keymap: buildKeymap(nil)}
	line := m.doctorLine()
	if !strings.Contains(line, "sh2pil "+version) || !strings.Contains(line, "config source") {
		t.Fatalf("doctor line = %q", line)
	}
}
