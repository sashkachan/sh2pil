package main

import (
	"fmt"
	"sort"
	"strings"
)

// The keyboard contexts. A context is the state that owns the keyboard, and a binding
// belongs to exactly one of them, so one chord can mean different things in different
// states (ctrl+w deletes a row in the list and kills a word in a field).
const (
	keyContextHelp    = "help"
	keyContextModal   = "modal"
	keyContextSearch  = "search"
	keyContextConfirm = "confirm"
	keyContextList    = "list"
)

// blockedChord is what a released default resolves to. It matches no case in the dispatch,
// so a default that the configuration moved away stops working instead of lingering.
const blockedChord = "\x00blocked"

// keyAction is one configurable binding. Name is unique and is exactly the suffix of its
// configuration key: the action list.resume is set with "key.list.resume". Context says
// which resolver uses it. The first chord is the canonical one, which is the literal the
// dispatch switch matches, so it must never change when the reader rebinds the action.
type keyAction struct {
	name    string
	context string
	chords  []string
	help    string
}

// keyActions is the whole keyboard in one place. Every key the program acts on is here,
// so the dispatch, the help box, and the documentation cannot disagree about it.
var keyActions = []keyAction{
	// The help box owns the keyboard while it is open.
	{name: "help.close", context: keyContextHelp, chords: []string{"?", "esc", "q", "enter", "ctrl+c"}},

	// The live-session question.
	{name: "confirm.yes", context: keyContextConfirm, chords: []string{"y", "Y"}},

	// A modal owns the keyboard while it is open.  Emacs editing keys edit the name field
	// or move in the placement menu; the list's keys are deliberately absent.
	{name: "modal.cancel", context: keyContextModal, chords: []string{"esc", "ctrl+c", "ctrl+g"}},
	{name: "modal.up", context: keyContextModal, chords: []string{"up", "ctrl+p"}},
	{name: "modal.down", context: keyContextModal, chords: []string{"down", "ctrl+n"}},
	{name: "modal.enter", context: keyContextModal, chords: []string{"enter"}},
	{name: "modal.backspace", context: keyContextModal, chords: []string{"backspace", "ctrl+h"}},
	{name: "modal.delete", context: keyContextModal, chords: []string{"delete", "ctrl+d"}},
	{name: "modal.home", context: keyContextModal, chords: []string{"ctrl+a", "home"}},
	{name: "modal.end", context: keyContextModal, chords: []string{"ctrl+e", "end"}},
	{name: "modal.left", context: keyContextModal, chords: []string{"ctrl+b", "left"}},
	{name: "modal.right", context: keyContextModal, chords: []string{"ctrl+f", "right"}},
	{name: "modal.word_left", context: keyContextModal, chords: []string{"alt+b", "alt+left", "ctrl+left"}},
	{name: "modal.word_right", context: keyContextModal, chords: []string{"alt+f", "alt+right", "ctrl+right"}},
	{name: "modal.kill_word_back", context: keyContextModal, chords: []string{"alt+backspace", "alt+ctrl+h", "ctrl+w"}},
	{name: "modal.kill_word_forward", context: keyContextModal, chords: []string{"alt+d"}},
	{name: "modal.kill_to_end", context: keyContextModal, chords: []string{"ctrl+k"}},
	{name: "modal.kill_to_start", context: keyContextModal, chords: []string{"ctrl+u"}},
	{name: "modal.yank", context: keyContextModal, chords: []string{"ctrl+y"}},
	{name: "modal.save", context: keyContextModal, chords: []string{"ctrl+s"}},

	// The slash-prefixed search field.
	{name: "search.accept", context: keyContextSearch, chords: []string{"enter"}},
	{name: "search.clear", context: keyContextSearch, chords: []string{"esc", "ctrl+g"}},
	{name: "search.backspace", context: keyContextSearch, chords: []string{"backspace", "ctrl+h"}},
	{name: "search.delete", context: keyContextSearch, chords: []string{"delete", "ctrl+d"}},
	{name: "search.home", context: keyContextSearch, chords: []string{"ctrl+a", "home"}},
	{name: "search.end", context: keyContextSearch, chords: []string{"ctrl+e", "end"}},
	{name: "search.left", context: keyContextSearch, chords: []string{"ctrl+b", "left"}},
	{name: "search.right", context: keyContextSearch, chords: []string{"ctrl+f", "right"}},
	{name: "search.word_left", context: keyContextSearch, chords: []string{"alt+b", "alt+left", "ctrl+left"}},
	{name: "search.word_right", context: keyContextSearch, chords: []string{"alt+f", "alt+right", "ctrl+right"}},
	{name: "search.kill_word_back", context: keyContextSearch, chords: []string{"ctrl+w", "alt+backspace", "alt+ctrl+h"}},
	{name: "search.kill_word_forward", context: keyContextSearch, chords: []string{"alt+d"}},
	{name: "search.kill_to_end", context: keyContextSearch, chords: []string{"ctrl+k"}},
	{name: "search.kill_to_start", context: keyContextSearch, chords: []string{"ctrl+u"}},
	{name: "search.yank", context: keyContextSearch, chords: []string{"ctrl+y"}},

	// The lists themselves.
	{name: "list.menu", context: keyContextList, chords: []string{"."},
		help: "the travel menu: the ways into the selected project or session"},
	{name: "list.palette", context: keyContextList, chords: []string{"alt+p", "ctrl+k", ":"},
		help: "the command palette: the commands that are not travel"},
	{name: "list.prune", context: keyContextList, chords: []string{"alt+d"},
		help: "delete every session older than an age, after showing the count"},
	{name: "list.help", context: keyContextList, chords: []string{"?"}, help: "this box"},
	{name: "list.search", context: keyContextList, chords: []string{"/"},
		help: "search the focused list: enter keeps the filter, esc clears it"},
	{name: "list.quit", context: keyContextList, chords: []string{"q", "ctrl+c"}, help: "quit (closes this box first)"},
	{name: "list.resume", context: keyContextList, chords: []string{"enter"},
		help: "open or close a project group; on a session, open or resume it"},
	{name: "list.new", context: keyContextList, chords: []string{"ctrl+a"},
		help: "a new chat in the selected project, in the store in force"},
	{name: "list.window", context: keyContextList, chords: []string{"ctrl+t"},
		help: "a new kitty OS window; a project row opens a shell there"},
	{name: "list.shell", context: keyContextList, chords: []string{"ctrl+x"}, help: "a login shell in the project"},
	{name: "list.lazygit", context: keyContextList, chords: []string{"ctrl+g"},
		help: "lazygit at the top of the repository"},
	{name: "list.copy_resume", context: keyContextList, chords: []string{"ctrl+y"},
		help: "copy the resume command, or the zmx attach command"},
	{name: "list.copy_path", context: keyContextList, chords: []string{"alt+y"},
		help: "copy the transcript path (Pi sessions only)"},
	{name: "list.project_editor", context: keyContextList, chords: []string{"alt+e"},
		help: "ask for a tab title, then open the selected directory in $EDITOR"},
	{name: "list.yazi", context: keyContextList, chords: []string{"alt+f"}, help: "yazi in the project directory"},
	{name: "list.rename", context: keyContextList, chords: []string{"ctrl+e"},
		help: "rename a Pi session (the other stores cannot be renamed)"},
	{name: "list.fork", context: keyContextList, chords: []string{"ctrl+f"}, help: "fork the selected session"},
	{name: "list.delete", context: keyContextList, chords: []string{"ctrl+w"},
		help: "delete the transcript (it asks first), ignore a project, or kill a zmx session"},
	{name: "list.ignore_toggle", context: keyContextList, chords: []string{"alt+i"},
		help: "show the ignored projects instead of the active ones (this machine only)"},
	{name: "list.live_only", context: keyContextList, chords: []string{"alt+l"},
		help: "show only the sessions a window already shows"},
	{name: "list.placement_cycle", context: keyContextList, chords: []string{"ctrl+l"},
		help: "cycle placement: tab, its own window, new pane, the picker's own pane, or ask"},
	{name: "list.store_cycle", context: keyContextList, chords: []string{"ctrl+b"},
		help: "cycle the store a new chat uses, over the stores this target has"},
	{name: "list.order_cycle", context: keyContextList, chords: []string{"ctrl+o"},
		help: "cycle the list order: the project groups, or everything by what needs you"},
	{name: "list.preview_toggle", context: keyContextList, chords: []string{"ctrl+v"},
		help: "show or hide the preview pane"},
	{name: "list.group_toggle", context: keyContextList, chords: []string{"tab"},
		help: "open or close the project group the cursor is in"},
	{name: "list.target_prev", context: keyContextList, chords: []string{"{", "shift+["},
		help: "previous target: this machine, then each host in zmx_servers"},
	{name: "list.target_next", context: keyContextList, chords: []string{"}", "shift+]"},
		help: "next target: this machine, then each host in zmx_servers"},
	{name: "list.target_1", context: keyContextList, chords: []string{"alt+1"}},
	{name: "list.target_2", context: keyContextList, chords: []string{"alt+2"}},
	{name: "list.target_3", context: keyContextList, chords: []string{"alt+3"}},
	{name: "list.target_4", context: keyContextList, chords: []string{"alt+4"}},
	{name: "list.target_5", context: keyContextList, chords: []string{"alt+5"}},
	{name: "list.target_6", context: keyContextList, chords: []string{"alt+6"}},
	{name: "list.target_7", context: keyContextList, chords: []string{"alt+7"}},
	{name: "list.target_8", context: keyContextList, chords: []string{"alt+8"}},
	{name: "list.target_9", context: keyContextList, chords: []string{"alt+9"}},
	{name: "list.focus_list", context: keyContextList, chords: []string{"h", "left"},
		help: "move between the list column and the transcript preview"},
	{name: "list.focus_preview", context: keyContextList, chords: []string{"l", "right"},
		help: "move between the list column and the transcript preview"},
	{name: "list.pane_next", context: keyContextList, chords: []string{"]"},
		help: "move between the panes: the project list, the zmx list, and the preview"},
	{name: "list.pane_prev", context: keyContextList, chords: []string{"["},
		help: "move between the panes: the project list, the zmx list, and the preview"},
	{name: "list.move_down", context: keyContextList, chords: []string{"j", "down", "ctrl+n"},
		help: "next row, or scroll the focused pane"},
	{name: "list.move_up", context: keyContextList, chords: []string{"k", "up", "ctrl+p"},
		help: "previous row, or scroll the focused pane"},
	{name: "list.top", context: keyContextList, chords: []string{"g", "home"}, help: "first row, in the focused pane"},
	{name: "list.bottom", context: keyContextList, chords: []string{"G", "end", "shift+g", "shift+end"},
		help: "last row, in the focused pane"},
	{name: "list.page_down", context: keyContextList, chords: []string{"pgdown"}, help: "page through the focused pane"},
	{name: "list.page_up", context: keyContextList, chords: []string{"pgup"}, help: "page through the focused pane"},
	{name: "list.half_down", context: keyContextList, chords: []string{"ctrl+d"}, help: "half a page down"},
	{name: "list.half_up", context: keyContextList, chords: []string{"ctrl+u"}, help: "half a page up"},
	{name: "list.refresh", context: keyContextList, chords: []string{"ctrl+r"},
		help: "read the current target again, connecting when its master is gone"},
	{name: "list.next_waiting", context: keyContextList, chords: []string{"n"},
		help: "the next chat that is blocked on you, opening its group"},
	{name: "list.reconnect", context: keyContextList, chords: []string{"alt+r"},
		help: "end the SSH master for the target on screen and make a new one"},
	{name: "list.clear_query", context: keyContextList, chords: []string{"esc"}, help: "clear the filter"},
}

