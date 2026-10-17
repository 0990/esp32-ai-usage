package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func testGUI(t *testing.T) *GUI {
	t.Helper()
	return newGUI(defaultConfig(), filepath.Join(t.TempDir(), "config.json"), true)
}
func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSettingsAreDraftUntilSave(t *testing.T) {
	g := testGUI(t)
	tt := ui.NewTester(g.view, 820, 900)
	click(t, tt, "claude")
	if !g.draft.providers["claude"] || g.engine.Config().wants("claude") {
		t.Fatal("provider edits must remain a draft")
	}
	click(t, tt, "保存")
	loaded := loadConfig(g.engine.cfgPath)
	if !loaded.wants("claude") || !loaded.wants("codex") {
		t.Fatal("save lost provider selections")
	}
	g.draft.baud = "not a number"
	click(t, tt, "保存")
	if !g.noticeError || g.engine.Config().Baud != 115200 {
		t.Fatal("invalid numeric input must not mutate active config")
	}
}

func TestNativeCustomObjectDraftAndValidation(t *testing.T) {
	g := testGUI(t)
	tt := ui.NewTester(g.view, 820, 900)
	click(t, tt, "新增自定义对象")
	if !g.editing {
		t.Fatal("custom editor did not open")
	}
	g.editObject = CustomObject{ID: "test", Name: "station", Type: "sub2api", BaseURL: "https://example.com/v1", APIKey: "test-key", Enabled: true}
	click(t, tt, "确定")
	if len(g.draft.CustomObjects) != 1 || len(g.engine.Config().CustomObjects) != 0 {
		t.Fatal("custom edits applied before save")
	}
	click(t, tt, "保存")
	if len(loadConfig(g.engine.cfgPath).CustomObjects) != 1 {
		t.Fatal("custom station did not persist")
	}
	g.editCustom(-1)
	g.editObject.Name = "station"
	g.editObject.BaseURL = "https://example.com"
	g.editObject.APIKey = "test"
	g.submitCustom()
	if g.editError == "" || !g.editing {
		t.Fatal("duplicate custom station name must be rejected")
	}
}

func TestNativeTabsAndJSONValidation(t *testing.T) {
	g := testGUI(t)
	tt := ui.NewTester(g.view, 820, 900)
	click(t, tt, "测试命令")
	g.testJSON = "{"
	tt.Frame()
	click(t, tt, "发送到 ESP32")
	if !g.noticeError {
		t.Fatal("invalid JSON was accepted")
	}
	g.testJSON = sampleJSON
	tt.Frame()
	click(t, tt, "发送到 ESP32")
	if g.noticeError || !strings.Contains(g.notice, "校验通过") {
		t.Fatal("preview should validate JSON without writing the serial port")
	}
	click(t, tt, "活动日志")
	if g.tab != 2 {
		t.Fatal("log tab not selected")
	}
}

func TestNativeStyleAndLayerControls(t *testing.T) {
	g := testGUI(t)
	tt := ui.NewTester(g.view, 820, 1000)
	click(t, tt, "悬浮窗样式")
	click(t, tt, "曜石仪表")
	if c := g.engine.Config(); c.OverlayStyle != "obsidian" || c.OverlayWidth != 348 || c.OverlayHeight != 150 {
		t.Fatal("style selection did not persist its matching dimensions")
	}
	click(t, tt, "悬浮窗层级")
	click(t, tt, "始终置底")
	if g.engine.Config().OverlayLayer != "bottom" {
		t.Fatal("layer change not saved")
	}
	click(t, tt, "悬浮窗样式")
	click(t, tt, "极简窄条")
	for _, style := range []string{"horizontal", "vertical"} {
		click(t, tt, "悬浮窗样式")
		d := overlayDesignFor(style)
		if tt.HasText("口袋数字") {
			t.Fatal("removed style is still available in the picker")
		}
		click(t, tt, d.name)
		if c := loadConfig(g.engine.cfgPath); c.OverlayStyle != style || c.OverlayWidth != int(d.width) || c.OverlayHeight != int(d.height) {
			t.Fatal("compact style selection did not persist its dimensions")
		}
	}
	if len(g.engine.reload) != 0 {
		t.Fatal("display controls must not trigger network requests")
	}
}

func TestNativeOverlayVariantsAndPinState(t *testing.T) {
	for _, style := range overlayStyleIDs {
		t.Run(style, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.OverlayStyle = style
			cfg.OverlayWidth = int(overlayDesignFor(style).width)
			cfg.OverlayHeight = int(overlayDesignFor(style).height)
			g := newGUI(cfg, filepath.Join(t.TempDir(), "config.json"), true)
			o := &nativeOverlay{g: g, cfg: cfg, hasFrame: true, frame: Frame{Source: "codex", Kind: "pct", P1: 100, S1: "15:17", P2: 0, S2: "10-23 14:08"}}
			g.overlay = o
			tt := ui.NewTester(o.view, cfg.OverlayWidth, cfg.OverlayHeight)
			if (!compactOverlayStyle(style) && !tt.HasText("Codex")) || !tt.HasText("100") || !tt.HasText("0") {
				t.Fatalf("quota data missing: %q", tt.Texts())
			}
			if !compactOverlayStyle(style) {
				click(t, tt, "当前置顶，点击置底")
				if g.engine.Config().OverlayLayer != "bottom" {
					t.Fatal("pin did not switch to bottom")
				}
				click(t, tt, "当前置底，点击置顶")
				if g.engine.Config().OverlayLayer != "top" {
					t.Fatal("pin did not immediately expose the new state")
				}
			} else {
				for _, layer := range []struct{ label, value string }{{"始终置底", "bottom"}, {"始终置顶", "top"}} {
					if err := tt.RightClick("拖动悬浮窗"); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(strings.Join(tt.Menu(), "\n"), "重置 10-23 14:08") {
						t.Fatal("compact context menu is missing reset details")
					}
					if err := tt.ChooseMenuItem(layer.label); err != nil {
						t.Fatal(err)
					}
					tt.Frame()
					if g.engine.Config().OverlayLayer != layer.value {
						t.Fatal("compact menu layer action did not persist")
					}
				}
			}
			o.frame = Frame{Source: "station", Kind: "money", L1: "Balance", V1: "$12"}
			tt.Frame()
			if (!compactOverlayStyle(style) && !tt.HasText("station")) || !tt.HasText("$12") || tt.HasText("100") {
				t.Fatal("relay page kept stale quota values")
			}
			o.frame = Frame{Source: "codex", Kind: "pct", P1: -1, P2: -1, Old: true}
			tt.SetSize(cfg.OverlayWidth, cfg.OverlayHeight+18)
			tt.Frame()
			if compactOverlayStyle(style) {
				if o.minHeight() != int(overlayDesignFor(style).minHeight) || !strings.Contains(strings.Join(o.compactDetails(), "\n"), "数据已过期") {
					t.Fatal("compact styles must keep stale details without expanding the window")
				}
			} else if !tt.HasText("数据已过期 · 正在重试更新") {
				t.Fatal("stale data warning missing")
			}
		})
	}
}
