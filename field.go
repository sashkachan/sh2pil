package main

import "unicode"

// field is one editable line of text and its cursor.  The rename and fork prompts are the
// only text fields this UI has, and the reader already lives in a terminal Emacs, so the
// field edits with the keys that Emacs uses: ctrl+a and ctrl+e go to the ends, ctrl+b and
// ctrl+f move by a character, alt+b and alt+f by a word, a kill (alt+backspace, ctrl+w,
// alt+d, ctrl+k, ctrl+u) removes text, and ctrl+y puts the last kill back.
//
// The cursor is a rune index, not a byte offset: a name with an accent moves one character
// at a time and never lands in the middle of a UTF-8 sequence.
type field struct {
	text   string
	cursor int
}

// newField starts a field with the cursor at the end, which is where a name that is offered
// as a default is edited from.
func newField(text string) field {
	return field{text: text, cursor: len([]rune(text))}
}

func (f field) runes() []rune { return []rune(f.text) }

// clamp pulls the cursor back inside the text after the text changed.
func (f *field) clamp() {
	length := len(f.runes())
	if f.cursor > length {
		f.cursor = length
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

func (f *field) home() { f.cursor = 0 }

func (f *field) end() { f.cursor = len(f.runes()) }

// move shifts the cursor by delta runes.
func (f *field) move(delta int) {
	f.cursor += delta
	f.clamp()
}

// wordLeft puts the cursor at the start of the word before it, the way Emacs does: over the
// separators first, then over the word itself.
func (f *field) wordLeft() { f.cursor = wordStart(f.runes(), f.cursor) }

// wordRight puts the cursor just past the word after it.
func (f *field) wordRight() { f.cursor = wordEnd(f.runes(), f.cursor) }

// insert puts text at the cursor and leaves the cursor behind it.
func (f *field) insert(s string) {
	f.clamp()
	r := f.runes()
	word := []rune(s)
	out := make([]rune, 0, len(r)+len(word))
	out = append(out, r[:f.cursor]...)
	out = append(out, word...)
	out = append(out, r[f.cursor:]...)
	f.text = string(out)
	f.cursor += len(word)
}

// backspace removes the character before the cursor.
func (f *field) backspace() {
	f.clamp()
	r := f.runes()
	if f.cursor == 0 {
		return
	}
	f.cursor--
	f.text = string(append(r[:f.cursor], r[f.cursor+1:]...))
}

// deleteForward removes the character under the cursor, which is ctrl+d in Emacs.
func (f *field) deleteForward() {
	f.clamp()
	r := f.runes()
	if f.cursor >= len(r) {
		return
	}
	f.text = string(append(r[:f.cursor], r[f.cursor+1:]...))
}

// kill removes text[from:to] and returns it, so ctrl+y can put it back.  One slot, not a
// ring: a session name is short, and one slot is what readline offers most terminals.
func (f *field) kill(from, to int) string {
	r := f.runes()
	from = clampInt(from, 0, len(r))
	to = clampInt(to, from, len(r))
	killed := string(r[from:to])
	out := make([]rune, 0, len(r)-(to-from))
	out = append(out, r[:from]...)
	out = append(out, r[to:]...)
	f.text = string(out)
	f.cursor = from
	return killed
}

// killToEnd removes everything after the cursor (ctrl+k).
func (f *field) killToEnd() string {
	f.clamp()
	return f.kill(f.cursor, len(f.runes()))
}

// killToStart removes everything before the cursor (ctrl+u).
func (f *field) killToStart() string {
	f.clamp()
	return f.kill(0, f.cursor)
}

// killWordBack removes the word before the cursor (alt+backspace, ctrl+w).
func (f *field) killWordBack() string {
	f.clamp()
	return f.kill(wordStart(f.runes(), f.cursor), f.cursor)
}

// killWordForward removes the word after the cursor (alt+d).
func (f *field) killWordForward() string {
	f.clamp()
	return f.kill(f.cursor, wordEnd(f.runes(), f.cursor))
}

func wordStart(r []rune, from int) int {
	from = clampInt(from, 0, len(r))
	for from > 0 && !wordRune(r[from-1]) {
		from--
	}
	for from > 0 && wordRune(r[from-1]) {
		from--
	}
	return from
}

func wordEnd(r []rune, from int) int {
	from = clampInt(from, 0, len(r))
	for from < len(r) && !wordRune(r[from]) {
		from++
	}
	for from < len(r) && wordRune(r[from]) {
		from++
	}
	return from
}

// wordRune is the word character a shell treats as part of a word, so a name like "sh2pil"
// moves in two steps ("pib", then "tui") and not in punctuation.
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