// keymap resolves a chord to the canonical chord of the action that owns it. The canonical
// chord never changes, so the dispatch switch can keep matching literals while the reader
// moves a binding anywhere. A key that a released default used resolves to blockedChord.
type keymap struct {
	byName     map[string]keyAction
	configured map[string]map[string]string // context -> chord -> action name
	defaults   map[string]map[string]bool   // context -> chord -> was a default
	effective  map[string][]string          // action name -> chords in force
	order      []string                     // action names, registry order
	conflicts  []string
}

// buildKeymap resolves the registry against the overrides read from configuration. An
// override is the full chord list for one action: "ctrl+f, alt+p", or "none" to disable it.
// A chord that two actions claim in one context is a conflict: the later action keeps its
// defaults and the conflict is reported.
func buildKeymap(overrides map[string]string) *keymap {
	km := &keymap{
		byName:     map[string]keyAction{},
		configured: map[string]map[string]string{},
		defaults:   map[string]map[string]bool{},
		effective:  map[string][]string{},
	}
	for _, action := range keyActions {
		km.byName[action.name] = action
		km.order = append(km.order, action.name)
		if km.defaults[action.context] == nil {
			km.defaults[action.context] = map[string]bool{}
			km.configured[action.context] = map[string]string{}
		}
		for _, chord := range action.chords {
			km.defaults[action.context][chord] = true
		}
		for _, chord := range action.chords {
			if _, used := km.configured[action.context][chord]; !used {
				km.configured[action.context][chord] = action.name
			}
		}
	}
	for _, name := range km.order {
		raw, has := overrides[name]
		if !has {
			continue
		}
		action := km.byName[name]
		chords := parseChordList(raw)
		conflict := ""
		for _, chord := range chords {
			if other, used := km.configured[action.context][chord]; used && other != name {
				conflict = fmt.Sprintf("%s: %s and %s both bind %s", action.context, other, name, chord)
				break
			}
		}
		if conflict != "" {
			km.conflicts = append(km.conflicts, conflict)
			continue
		}
		for _, chord := range action.chords {
			if km.configured[action.context][chord] == name {
				delete(km.configured[action.context], chord)
			}
		}
		for _, chord := range chords {
			km.configured[action.context][chord] = name
		}
	}
	for _, action := range keyActions {
		if raw, has := overrides[action.name]; has {
			km.effective[action.name] = parseChordList(raw)
			continue
		}
		km.effective[action.name] = append([]string{}, action.chords...)
	}
	return km
}

