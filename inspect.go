package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// printConfiguration writes the effective settings, which is what --print-config asks for.
func printConfiguration(out io.Writer, cfg Config) {
	fmt.Fprintln(out, "sources:")
	if len(cfg.Sources) == 0 {
		fmt.Fprintln(out, "  (none; every setting is at its default)")
	}
	for _, path := range cfg.Sources {
		fmt.Fprintln(out, "  "+path)
	}
	fmt.Fprintln(out, "harnesses: "+strings.Join(cfg.Harnesses, ", "))
	fmt.Fprintln(out, "default_view: "+cfg.DefaultView)
	fmt.Fprintln(out, "default_target: "+orLocal(cfg.DefaultTarget))
	fmt.Fprintln(out, fmt.Sprintf("close_on_navigate: %t", cfg.CloseOnNavigate))
	fmt.Fprintln(out, fmt.Sprintf("zmx: %t", cfg.Zmx))
	fmt.Fprintln(out, fmt.Sprintf("state_poll_seconds: %g", cfg.StatePoll))
	fmt.Fprintln(out, fmt.Sprintf("attention_sort: %t", cfg.AttentionSort))
	fmt.Fprintln(out, "notify: "+cfg.Notify)
	fmt.Fprintln(out, "zmx_servers: "+strings.Join(cfg.ZmxServers, ", "))
	fmt.Fprintln(out, "mode: "+cfg.Mode)
	fmt.Fprintln(out, "editor: "+cfg.Editor)
	fmt.Fprintln(out, "file_browser: "+cfg.FileBrowser)
	fmt.Fprintln(out, "git_tool: "+cfg.GitTool)
	fmt.Fprintln(out, "shell: "+cfg.Shell)
	if len(cfg.ToolHosts) > 0 {
		hosts := make([]string, 0, len(cfg.ToolHosts))
		for host := range cfg.ToolHosts {
			hosts = append(hosts, host)
		}
		sort.Strings(hosts)
		for _, host := range hosts {
			keys := make([]string, 0, len(cfg.ToolHosts[host]))
			for key := range cfg.ToolHosts[host] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			pairs := make([]string, 0, len(keys))
			for _, key := range keys {
				pairs = append(pairs, key+"="+cfg.ToolHosts[host][key])
			}
			fmt.Fprintln(out, "tools.hosts."+host+": "+strings.Join(pairs, ", "))
		}
	}
	fmt.Fprintf(out, "filter_hide: %t\n", cfg.FilterHide)
	fmt.Fprintf(out, "filter_keep: %t\n", cfg.FilterKeep)
	fmt.Fprintln(out, "filter_fields: "+strings.Join(cfg.FilterFields, ", "))
	if len(cfg.Values) == 0 {
		return
	}
	fmt.Fprintln(out, "values:")
	names := make([]string, 0, len(cfg.Values))
	for name := range cfg.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintln(out, "  "+name+": "+cfg.Values[name])
	}
}

// orLocal names this machine in a report.
func orLocal(target string) string {
	if target == "" {
		return "local"
	}
	return target
}

// checkConfiguration writes the effective settings, every warning, the resolved keybindings,
// and any conflict.  It returns the exit status: 2 when a binding cannot be honored, else 0.
func checkConfiguration(out io.Writer, cfg Config) int {
	printConfiguration(out, cfg)
	km := buildKeymap(cfg.Keys)
	fmt.Fprintln(out, "keybindings:")
	for _, line := range km.bindingTable() {
		fmt.Fprintln(out, "  "+line)
	}
	if len(cfg.Warnings) > 0 {
		fmt.Fprintln(out, "warnings:")
		for _, warning := range cfg.Warnings {
			fmt.Fprintln(out, "  "+warning)
		}
	}
	if len(km.conflicts) > 0 {
		fmt.Fprintln(out, "conflicts:")
		for _, conflict := range km.conflicts {
			fmt.Fprintln(out, "  "+conflict)
		}
	}
	if len(cfg.KeyErrors) > 0 || len(km.conflicts) > 0 {
		return 2
	}
	return 0
}
