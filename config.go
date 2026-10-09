package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const defaultCloseOnNavigate = true

// defaultStatePoll is how often the picker asks what a running chat is doing.  The cheap read
// costs about 70 ms on a machine with a dozen registered chats, against 346 ms for the full
// live read, so this is the interval that read can afford.  Zero turns the clock off and leaves
// the state a snapshot of the last action.
const defaultStatePoll = 3.0

// The default directory tools.  The editor has its own flag and an $EDITOR fallback, so it
// needs no constant here; these two are the tools the picker runs on a project directory.
const (
	defaultFileBrowser = "yazi"
	defaultGitTool     = "lazygit"
)

// defaultMode is how the primary keys act: keybind runs the action directly, prompt opens the
// next-step menu, and both keeps the keys and offers the menu on its own key.
const defaultMode = "keybind"

// defaultAttentionSort lifts the rows that wait on a person above the rest of their group.  It
// is on by default because the picker exists to answer "which of my chats is waiting for me?";
// a reader who wants the plain read order sets attention_sort to false.
const defaultAttentionSort = true

// defaultNotifyMode is how a chat that starts waiting on a person is announced.  The default is
// a desktop notification (OSC 9), which is the one thing that reaches a reader who is not
// looking at the picker; off and bell are the quieter choices.
const defaultNotifyMode = "terminal"

// knownNotifyModes are the accepted values of the notify setting.
var knownNotifyModes = map[string]bool{"off": true, "bell": true, "terminal": true}

// knownModes are the accepted values of the mode setting.
var knownModes = map[string]bool{"keybind": true, "prompt": true, "both": true}

// knownTriggers are the accepted values of a trigger setting.
var knownTriggers = map[string]bool{"key": true, "prompt": true}

// knownHarnesses is every store the picker can read, in the order the stores were added, so
// the columns, the header flags, and the cycling read the same way on every machine.  A store
// that a target does not have is still known here: the target says so, and its rows simply do
// not arrive.
var knownHarnesses = []string{"pi", "opencode", "claude", "codex"}

// isKnownHarness reports whether the picker can read a store by this name.
func isKnownHarness(name string) bool {
	for _, known := range knownHarnesses {
		if name == known {
			return true
		}
	}
	return false
}

// The two lists the picker shows, one per pane of the left column: the project groups with
// the sessions under them, and the live zmx sessions of the same target.  The targets have
// their own pair, { and }, so the zmx list is a pane rather than a view to cycle to, and both
// are live at once.
const (
	viewSessions = "sessions" // the project groups, and the sessions under them
	viewZmx      = "zmx"      // the live zmx sessions of the same target
)

// configPathOverride is the file named by --config or PIB_CONFIG.  The test suites and a
// second profile use it; when it is empty the shared file is used.
var configPathOverride string

// configFile is the settings file the picker shares with pib-open.  Each program reads the
// keys it knows and ignores the rest, so one file holds both.
func configFile() string {
	if configPathOverride != "" {
		return configPathOverride
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "sh2pil", "config.yaml")
}

// configChain is every file that contributes settings, in the order later files override
// earlier ones: the main file, then each config.d/*.yaml in name order.  The overlays let a
// machine or a session change a setting without rewriting the hand-written, tracked file.
func configChain() []string {
	main := configFile()
	if main == "" {
		return nil
	}
	chain := []string{main}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(main), "config.d"))
	if err != nil {
		return chain
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		chain = append(chain, filepath.Join(filepath.Dir(main), "config.d", name))
	}
	return chain
}

// Config is the effective settings for one run.  Values come from defaults, then the main
// file, then each overlay, so a later line wins.  Values holds every scalar that was read,
// including the keys pib-open owns, so inspection can show the whole file.
type Config struct {
	Harnesses       []string
	DefaultView     string
	DefaultTarget   string
	CloseOnNavigate bool
	Zmx             bool
	StatePoll       float64 // seconds between state reads; 0 follows it only on an action
	AttentionSort   bool    // lift the rows that wait on a person above the rest of their group
	Notify          string  // off, bell, or terminal: how a chat that starts waiting is announced
	ZmxServers      []string
	// The directory tools this machine runs, from editor, file_browser, git_tool, and shell.
	Editor      string
	FileBrowser string
	GitTool     string
	Shell       string
	// Mode and Triggers decide how an action is reached: by its key, from the next-step menu,
	// or both.  A trigger names one action and overrides the mode for that action.
	Mode      string
	Triggers  map[string]string
	Keys      map[string]string // action name -> configured chord list
	Values    map[string]string
	Sources   []string
	Warnings  []string
	KeyErrors []string
}

