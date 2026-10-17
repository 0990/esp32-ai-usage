package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const defaultOverlayStyle = "strip"

var overlayStyleIDs = []string{"paper", "obsidian", "strip"}
var overlayStyleNames = []string{"雾白卡片", "曜石仪表", "极简窄条"}

var overlayPinResource = fyne.NewStaticResource("overlay-pin.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#ffffff" fill-rule="evenodd" d="M8 2h8v2h-1v5l4 5v3h-6v5h-2v-5H5v-3l4-5V4H8zm3 2v6l-4 5h10l-4-5V4z"/></svg>`))
var overlayPinActive = theme.NewPrimaryThemedResource(overlayPinResource)
var overlayPinOffResource = fyne.NewStaticResource("overlay-pin-off.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#ffffff" d="M8 2h8v2h-1v5l4 5v3h-2v-2l-4-5V4h-2v3L9 5V4H8zM7 11l1.4 1.4L7 15h4.2l2 2H13v5h-2v-5H5v-3zM2.7 1.3l20 20-1.4 1.4-20-20z"/></svg>`))
var overlayPinInactive = theme.NewColoredResource(overlayPinOffResource, theme.ColorNameDisabled)

type overlayDesign struct {
	name                                                string
	width, height, minHeight, radius                    float32
	background, text, muted, track, accent, line, badge color.NRGBA
}

func overlayRGB(hex uint32) color.NRGBA {
	return color.NRGBA{R: byte(hex >> 16), G: byte(hex >> 8), B: byte(hex), A: 255}
}

func normalizeOverlayStyle(style string) string {
	for _, id := range overlayStyleIDs {
		if style == id {
			return id
		}
	}
	return defaultOverlayStyle
}

func overlayDesignFor(style string) overlayDesign {
	switch normalizeOverlayStyle(style) {
	case "paper":
		return overlayDesign{"雾白卡片", 296, 166, 158, 16, overlayRGB(0xffffff), overlayRGB(0x26332f), overlayRGB(0x77827d), overlayRGB(0xedf1ef), overlayRGB(0x509f80), overlayRGB(0xedf0ee), overlayRGB(0xeaf3ee)}
	case "obsidian":
		return overlayDesign{"曜石仪表", 348, 150, 146, 14, overlayRGB(0x232827), overlayRGB(0xedf3ef), overlayRGB(0xa1ada6), overlayRGB(0x3c4640), overlayRGB(0x9bcbb0), overlayRGB(0x3b4440), overlayRGB(0x35453b)}
	default:
		return overlayDesign{"极简窄条", 330, 132, 128, 12, overlayRGB(0xffffff), overlayRGB(0x26332f), overlayRGB(0x77827d), overlayRGB(0xedf1ef), overlayRGB(0x6d8ba6), overlayRGB(0xedf0ee), overlayRGB(0xeaf0f5)}
	}
}

func normalizeOverlaySize(style string, width, height, opacity int) (int, int, int) {
	d := overlayDesignFor(style)
	if width == 0 {
		width = int(d.width)
	}
	if height == 0 {
		height = int(d.height)
	}
	width, height, opacity = normalizeOverlayGeometry(width, height, opacity)
	return max(264, width), max(int(d.minHeight), height), opacity
}

// 专用画布渲染文字与细进度条，避免普通表单控件的内边距和粗进度条。
type overlayView struct {
	widget.BaseWidget
	owner            *desktopOverlay
	frame            Frame
	hasFrame         bool
	pin, more, close *widget.Button
}

func newOverlayView(o *desktopOverlay, hide func()) *overlayView {
	v := &overlayView{owner: o}
	v.ExtendBaseWidget(v)
	v.pin = widget.NewButtonWithIcon("", overlayPinActive, func() { o.toggleLayer() })
	v.more = widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), func() { o.showMenu() })
	v.close = widget.NewButtonWithIcon("", theme.CancelIcon(), hide)
	for _, button := range []*widget.Button{v.pin, v.more, v.close} {
		button.Importance = widget.LowImportance
	}
	v.refreshLayerIndicator()
	return v
}

