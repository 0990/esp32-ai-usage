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

func TestOverlayDefaultsAndSavedStyleSizes(t *testing.T) {
	for _, tc := range []struct {
		name, raw, style string
		width, height    int
	}{
		{"new", `{}`, "horizontal", 136, 24},
		{"legacy", `{"overlay_width":420,"overlay_height":240,"overlay_x":-350,"overlay_position_set":true}`, "horizontal", 136, 24},
		{"removed_nano", `{"overlay_style":"nano","overlay_width":120,"overlay_height":56,"overlay_opacity":75,"overlay_x":-350,"overlay_position_set":true}`, "horizontal", 136, 24},
		{"horizontal", `{"overlay_style":"horizontal","overlay_width":136,"overlay_height":24}`, "horizontal", 136, 24},
		{"vertical", `{"overlay_style":"vertical","overlay_width":44,"overlay_height":88}`, "vertical", 44, 88},
		{"resized_vertical", `{"overlay_style":"vertical","overlay_width":50,"overlay_height":100}`, "vertical", 50, 100},
		{"paper", `{"overlay_style":"paper"}`, "paper", 296, 166},
		{"obsidian", `{"overlay_style":"obsidian"}`, "obsidian", 348, 150},
		{"strip", `{"overlay_style":"strip","overlay_width":420,"overlay_height":200}`, "strip", 420, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := loadConfig(path)
			if cfg.OverlayStyle != tc.style || cfg.OverlayWidth != tc.width || cfg.OverlayHeight != tc.height {
				t.Fatalf("unexpected style/size: %s %dx%d", cfg.OverlayStyle, cfg.OverlayWidth, cfg.OverlayHeight)
			}
			if tc.name == "legacy" && (cfg.OverlayX != -350 || !cfg.OverlayPositionSet) {
				t.Fatal("default style migration lost saved screen position")
			}
			if tc.name == "removed_nano" && (cfg.OverlayX != -350 || !cfg.OverlayPositionSet || cfg.OverlayOpacity != 75) {
				t.Fatal("removed style migration lost position or opacity")
			}
			if err := saveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			reloaded := loadConfig(path)
			if reloaded.OverlayStyle != tc.style || reloaded.OverlayWidth != tc.width || reloaded.OverlayHeight != tc.height {
				t.Fatal("style/size changed after saving and restarting")
			}
		})
	}
}

func TestOverlayStyleSwitchRestoresCompactSize(t *testing.T) {
	e := NewEngine(defaultConfig(), filepath.Join(t.TempDir(), "config.json"))
	for _, style := range overlayStyleIDs {
		if err := e.SetOverlayStyle(style); err != nil {
			t.Fatal(err)
		}
		if err := e.SetOverlayGeometry(1, 1, 100); err != nil {
			t.Fatal(err)
		}
		c := loadConfig(e.cfgPath)
		d := overlayDesignFor(style)
		if c.OverlayWidth != int(d.minWidth) || c.OverlayHeight != int(d.minHeight) {
			t.Fatalf("%s: style-specific size limits not respected: %dx%d", style, c.OverlayWidth, c.OverlayHeight)
		}
	}
	if len(e.reload) != 0 {
		t.Fatal("style and size changes must not trigger usage requests")
	}
}
