package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
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

	logLines  []string
	logLabel  *widget.Label
	logScroll *container.Scroll
	status    *widget.Label // 常驻底部的串口状态
}

// RunGUI 构建界面、启动引擎并进入事件循环（阻塞直到退出）。
// ln 是单实例 listener：后续被双击启动的实例会连上来，唤起本窗口。
func RunGUI(cfg Config, cfgPath string, ln net.Listener) {
	a := app.NewWithID("com.local.esp32-ai-credits")
	icon := fyne.NewStaticResource("icon.png", iconPNG)
	a.SetIcon(icon)

	w := a.NewWindow("ESP32 AI 额度表")
	g := &GUI{app: a, win: w, engine: NewEngine(cfg, cfgPath)}

	// 引擎回调 → UI：一律包进 fyne.Do（Fyne 要求 UI 改动在 UI goroutine）。
	g.engine.OnLog = func(e LogEntry) { fyne.Do(func() { g.appendLog(e) }) }
	g.engine.OnStatus = func(s string) { fyne.Do(func() { g.status.SetText(s) }) }

	// 单实例：后启动的实例来敲门时，把本窗口拉到前台（feature 3）。
	if ln != nil {
		go serveSingleInstance(ln, func() {
			fyne.Do(func() { w.Show(); w.RequestFocus() })
		})
	}

	w.SetContent(g.buildUI())
	w.Resize(fyne.NewSize(760, 520))

	g.setupTray(icon)
	w.SetCloseIntercept(func() { w.Hide() }) // 关窗 → 收进托盘，不退出

	// 首次运行：创建配置文件并按默认值（开机启动）写注册表。
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := saveConfig(cfgPath, cfg); err != nil {
			g.engine.log(LogWarn, "engine", "首次写配置失败: %v", err)
		}
		if err := setAutostart(cfg.Autostart); err != nil {
			g.engine.log(LogWarn, "autostart", "设置开机启动失败: %v", err)
		}
	}

	g.engine.Start(context.Background())
	go g.connectSerial(cfg) // 串口连接放后台；失败不影响界面

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

	tabs := container.NewAppTabs(
		container.NewTabItem("测试命令", g.buildTestTab()),
		container.NewTabItem("设置", g.buildSettingsTab()),
		container.NewTabItem("活动日志", g.buildLogTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	return container.NewBorder(nil, g.status, nil, nil, tabs)
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

	providerSel := widget.NewSelect([]string{"both", "codex", "claude"}, nil)
	providerSel.SetSelected(cfg.Provider)

	proxyEntry := widget.NewEntry()
	proxyEntry.SetText(cfg.Proxy)
	proxyEntry.SetPlaceHolder("如 http://127.0.0.1:7890，留空用 HTTPS_PROXY")

	claudeEntry := widget.NewEntry()
	claudeEntry.SetText(cfg.ClaudeEndPoint)
	claudeEntry.SetPlaceHolder("留空走 ANTHROPIC_BASE_URL（中转地址）")

	codexEntry := widget.NewEntry()
	codexEntry.SetText(cfg.CodexEndpoint)
	codexEntry.SetPlaceHolder(defaultCodexEndpoint)

	codexModeSel := widget.NewSelect([]string{"auto", "official", "relay"}, nil)
	codexModeSel.SetSelected(orAuto(cfg.CodexMode))

	// 中转站类型映射：每行一条 `主机名=类型`（类型 sub2api / new-api）。
	// base_url 始终读 ~/.codex/config.toml；按其主机名查此表，未命中则自动识别。
	relayMapEntry := widget.NewMultiLineEntry()
	relayMapEntry.SetText(formatRelayTypes(cfg.CodexRelayTypes))
	relayMapEntry.SetPlaceHolder("每行一条，如：\ngoai.im=sub2api\nmuyuan.do=new-api\n（不在表内的主机名自动识别）")

	autostartCheck := widget.NewCheck("开机时自动启动本程序", nil)
	autostartCheck.SetChecked(cfg.Autostart)

	form := widget.NewForm(
		widget.NewFormItem("显示对象", providerSel),
		widget.NewFormItem("串口 Port", portEntry),
		widget.NewFormItem("波特率 Baud", baudEntry),
		widget.NewFormItem("刷新间隔(成功,秒)", intervalEntry),
		widget.NewFormItem("重试间隔(失败,秒)", retryIntervalEntry),
		widget.NewFormItem("轮播间隔(both,秒)", pageIntervalEntry),
		widget.NewFormItem("代理 Proxy", proxyEntry),
		widget.NewFormItem("Claude 端点", claudeEntry),
		widget.NewFormItem("Codex 端点", codexEntry),
		widget.NewFormItem("Codex 模式", codexModeSel),
		widget.NewFormItem("中转站类型映射", relayMapEntry),
		widget.NewFormItem("开机启动", autostartCheck),
	)
	form.SubmitText = "保存"
	form.OnSubmit = func() {
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
		old := g.engine.Config()
		newCfg := Config{
			Port:                 strings.TrimSpace(portEntry.Text),
			Baud:                 baud,
			IntervalSeconds:      interval,
			RetryIntervalSeconds: retryInterval,
			PageIntervalSeconds:  pageInterval,
			Provider:             providerSel.Selected,
			Proxy:                strings.TrimSpace(proxyEntry.Text),
			ClaudeEndPoint:       strings.TrimSpace(claudeEntry.Text),
			CodexEndpoint:        strings.TrimSpace(codexEntry.Text),
			CodexMode:            fromAuto(codexModeSel.Selected),
			CodexRelayTypes:      parseRelayTypes(relayMapEntry.Text),
			Autostart:            autostartCheck.Checked,
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
			go g.connectSerial(newCfg)
		}
	}

	return container.NewVScroll(form)
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
	menu := fyne.NewMenu("ESP32 AI 额度表",
		fyne.NewMenuItem("显示窗口", func() { g.win.Show() }),
		fyne.NewMenuItem("隐藏窗口", func() { g.win.Hide() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("退出", func() { g.quit() }),
	)
	desk.SetSystemTrayMenu(menu) // 先建托盘
	desk.SetSystemTrayIcon(icon) // 再设图标
}

// connectSerial 在后台连接串口；失败只记日志+置状态，不阻塞界面。
func (g *GUI) connectSerial(cfg Config) {
	defer recoverLog("connectSerial")
	name, err := findPort(cfg.Port)
	if err != nil {
		g.engine.log(LogWarn, "serial", "未连接: %v", err)
		g.engine.status("串口: 未连接")
		return
	}
	p, err := openSerial(name, cfg.Baud)
	if err != nil {
		g.engine.log(LogError, "serial", "%v", err)
		g.engine.status("串口: 打开失败")
		return
	}
	time.Sleep(2 * time.Second) // 等 ESP32 复位就绪
	g.engine.SetPort(p, fmt.Sprintf("串口: %s @ %d", name, cfg.Baud))
	g.engine.log(LogInfo, "serial", "已连接 %s @ %d", name, cfg.Baud)
}

// orAuto/fromAuto 在 Select 的 "auto" 与配置里的空串之间转换。
func orAuto(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}

func fromAuto(s string) string {
	if s == "auto" {
		return ""
	}
	return s
}

// parseRelayTypes 把多行文本（每行 `主机名=类型`）解析成映射；忽略空行/注释/非法行。
func parseRelayTypes(text string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(strings.ToLower(v))
		if !ok || k == "" || v == "" {
			continue
		}
		m[strings.ToLower(k)] = v
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// formatRelayTypes 把映射序列化回多行文本（主机名排序，稳定显示）。
func formatRelayTypes(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// quit 干净退出：停引擎并结束事件循环。
func (g *GUI) quit() {
	g.engine.Stop()
	g.app.Quit()
}