// knownConfigKeys is the union of the keys sh2pil and pib-open read from the shared file.
// A key outside it is a warning, not an error: the file is shared, and a newer program may
// write a key this build does not know yet.
var knownConfigKeys = map[string]bool{
	"harnesses": true, "default_view": true, "default_target": true,
	"close_on_navigate": true, "zmx": true, "zmx_servers": true,
	"zmx_remote_binary": true, "zoxide_remote_binary": true,
	"editor": true, "file_browser": true, "git_tool": true, "shell": true,
	"mode": true, "ssh_env": true, "state_poll_seconds": true,
	"attention_sort": true, "notify": true,
}

// knownConfigPrefixes are the keys a program owns as a family rather than one setting: the
// picker owns key.<context>.<action> and trigger.<action>, and the helper owns one
// environment set per host under ssh_env.<destination>.
var knownConfigPrefixes = []string{"key.", "trigger.", "ssh_env."}

// loadConfig reads every file in the chain and builds the effective configuration.
func loadConfig() Config {
	cfg := Config{
		Harnesses:       append([]string{}, knownHarnesses...),
		DefaultView:     viewSessions,
		CloseOnNavigate: defaultCloseOnNavigate,
		StatePoll:       defaultStatePoll,
		AttentionSort:   defaultAttentionSort,
		Notify:          defaultNotifyMode,
		Mode:            defaultMode,
		Keys:            map[string]string{},
		Triggers:        map[string]string{},
		Values:          map[string]string{},
	}
	for _, path := range configChain() {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: %v", path, err))
			}
			continue
		}
		cfg.Sources = append(cfg.Sources, path)
		for number, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
			if line == "" {
				continue
			}
			name, raw, ok := strings.Cut(line, ":")
			if !ok {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf("%s:%d: not a key: value line", path, number+1))
				continue
			}
			cfg.Values[strings.TrimSpace(name)] = strings.TrimSpace(raw)
		}
	}
	cfg.Harnesses = parseHarnesses(cfg.Values["harnesses"])
	if raw, ok := cfg.Values["default_view"]; ok && (raw == viewSessions || raw == viewZmx) {
		cfg.DefaultView = raw
	}
	if raw, ok := cfg.Values["close_on_navigate"]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			cfg.CloseOnNavigate = parsed
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("close_on_navigate: %q is not true or false; using %t", raw, defaultCloseOnNavigate))
		}
	}
	if raw, ok := cfg.Values["state_poll_seconds"]; ok {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed >= 0 {
			cfg.StatePoll = parsed
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("state_poll_seconds: %q is not a number of seconds; using %g", raw, defaultStatePoll))
		}
	}
	if raw, ok := cfg.Values["attention_sort"]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			cfg.AttentionSort = parsed
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("attention_sort: %q is not true or false; using %t", raw, defaultAttentionSort))
		}
	}
	if raw, ok := cfg.Values["notify"]; ok {
		value := strings.ToLower(strings.TrimSpace(raw))
		if knownNotifyModes[value] {
			cfg.Notify = value
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("notify: %q is not off, bell, or terminal; using %s", raw, defaultNotifyMode))
		}
	}
	if raw, ok := cfg.Values["zmx"]; ok {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			cfg.Zmx = parsed
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("zmx: %q is not true or false; using false", raw))
		}
	}
	if raw, found := cfg.Values["zmx_servers"]; found {
		cfg.ZmxServers = splitList(raw)
	}
	cfg.Editor = strings.TrimSpace(cfg.Values["editor"])
	cfg.FileBrowser = defaultFileBrowser
	if raw := strings.TrimSpace(cfg.Values["file_browser"]); raw != "" {
		cfg.FileBrowser = raw
	}
	cfg.GitTool = defaultGitTool
	if raw := strings.TrimSpace(cfg.Values["git_tool"]); raw != "" {
		cfg.GitTool = raw
	}
	cfg.Shell = strings.TrimSpace(cfg.Values["shell"])
	if raw := strings.TrimSpace(cfg.Values["mode"]); raw != "" {
		if knownModes[raw] {
			cfg.Mode = raw
		} else {
			cfg.Warnings = append(cfg.Warnings,
				fmt.Sprintf("mode: %q is not keybind, prompt, or both; using %s", raw, defaultMode))
		}
	}
	cfg.DefaultTarget = readDefaultTargetFrom(cfg.Values["default_target"], cfg.ZmxServers)
	for name := range cfg.Values {
		if strings.HasPrefix(name, "key.") {
			action := strings.TrimPrefix(name, "key.")
			if _, known := keyActionByName(action); !known {
				message := fmt.Sprintf("key.%s: no action by that name", action)
				cfg.Warnings = append(cfg.Warnings, message)
				cfg.KeyErrors = append(cfg.KeyErrors, message)
				continue
			}
			cfg.Keys[action] = cfg.Values[name]
			continue
		}
		if strings.HasPrefix(name, "trigger.") {
			action := strings.TrimPrefix(name, "trigger.")
			if _, known := keyActionByName(action); !known {
				message := fmt.Sprintf("trigger.%s: no action by that name", action)
				cfg.Warnings = append(cfg.Warnings, message)
				cfg.KeyErrors = append(cfg.KeyErrors, message)
				continue
			}
			value := strings.TrimSpace(cfg.Values[name])
			if !knownTriggers[value] {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf("trigger.%s: %q is not key or prompt; ignored", action, value))
				continue
			}
			cfg.Triggers[action] = value
		}
	}
	for name := range cfg.Values {
		if knownConfigKeys[name] || knownConfigPrefix(name) {
			continue
		}
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s: unknown key, ignored", name))
	}
	sort.Strings(cfg.Warnings)
	return cfg
}

