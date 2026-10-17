package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

var pinIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" d="M9 3h6m-5 0v7l-4 5v2h12v-2l-4-5V3M12 17v5"/></svg>`))
var pinOffIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" d="M9 3h6m-1 0v7l4 5v2h-3M10 3v3M7 12l-1 3v2h6v5M3 3l18 18"/></svg>`))
var closeIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" d="M6 6l12 12M18 6L6 18"/></svg>`))
var moreIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><circle fill="currentColor" cx="5" cy="12" r="1.5"/><circle fill="currentColor" cx="12" cy="12" r="1.5"/><circle fill="currentColor" cx="19" cy="12" r="1.5"/></svg>`))

func (o *nativeOverlay) view(c *ui.Context) {
	if o.g != nil {
		o.cfg = o.g.engine.Config()
	}
	if o.boundsPending {
		if remaining := time.Until(o.boundsDue); remaining > 0 {
			c.After(remaining)
		} else {
			o.flushBounds()
		}
	}
	d := overlayDesignFor(o.cfg.OverlayStyle)
	t := *ui.LightTheme()
	if o.cfg.OverlayStyle == "obsidian" {
		t = *ui.DarkTheme()
	}
	t.Background = d.background
	t.Text = d.text
	t.TextMuted = d.muted
	t.Accent = d.accent
	t.Border = d.line
	t.Surface = d.badge
	c.SetTheme(&t)
	c.Root().Background(d.background)
	if compactOverlayStyle(o.cfg.OverlayStyle) {
		o.compactView(c, d)
		return
	}
	w, h := c.Size()
	padding, headerY := float32(17), float32(14)
	if o.cfg.OverlayStyle == "obsidian" {
		padding = 16
		headerY = 13
	}
	if o.cfg.OverlayStyle == "strip" {
		padding = 15
		headerY = 11
	}
	text := func(key, value string, x, y, width, font float32, clr ui.Color, right bool) {
		e := ui.Text(c.Key(key), value).Absolute().Left(x).Top(y).Width(max(0, width)).FontSize(font).TextColor(clr).SingleLine().FontFeatures("tnum")
		if right {
			e.TextAlign(ui.End)
		}
	}
	// Let passive content share the card's drag handle; child buttons keep their own input.
	ui.Box(c.Key("drag-region")).Fill().Background(d.background).DragWindow().Cursor(ui.CursorGrab).Label("拖动悬浮窗").Children(func() {
		name := "AI 额度"
		if o.hasFrame {
			name = overlayTitle(o.frame.Source)
		}
		if o.cfg.OverlayStyle == "strip" {
			ui.Box(c).Absolute().Left(padding).Top(headerY+8).Size(7, 7).Radius(4).Background(d.accent)
			text("source", name, padding+16, headerY+2, w-padding*2-90, 12, d.text, false)
		} else {
			ui.Box(c).Absolute().Left(padding).Top(headerY).Size(23, 23).Radius(7).Background(d.badge).Center().Children(func() {
				letter := "C"
				if name != "" {
					letter = strings.ToUpper(string([]rune(name)[0]))
				}
				ui.Text(c, letter).FontSize(14).TextColor(d.accent)
			})
			text("source", name, padding+31, headerY+3, w-padding*2-104, 13, d.text, false)
		}
		icon, label, face := pinOffIcon, "当前置底，点击置顶", ui.Transparent
		if o.cfg.OverlayLayer == "top" {
			icon = pinIcon
			label = "当前置顶，点击置底"
			face = d.badge
		}
		button := ui.ButtonBase(c.Key("pin")).Absolute().Left(w-padding-69).Top(headerY).Size(23, 23).Radius(6).Background(face).Center().Label(label).Tooltip(label)
		button.Children(func() {
			clr := d.muted
			if o.cfg.OverlayLayer == "top" {
				clr = d.accent
			}
			ui.Icon(c, icon).FontSize(13).TextColor(clr)
		})
		button.OnClick(func() {
			layer := "top"
			if o.cfg.OverlayLayer == "top" {
				layer = "bottom"
			}
			o.g.setOverlayLayer(layer)
		})
		ui.ButtonBase(c.Key("overlay-menu")).Absolute().Left(w-padding-46).Top(headerY).Size(23, 23).Center().Label("悬浮窗菜单").Tooltip("悬浮窗菜单").Children(func() { ui.Icon(c, moreIcon).FontSize(13).TextColor(d.muted) }).OnClick(o.popupMenu)
		ui.ButtonBase(c.Key("overlay-close")).Absolute().Left(w-padding-23).Top(headerY).Size(23, 23).Center().Label("关闭悬浮窗").Tooltip("关闭悬浮窗").Children(func() { ui.Icon(c, closeIcon).FontSize(13).TextColor(d.muted) }).OnClick(func() { o.g.setOverlayEnabled(false) })
		bottom := h - 18
		if status := overlayStatus(o.frame, o.hasFrame); status != "" {
			text("overlay-status", status, padding, h-25, w-padding*2, 11, d.muted, false)
			bottom -= 20
		}
		f := o.frame
		if o.hasFrame && f.Kind == "money" {
			label := "已用"
			if f.L1 == "Today" {
				label = "今日已用"
			}
			if f.L1 == "Balance" {
				label = "剩余余额"
			}
			text("money-label-0", label, padding, 52, w-padding*2, 11, d.muted, false)
			text("money-value-0", f.V1, padding, 70, w-padding*2, 24, d.text, false)
			if f.L1 != "Balance" {
				text("money-label-1", "剩余余额", padding, 103, w-padding*2, 11, d.muted, false)
				text("money-value-1", f.V2, padding+80, 99, w-padding*2-80, 20, d.text, true)
			}
		} else {
			lefts, resets := [2]int{f.P1, f.P2}, [2]string{f.S1, f.S2}
			if !o.hasFrame {
				lefts = [2]int{-1, -1}
			}
			for i, period := range []string{"5小时", "一周"} {
				prefix := fmt.Sprintf("quota-%d-", i)
				value, reset := "—", resets[i]
				if lefts[i] < 0 {
					reset = "--"
				} else {
					value = fmt.Sprint(max(0, min(100, lefts[i])))
				}
				if reset == "" {
					reset = "--"
				}
				percent := func(x, y, width, font, unit float32) {
					if lefts[i] < 0 {
						text(prefix+"value", value, x, y, width, font, d.muted, true)
						return
					}
					fitted := font
					for fitted > 11 {
						measured, _ := c.MeasureText(0, ui.Span{Text: value, Size: fitted})
						if measured <= width-unit-3 {
							break
						}
						fitted -= .5
					}
					text(prefix+"value", value, x, y+font-fitted, width-unit-3, fitted, d.text, true)
					text(prefix+"unit", "%", x+width-unit, y+font-unit-1, unit, unit, d.muted, true)
				}
				var bx, by, bw, bh float32
				switch o.cfg.OverlayStyle {
				case "paper":
					y := float32(54)
					if i == 1 {
						y = bottom - 30
					}
					text(prefix+"period", period, padding, y, w-padding*2-55, 12, d.text, false)
					text(prefix+"reset", "重置 "+reset, padding+41, y+1, w-padding*2-102, 11, d.muted, false)
					percent(w-padding-55, y-5, 55, 20, 11)
					bx, by, bw, bh = padding, y+25, w-padding*2, 5
				case "obsidian":
					col := (w - padding*2 - 36) / 2
					x := padding + float32(i)*(col+36)
					text(prefix+"period", period, x, 64, col-60, 11, d.text, false)
					percent(x+col-60, 50, 60, 27, 12)
					text(prefix+"reset", "重置 "+reset, x, bottom-16, col, 11, d.muted, false)
					bx, by, bw, bh = x, (78+bottom-25)/2, col, 4
				default:
					y := float32(45)
					if i == 1 {
						y = bottom - 27
					}
					text(prefix+"period", period, padding, y, 99, 11, d.text, false)
					text(prefix+"reset", reset+" 重置", padding, y+15, 99, 11, d.muted, false)
					percent(w-padding-38, y+4, 38, 17, 10)
					bx, by, bw, bh = padding+110, y+13, w-padding*2-159, 4
				}
				ui.Box(c.Key(prefix+"bar")).Absolute().Left(bx).Top(by).Size(max(0, bw), bh).Radius(bh / 2).Background(d.track).Label(period + "余量").Children(func() {
					if lefts[i] > 0 {
						ui.Box(c).Size(bw*float32(min(100, lefts[i]))/100, bh).Radius(bh / 2).Background(d.accent)
					}
				})
			}
			if o.cfg.OverlayStyle == "obsidian" {
				ui.Box(c).Absolute().Left(w/2).Top(52).Size(1, max(0, bottom-52)).Background(d.line)
			}
		}
		ui.Box(c).Absolute().Right(5).Bottom(5).Size(8, 8).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
			p.Line(r.X, r.Y+r.H, r.X+r.W, r.Y, 1, d.line)
			p.Line(r.X+4, r.Y+r.H, r.X+r.W, r.Y+4, 1, d.line)
		})
	})
}
