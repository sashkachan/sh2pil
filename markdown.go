package main

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
)

// markdownStyle names the glamour style. It is set from --md-style, or system appearance.
//
// It is deliberately not glamour.WithAutoStyle: that asks the terminal for its background
// colour, and inside a Bubble Tea program the reply is read by the UI as typed input, which
// showed up as a filter full of "rgb:0000/0000/0000" and a list that matched nothing, while
// the renderer waited for a timeout.  A program that owns stdin must not let a library query
// the terminal.
var markdownStyle = "dark"

// markdownCache holds one glamour renderer per pane width, shared by the preview goroutine
// and the UI.
//
// Building a renderer is cheap once the style is known, and it is rebuilt only when the word
// wrap changes with the pane width.  The mutex makes the shared renderer safe from the
// goroutine that reads a transcript while the UI is drawing.
var markdownCache = struct {
	sync.Mutex
	renderer *glamour.TermRenderer
	width    int
}{}

// renderMarkdown renders markdown to styled lines, or returns the plain lines when the text,
// the width, or the terminal makes rendering pointless or impossible.
func renderMarkdown(text string, width int) []string {
	// Pi's transcript renderer can include ANSI color codes. Strip those before
	// Glamour wraps the Markdown, or the escape sequences can distort preview layout.
	text = ansi.Strip(text)
	plain := func() []string { return strings.Split(text, "\n") }
	// glamour needs a little room; below that the pane shows raw markdown, which is still
	// readable and beats squeezing every heading into nothing.
	if width < 24 || strings.TrimSpace(text) == "" {
		return plain()
	}
	markdownCache.Lock()
	defer markdownCache.Unlock()
	if markdownCache.renderer == nil || markdownCache.width != width {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(markdownStyle),
			glamour.WithWordWrap(width),
			glamour.WithPreservedNewLines(),
		)
		if err != nil {
			return plain()
		}
		markdownCache.renderer, markdownCache.width = renderer, width
	}
	rendered, err := markdownCache.renderer.Render(text)
	if err != nil {
		return plain()
	}
	return strings.Split(strings.TrimRight(rendered, "\n"), "\n")
}