// 层级变更时立即刷新，不依赖窗口改尺寸或下一帧额度数据触发 Layout。
func (v *overlayView) refreshLayerIndicator() {
	icon, importance := fyne.Resource(overlayPinInactive), widget.LowImportance
	if v.owner.layer == "top" {
		icon, importance = overlayPinActive, widget.MediumImportance
	}
	if v.pin.Icon == icon && v.pin.Importance == importance {
		return
	}
	v.pin.Icon, v.pin.Importance = icon, importance
	v.pin.Refresh()
}

func overlayStatus(f Frame, hasFrame bool) string {
	switch {
	case !hasFrame:
		return "正在查询使用量…"
	case f.Old:
		return "数据已过期 · 正在重试更新"
	case f.Kind != "money" && f.P1 < 0 && f.P2 < 0:
		return "查询失败 · 请查看主窗口活动日志"
	case f.Kind == "money" && (f.V1 == "ERR" || f.V2 == "ERR"):
		return "查询失败 · 请查看主窗口活动日志"
	default:
		return ""
	}
}

type overlayViewRenderer struct {
	view                    *overlayView
	background, badge       *canvas.Rectangle
	brand, title, status    *canvas.Text
	labels, resets, values  [2]*canvas.Text
	units                   [2]*canvas.Text
	tracks, fills           [2]*canvas.Rectangle
	separator, grip1, grip2 *canvas.Line
	objects                 []fyne.CanvasObject
}

func (v *overlayView) CreateRenderer() fyne.WidgetRenderer {
	r := &overlayViewRenderer{view: v, background: canvas.NewRectangle(color.Transparent), badge: canvas.NewRectangle(color.Transparent), brand: canvas.NewText("C", color.White), title: canvas.NewText("AI 额度", color.White), status: canvas.NewText("", color.White), separator: canvas.NewLine(color.Transparent), grip1: canvas.NewLine(color.Transparent), grip2: canvas.NewLine(color.Transparent)}
	r.objects = []fyne.CanvasObject{r.background, r.badge, r.brand, r.title, r.status, r.separator}
	for i := range r.labels {
		r.labels[i] = canvas.NewText("", color.White)
		r.resets[i] = canvas.NewText("", color.White)
		r.values[i] = canvas.NewText("", color.White)
		r.units[i] = canvas.NewText("", color.White)
		r.tracks[i] = canvas.NewRectangle(color.Transparent)
		r.fills[i] = canvas.NewRectangle(color.Transparent)
		r.objects = append(r.objects, r.tracks[i], r.fills[i], r.labels[i], r.resets[i], r.values[i], r.units[i])
	}
	r.objects = append(r.objects, v.owner.dragArea, v.pin, v.more, v.close, r.grip1, r.grip2, v.owner.resizeArea)
	r.Layout(v.Size())
	return r
}

func (r *overlayViewRenderer) MinSize() fyne.Size {
	s := float32(r.view.owner.scale) / 100
	d := overlayDesignFor(r.view.owner.style)
	h := d.minHeight
	if r.view.hasFrame && r.view.frame.Kind == "money" && r.view.frame.L1 != "Balance" {
		h = max(h, 160)
	}
	if overlayStatus(r.view.frame, r.view.hasFrame) != "" {
		h += 18
	}
	return fyne.NewSize(264*s, h*s)
}
func (r *overlayViewRenderer) Destroy()                     {}
func (r *overlayViewRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *overlayViewRenderer) Refresh() {
	r.Layout(r.view.Size())
	for _, obj := range r.objects {
		obj.Refresh()
	}
	canvas.Refresh(r.view)
}

func overlayFitText(text string, width, size float32, style fyne.TextStyle) string {
	if fyne.MeasureText(text, size, style).Width <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if fyne.MeasureText(string(runes)+"…", size, style).Width <= width {
			return string(runes) + "…"
		}
	}
	return ""
}

