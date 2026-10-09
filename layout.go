package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// placements are the real places a new terminal can go: a tab, an OS window of its own, a
// split beside the current pane, and the pane the picker itself runs in.
var placements = []string{"tab", "window", "pane", "current"}

// layouts are what ctrl+l cycles: every placement, and "ask", which asks each time instead of
// remembering one place.
var layouts = append(append([]string{}, placements...), "ask")

// defaultLayout is the pane the picker itself runs in: opening a session does not make a
// second window for it, and the picker is there again when the program exits.
const defaultLayout = "current"

// layoutFile is where the picker remembers the reader's choice.
//
// ctrl+l writes it when it cycles, so the choice survives a restart and every terminal that
// is opened later uses the same choice. The value is one word, and an unknown value falls
// back to the pane the picker itself runs in.
func layoutFile() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "sh2pil", "layout")
}

func readLayout() string {
	data, err := os.ReadFile(layoutFile())
	if err != nil {
		return defaultLayout
	}
	return knownLayout(strings.TrimSpace(string(data)))
}

func knownLayout(value string) string {
	for _, known := range layouts {
		if value == known {
			return value
		}
	}
	return defaultLayout
}

// writeLayout records the choice, and stays quiet when it cannot: a read-only state
// directory costs the reader the memory of the setting, nothing more.
func writeLayout(value string) {
	path := layoutFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(value+"\n"), 0o644)
}

// harnessTitle names a store the way a reader sees it in a message.
func harnessTitle(harness string) string {
	switch harness {
	case "opencode":
		return "OpenCode"
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return "Pi"
}

func ignoredProjectsFile() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "sh2pil", "ignored-projects.json")
}

func readIgnoredProjects() map[string]bool {
	data, err := os.ReadFile(ignoredProjectsFile())
	if err != nil {
		return map[string]bool{}
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return map[string]bool{}
	}
	ignored := make(map[string]bool, len(paths))
	for _, path := range paths {
		ignored[filepath.Clean(path)] = true
	}
	return ignored
}

func writeIgnoredProjects(ignored map[string]bool) error {
	paths := make([]string, 0, len(ignored))
	for path := range ignored {
		paths = append(paths, filepath.Clean(path))
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := ignoredProjectsFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func harnessFile() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "sh2pil", "harness")
}

func knownHarness(value string) string {
	for _, known := range knownHarnesses {
		if value == known {
			return value
		}
	}
	return knownHarnesses[0]
}

func readHarness() string {
	data, err := os.ReadFile(harnessFile())
	if err != nil {
		return knownHarnesses[0]
	}
	return knownHarness(strings.TrimSpace(string(data)))
}

func writeHarness(value string) {
	path := harnessFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(knownHarness(value)+"\n"), 0o644)
}

// cycleHarness returns the next store in the given set, which is the set the target on screen
// has: a store a machine does not run is not offered there, so ctrl+b can never select a store
// that would answer with nothing.  The set is walked in the order the picker reads the stores in,
// so the cycle is the same on every machine whatever set each one has.
func cycleHarness(current string, available []string) string {
	if len(available) == 0 {
		return knownHarness(current)
	}
	at := -1
	for index, name := range available {
		if name == current {
			at = index
			break
		}
	}
	if at < 0 {
		// The store in force is not one this target has, so the first one it does have is the
		// honest next answer rather than a step off the end of a list the reader cannot see.
		return available[0]
	}
	return available[(at+1)%len(available)]
}

// cycleLayout returns the next place in the cycle.
func cycleLayout(current string) string {
	for index, known := range layouts {
		if current == known {
			return layouts[(index+1)%len(layouts)]
		}
	}
	return layouts[0]
}

// placementIndex returns where a placement sits in the ask menu, which numbers its choices
// from one, so the menu can open on the placement the picker would use anyway.
func placementIndex(value string) int {
	for index, known := range placements {
		if known == value {
			return index
		}
	}
	return 0
}

// describeLayout spells out what a placement means, because "window" and "tab" are exactly
// the words kitty uses for the opposite of what a reader expects.
func describeLayout(value string) string {
	switch value {
	case "window":
		return "window (its own OS window)"
	case "pane":
		return "new pane (same window)"
	case "current":
		return "same window / same pane"
	case "ask":
		return "ask each time"
	default:
		return "tab (a new tab)"
	}
}
