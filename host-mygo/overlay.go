package main

import (
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type nativeOverlay struct {
	g             *GUI
	win           *mygo.Window
	cfg           Config
	frame         Frame
	hasFrame      bool
	applying      bool
	boundsPending bool
	boundsDue     time.Time
	positionWarn  bool
}

func (g *GUI) applyOverlay() {
	c := g.engine.Config()
	if !c.DesktopOverlay {
		if g.overlay != nil && g.overlay.win != nil {
			g.overlay.flushBounds()
			g.overlay.win.Hide()
		}
		return
	}
	if g.overlay == nil {
		o := &nativeOverlay{g: g, cfg: c, applying: true}
		d := overlayDesignFor(c.OverlayStyle)
		o.win = mygo.NewWindow(mygo.WindowOptions{Title: "AI 额度悬浮窗 · MyGo", Width: c.OverlayWidth, Height: c.OverlayHeight, MinWidth: 264, MinHeight: int(d.minHeight), BackgroundColor: fmt.Sprintf("#%02x%02x%02x", d.background.R, d.background.G, d.background.B), Hidden: true, Frameless: true, SkipTaskbar: !g.preview, DisableMinimize: true, DisableMaximize: true, Content: ui.View(o.view)})
		g.overlay = o
		o.win.OnClose(func(e *mygo.CloseEvent) {
			if g.quitting {
				return
			}
			e.PreventDefault()
			g.setOverlayEnabled(false)
		})
		o.win.OnMove(o.rememberBounds)
		o.win.OnResize(o.rememberBounds)
		o.applying = false
	}
	g.overlay.apply(c, true)
	if g.preview {
		g.overlay.update(Frame{Source: "codex", Kind: "pct", P1: 78, S1: "15:17", P2: 69, S2: "10-23 14:08"})
	} else {
		frames := g.engine.buildFrames()
		if len(frames) > 0 {
			g.overlay.update(frames[0])
		}
	}
	g.overlay.win.ShowInactive()
	g.overlay.position(true)
}

func (g *GUI) overlayChanged(err error) {
	if err != nil {
		g.message(err.Error(), true)
		return
	}
	c := g.engine.Config()
	copyOverlayConfig(&g.draft.Config, c)
	g.displaySync = true
	if g.trayOverlay != nil {
		g.trayOverlay.SetChecked(c.DesktopOverlay)
	}
	if g.win != nil {
		if c.DesktopOverlay {
			g.applyOverlay()
		} else if g.overlay != nil {
			g.overlay.win.Hide()
		}
		g.win.Invalidate()
	}
}
func (g *GUI) setOverlayEnabled(on bool) {
	if on == g.engine.Config().DesktopOverlay {
		return
	}
	if !on && g.overlay != nil {
		g.overlay.flushBounds()
	}
	g.overlayChanged(g.engine.SetDesktopOverlay(on))
}
func (g *GUI) setOverlayStyle(style string) {
	if g.overlay != nil {
		g.overlay.flushBounds()
	}
	if normalizeOverlayStyle(style) == g.engine.Config().OverlayStyle {
		return
	}
	g.overlayChanged(g.engine.SetOverlayStyle(style))
}
func (g *GUI) setOverlayLayer(layer string) {
	if g.overlay != nil {
		g.overlay.flushBounds()
	}
	c := g.engine.Config()
	if c.OverlayLayer == layer {
		return
	}
	g.overlayChanged(g.engine.SetOverlayAppearance(c.OverlayScale, layer))
}
func (g *GUI) setOverlayGeometry(w, h, opacity int) {
	g.overlayChanged(g.engine.SetOverlayGeometry(w, h, opacity))
}
func (g *GUI) resetOverlayPosition() { g.overlayChanged(g.engine.SetOverlayPosition(0, 0, false)) }

func (o *nativeOverlay) minHeight() int {
	h := int(overlayDesignFor(o.cfg.OverlayStyle).minHeight)
	if o.hasFrame && o.frame.Kind == "money" && o.frame.L1 != "Balance" {
		h = max(h, 160)
	}
	if overlayStatus(o.frame, o.hasFrame) != "" {
		h += 18
	}
	return h
}
func (o *nativeOverlay) apply(cfg Config, reposition bool) {
	o.applying = true
	defer func() { o.applying = false }()
	if o.cfg.OverlayStyle != cfg.OverlayStyle {
		d := overlayDesignFor(cfg.OverlayStyle)
		o.win.SetBackgroundColor(fmt.Sprintf("#%02x%02x%02x", d.background.R, d.background.G, d.background.B))
	}
	o.cfg = cfg
	o.boundsPending = false
	o.win.SetMinimumSize(264, o.minHeight())
	w, h := o.win.Size()
	if w != cfg.OverlayWidth || h != max(cfg.OverlayHeight, o.minHeight()) {
		o.win.SetSize(cfg.OverlayWidth, max(cfg.OverlayHeight, o.minHeight()))
	}
	o.win.SetOpacity(float64(cfg.OverlayOpacity) / 100)
	o.win.SetAlwaysOnTop(cfg.OverlayLayer == "top")
	o.position(reposition)
	o.win.Invalidate()
}
func (o *nativeOverlay) position(reposition bool) {
	if err := positionNativeOverlay(o.win, o.cfg, reposition, !o.g.preview); err != nil && !o.positionWarn {
		o.positionWarn = true
		o.g.engine.log(LogWarn, "overlay", "设置悬浮窗失败: %v", err)
	}
}
func (o *nativeOverlay) update(f Frame) {
	if o.hasFrame && reflect.DeepEqual(o.frame, f) {
		return
	}
	o.frame = f
	o.hasFrame = true
	o.apply(o.g.engine.Config(), false)
}
func (o *nativeOverlay) rememberBounds() {
	if o.applying || o.win == nil {
		return
	}
	w, h := o.win.Size()
	x, y, err := nativeOverlayPosition(o.win)
	if err != nil {
		return
	}
	expectedH := max(o.cfg.OverlayHeight, o.minHeight())
	if math.Abs(float64(w-o.cfg.OverlayWidth)) <= 1 && math.Abs(float64(h-expectedH)) <= 1 && o.cfg.OverlayPositionSet && x == o.cfg.OverlayX && y == o.cfg.OverlayY {
		return
	}
	o.boundsPending = true
	o.boundsDue = time.Now().Add(250 * time.Millisecond)
	o.win.Invalidate()
}
func (o *nativeOverlay) flushBounds() {
	if !o.boundsPending || o.win == nil {
		return
	}
	o.boundsPending = false
	w, h := o.win.Size()
	x, y, err := nativeOverlayPosition(o.win)
	if err != nil {
		return
	}
	// 自动展开过期提示不改变用户保存的窗口高度。
	if h == o.minHeight() && o.cfg.OverlayHeight < h {
		h = o.cfg.OverlayHeight
	}
	if err = o.g.engine.SetOverlayBounds(w, h, x, y); err != nil {
		o.g.message(err.Error(), true)
		return
	}
	o.cfg = o.g.engine.Config()
	copyOverlayConfig(&o.g.draft.Config, o.cfg)
	o.g.displaySync = true
	if o.g.win != nil {
		o.g.win.Invalidate()
	}
}
func (o *nativeOverlay) popupMenu() {
	if o.win == nil {
		return
	}
	items := []*mygo.MenuItem{}
	for i, id := range overlayStyleIDs {
		style := id
		items = append(items, &mygo.MenuItem{Label: overlayStyleNames[i], Type: mygo.MenuItemRadio, Checked: o.cfg.OverlayStyle == style, Click: func(*mygo.MenuItem, *mygo.Window) { o.g.setOverlayStyle(style) }})
	}
	items = append(items, mygo.Separator(), &mygo.MenuItem{Label: "始终置顶", Type: mygo.MenuItemRadio, Checked: o.cfg.OverlayLayer == "top", Click: func(*mygo.MenuItem, *mygo.Window) { o.g.setOverlayLayer("top") }}, &mygo.MenuItem{Label: "始终置底", Type: mygo.MenuItemRadio, Checked: o.cfg.OverlayLayer == "bottom", Click: func(*mygo.MenuItem, *mygo.Window) { o.g.setOverlayLayer("bottom") }}, mygo.Separator(), &mygo.MenuItem{Label: "回到右上角", Click: func(*mygo.MenuItem, *mygo.Window) { o.g.resetOverlayPosition() }})
	mygo.NewMenu(items).Popup(o.win)
}
