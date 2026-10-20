package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAppearanceChangeUpdatesAdaptiveColorsAndMarkdown(t *testing.T) {
	oldDark := lipgloss.HasDarkBackground()
	oldMarkdownStyle := markdownStyle
	t.Cleanup(func() {
		setColorScheme(map[bool]string{true: "dark", false: "light"}[oldDark])
		setSystemMarkdownStyle(oldMarkdownStyle)
	})

	m := &model{
		colorScheme: "dark", width: 100,
		preview: preview{id: "session-1", width: 40, source: []string{"# preview"}},
		cache: map[string]preview{"session-2": {id: "session-2", width: 40,
			source: []string{"# cached"}, lines: []string{"old theme"}}},
		wrapped: []string{"old theme"}, wrapID: "session-1", wrapWidth: 40,
	}
	m.applySystemAppearance("light")

	if lipgloss.HasDarkBackground() {
		t.Fatal("adaptive colors still use the dark palette")
	}
	if markdownStyle != "light" {
		t.Errorf("markdown style = %q, want light", markdownStyle)
	}
	if m.preview.width != m.previewWidth()-1 || m.wrapID != m.preview.id || strings.Join(m.wrapped, "") == "old theme" {
		t.Errorf("preview was not re-rendered for the new theme: preview=%+v wrapped=%v", m.preview, m.wrapped)
	}
	if cached := m.cache["session-2"]; cached.width != 0 || cached.lines != nil {
		t.Errorf("cached preview kept the old theme: %+v", cached)
	}
}

func TestAppearanceChangeKeepsExplicitMarkdownStyle(t *testing.T) {
	oldDark := lipgloss.HasDarkBackground()
	oldMarkdownStyle := markdownStyle
	t.Cleanup(func() {
		setColorScheme(map[bool]string{true: "dark", false: "light"}[oldDark])
		setSystemMarkdownStyle(oldMarkdownStyle)
	})
	markdownStyle = "dracula"

	m := &model{colorScheme: "dark", mdStyleOverride: true}
	m.applySystemAppearance("light")

	if lipgloss.HasDarkBackground() {
		t.Fatal("adaptive colors still use the dark palette")
	}
	if markdownStyle != "dracula" {
		t.Errorf("explicit markdown style = %q, want dracula", markdownStyle)
	}
}

func TestInvalidAppearanceChangeDoesNothing(t *testing.T) {
	oldDark := lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		setColorScheme(map[bool]string{true: "dark", false: "light"}[oldDark])
	})

	m := &model{colorScheme: "dark"}
	m.applySystemAppearance("unknown")
	if !lipgloss.HasDarkBackground() {
		t.Fatal("invalid appearance changed the active color scheme")
	}
	if m.colorScheme != "dark" {
		t.Fatalf("model appearance = %q, want dark", m.colorScheme)
	}
}