func (r *overlayViewRenderer) Layout(size fyne.Size) {
	o := r.view.owner
	d := overlayDesignFor(o.style)
	s := float32(o.scale) / 100
	w, h := size.Width/s, size.Height/s
	text := func(t *canvas.Text, value string, x, y, width, font float32, clr color.Color, right bool) {
		t.TextSize = font * s
		t.Color = clr
		t.Alignment = fyne.TextAlignLeading
		t.Text = overlayFitText(value, width*s, font*s, t.TextStyle)
		if right {
			t.Alignment = fyne.TextAlignTrailing
		}
		t.Move(fyne.NewPos(x*s, y*s))
		t.Resize(fyne.NewSize(width*s, (font+5)*s))
		t.Show()
	}
	box := func(rect *canvas.Rectangle, x, y, width, height, radius float32, clr color.Color) {
		rect.FillColor = clr
		rect.CornerRadius = radius * s
		rect.Move(fyne.NewPos(x*s, y*s))
		rect.Resize(fyne.NewSize(max(0, width)*s, max(0, height)*s))
		rect.Show()
	}
	line := func(l *canvas.Line, x1, y1, x2, y2 float32, clr color.Color) {
		l.StrokeColor = clr
		l.StrokeWidth = s
		l.Position1 = fyne.NewPos(x1*s, y1*s)
		l.Position2 = fyne.NewPos(x2*s, y2*s)
		l.Show()
	}
	box(r.background, 0, 0, w, h, d.radius, d.background)
	padding, headerY := float32(17), float32(14)
	if o.style == "strip" {
		padding = 15
		headerY = 11
	}
	if o.style == "obsidian" {
		padding = 16
		headerY = 13
	}
	name := "AI 额度"
	if r.view.hasFrame {
		name = overlayTitle(r.view.frame.Source)
	}
	if o.style == "strip" {
		box(r.badge, padding, headerY+8, 7, 7, 4, d.accent)
		r.brand.Hide()
		text(r.title, name, padding+16, headerY+2, w-padding*2-90, 12, d.text, false)
	} else {
		box(r.badge, padding, headerY, 23, 23, 7, d.badge)
		letter := "C"
		if name != "" {
			letter = strings.ToUpper(string([]rune(name)[0]))
		}
		text(r.brand, letter, padding+6, headerY+2, 14, 14, d.accent, false)
		text(r.title, name, padding+31, headerY+3, w-padding*2-104, 13, d.text, false)
	}
	buttons := []*widget.Button{r.view.pin, r.view.more, r.view.close}
	for i, b := range buttons {
		b.Move(fyne.NewPos((w-padding-69+float32(i)*23)*s, headerY*s))
		b.Resize(fyne.NewSize(23*s, 23*s))
	}
	r.view.refreshLayerIndicator()
	o.dragArea.Move(fyne.NewPos(padding*s, headerY*s))
	o.dragArea.Resize(fyne.NewSize(max(20, w-2*padding-72)*s, 23*s))
	status := overlayStatus(r.view.frame, r.view.hasFrame)
	bodyBottom := h - 18
	if status != "" {
		text(r.status, status, padding, h-25, w-2*padding, 11, d.muted, false)
		bodyBottom -= 20
	} else {
		r.status.Hide()
	}
	f := r.view.frame
	for i := 0; i < 2; i++ {
		r.labels[i].Hide()
		r.resets[i].Hide()
		r.values[i].Hide()
		r.units[i].Hide()
		r.tracks[i].Hide()
		r.fills[i].Hide()
	}
	r.separator.Hide()
	if r.view.hasFrame && f.Kind == "money" {
		label := "已用"
		if f.L1 == "Today" {
			label = "今日已用"
		}
		if f.L1 == "Balance" {
			label = "剩余余额"
		}
		text(r.labels[0], label, padding, 52, w-2*padding, 11, d.muted, false)
		text(r.values[0], f.V1, padding, 70, w-2*padding, 24, d.text, false)
		if f.L1 != "Balance" {
			text(r.labels[1], "剩余余额", padding, 103, w-2*padding, 11, d.muted, false)
			text(r.values[1], f.V2, padding+80, 99, w-2*padding-80, 20, d.text, true)
		}
	} else {
		lefts, resets := [2]int{f.P1, f.P2}, [2]string{f.S1, f.S2}
		if !r.view.hasFrame {
			lefts = [2]int{-1, -1}
		}
		for i, period := range []string{"5小时", "一周"} {
			reset := resets[i]
			value := "—"
			if lefts[i] < 0 {
				reset = "--"
			} else {
				value = fmt.Sprintf("%d", max(0, min(100, lefts[i])))
			}
			if reset == "" {
				reset = "--"
			}
			var bx, by, bw, bh float32
			percent := func(x, y, width, font, unit float32) {
				if lefts[i] < 0 {
					text(r.values[i], value, x, y, width, font, d.muted, true)
					return
				}
				// 不同字体下 100 比两位数更宽，保持完整数值并对齐基线。
				numberWidth := width - unit - 3
				fitted := font
				for fyne.MeasureText(value, fitted*s, r.values[i].TextStyle).Width > numberWidth*s && fitted > 11 {
					fitted -= .5
				}
				text(r.values[i], value, x, y+font-fitted, numberWidth, fitted, d.text, true)
				text(r.units[i], "%", x+width-unit, y+font-unit-1, unit, unit, d.muted, true)
			}
			switch o.style {
			case "paper":
				y := float32(54)
				if i == 1 {
					y = bodyBottom - 30
				}
				text(r.labels[i], period, padding, y, w-2*padding-55, 12, d.text, false)
				text(r.resets[i], "重置 "+reset, padding+41, y+1, w-2*padding-102, 11, d.muted, false)
				percent(w-padding-55, y-5, 55, 20, 11)
				bx, by, bw, bh = padding, y+25, w-2*padding, 5
			case "obsidian":
				col := (w - 2*padding - 36) / 2
				x := padding + float32(i)*(col+36)
				text(r.labels[i], period, x, 64, col-60, 11, d.text, false)
				percent(x+col-60, 50, 60, 27, 12)
				text(r.resets[i], "重置 "+reset, x, bodyBottom-16, col, 11, d.muted, false)
				bx, by, bw, bh = x, (78+bodyBottom-25)/2, col, 4
			default:
				y := float32(45)
				if i == 1 {
					y = bodyBottom - 27
				}
				text(r.labels[i], period, padding, y, 99, 11, d.text, false)
				text(r.resets[i], reset+" 重置", padding, y+15, 99, 11, d.muted, false)
				percent(w-padding-38, y+4, 38, 17, 10)
				bx, by, bw, bh = padding+110, y+13, w-2*padding-159, 4
			}
			box(r.tracks[i], bx, by, bw, bh, bh/2, d.track)
			box(r.fills[i], bx, by, bw*float32(max(0, min(100, lefts[i])))/100, bh, bh/2, d.accent)
			if lefts[i] < 0 {
				r.fills[i].Hide()
			}
		}
		if o.style == "obsidian" {
			line(r.separator, w/2, 52, w/2, bodyBottom, d.line)
		}
	}
	line(r.grip1, w-11, h-5, w-5, h-11, d.line)
	line(r.grip2, w-7, h-5, w-5, h-7, d.line)
	o.resizeArea.Move(fyne.NewPos((w-17)*s, (h-17)*s))
	o.resizeArea.Resize(fyne.NewSize(17*s, 17*s))
}

// Prevent floating point resize feedback from accumulating a subpixel on each drag event.
func overlayLogicalPixels(value float32) int { return int(math.Round(float64(value))) }
