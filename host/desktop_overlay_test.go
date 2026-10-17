package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	for _, style := range overlayStyleIDs {
		cfg := defaultConfig()
		cfg.OverlayStyle = style
		cfg.OverlayWidth = 0
		cfg.OverlayHeight = 0
		o.applyConfig(cfg)
		r := test.WidgetRenderer(o.view).(*overlayViewRenderer)
		o.update(Frame{Source: "codex", Kind: "pct", P1: 73, S1: "18:30", P2: -1, S2: "old reset"})
		if r.title.Text != "Codex" || r.values[0].Text != "73" || r.units[0].Text != "%" || r.status.Visible() || !strings.Contains(r.resets[0].Text, "18:30") {
			t.Fatalf("%s: quota/reset text not shown", style)
		}
		if strings.Contains(r.resets[1].Text, "old reset") || r.fills[1].Visible() {
			t.Fatalf("%s: missing quota displayed old data", style)
		}
		o.update(Frame{Source: "claude", Kind: "money", L1: "Today", V1: "$3", V2: "$12"})
		if r.values[1].Text != "$12" || r.tracks[0].Visible() || r.units[0].Visible() {
			t.Fatalf("%s: relay page must show balances", style)
		}
		o.update(Frame{Source: "My station", Kind: "money", L1: "Balance", V1: "$12"})
		if r.title.Text != "My station" || r.values[0].Text != "$12" || r.values[1].Visible() {
			t.Fatalf("%s: custom balance transition failed", style)
		}
		o.update(Frame{Source: "reclaude", Kind: "pct", P1: 0, P2: 100, Old: true})
		if r.values[0].Text != "0" || r.values[1].Text != "100" || !r.tracks[0].Visible() || !r.status.Visible() || r.status.Text != "数据已过期 · 正在重试更新" {
			t.Fatalf("%s: 0/100 percent or stale transition failed: %s / %s", style, r.values[0].Text, r.values[1].Text)
		}
		if r.fills[0].Size().Width != 0 || math.Abs(float64(r.fills[1].Size().Width-r.tracks[1].Size().Width)) > .01 {
			t.Fatal("quota fill did not match 0/100%")
		}
	}
}

func TestDesktopOverlayAppearancePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.DesktopOverlay = true
	e := NewEngine(cfg, path)
	for _, setting := range []struct {
		scale int
		layer string
	}{{70, "bottom"}, {160, "top"}} {
		if err := e.SetOverlayAppearance(setting.scale, setting.layer); err != nil {
			t.Fatal(err)
		}
		loaded := loadConfig(path)
		if loaded.OverlayScale != setting.scale || loaded.OverlayLayer != setting.layer || !loaded.DesktopOverlay {
			t.Fatalf("appearance did not persist or lost the display switch: %+v", loaded)
		}
		if len(e.reload) != 0 {
			t.Fatal("appearance changes must not trigger usage requests")
		}
	}
	e.cfgPath = filepath.Dir(path)
	if err := e.SetOverlayAppearance(70, "bottom"); err == nil || e.Config().OverlayScale != 160 || e.Config().OverlayLayer != "top" {
		t.Fatal("failed save must preserve active appearance")
	}
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"desktop_overlay":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if loaded := loadConfig(legacy); loaded.OverlayScale != 100 || loaded.OverlayLayer != "top" {
		t.Fatal("legacy configs must default to compact size and top layer")
	}
}

func TestDesktopOverlayCompactSizing(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	o := newDesktopOverlayWindow(a.NewWindow("test"), func() {}, func(error) {})
	o.update(Frame{Source: "codex", Kind: "pct", P1: 98, S1: "15:17", P2: 72, S2: "10-23 14:08"})
	if o.win.FixedSize() || o.win.Padded() {
		t.Fatal("overlay must update its rendering viewport on resize and have no window padding")
	}
	for _, dimensions := range [][2]int{{330, 132}, {420, 132}, {420, 240}, {280, 140}} {
		cfg := defaultConfig()
		cfg.OverlayWidth, cfg.OverlayHeight = dimensions[0], dimensions[1]
		o.applyConfig(cfg)
		size := o.win.Canvas().Size()
		if size.Width != float32(dimensions[0]) || size.Height != float32(dimensions[1]) {
			t.Fatalf("width and height must be independent: requested=%v actual=%+v", dimensions, size)
		}
		r := test.WidgetRenderer(o.view).(*overlayViewRenderer)
		for i, bar := range r.tracks {
			if !bar.Visible() || bar.Size().Width < 40 || bar.Size().Height > 5 {
				t.Fatalf("quota bar %d clipped at %v: %+v", i, dimensions, bar.Size())
			}
		}
		o.update(Frame{Source: "codex", Kind: "pct", P1: 60, P2: 70})
		if o.win.Canvas().Size() != size {
			t.Fatal("refresh changed the user's dimensions")
		}
	}
	if o.content.Theme.Color("background", 0) != overlayDesignFor("strip").background {
		t.Fatal("overlay must have a solid card background")
	}
	count := 0
	o.x, o.y, o.positioned = -350, 200, true
	o.onMoved = func(x, y int32) error {
		count++
		if x != -350 || y != 200 {
			t.Error("drag position lost")
		}
		return nil
	}
	o.dragOrigin, o.dragMoved = &overlayDragOrigin{}, true
	o.endDrag()
	o.endDrag()
	if count != 1 {
		t.Fatal("drag completion should save position once")
	}
}

func TestDesktopOverlayStylePersistenceAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	e := NewEngine(defaultConfig(), path)
	if e.Config().OverlayStyle != "strip" {
		t.Fatal("default style must be the minimal strip")
	}
	if err := e.SetOverlayPosition(-350, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := e.SetOverlayAppearance(100, "bottom"); err != nil {
		t.Fatal(err)
	}
	for _, style := range overlayStyleIDs {
		if err := e.SetOverlayStyle(style); err != nil {
			t.Fatal(err)
		}
		loaded := loadConfig(path)
		d := overlayDesignFor(style)
		if loaded.OverlayStyle != style || loaded.OverlayWidth != int(d.width) || loaded.OverlayHeight != int(d.height) || loaded.OverlayX != -350 || loaded.OverlayLayer != "bottom" {
			t.Fatalf("style selection lost other settings: %+v", loaded)
		}
	}
	if len(e.reload) != 0 {
		t.Fatal("style changes triggered network requests")
	}
	before := e.Config()
	e.cfgPath = filepath.Dir(path)
	if e.SetOverlayStyle("paper") == nil || !reflect.DeepEqual(before, e.Config()) {
		t.Fatal("failed save must preserve active style and size")
	}
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"provider":"claude","overlay_width":420,"overlay_height":240,"overlay_opacity":50,"overlay_x":-350,"overlay_y":200,"overlay_position_set":true,"overlay_layer":"bottom"}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := loadConfig(legacy)
	if loaded.OverlayStyle != "strip" || loaded.OverlayWidth != 330 || loaded.OverlayHeight != 132 || loaded.OverlayOpacity != 100 || loaded.OverlayX != -350 || loaded.Provider != "claude" || loaded.OverlayLayer != "bottom" {
		t.Fatalf("legacy migration failed: %+v", loaded)
	}
}

func TestDesktopOverlayStyleLayoutsAtMinimumSize(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	o := newDesktopOverlayWindow(a.NewWindow("test"), func() {}, func(error) {})
	for _, style := range overlayStyleIDs {
		cfg := defaultConfig()
		cfg.OverlayStyle = style
		cfg.OverlayWidth = 264
		cfg.OverlayHeight = int(overlayDesignFor(style).minHeight)
		o.update(Frame{Source: "codex", Kind: "pct", P1: 100, P2: 0, S1: "15:17", S2: "10-23 14:08"})
		o.applyConfig(cfg)
		r := test.WidgetRenderer(o.view).(*overlayViewRenderer)
		if r.values[0].Text != "100" || r.labels[0].Text != "5小时" || r.fills[0].Size().Width <= 0 || r.tracks[0].Size().Height > 5 {
			t.Fatalf("minimum %s layout clipped core data", style)
		}
		if style == "paper" && r.tracks[0].Position().Y >= r.tracks[1].Position().Y {
			t.Fatal("paper must stack quota rows")
		}
		if style == "obsidian" && (r.tracks[0].Position().Y != r.tracks[1].Position().Y || !r.separator.Visible()) {
			t.Fatal("obsidian must have two side-by-side columns")
		}
		if style == "strip" && (r.tracks[0].Position().X <= r.resets[0].Position().X || r.values[0].Position().X <= r.tracks[0].Position().X+r.tracks[0].Size().Width) {
			t.Fatal("strip must align label, thin bar and value in each row")
		}
	}
}

func TestDesktopOverlayGeometryPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	e := NewEngine(defaultConfig(), path)
	if err := e.SetOverlayPosition(-350, 200, true); err != nil {
		t.Fatal(err)
	}
	if err := e.SetOverlayGeometry(420, 240, 75); err != nil {
		t.Fatal(err)
	}
	if err := e.SetOverlayAppearance(100, "bottom"); err != nil {
		t.Fatal(err)
	}
	loaded := loadConfig(path)
	if loaded.OverlayWidth != 420 || loaded.OverlayHeight != 240 || loaded.OverlayOpacity != 75 || loaded.OverlayX != -350 || loaded.OverlayY != 200 || !loaded.OverlayPositionSet || loaded.OverlayLayer != "bottom" {
		t.Fatalf("size, transparency, layer and negative monitor position must coexist: %+v", loaded)
	}
	if len(e.reload) != 0 {
		t.Fatal("drag/resize must not trigger usage requests")
	}
	e.cfgPath = filepath.Dir(path)
	if e.SetOverlayGeometry(260, 130, 90) == nil || e.SetOverlayPosition(0, 0, false) == nil {
		t.Fatal("failed writes should report errors")
	}
	if !reflect.DeepEqual(e.Config(), loaded) {
		t.Fatal("failed save changed active geometry")
	}
}
