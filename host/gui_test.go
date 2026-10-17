package main

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// The settings controls must edit a draft until the user presses Save.
func TestCustomSettingsDraft(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("settings")
	defer w.Close()
	cfg := defaultConfig()
	cfg.CustomObjects = []CustomObject{{ID: "one", Name: "station", Type: "sub2api", BaseURL: "https://example.com", APIKey: "secret", Enabled: true}}
	g := &GUI{app: a, win: w, engine: NewEngine(cfg, filepath.Join(t.TempDir(), "config.json"))}
	content := g.buildSettingsTab()
	w.SetContent(content)
	box := content.(*fyne.Container)
	form := box.Objects[1].(*widget.Form)
	var brightnessSlider *widget.Slider
	var brightnessLabel *widget.Label
	for _, item := range form.Items {
		if item.Text != "OLED 亮度" {
			continue
		}
		for _, o := range item.Widget.(*fyne.Container).Objects {
			if slider, ok := o.(*widget.Slider); ok {
				brightnessSlider = slider
			}
			if label, ok := o.(*widget.Label); ok {
				brightnessLabel = label
			}
		}
	}
	if brightnessSlider == nil || brightnessLabel == nil {
		t.Fatal("missing brightness controls")
	}
	brightnessSlider.SetValue(0)
	if brightnessLabel.Text != "0 / 255" {
		t.Fatal("brightness value label not updated")
	}
	if g.engine.Config().OLEDBrightness != 143 {
		t.Fatal("brightness changed before Save")
	}
	objects := form.Items[0].Widget.(*fyne.Container)
	list := objects.Objects[1].(*fyne.Container)
	row := list.Objects[0].(*fyne.Container)
	var check *widget.Check
	var buttons *fyne.Container
	for _, o := range row.Objects {
		if c, ok := o.(*widget.Check); ok {
			check = c
		}
		if c, ok := o.(*fyne.Container); ok {
			buttons = c
		}
	}
	if check == nil || buttons == nil {
		t.Fatal("missing custom object controls")
	}
	check.SetChecked(false)
	if !g.engine.Config().CustomObjects[0].Enabled {
		t.Fatal("draft toggle applied before Save")
	}
	test.Tap(buttons.Objects[1].(*widget.Button))
	if len(list.Objects) != 0 {
		t.Fatal("delete did not remove draft row")
	}
	if len(g.engine.Config().CustomObjects) != 1 {
		t.Fatal("draft delete applied before Save")
	}
	add := objects.Objects[2].(*fyne.Container).Objects[0].(*widget.Button)
	test.Tap(add)
	if len(w.Canvas().Overlays().List()) == 0 {
		t.Fatal("add did not open dialog")
	}
	// Check that the obsolete relay type row is gone.
	for _, item := range form.Items {
		if item.Text == "中转站类型" {
			t.Fatal("obsolete relay settings visible")
		}
	}
}

func TestOverlayStyleControlsApplyImmediately(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.OverlayX = -300
	cfg.OverlayPositionSet = true
	g := &GUI{app: a, win: a.NewWindow("settings"), engine: NewEngine(cfg, path)}
	g.win.SetContent(g.buildSettingsTab())
	if g.overlayStyleSelect.Selected != "极简窄条" {
		t.Fatal("settings must default to minimal strip")
	}
	for i, name := range overlayStyleNames {
		g.overlayStyleSelect.SetSelected(name)
		loaded := loadConfig(path)
		d := overlayDesignFor(overlayStyleIDs[i])
		if loaded.OverlayStyle != overlayStyleIDs[i] || g.overlayWidthSlider.Value != float64(d.width) || g.overlayHeightSlider.Value != float64(d.height) || loaded.OverlayX != -300 {
			t.Fatalf("style, size controls or position not synchronized: %+v", loaded)
		}
	}
	if len(g.engine.reload) != 0 {
		t.Fatal("choosing a style should not refresh usage")
	}
}

func TestOverlayLayerIndicatorUpdatesWithoutNewFrame(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	g := &GUI{app: a, win: a.NewWindow("settings"), engine: NewEngine(defaultConfig(), filepath.Join(t.TempDir(), "config.json"))}
	g.win.SetContent(g.buildSettingsTab())
	o := newDesktopOverlayWindow(a.NewWindow("overlay"), func() {}, func(error) {})
	g.overlay = o
	o.onLayer = func(layer string) { g.setOverlayAppearance(g.engine.Config().OverlayScale, layer) }
	o.update(Frame{Source: "codex", Kind: "pct", P1: 78, P2: 69})
	if o.view.pin.Icon != overlayPinActive || o.view.pin.Importance != widget.MediumImportance {
		t.Fatal("top layer must start with a highlighted pin")
	}
	// No display update or window resize after either action: the icon itself must refresh.
	test.Tap(o.view.pin)
	if o.layer != "bottom" || o.view.pin.Icon != overlayPinInactive || o.view.pin.Importance != widget.LowImportance || g.overlayLayerSelect.Selected != "始终置底" {
		t.Fatal("pin click did not immediately show bottom state")
	}
	g.overlayLayerSelect.SetSelected("始终置顶")
	if o.layer != "top" || o.view.pin.Icon != overlayPinActive || o.view.pin.Importance != widget.MediumImportance {
		t.Fatal("settings change did not immediately restore top state")
	}
	if o.view.frame.P1 != 78 || len(g.engine.reload) != 0 {
		t.Fatal("indicator update must not require new usage data")
	}
}
