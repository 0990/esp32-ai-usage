package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

//go:embed icon.png
var iconPNG []byte

const maxLogLines = 500

// 测试输入框的初始示例 Frame（新协议：一行 = 一屏）。
const sampleJSON = `{"ic":"claude","k":"pct","l1":"5h","p1":50,"s1":"15:47","l2":"1w","p2":30,"s2":"06-25 14:30"}`

// GUI 持有 Fyne 对象与后台引擎。
type GUI struct {
	app    fyne.App
	win    fyne.Window
	engine *Engine

	logLines                                []string
	logLabel                                *widget.Label
	logScroll                               *container.Scroll
	status                                  *widget.Label  // 常驻底部的串口状态
	actLabel                                *widget.Label  // 常驻底部右侧：Claude / Codex 的工作状态
	actState                                map[string]int // provider -> Act*（OnActivity 回调更新）
	overlay                                 *desktopOverlay
	overlayCheck                            *widget.Check
	overlayItem                             *fyne.MenuItem
	trayMenu                                *fyne.Menu
	overlayStyleSelect, overlayLayerSelect  *widget.Select
	overlayWidthSlider, overlayHeightSlider *widget.Slider
	syncingOverlay                          bool
}

// RunGUI 构建界面、启动引擎并进入事件循环（阻塞直到退出）。
// ln 是单实例 listener：后续被双击启动的实例会连上来，唤起本窗口。
func RunGUI(cfg Config, cfgPath string, ln net.Listener) {
	a := app.NewWithID("com.local.esp32-ai-usage")
	icon := fyne.NewStaticResource("icon.png", iconPNG)
	a.SetIcon(icon)

	w := a.NewWindow("ESP32 AI 额度表")
	g := &GUI{app: a, win: w, engine: NewEngine(cfg, cfgPath), actState: map[string]int{}}

	// 引擎回调 → UI：一律包进 fyne.Do（Fyne 要求 UI 改动在 UI goroutine）。
	g.engine.OnLog = func(e LogEntry) { fyne.Do(func() { g.appendLog(e) }) }
	g.engine.OnStatus = func(s string) { fyne.Do(func() { g.status.SetText(s) }) }
	g.engine.OnActivity = func(p string, s int) { fyne.Do(func() { g.setActivity(p, s) }) }
	g.engine.OnDisplay = func(f Frame) {
		fyne.Do(func() {
			if g.overlay != nil {
				g.overlay.update(f)
			}
		})
	}

	// 单实例：后启动的实例来敲门时，把本窗口拉到前台（feature 3）。
	if ln != nil {
		go serveSingleInstance(ln, func() {
			fyne.Do(func() { w.Show(); w.RequestFocus() })
		})
	}

	w.SetContent(g.buildUI())
	w.Resize(fyne.NewSize(760, 520))
	g.fitWindowHeight()

	g.setupTray(icon)
	a.Lifecycle().SetOnStarted(func() {
		fyne.Do(func() { g.applyDesktopOverlay() })
	})
	w.SetCloseIntercept(func() { w.Hide() }) // 关窗 → 收进托盘，不退出

	// 首次运行：创建配置文件（开机启动默认关闭，setAutostart(false) 只是确保注册表无残留）。
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := saveConfig(cfgPath, cfg); err != nil {
			g.engine.log(LogWarn, "engine", "首次写配置失败: %v", err)
		}
		if err := setAutostart(cfg.Autostart); err != nil {
			g.engine.log(LogWarn, "autostart", "设置开机启动失败: %v", err)
		}
	}

	g.engine.Start(context.Background()) // 含串口连接/断线重连（engine 内的管理循环）

	w.ShowAndRun()
	g.engine.Stop() // 事件循环结束（真正退出）后清理
}

