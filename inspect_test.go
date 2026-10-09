package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPrintConfigurationShowsPerHostTools pins that a per-host tool is visible where a
// reader looks for it, and that a host with no override adds no line.
func TestPrintConfigurationShowsPerHostTools(t *testing.T) {
	writeConfig(t, "tools:\n  hosts:\n    build-host:\n      file_browser: lf\n      git_tool: tig\n")
	var out bytes.Buffer
	printConfiguration(&out, loadConfig())
	if !strings.Contains(out.String(), "tools.hosts.build-host: file_browser=lf, git_tool=tig") {
		t.Fatalf("printConfiguration did not show the host tools:\n%s", out.String())
	}
	if strings.Contains(out.String(), "tools.hosts.gpu-host") {
		t.Fatal("printConfiguration invented a host")
	}
}
