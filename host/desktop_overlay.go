package main

import (
	"fmt"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// desktopOverlay 的所有方法都在 Fyne UI goroutine 中调用。
type desktopOverlay struct {
	win                    fyne.Window
	content                *container.ThemeOverride
	view                   *overlayView
	dragArea               *overlayDragArea
	resizeArea             *overlayDragArea
	resizeOrigin           *overlayResizeOrigin
	scale                  int
	style                  string
	layer                  string
	width, height, opacity int
	x, y                   int32
	positioned             bool
	dragOrigin             *overlayDragOrigin
	dragMoved              bool
	onMoved                func(int32, int32) error
	onResized              func(int, int, int32, int32) error
	onStyle                func(string)
	onLayer                func(string)
	onReset                func()
	visible                bool
	positionWarn           bool
	onError                func(error)
}

type overlayPoint struct{ X, Y int32 }
type overlayDragOrigin struct{ Window, Cursor overlayPoint }
type overlayResizeOrigin struct {
	Cursor overlayPoint
	Size   fyne.Size
}

func normalizeOverlayGeometry(width, height, opacity int) (int, int, int) {
	if width == 0 {
		width = 330
	}
	if height == 0 {
		height = 132
	}
	if opacity == 0 {
		opacity = 100
	}
	return max(180, min(1000, width)), max(120, min(800, height)), max(40, min(100, opacity))
}

func (t overlayTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	d := overlayDesignFor(t.style)
	switch name {
	case theme.ColorNameBackground, theme.ColorNameOverlayBackground:
		return d.background
	case theme.ColorNameForeground:
		return d.text
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		return d.muted
	case theme.ColorNamePrimary:
		return d.accent
	case theme.ColorNameHover, theme.ColorNameButton:
		return d.badge
	}
	return t.Theme.Color(name, theme.VariantDark)
}

// 标题区域使用屏幕光标坐标拖动，避免窗口移动改变相对坐标后反复抖动。
type overlayDragArea struct {
	widget.BaseWidget
	content   fyne.CanvasObject
	begin     func(*desktop.MouseEvent)
	move, end func()
	cursor    desktop.Cursor
}

func (d *overlayDragArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(d.content)
}
func (d *overlayDragArea) MouseDown(e *desktop.MouseEvent) {
	if e.Button == desktop.MouseButtonPrimary {
		d.begin(e)
	}
}
func (d *overlayDragArea) MouseUp(*desktop.MouseEvent) { d.end() }
func (d *overlayDragArea) Dragged(*fyne.DragEvent)     { d.move() }
func (d *overlayDragArea) DragEnd()                    { d.end() }
func (d *overlayDragArea) Cursor() desktop.Cursor {
	if d.cursor != nil {
		return d.cursor
	}
	return desktop.PointerCursor
}

func normalizeOverlayAppearance(scale int, layer string) (int, string) {
	if scale == 0 {
		scale = 100
	}
	if scale < 70 {
		scale = 70
	}
	if scale > 160 {
		scale = 160
	}
	if layer != "bottom" {
		layer = "top"
	}
	return scale, layer
}

// 只缩放悬浮窗，主窗口沿用系统主题和字号。
type overlayTheme struct {
	fyne.Theme
	scale float32
	style string
}

func (t overlayTheme) Size(name fyne.ThemeSizeName) float32 {
	var size float32
	switch name {
	case theme.SizeNameText:
		size = 12
	case theme.SizeNameInlineIcon:
		size = 13
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		size = 2
	default:
		size = t.Theme.Size(name)
	}
	return size * t.scale
}

func newDesktopOverlay(a fyne.App, hide func(), onError func(error)) *desktopOverlay {
	d, ok := a.Driver().(desktop.Driver)
	if !ok {
		return nil
	}
	return newDesktopOverlayWindow(d.CreateSplashWindow(), hide, onError)
}

func newDesktopOverlayWindow(w fyne.Window, hide func(), onError func(error)) *desktopOverlay {
	o := &desktopOverlay{win: w, onError: onError, scale: 100, layer: "top", style: defaultOverlayStyle, width: 330, height: 132, opacity: 100}
	o.win.SetTitle("AI 额度悬浮窗")
	// 固定大小模式会跳过 GLFW 的渲染视口更新，内容变化时出现顶部空白/底部裁切。
	o.win.SetFixedSize(false)
	o.win.SetPadded(false)
	o.win.SetCloseIntercept(hide)
	o.dragArea = &overlayDragArea{content: canvas.NewRectangle(color.Transparent), begin: o.beginDrag, move: o.moveDrag, end: o.endDrag}
	o.dragArea.ExtendBaseWidget(o.dragArea)
	o.resizeArea = &overlayDragArea{content: canvas.NewRectangle(color.Transparent), begin: o.beginResize, move: o.moveResize, end: o.endResize, cursor: desktop.HResizeCursor}
	o.resizeArea.ExtendBaseWidget(o.resizeArea)
	o.view = newOverlayView(o, hide)
	o.content = container.NewThemeOverride(o.view, overlayTheme{Theme: theme.LightTheme(), scale: 1, style: o.style})
	o.win.SetContent(o.content)
	o.resize()
	return o
}

func (o *desktopOverlay) setAppearance(scale int, layer string) {
	scale, layer = normalizeOverlayAppearance(scale, layer)
	if scale != o.scale {
		o.scale = scale
		o.refreshTheme()
		o.content.Refresh()
		o.resize()
	}
	o.layer = layer
	o.view.refreshLayerIndicator()
	o.position()
}

func (o *desktopOverlay) resize() {
	o.win.Resize(fyne.NewSize(float32(o.width), float32(o.height)).Max(o.content.MinSize()))
}

func (o *desktopOverlay) applyConfig(cfg Config) {
	style := normalizeOverlayStyle(cfg.OverlayStyle)
	if o.style != style {
		o.style = style
		o.refreshTheme()
	}
	o.width, o.height, o.opacity = normalizeOverlaySize(style, cfg.OverlayWidth, cfg.OverlayHeight, cfg.OverlayOpacity)
	o.x, o.y, o.positioned = cfg.OverlayX, cfg.OverlayY, cfg.OverlayPositionSet
	o.setAppearance(cfg.OverlayScale, cfg.OverlayLayer)
	o.resize()
	o.position()
}

func (o *desktopOverlay) refreshTheme() {
	base := theme.LightTheme()
	if o.style == "obsidian" {
		base = theme.DarkTheme()
	}
	o.content.Theme = overlayTheme{Theme: base, scale: float32(o.scale) / 100, style: o.style}
	o.content.Refresh()
}

func (o *desktopOverlay) toggleLayer() {
	layer := "top"
	if o.layer == "top" {
		layer = "bottom"
	}
	if o.onLayer != nil {
		o.onLayer(layer)
	} else {
		o.layer = layer
		o.view.Refresh()
		o.position()
	}
}

func (o *desktopOverlay) showMenu() {
	var items []*fyne.MenuItem
	for i, id := range overlayStyleIDs {
		style := id
		item := fyne.NewMenuItem(overlayStyleNames[i], func() {
			if o.onStyle != nil {
				o.onStyle(style)
			}
		})
		item.Checked = o.style == id
		items = append(items, item)
	}
	items = append(items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("切换置顶 / 置底", o.toggleLayer), fyne.NewMenuItem("回到右上角", func() {
		if o.onReset != nil {
			o.onReset()
		}
	}))
	menu := widget.NewPopUpMenu(fyne.NewMenu("", items...), o.win.Canvas())
	menu.ShowAtPosition(fyne.NewPos(max(0, o.win.Canvas().Size().Width-menu.MinSize().Width-10), 38))
}

func (o *desktopOverlay) beginResize(e *desktop.MouseEvent) {
	origin, err := beginDesktopOverlayDrag(o.win)
	if err != nil {
		o.onError(err)
		return
	}
	s := o.win.Canvas().Scale()
	o.resizeOrigin = &overlayResizeOrigin{Cursor: overlayPoint{origin.Window.X + int32(math.Round(float64(e.AbsolutePosition.X*s))), origin.Window.Y + int32(math.Round(float64(e.AbsolutePosition.Y*s)))}, Size: o.win.Canvas().Size()}
	o.x, o.y, o.positioned = origin.Window.X, origin.Window.Y, true
}
func (o *desktopOverlay) moveResize() {
	if o.resizeOrigin == nil {
		return
	}
	cursor, err := desktopOverlayCursor()
	if err != nil {
		o.onError(err)
		return
	}
	s := o.win.Canvas().Scale()
	origin := o.resizeOrigin
	o.width, o.height, _ = normalizeOverlaySize(o.style, overlayLogicalPixels(origin.Size.Width+float32(cursor.X-origin.Cursor.X)/s), overlayLogicalPixels(origin.Size.Height+float32(cursor.Y-origin.Cursor.Y)/s), o.opacity)
	o.resize()
}
func (o *desktopOverlay) endResize() {
	if o.resizeOrigin == nil {
		return
	}
	o.resizeOrigin = nil
	if o.onResized != nil {
		if err := o.onResized(o.width, o.height, o.x, o.y); err != nil {
			o.onError(err)
		}
	}
	o.position()
}

func (o *desktopOverlay) beginDrag(e *desktop.MouseEvent) {
	origin, err := beginDesktopOverlayDrag(o.win)
	if err != nil {
		o.onError(err)
		return
	}
	// UI 事件可能排队；GetCursorPos 此时可能已到终点，起点必须取按下事件。
	scale := o.win.Canvas().Scale()
	origin.Cursor = overlayPoint{
		origin.Window.X + int32(math.Round(float64(e.AbsolutePosition.X*scale))),
		origin.Window.Y + int32(math.Round(float64(e.AbsolutePosition.Y*scale))),
	}
	o.dragOrigin = &origin
	o.dragMoved = false
}

func (o *desktopOverlay) moveDrag() {
	if o.dragOrigin == nil {
		return
	}
	point, err := moveDesktopOverlayDrag(o.win, *o.dragOrigin)
	if err != nil {
		o.onError(err)
		return
	}
	o.x, o.y, o.positioned, o.dragMoved = point.X, point.Y, true, true
}

func (o *desktopOverlay) endDrag() {
	if o.dragOrigin == nil {
		return
	}
	o.dragOrigin = nil
	if o.dragMoved && o.onMoved != nil {
		if err := o.onMoved(o.x, o.y); err != nil {
			o.onError(err)
		}
	}
	o.position()
}

func (o *desktopOverlay) show() {
	if o.visible {
		return
	}
	o.visible = true
	o.win.Show()
	o.position()
}

func (o *desktopOverlay) hide() {
	o.visible = false
	o.win.Hide()
}

func (o *desktopOverlay) position() {
	if !o.visible || o.dragOrigin != nil || o.resizeOrigin != nil {
		return
	}
	if err := positionDesktopOverlay(o.win, o.layer, o.opacity, o.x, o.y, o.positioned, o.style); err != nil && !o.positionWarn {
		o.positionWarn = true
		o.onError(err)
	}
}

func overlayTitle(source string) string {
	switch source {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "reclaude":
		return "ReClaude"
	case "kimi":
		return "Kimi"
	default:
		if source != "" {
			return source
		}
		return "AI"
	}
}

func (o *desktopOverlay) update(f Frame) {
	o.view.frame = f
	o.view.hasFrame = true
	o.view.Refresh()
	o.resize()
	o.position() // 同时适应分辨率、任务栏位置等工作区变化。
}

func (g *GUI) setDesktopOverlay(enabled bool) {
	if enabled == g.engine.Config().DesktopOverlay || !desktopOverlaySupported {
		return
	}
	if err := g.engine.SetDesktopOverlay(enabled); err != nil {
		g.overlayCheck.SetChecked(g.engine.Config().DesktopOverlay)
		dialog.ShowError(fmt.Errorf("保存悬浮窗开关失败：%w", err), g.win)
		return
	}
	g.overlayCheck.SetChecked(enabled)
	if g.overlayItem != nil {
		g.overlayItem.Checked = enabled
		g.trayMenu.Refresh()
	}
	g.applyDesktopOverlay()
}

func (g *GUI) applyDesktopOverlay() {
	if !desktopOverlaySupported {
		return
	}
	if !g.engine.Config().DesktopOverlay {
		if g.overlay != nil {
			g.overlay.hide()
		}
		return
	}
	if g.overlay == nil {
		g.overlay = newDesktopOverlay(g.app, func() { g.setDesktopOverlay(false) }, func(err error) {
			g.engine.log(LogWarn, "overlay", "设置悬浮窗位置失败: %v", err)
		})
		if g.overlay != nil {
			g.overlay.onMoved = func(x, y int32) error { return g.engine.SetOverlayPosition(x, y, true) }
			g.overlay.onStyle = g.setOverlayStyle
			g.overlay.onLayer = func(layer string) { g.setOverlayAppearance(g.engine.Config().OverlayScale, layer) }
			g.overlay.onReset = g.resetOverlayPosition
			g.overlay.onResized = func(width, height int, x, y int32) error {
				err := g.engine.SetOverlayBounds(width, height, x, y)
				g.syncOverlayControls()
				return err
			}
		}
	}
	if g.overlay != nil {
		cfg := g.engine.Config()
		g.overlay.applyConfig(cfg)
		frames := g.engine.buildFrames()
		if len(frames) > 0 {
			g.overlay.update(frames[0])
		}
		g.overlay.show()
	}
}

func (g *GUI) setOverlayAppearance(scale int, layer string) {
	current := g.engine.Config()
	scale, layer = normalizeOverlayAppearance(scale, layer)
	if current.OverlayScale == scale && current.OverlayLayer == layer {
		return
	}
	if err := g.engine.SetOverlayAppearance(scale, layer); err != nil {
		dialog.ShowError(fmt.Errorf("保存悬浮窗外观失败：%w", err), g.win)
		return
	}
	if g.overlay != nil {
		cfg := g.engine.Config()
		g.overlay.applyConfig(cfg)
	}
	g.syncOverlayControls()
}

func (g *GUI) setOverlayGeometry(width, height, opacity int) {
	if err := g.engine.SetOverlayGeometry(width, height, opacity); err != nil {
		g.syncOverlayControls()
		dialog.ShowError(fmt.Errorf("保存悬浮窗尺寸失败：%w", err), g.win)
		return
	}
	if g.overlay != nil {
		g.overlay.applyConfig(g.engine.Config())
	}
	g.syncOverlayControls()
}

func (g *GUI) setOverlayStyle(style string) {
	if normalizeOverlayStyle(style) == g.engine.Config().OverlayStyle {
		return
	}
	if err := g.engine.SetOverlayStyle(style); err != nil {
		g.syncOverlayControls()
		dialog.ShowError(err, g.win)
		return
	}
	if g.overlay != nil {
		g.overlay.applyConfig(g.engine.Config())
	}
	g.syncOverlayControls()
}

func (g *GUI) syncOverlayControls() {
	if g.syncingOverlay {
		return
	}
	g.syncingOverlay = true
	defer func() { g.syncingOverlay = false }()
	c := g.engine.Config()
	if g.overlayStyleSelect != nil {
		g.overlayStyleSelect.SetSelected(overlayDesignFor(c.OverlayStyle).name)
	}
	if g.overlayLayerSelect != nil {
		name := "始终置顶"
		if c.OverlayLayer == "bottom" {
			name = "始终置底"
		}
		if g.overlayLayerSelect.Selected != name {
			g.overlayLayerSelect.SetSelected(name)
		}
	}
	if g.overlayWidthSlider != nil {
		g.overlayWidthSlider.SetValue(float64(c.OverlayWidth))
	}
	if g.overlayHeightSlider != nil {
		g.overlayHeightSlider.SetValue(float64(c.OverlayHeight))
	}
}

func (g *GUI) resetOverlayPosition() {
	if err := g.engine.SetOverlayPosition(0, 0, false); err != nil {
		dialog.ShowError(err, g.win)
		return
	}
	if g.overlay != nil {
		g.overlay.applyConfig(g.engine.Config())
	}
}
