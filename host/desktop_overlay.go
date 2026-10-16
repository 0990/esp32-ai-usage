package main

import (
	"fmt"

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
	win          fyne.Window
	title        *widget.Label
	values       [2]*widget.Label
	resets       [2]*widget.Label
	bars         [2]*widget.ProgressBar
	status       *widget.Label
	visible      bool
	positionWarn bool
	onError      func(error)
}

func newDesktopOverlay(a fyne.App, hide func(), onError func(error)) *desktopOverlay {
	d, ok := a.Driver().(desktop.Driver)
	if !ok {
		return nil
	}
	return newDesktopOverlayWindow(d.CreateSplashWindow(), hide, onError)
}

func newDesktopOverlayWindow(w fyne.Window, hide func(), onError func(error)) *desktopOverlay {
	o := &desktopOverlay{win: w, onError: onError}
	o.win.SetTitle("AI 额度悬浮窗")
	o.win.SetFixedSize(true)
	o.win.SetCloseIntercept(hide)
	o.title = widget.NewLabelWithStyle("AI 额度", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), hide)
	closeBtn.Importance = widget.LowImportance
	content := container.NewVBox(container.NewBorder(nil, nil, nil, closeBtn, o.title))
	for i, name := range []string{"5 小时", "一周"} {
		o.values[i] = widget.NewLabelWithStyle(name+" · 等待数据", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		o.resets[i] = widget.NewLabel("重置：--")
		o.bars[i] = widget.NewProgressBar()
		o.bars[i].Hide()
		content.Add(container.NewVBox(o.values[i], o.resets[i], o.bars[i]))
	}
	o.status = widget.NewLabel("正在查询使用量…")
	content.Add(o.status)
	bg := canvas.NewRectangle(theme.DarkTheme().Color(theme.ColorNameBackground, theme.VariantDark))
	o.win.SetContent(container.NewThemeOverride(container.NewStack(bg, container.NewPadded(content)), theme.DarkTheme()))
	o.win.Resize(fyne.NewSize(340, content.MinSize().Height+16))
	return o
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
	if !o.visible {
		return
	}
	if err := positionDesktopOverlay(o.win); err != nil && !o.positionWarn {
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

func overlayWindowText(name string, left int, reset string) (string, string) {
	if left < 0 {
		return name + " · 暂无数据", "重置：--"
	}
	if reset == "" {
		reset = "--"
	}
	return fmt.Sprintf("%s · 剩余 %d%%", name, left), "重置：" + reset
}

func (o *desktopOverlay) update(f Frame) {
	for _, value := range o.values {
		value.Show()
	}
	o.title.SetText(overlayTitle(f.Source) + " · 额度余量")
	if f.Kind == "money" {
		o.title.SetText(overlayTitle(f.Source) + " · 中转站余额")
		label := "已用"
		if f.L1 == "Today" {
			label = "今日已用"
		}
		o.values[0].SetText(label + "：" + f.V1)
		o.values[1].SetText("剩余余额：" + f.V2)
		if f.L1 == "Balance" {
			o.values[0].SetText("剩余余额：" + f.V1)
			o.values[1].Hide()
		}
		for i := range o.bars {
			o.bars[i].Hide()
			o.resets[i].Hide()
		}
	} else {
		for i, w := range []struct {
			name  string
			left  int
			reset string
		}{{"5 小时", f.P1, f.S1}, {"一周", f.P2, f.S2}} {
			value, reset := overlayWindowText(w.name, w.left, w.reset)
			o.values[i].SetText(value)
			o.resets[i].SetText(reset)
			o.resets[i].Show()
			if w.left >= 0 {
				o.bars[i].SetValue(float64(w.left) / 100)
				o.bars[i].Show()
			} else {
				o.bars[i].Hide()
			}
		}
	}
	switch {
	case f.Old:
		o.status.SetText("数据已过期 · 正在重试更新")
	case f.Kind != "money" && f.P1 < 0 && f.P2 < 0:
		o.status.SetText("查询失败 · 请查看主窗口活动日志")
	case f.Kind == "money" && (f.V1 == "ERR" || f.V2 == "ERR"):
		o.status.SetText("查询失败 · 请查看主窗口活动日志")
	default:
		o.status.SetText("自动刷新 · 重置时间为本地时间")
	}
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
	}
	if g.overlay != nil {
		frames := g.engine.buildFrames()
		if len(frames) > 0 {
			g.overlay.update(frames[0])
		}
		g.overlay.show()
	}
}