// buildUI 组装三个 Tab 页 + 底部常驻串口状态栏。
func (g *GUI) buildUI() fyne.CanvasObject {
	// 日志/状态控件先建好，避免构建期的任何回调写到 nil。
	g.logLabel = widget.NewLabel("")
	g.logLabel.Wrapping = fyne.TextWrapWord
	g.logLabel.TextStyle = fyne.TextStyle{Monospace: true}
	g.logScroll = container.NewVScroll(g.logLabel)
	g.status = widget.NewLabel("串口: 未连接")
	g.actLabel = widget.NewLabel("")
	g.setActivity("", 0) // 初始文案

	tabs := container.NewAppTabs(
		container.NewTabItem("设置", g.buildSettingsTab()),
		container.NewTabItem("测试命令", g.buildTestTab()),
		container.NewTabItem("活动日志", g.buildLogTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	bottom := container.NewBorder(nil, nil, g.status, g.actLabel)
	return container.NewBorder(nil, bottom, nil, nil, tabs)
}

// setActivity 更新底部右侧的工作状态文案。provider 为空只重绘。须在 UI 线程调用。
func (g *GUI) setActivity(provider string, state int) {
	if provider != "" {
		g.actState[provider] = state
	}
	g.actLabel.SetText(fmt.Sprintf("Claude: %s · Codex: %s",
		actName(g.actState["claude"]), actName(g.actState["codex"])))
}

// buildTestTab：自定义 JSON 输入框 + 发送按钮。
func (g *GUI) buildTestTab() fyne.CanvasObject {
	jsonIn := widget.NewMultiLineEntry()
	jsonIn.SetPlaceHolder(`一行 Frame JSON（一行=一屏），例如：` + "\n" + sampleJSON)
	jsonIn.SetText(sampleJSON)
	jsonIn.Wrapping = fyne.TextWrapBreak

	sendBtn := widget.NewButton("发送到 ESP32", func() {
		text := strings.TrimSpace(jsonIn.Text)
		if text == "" {
			return
		}
		var f Frame
		if err := json.Unmarshal([]byte(text), &f); err != nil {
			dialog.ShowError(fmt.Errorf("JSON 无效: %w", err), g.win)
			return
		}
		out, _ := json.Marshal(f) // 重新序列化 → 保证单行、字段合法
		g.engine.Send(out)
		g.engine.log(LogInfo, "test", "已发送测试 Frame (%d 字节)", len(out)+1)
	})
	sendBtn.Importance = widget.HighImportance

	top := container.NewVBox(
		widget.NewLabelWithStyle("发送自定义 Frame 到串口", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("输入一行 Frame JSON（k=pct 百分比页 / k=money 中转站页），点「发送」即校验并下发（验证 OLED 显示）。"),
	)
	return container.NewBorder(top, sendBtn, nil, nil, jsonIn)
}

// buildSettingsTab：config.json 的全部字段 + 开机启动 + 保存。
func (g *GUI) buildSettingsTab() fyne.CanvasObject {
	cfg := g.engine.Config()

	portEntry := widget.NewEntry()
	portEntry.SetText(cfg.Port)
	portEntry.SetPlaceHolder("AUTO 或 COM5")

	baudEntry := widget.NewEntry()
	baudEntry.SetText(strconv.Itoa(cfg.Baud))

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(cfg.IntervalSeconds))

	retryIntervalEntry := widget.NewEntry()
	retryIntervalEntry.SetText(strconv.Itoa(cfg.RetryIntervalSeconds))

	pageIntervalEntry := widget.NewEntry()
	pageIntervalEntry.SetText(strconv.Itoa(cfg.PageIntervalSeconds))

	// 监控对象：横向多选框，可同时勾多个（如 kimi+codex），轮播显示；全勾存为 "all"。
	providerChecks := widget.NewCheckGroup(providerOrder, nil)
	providerChecks.Horizontal = true
	providerChecks.SetSelected(providerList(cfg.Provider))

	proxyEntry := widget.NewEntry()
	proxyEntry.SetText(cfg.Proxy)
	proxyEntry.SetPlaceHolder("如 http://127.0.0.1:7890，留空用 HTTPS_PROXY")

	claudeEntry := widget.NewEntry()
	claudeEntry.SetText(cfg.ClaudeEndPoint)
	claudeEntry.SetPlaceHolder("留空走 ANTHROPIC_BASE_URL（中转地址）")

	codexEntry := widget.NewEntry()
	codexEntry.SetText(cfg.CodexEndpoint)
	codexEntry.SetPlaceHolder(defaultCodexEndpoint)

	customObjects := append([]CustomObject(nil), cfg.CustomObjects...)
	customList := container.NewVBox()
	var rebuildCustom func()
	var editCustom func(int)
	editCustom = func(index int) {
		obj := CustomObject{ID: newCustomID(), Enabled: true, Type: "sub2api"}
		if index >= 0 {
			obj = customObjects[index]
		}
		name := widget.NewEntry()
		name.SetText(obj.Name)
		typ := widget.NewSelect([]string{"sub2api", "newapi"}, nil)
		typ.SetSelected(obj.Type)
		base := widget.NewEntry()
		base.SetText(obj.BaseURL)
		base.SetPlaceHolder("https://relay.example 或 https://relay.example/v1")
		key := widget.NewPasswordEntry()
		key.SetText(obj.APIKey)
		var d *dialog.CustomDialog
		submit := widget.NewButton("确定", func() {
			obj.Name = strings.TrimSpace(name.Text)
			obj.Type = typ.Selected
			obj.BaseURL = strings.TrimRight(strings.TrimSpace(base.Text), "/")
			obj.APIKey = strings.TrimSpace(key.Text)
			if err := obj.validate(); err != nil {
				dialog.ShowError(err, g.win)
				return
			}
			for i, other := range customObjects {
				if i != index && strings.EqualFold(other.Name, obj.Name) {
					dialog.ShowError(fmt.Errorf("名称已存在，请使用不同名称"), g.win)
					return
				}
			}
			if index >= 0 {
				customObjects[index] = obj
			} else {
				customObjects = append(customObjects, obj)
			}
			rebuildCustom()
			d.Hide()
		})
		form := widget.NewForm(widget.NewFormItem("名称", name), widget.NewFormItem("类型", typ), widget.NewFormItem("Base URL", base), widget.NewFormItem("API key", key))
		d = dialog.NewCustom("自定义监控对象", "取消", container.NewVBox(form, widget.NewLabel("点击设置页的保存后生效"), submit), g.win)
		d.Resize(fyne.NewSize(580, 320))
		d.Show()
	}
	rebuildCustom = func() {
		customList.Objects = nil
		for i, obj := range customObjects {
			index := i
			check := widget.NewCheck(obj.Name+" ("+obj.Type+")", func(on bool) { customObjects[index].Enabled = on })
			check.SetChecked(obj.Enabled)
			edit := widget.NewButton("编辑", func() { editCustom(index) })
			remove := widget.NewButton("删除", func() { customObjects = append(customObjects[:index], customObjects[index+1:]...); rebuildCustom() })
			customList.Add(container.NewBorder(nil, nil, nil, container.NewHBox(edit, remove), check))
		}
		customList.Refresh()
		if g.win.Canvas().Content() != nil {
			g.fitWindowHeight()
		}
	}
	rebuildCustom()
	addCustom := widget.NewButton("新增自定义对象", func() { editCustom(-1) })
	monitorObjects := container.NewVBox(providerChecks, customList, container.NewHBox(addCustom))

	autostartCheck := widget.NewCheck("开机时自动启动本程序", nil)
	autostartCheck.SetChecked(cfg.Autostart)
	g.overlayCheck = widget.NewCheck("在桌面显示额度（立即生效）", g.setDesktopOverlay)
	g.overlayCheck.Disable()
	if desktopOverlaySupported {
		g.overlayCheck.Enable()
	}
	g.overlayCheck.SetChecked(cfg.DesktopOverlay)
	g.overlayStyleSelect = widget.NewSelect(overlayStyleNames, nil)
	g.overlayStyleSelect.SetSelected(overlayDesignFor(cfg.OverlayStyle).name)
	g.overlayStyleSelect.OnChanged = func(name string) {
		for i, label := range overlayStyleNames {
			if name == label {
				g.setOverlayStyle(overlayStyleIDs[i])
				return
			}
		}
	}
	_, overlayLayer := normalizeOverlayAppearance(cfg.OverlayScale, cfg.OverlayLayer)
	width, height, opacity := normalizeOverlaySize(cfg.OverlayStyle, cfg.OverlayWidth, cfg.OverlayHeight, cfg.OverlayOpacity)
	geometrySlider := func(value, low, high float64, unit string, save func(int)) (*widget.Slider, *fyne.Container) {
		slider := widget.NewSlider(low, high)
		slider.Step = 1
		slider.SetValue(value)
		label := widget.NewLabel(fmt.Sprintf("%d%s", int(value), unit))
		slider.OnChanged = func(v float64) { label.SetText(fmt.Sprintf("%d%s", int(v), unit)) }
		slider.OnChangeEnded = func(v float64) {
			if !g.syncingOverlay {
				save(int(v))
			}
		}
		return slider, container.NewBorder(nil, nil, nil, label, slider)
	}
	widthSlider, widthRow := geometrySlider(float64(width), 180, 1000, "", func(v int) {
		c := g.engine.Config()
		g.setOverlayGeometry(v, c.OverlayHeight, c.OverlayOpacity)
	})
	heightSlider, heightRow := geometrySlider(float64(height), 120, 800, "", func(v int) {
		c := g.engine.Config()
		g.setOverlayGeometry(c.OverlayWidth, v, c.OverlayOpacity)
	})
	g.overlayWidthSlider, g.overlayHeightSlider = widthSlider, heightSlider
	opacitySlider, opacityRow := geometrySlider(float64(opacity), 40, 100, "%", func(v int) {
		c := g.engine.Config()
		g.setOverlayGeometry(c.OverlayWidth, c.OverlayHeight, v)
	})
	overlayLayerSelect := widget.NewSelect([]string{"始终置顶", "始终置底"}, nil)
	g.overlayLayerSelect = overlayLayerSelect
	if overlayLayer == "bottom" {
		overlayLayerSelect.SetSelected("始终置底")
	} else {
		overlayLayerSelect.SetSelected("始终置顶")
	}
	overlayLayerSelect.OnChanged = func(s string) {
		layer := "top"
		if s == "始终置底" {
			layer = "bottom"
		}
		g.setOverlayAppearance(g.engine.Config().OverlayScale, layer)
	}
	if !desktopOverlaySupported {
		g.overlayStyleSelect.Disable()
		widthSlider.Disable()
		heightSlider.Disable()
		opacitySlider.Disable()
		overlayLayerSelect.Disable()
	}
	resetOverlay := widget.NewButton("回到右上角", g.resetOverlayPosition)
	if !desktopOverlaySupported {
		resetOverlay.Disable()
	}
	overlayControls := container.NewVBox(g.overlayCheck,
		widget.NewForm(widget.NewFormItem("样式", g.overlayStyleSelect), widget.NewFormItem("宽度", widthRow), widget.NewFormItem("高度", heightRow),
			widget.NewFormItem("不透明度", opacityRow), widget.NewFormItem("层级", overlayLayerSelect)),
		container.NewHBox(widget.NewLabel("拖动悬浮窗顶部名称可移动"), resetOverlay))

	// 顶部只放常用项；其余折叠进「更多」。
	fetchNowBtn := widget.NewButton("立即刷新使用量", func() {
		g.engine.log(LogInfo, "engine", "手动触发：立即拉取使用量")
		g.engine.Reload()
	})

	// 工作状态钩子：往 Claude Code / Codex 的 hook 配置里写回调，让屏幕显示「正在运行」标志。
	hooksStatus := widget.NewLabel(activityHooksStatus())
	installHooksBtn := widget.NewButton("安装", func() {
		if err := installActivityHooks(); err != nil {
			g.engine.log(LogError, "activity", "安装钩子失败: %v", err)
			dialog.ShowError(err, g.win)
		} else {
			g.engine.log(LogInfo, "activity", "已安装工作状态钩子（Claude: %s；Codex: %s）", claudeSettingsPath(), codexHooksPath())
			dialog.ShowInformation("钩子已安装",
				"已写入：\n"+
					"  "+claudeSettingsPath()+"\n    Claude Code：新开的会话立即生效。\n"+
					"  "+codexHooksPath()+"\n    Codex：需在 Codex 里输入 /hooks 审核并信任新钩子，否则不会执行。\n\n"+
					"之后 CLI 工作时屏幕左下角会显示跑动的点，等你授权时图标闪烁。",
				g.win)
		}
		hooksStatus.SetText(activityHooksStatus())
	})
	removeHooksBtn := widget.NewButton("移除", func() {
		if err := removeActivityHooks(); err != nil {
			g.engine.log(LogError, "activity", "移除钩子失败: %v", err)
			dialog.ShowError(err, g.win)
		} else {
			g.engine.log(LogInfo, "activity", "已移除工作状态钩子")
		}
		hooksStatus.SetText(activityHooksStatus())
	})
	hooksRow := container.NewHBox(hooksStatus, installHooksBtn, removeHooksBtn)

	brightnessSlider := widget.NewSlider(0, 255)
	brightnessSlider.Step = 1
	brightnessValue := widget.NewLabel("")
	brightnessSlider.OnChanged = func(value float64) { brightnessValue.SetText(fmt.Sprintf("%d / 255", int(value))) }
	brightnessSlider.SetValue(float64(clampOLEDBrightness(cfg.OLEDBrightness)))
	brightnessValue.SetText(fmt.Sprintf("%d / 255", int(brightnessSlider.Value)))
	brightnessRow := container.NewBorder(nil, nil, nil, brightnessValue, brightnessSlider)

	topForm := widget.NewForm(
		widget.NewFormItem("监控对象", monitorObjects),
		widget.NewFormItem("OLED 亮度", brightnessRow),
		widget.NewFormItem("开机启动", autostartCheck),
		widget.NewFormItem("桌面悬浮窗", overlayControls),
		widget.NewFormItem("运行标志钩子", hooksRow),
	)
	moreForm := widget.NewForm(
		widget.NewFormItem("串口 Port", portEntry),
		widget.NewFormItem("波特率 Baud", baudEntry),
		widget.NewFormItem("刷新间隔(成功,秒)", intervalEntry),
		widget.NewFormItem("重试间隔(失败,秒)", retryIntervalEntry),
		widget.NewFormItem("轮播间隔(秒)", pageIntervalEntry),
		widget.NewFormItem("代理 Proxy", proxyEntry),
		widget.NewFormItem("Claude 端点", claudeEntry),
		widget.NewFormItem("Codex 端点", codexEntry),
	)
	// 「更多」不用 Accordion：展开时窗口直接变长（不出现滚动条），收起时缩回。
	moreForm.Hide()
	var moreBtn *widget.Button
	moreBtn = widget.NewButton("更多 ▾", func() {
		if moreForm.Visible() {
			moreForm.Hide()
			moreBtn.SetText("更多 ▾")
		} else {
			moreForm.Show()
			moreBtn.SetText("收起 ▴")
		}
		g.fitWindowHeight()
	})

	save := func() {
		baud, err := strconv.Atoi(strings.TrimSpace(baudEntry.Text))
		if err != nil || baud <= 0 {
			dialog.ShowError(fmt.Errorf("波特率必须是正整数"), g.win)
			return
		}
		interval, err := strconv.Atoi(strings.TrimSpace(intervalEntry.Text))
		if err != nil || interval <= 0 {
			dialog.ShowError(fmt.Errorf("刷新间隔必须是正整数"), g.win)
			return
		}
		retryInterval, err := strconv.Atoi(strings.TrimSpace(retryIntervalEntry.Text))
		if err != nil || retryInterval <= 0 {
			dialog.ShowError(fmt.Errorf("重试间隔必须是正整数"), g.win)
			return
		}
		pageInterval, err := strconv.Atoi(strings.TrimSpace(pageIntervalEntry.Text))
		if err != nil || pageInterval <= 0 {
			dialog.ShowError(fmt.Errorf("轮播间隔必须是正整数"), g.win)
			return
		}
		hasCustom := false
		for _, obj := range customObjects {
			if obj.Enabled {
				hasCustom = true
			}
		}
		if len(providerChecks.Selected) == 0 && !hasCustom {
			dialog.ShowError(fmt.Errorf("请至少勾选一个监控对象"), g.win)
			return
		}
		providers := map[string]bool{}
		for _, p := range providerChecks.Selected {
			providers[p] = true
		}
		old := g.engine.Config()
		newCfg := Config{
			OLEDBrightness:       int(brightnessSlider.Value),
			Port:                 strings.TrimSpace(portEntry.Text),
			Baud:                 baud,
			IntervalSeconds:      interval,
			RetryIntervalSeconds: retryInterval,
			PageIntervalSeconds:  pageInterval,
			Provider:             joinProviders(providers),
			Proxy:                strings.TrimSpace(proxyEntry.Text),
			ClaudeEndPoint:       strings.TrimSpace(claudeEntry.Text),
			CodexEndpoint:        strings.TrimSpace(codexEntry.Text),
			CustomObjects:        append([]CustomObject(nil), customObjects...),
			KimiEndpoint:         old.KimiEndpoint,
			Autostart:            autostartCheck.Checked,
			DesktopOverlay:       old.DesktopOverlay,
			OverlayScale:         old.OverlayScale,
			OverlayLayer:         old.OverlayLayer,
			OverlayStyle:         old.OverlayStyle,
			OverlayWidth:         old.OverlayWidth,
			OverlayHeight:        old.OverlayHeight,
			OverlayOpacity:       old.OverlayOpacity,
			OverlayX:             old.OverlayX,
			OverlayY:             old.OverlayY,
			OverlayPositionSet:   old.OverlayPositionSet,
		}
		if newCfg.Port == "" {
			newCfg.Port = "AUTO"
		}
		if err := g.engine.UpdateConfig(newCfg); err != nil {
			dialog.ShowError(err, g.win)
			return
		}
		if err := setAutostart(newCfg.Autostart); err != nil {
			g.engine.log(LogWarn, "autostart", "设置开机启动失败: %v", err)
			dialog.ShowError(fmt.Errorf("配置已保存，但设置开机启动失败：%w", err), g.win)
		} else {
			dialog.ShowInformation("已保存", "配置已保存到：\n"+g.engine.cfgPath, g.win)
		}
		// 串口（port/baud）变化才重连，避免每次保存都打断 ESP32。
		if newCfg.Port != old.Port || newCfg.Baud != old.Baud {
			g.engine.log(LogInfo, "serial", "串口设置已变化，重新连接…")
			g.engine.ReconnectSerial()
		}
	}
	saveBtn := widget.NewButton("保存", save)
	saveBtn.Importance = widget.HighImportance

	return container.NewVBox(
		container.NewHBox(fetchNowBtn),
		topForm,
		moreBtn,
		moreForm,
		container.NewHBox(layout.NewSpacer(), saveBtn),
	)
}

// fitWindowHeight 让窗口高度跟随内容所需（展开「更多」时拉长、收起时缩回），
// 避免设置页出现滚动。宽度保持不变，高度不小于初始 520。须在 UI 线程调用。
func (g *GUI) fitWindowHeight() {
	cur := g.win.Canvas().Size()
	need := g.win.Canvas().Content().MinSize().Height + 8
	if need < 520 {
		need = 520
	}
	g.win.Resize(fyne.NewSize(cur.Width, need))
}

// buildLogTab：滚动日志 + 清空按钮。
func (g *GUI) buildLogTab() fyne.CanvasObject {
	clearBtn := widget.NewButton("清空", func() {
		g.logLines = nil
		g.logLabel.SetText("")
	})
	top := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("活动日志", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		clearBtn, nil)
	return container.NewBorder(top, nil, nil, nil, g.logScroll)
}

// appendLog 追加一条日志（限长 maxLogLines），并滚到底。须在 UI goroutine 调用。
func (g *GUI) appendLog(e LogEntry) {
	line := fmt.Sprintf("%s [%s][%s] %s",
		e.Time.Format("15:04:05"), e.Level, e.Tag, e.Msg)
	g.logLines = append(g.logLines, line)
	if len(g.logLines) > maxLogLines {
		g.logLines = g.logLines[len(g.logLines)-maxLogLines:]
	}
	if g.logLabel == nil { // 控件尚未就绪：先攒着，不丢日志
		return
	}
	g.logLabel.SetText(strings.Join(g.logLines, "\n"))
	g.logScroll.ScrollToBottom()
}

// setupTray 安装系统托盘图标与菜单（仅桌面驱动支持）。
func (g *GUI) setupTray(icon fyne.Resource) {
	desk, ok := g.app.(desktop.App)
	if !ok {
		return
	}
	g.overlayItem = fyne.NewMenuItem("显示桌面额度悬浮窗", func() {
		g.setDesktopOverlay(!g.engine.Config().DesktopOverlay)
	})
	g.overlayItem.Checked = g.engine.Config().DesktopOverlay
	g.overlayItem.Disabled = !desktopOverlaySupported
	menu := fyne.NewMenu("ESP32 AI 额度表",
		fyne.NewMenuItem("显示窗口", func() { g.win.Show() }),
		fyne.NewMenuItem("隐藏窗口", func() { g.win.Hide() }),
		g.overlayItem,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("退出", func() { g.quit() }),
	)
	g.trayMenu = menu
	desk.SetSystemTrayMenu(menu) // 先建托盘
	desk.SetSystemTrayIcon(icon) // 再设图标
	// 关联主窗口：点击/双击托盘图标 → 显示主窗口（右键仍弹菜单）。
	// 内部用的是 systray.SetOnTapped，左键抬起即触发，双击自然生效。
	desk.SetSystemTrayWindow(g.win)
}

// quit 干净退出：停引擎并结束事件循环。
func (g *GUI) quit() {
	g.engine.Stop()
	g.app.Quit()
}
