package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBrightnessConfig(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{`{}`, 143}, {`{"oled_brightness":0}`, 0}, {`{"oled_brightness":255}`, 255},
		{`{"oled_brightness":-10}`, 0}, {`{"oled_brightness":300}`, 255},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		cfg := loadConfig(path)
		if cfg.OLEDBrightness != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.raw, cfg.OLEDBrightness, tc.want)
		}
		if err := saveConfig(path, cfg); err != nil {
			t.Fatal(err)
		}
		if loadConfig(path).OLEDBrightness != tc.want {
			t.Fatal("brightness not persisted")
		}
	}
}

func TestBrightnessCommandBeforeUsage(t *testing.T) {
	e := NewEngine(defaultConfig(), filepath.Join(t.TempDir(), "config.json"))
	e.ctx = context.Background()
	cfg := e.Config()
	cfg.OLEDBrightness = 0
	if err := e.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-e.writeCh:
		var cmd struct {
			Command    string `json:"cmd"`
			Brightness *int   `json:"brightness"`
		}
		if err := json.Unmarshal(raw, &cmd); err != nil {
			t.Fatal(err)
		}
		if cmd.Command != "display" || cmd.Brightness == nil || *cmd.Brightness != 0 {
			t.Fatalf("unexpected command %s", raw)
		}
	default:
		t.Fatal("saving did not send brightness before first usage result")
	}
	if e.started {
		t.Fatal("display settings created a usage frame")
	}
	// The same command is sent after opening/reopening a serial connection.
	e.sendDisplaySettings()
	if raw := <-e.writeCh; len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("invalid serial command")
	}
}

func TestRenderedFrameCarriesBrightness(t *testing.T) {
	e := NewEngine(defaultConfig(), "")
	e.ctx = context.Background()
	e.started = true
	e.cfg.OLEDBrightness = 0
	e.renderCurrent(false)
	var f Frame
	if err := json.Unmarshal(<-e.writeCh, &f); err != nil {
		t.Fatal(err)
	}
	if f.Brightness == nil || *f.Brightness != 0 || f.Icon != "codex" {
		t.Fatalf("brightness missing from frame: %+v", f)
	}
	// Old test frames can omit brightness without inadvertently setting it to zero.
	if err := json.Unmarshal([]byte(`{"ic":"codex","k":"pct"}`), &f); err != nil {
		t.Fatal(err)
	}
	// Decode into a fresh value, as the firmware does for every frame.
	var old Frame
	if err := json.Unmarshal([]byte(`{"ic":"codex","k":"pct"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Brightness != nil {
		t.Fatal("omitted brightness must preserve device state")
	}
}