// resolve returns the canonical chord a key acts as in one context. A key no action knows
// is returned unchanged, so a field still receives it.
func (km *keymap) resolve(context, key string) string {
	if km == nil {
		return key
	}
	if name, ok := km.configured[context][key]; ok {
		return km.byName[name].chords[0]
	}
	if km.defaults[context][key] {
		return blockedChord
	}
	return key
}

// chords is the chord list in force for one action, for the help box and inspection.
func (km *keymap) chords(name string) []string {
	if km == nil {
		return nil
	}
	return km.effective[name]
}

// firstChord is the chord the help box shows for one action.
func (km *keymap) firstChord(name string) string {
	chords := km.chords(name)
	if len(chords) == 0 {
		return "none"
	}
	return chords[0]
}

// parseChordList splits a configured chord list. "none" (or an empty value) disables it.
func parseChordList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "none") {
		return nil
	}
	var chords []string
	for _, part := range strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if part != "" {
			chords = append(chords, part)
		}
	}
	return chords
}

// bindingTable is every action and its chords, for --check-config and --print-config.
func (km *keymap) bindingTable() []string {
	lines := make([]string, 0, len(km.order))
	for _, name := range km.order {
		action := km.byName[name]
		chords := km.effective[name]
		shown := strings.Join(chords, ", ")
		if shown == "" {
			shown = "none"
		}
		lines = append(lines, fmt.Sprintf("%-24s %-8s %s", name, action.context, shown))
	}
	sort.Strings(lines)
	return lines
}
