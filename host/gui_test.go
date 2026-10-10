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
