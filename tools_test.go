package main

import (
	"reflect"
	"testing"
)

// TestRunToolArgsUsesTheConfiguredTool pins that a configured directory tool reaches the
// helper as a command with its own arguments.
func TestRunToolArgsUsesTheConfiguredTool(t *testing.T) {
	m := &model{}
	args, note := m.runToolArgs("jj --no-pager", defaultGitTool, "/tmp/repo", "tab")
	want := []string{"run-tool", "--label", "jj repo", "--place", "tab", "/tmp/repo",
		"jj", "--no-pager"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("runToolArgs = %#v, want %#v", args, want)
	}
	if note != "opened jj in repo" {
		t.Fatalf("note = %q, want opened jj in repo", note)
	}
}

// TestRunToolArgsFallsBackToTheDefault pins that a model without a configured tool still
// produces the default command, which is what a hand-built test model and a first run need.
func TestRunToolArgsFallsBackToTheDefault(t *testing.T) {
	m := &model{}
	args, _ := m.runToolArgs("", defaultFileBrowser, "/tmp/repo", "tab")
	if len(args) == 0 || args[len(args)-1] != "yazi" {
		t.Fatalf("runToolArgs fallback = %#v, want the default yazi last", args)
	}
}

// TestToolConfigKeys pins the tool settings and their defaults.
func TestToolConfigKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeConfig(t, "file_browser: ranger\ngit_tool: jj")
	cfg := loadConfig()
	if cfg.FileBrowser != "ranger" || cfg.GitTool != "jj" {
		t.Fatalf("tools = %q and %q, want ranger and jj", cfg.FileBrowser, cfg.GitTool)
	}
	if cfg.Editor != "" {
		t.Fatalf("editor = %q, want the empty default", cfg.Editor)
	}
	t.Setenv("HOME", t.TempDir())
	cfg = loadConfig()
	if cfg.FileBrowser != defaultFileBrowser || cfg.GitTool != defaultGitTool {
		t.Fatalf("default tools = %q and %q", cfg.FileBrowser, cfg.GitTool)
	}
}
