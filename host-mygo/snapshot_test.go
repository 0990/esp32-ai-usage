package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeRenderSnapshots(t *testing.T) {
	dir := os.Getenv("MYGO_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set MYGO_SNAPSHOT_DIR to export native UI snapshots")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	save := func(name string, tt *ui.Tester) {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err = png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	g := testGUI(t)
	save("settings", ui.NewTester(g.view, 820, 820))
	g.editCustom(-1)
	save("custom-editor", ui.NewTester(g.view, 820, 820))
	g.editing = false
	g.tab = 1
	save("test-json", ui.NewTester(g.view, 820, 820))
	for _, style := range overlayStyleIDs {
		cfg := defaultConfig()
		cfg.OverlayStyle = style
		d := overlayDesignFor(style)
		cfg.OverlayWidth = int(d.width)
		cfg.OverlayHeight = int(d.height)
		g := newGUI(cfg, filepath.Join(t.TempDir(), "config.json"), true)
		o := &nativeOverlay{g: g, cfg: cfg, frame: Frame{Source: "codex", Kind: "pct", P1: 78, P2: 69, S1: "15:17", S2: "10-23 14:08"}, hasFrame: true}
		g.overlay = o
		save("overlay-"+style, ui.NewTester(o.view, cfg.OverlayWidth, cfg.OverlayHeight))
		if compactOverlayStyle(style) {
			for _, state := range []struct {
				name  string
				frame Frame
			}{
				{"limits", Frame{Source: "reclaude", Kind: "pct", P1: 100, P2: 0, S1: "15:17", S2: "10-23 14:08"}},
				{"balance", Frame{Source: "我的中转站", Kind: "money", L1: "Balance", V1: "$12.8"}},
				{"stale", Frame{Source: "codex", Kind: "pct", P1: 78, P2: 69, Old: true}},
				{"failed", Frame{Source: "codex", Kind: "pct", P1: -1, P2: -1}},
			} {
				o.frame = state.frame
				save("overlay-"+style+"-"+state.name, ui.NewTester(o.view, cfg.OverlayWidth, cfg.OverlayHeight))
			}
		}
	}
}
