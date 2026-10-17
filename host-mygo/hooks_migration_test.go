package main

import (
	"path/filepath"
	"testing"
)

func TestInstallingNativeHooksReplacesLegacyAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "http", "url": "http://127.0.0.1:52736/hook/claude"}}}, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}}}}}
	if err := writeJSONObject(path, legacy); err != nil {
		t.Fatal(err)
	}
	if err := updateHookFile(path, []string{"Stop"}, claudeHookHandler(), true); err != nil {
		t.Fatal(err)
	}
	obj, err := readJSONObject(path)
	if err != nil {
		t.Fatal(err)
	}
	groups := obj["hooks"].(map[string]any)["Stop"].([]any)
	if len(groups) != 2 {
		t.Fatal("legacy hook was retained alongside the native hook")
	}
	count := 0
	for _, group := range groups {
		if isOurGroup(group) {
			count++
		}
	}
	if count != 1 {
		t.Fatal("native hook must be installed once")
	}
	if err = updateHookFile(path, []string{"Stop"}, claudeHookHandler(), false); err != nil {
		t.Fatal(err)
	}
	obj, _ = readJSONObject(path)
	if len(obj["hooks"].(map[string]any)["Stop"].([]any)) != 1 {
		t.Fatal("removal changed unrelated hooks")
	}
}
