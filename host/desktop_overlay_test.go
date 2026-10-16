package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func TestDesktopOverlaySwitchPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.Proxy = "http://127.0.0.1:7890"
	e := NewEngine(cfg, path)
	for _, enabled := range []bool{true, false} {
		if err := e.SetDesktopOverlay(enabled); err != nil {
			t.Fatal(err)
		}
		loaded := loadConfig(path)
		if loaded.DesktopOverlay != enabled || loaded.Proxy != cfg.Proxy {
			t.Fatalf("switch did not persist or changed unrelated config: %+v", loaded)
		}
		if len(e.reload) != 0 {
			t.Fatal("toggling overlay triggered a network refresh")
		}
	}
	// A failed write must preserve the active setting.
	e.cfgPath = filepath.Dir(path) // writing to a directory fails
	if err := e.SetDesktopOverlay(true); err == nil || e.Config().DesktopOverlay {
		t.Fatal("failed save must not enable overlay")
	}
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"provider":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if loadConfig(legacy).DesktopOverlay {
		t.Fatal("legacy config must default to overlay off")
	}
}

func TestDesktopDisplayWithoutSerial(t *testing.T) {
	cfg := defaultConfig()
	cfg.Provider = "codex,reclaude"
	e := NewEngine(cfg, "")
	e.ctx = context.Background()
	e.started = true
	e.codex = Provider{OK: true, H5: Window{Left: 73, Reset: "18:30"}, Wk: Window{Left: 41, Reset: "10-20 12:00"}}
	e.codexAt = time.Now()
	var got []Frame
	e.OnDisplay = func(f Frame) { got = append(got, f) }
	e.renderCurrent(false)
	e.renderCurrent(true)
	if len(got) != 2 || got[0].Source != "reclaude" || got[1].Source != "codex" || got[1].P1 != 73 {
		t.Fatalf("display did not receive provider pages without serial: %+v", got)
	}
	var wire map[string]any
	if err := json.Unmarshal(<-e.writeCh, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["Source"]; exists {
		t.Fatal("desktop provider metadata leaked into serial protocol")
	}
	// Previous good data remains visible, with an explicit stale warning.
	e.codexAt = time.Now().Add(-time.Hour)
	e.renderCurrent(false)
	if !got[2].Old {
		t.Fatal("expired quota must be marked stale on desktop")
	}
	// Custom relay pages use the station name and the new balance-only protocol.
	obj := CustomObject{ID: "station", Name: "My station", Type: "sub2api", Enabled: true}
	e.cfg.Provider = "none"
	e.cfg.CustomObjects = []CustomObject{obj}
	e.custom = map[string]customResult{obj.cacheKey(): {Relay: Relay{Balance: 12, Unit: "$", OK: true}, At: time.Now()}}
	e.renderCurrent(false)
	if got[3].Source != obj.Name || got[3].V1 != "$12" || got[3].Brightness == nil {
		t.Fatalf("custom relay metadata, balance or OLED brightness lost: %+v", got[3])
	}
}

func TestDesktopOverlayDataTransitions(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	o := newDesktopOverlayWindow(a.NewWindow("test"), func() {}, func(error) {})
	o.update(Frame{Source: "codex", Kind: "pct", P1: 73, S1: "18:30", P2: -1, S2: "old reset"})
	if o.title.Text != "Codex · 额度余量" || o.values[0].Text != "5 小时 · 剩余 73%" || o.resets[0].Text != "重置：18:30" {
		t.Fatal("quota/reset text not shown")
	}
	if o.resets[1].Text != "重置：--" || o.bars[1].Visible() {
		t.Fatal("missing quota must not display old resets or a quota bar")
	}
	o.update(Frame{Source: "claude", Kind: "money", L1: "Today", V1: "$3", V2: "$12"})
	if o.values[1].Text != "剩余余额：$12" || o.resets[0].Visible() || o.bars[0].Visible() {
		t.Fatal("relay page must show balances")
	}
	o.update(Frame{Source: "My station", Kind: "money", L1: "Balance", V1: "$12"})
	if o.title.Text != "My station · 中转站余额" || o.values[0].Text != "剩余余额：$12" || o.values[1].Visible() {
		t.Fatal("custom station must show its name and balance")
	}
	o.update(Frame{Source: "reclaude", Kind: "pct", P1: 0, P2: 100, Old: true})
	if !o.values[1].Visible() || !o.resets[0].Visible() || !o.bars[0].Visible() || o.values[0].Text != "5 小时 · 剩余 0%" || o.status.Text != "数据已过期 · 正在重试更新" {
		t.Fatal("relay -> subscription transition or stale warning failed")
	}
}
