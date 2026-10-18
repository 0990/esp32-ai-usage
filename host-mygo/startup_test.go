package main

import "testing"

func TestStartupArgs(t *testing.T) {
	for _, tc := range []struct {
		name            string
		args            []string
		path            string
		preview, hidden bool
	}{
		{name: "manual", path: defaultConfigPath()},
		{name: "autostart", args: []string{"--autostart"}, path: defaultConfigPath(), hidden: true},
		{name: "custom config", args: []string{"--autostart", `D:\my configs\usage.json`}, path: `D:\my configs\usage.json`, hidden: true},
		{name: "preview", args: []string{"test.json", "--preview"}, path: "test.json", preview: true},
		{name: "silent preview", args: []string{"--preview", "--autostart", "test.json"}, path: "test.json", preview: true, hidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, preview, hidden := parseStartupArgs(tc.args)
			if path != tc.path || preview != tc.preview || hidden != tc.hidden {
				t.Fatalf("got (%q, %v, %v), want (%q, %v, %v)", path, preview, hidden, tc.path, tc.preview, tc.hidden)
			}
		})
	}
}