// keyActionByName reports whether the registry knows an action.
func keyActionByName(name string) (keyAction, bool) {
	for _, action := range keyActions {
		if action.name == name {
			return action, true
		}
	}
	return keyAction{}, false
}

// knownConfigPrefix reports whether a key belongs to a family one program owns.
func knownConfigPrefix(name string) bool {
	for _, prefix := range knownConfigPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parseHarnesses returns the stores the picker reads: the configured set when it names any
// store the picker knows, and every known store otherwise.  The configured order is kept.
func parseHarnesses(value string) []string {
	if value == "" {
		return append([]string{}, knownHarnesses...)
	}
	seen := map[string]bool{}
	chosen := make([]string, 0, len(knownHarnesses))
	for _, name := range splitList(value) {
		if name == "" || seen[name] || !isKnownHarness(name) {
			continue
		}
		seen[name] = true
		chosen = append(chosen, name)
	}
	if len(chosen) == 0 {
		return append([]string{}, knownHarnesses...)
	}
	return chosen
}

// splitList splits a comma- or space-separated scalar, keeping order and dropping duplicates.
func splitList(value string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// configValue returns the value of one scalar key from the merged chain, with any trailing
// comment removed.  The last setting of a key wins, so a later file or line overrides one.
func configValue(key string) (string, bool) {
	cfg := loadConfig()
	value, found := cfg.Values[key]
	return value, found
}

func readCloseOnNavigate() bool {
	value, found := configValue("close_on_navigate")
	if !found {
		return defaultCloseOnNavigate
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultCloseOnNavigate
	}
	return parsed
}

// readHarnesses returns the stores the picker reads on every target.
func readHarnesses() []string {
	value, found := configValue("harnesses")
	if !found {
		return append([]string{}, knownHarnesses...)
	}
	return parseHarnesses(value)
}

// readDefaultTarget returns the host the picker opens on, as the destination the config
// names, or an empty string for this machine.  A setting that names no configured host gives
// this machine, so a stale alias cannot leave the picker on a list it cannot read.
func readDefaultTarget() string {
	value, _ := configValue("default_target")
	return readDefaultTargetFrom(value, readZmxServers())
}

// readDefaultTargetFrom is readDefaultTarget over an already-read list, so loadConfig does
// not read the files a second time.
func readDefaultTargetFrom(value string, servers []string) string {
	if value == "" {
		return ""
	}
	for _, server := range servers {
		if strings.EqualFold(value, server) {
			return server
		}
	}
	return ""
}

// readZmxServers returns the SSH config aliases listed in zmx_servers. Commas and spaces
// are both separators, so a short scalar works with the shared config reader.
func readZmxServers() []string {
	value, found := configValue("zmx_servers")
	if !found {
		return nil
	}
	return splitList(value)
}
