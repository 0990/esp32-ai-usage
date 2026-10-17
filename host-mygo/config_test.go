package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigImportIsIsolatedAndOneTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".esp32-ai-usage", "config.json")
	c := defaultConfig()
	c.Provider = "claude"
	c.Autostart = true
	c.OverlayStyle = "paper"
	c.OverlayX = -350
	c.OverlayPositionSet = true
	if err := saveConfig(legacy, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(legacy)
	path := defaultConfigPath()
	if path == legacy {
		t.Fatal("config must use its own directory")
	}
	importLegacyConfig(path)
	loaded := loadConfig(path)
	if loaded.Provider != "claude" || loaded.Autostart || loaded.OverlayStyle != "paper" || loaded.OverlayX != -350 {
		t.Fatal("import lost settings or enabled a second autostart")
	}
	loaded.Provider = "kimi"
	if err := saveConfig(path, loaded); err != nil {
		t.Fatal(err)
	}
	importLegacyConfig(path)
	if loadConfig(path).Provider != "kimi" {
		t.Fatal("subsequent startup overwrote native settings")
	}
	after, _ := os.ReadFile(legacy)
	if string(before) != string(after) {
		t.Fatal("import changed the Fyne configuration")
	}
}
