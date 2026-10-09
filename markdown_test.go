package main

import (
	"reflect"
	"testing"
)

func TestRenderMarkdownStripsTranscriptANSI(t *testing.T) {
	plain := "## session preview\n\nSome content with **emphasis**."
	styled := "\x1b[0;90m## session preview\x1b[0m\n\nSome \x1b[1mcontent\x1b[0m with **emphasis**."
	if got, want := renderMarkdown(styled, 60), renderMarkdown(plain, 60); !reflect.DeepEqual(got, want) {
		t.Fatalf("ANSI-styled preview differs from plain Markdown:\n got: %#v\nwant: %#v", got, want)
	}
}
