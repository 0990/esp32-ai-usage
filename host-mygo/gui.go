package main

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

//go:embed icon.png
var iconPNG []byte

const maxLogLines = 500

type GUI struct {
	win               *mygo.Window
	engine            *Engine
	draft             settingsDraft
	overlay           *nativeOverlay
	preview           bool
	tab               int
	more              bool
	notice            string
	noticeError       bool
	status            string
	activity          map[string]int
	logLines          []string
	logText, testJSON string
	editing           bool
	editIndex         int
	editObject        CustomObject
	editError         string
	hooksText         string
	tray              *mygo.Tray
	trayOverlay       *mygo.MenuItem
	displaySync       bool
	quitting          bool
}

func newGUI(cfg Config, path string, preview bool) *GUI {
	return &GUI{engine: NewEngine(cfg, path), draft: newSettingsDraft(cfg), preview: preview, status: "串口：未连接", activity: map[string]int{}, testJSON: sampleJSON, hooksText: activityHooksStatus()}
}

func RunGUI(cfg Config, path string, ln net.Listener, preview, startHidden bool) {
	g := newGUI(cfg, path, preview)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		// App.Quit also calls each window's OnClose handler. Let those
		// closes proceed instead of treating them as hide-to-tray actions.
		g.quitting = true
		if g.overlay != nil {
			g.overlay.flushBounds()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if g.tray != nil {
			g.tray.Destroy()
			g.tray = nil
		}
	})
	mygo.App.WhenReady(func() {
		g.win = mygo.NewWindow(mygo.WindowOptions{Title: "ESP32 AI 额度表 · MyGo", Width: 820, Height: 820, MinWidth: 660, MinHeight: 560, Hidden: true, Content: ui.View(g.view)})
		if err := g.win.SetIcon(iconPNG); err != nil {
			g.engine.log(LogWarn, "gui", "设置图标失败: %v", err)
		}
		g.setupTray()
		g.win.OnClose(func(e *mygo.CloseEvent) {
			if g.quitting {
				return
			}
			if g.tray != nil {
				e.PreventDefault()
				g.win.Hide()
			} else {
				mygo.App.Quit()
			}
		})
		g.engine.OnLog = func(e LogEntry) { g.win.Update(func() { g.appendLog(e) }) }
		g.engine.OnStatus = func(s string) { g.win.Update(func() { g.status = s }) }
		g.engine.OnActivity = func(p string, s int) { g.win.Update(func() { g.activity[p] = s }) }
		g.engine.OnDisplay = func(f Frame) {
			g.win.Update(func() {
				if g.overlay != nil {
					g.overlay.update(f)
				}
			})
		}
		if ln != nil {
			go serveSingleInstance(ln, func() { g.win.Show(); g.win.Focus() })
		}
		if !preview {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				if err = saveConfig(path, cfg); err != nil {
					g.message(err.Error(), true)
				}
			}
			g.engine.Start(context.Background())
		} else {
			g.status = "预览模式 · 未启动串口及网络查询"
		}
		g.applyOverlay()
		// 托盘创建失败时保留可操作入口，避免隐藏后无法打开程序。
		if !startHidden || g.tray == nil {
			g.win.Show()
		}
	})
	if err := mygo.App.Run(); err != nil {
		writeLogLine("[ERR][gui] " + err.Error())
	}
	g.engine.Stop()
}

func (g *GUI) showMain() {
	if g.win != nil {
		g.win.Show()
		g.win.Focus()
	}
}
func (g *GUI) message(text string, failed bool) {
	g.notice = text
	g.noticeError = failed
	if g.win != nil {
		g.win.Invalidate()
	}
}
func (g *GUI) appendLog(e LogEntry) {
	g.logLines = append(g.logLines, fmt.Sprintf("%s [%s][%s] %s", e.Time.Format("15:04:05"), e.Level, e.Tag, e.Msg))
	if len(g.logLines) > maxLogLines {
		g.logLines = append([]string(nil), g.logLines[len(g.logLines)-maxLogLines:]...)
	}
	g.logText = strings.Join(g.logLines, "\n")
}
func (g *GUI) setupTray() {
	g.trayOverlay = &mygo.MenuItem{Label: "显示桌面额度悬浮窗", Type: mygo.MenuItemCheckbox, Checked: g.engine.Config().DesktopOverlay, Click: func(item *mygo.MenuItem, _ *mygo.Window) { g.setOverlayEnabled(item.IsChecked()) }}
	menu := mygo.NewMenu([]*mygo.MenuItem{{Label: "显示窗口", Click: func(*mygo.MenuItem, *mygo.Window) { g.showMain() }}, {Label: "隐藏窗口", Click: func(*mygo.MenuItem, *mygo.Window) { g.win.Hide() }}, g.trayOverlay, mygo.Separator(), {Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }}})
	opts := mygo.TrayOptions{Icon: iconPNG, ToolTip: "ESP32 AI 额度表 · MyGo"}
	if runtime.GOOS == "linux" {
		opts.Menu = menu
	}
	tray, err := mygo.NewTray(opts)
	if err != nil {
		g.engine.log(LogWarn, "tray", "托盘创建失败: %v", err)
		return
	}
	g.tray = tray
	tray.OnClick(func() { g.showMain() })
	tray.OnRightClick(func() { tray.SetMenu(menu); defer tray.SetMenu(nil); tray.PopUpMenu() })
}

func (g *GUI) view(c *ui.Context) {
	ui.Column(c).Fill().FontSize(13).Children(func() {
		ui.Tabs(c.Key("tabs"), &g.tab, "设置", "测试命令", "活动日志").Margin(12, 16, 0, 16)
		if g.notice != "" {
			clr := c.Theme().TextMuted
			if g.noticeError {
				clr = c.Theme().Danger
			}
			ui.Row(c).Padding(8, 18).Gap(8).Children(func() {
				ui.Text(c, g.notice).TextColor(clr).Grow(1)
				ui.Button(c.Key("dismiss-notice"), "关闭提示").OnClick(func() { g.notice = "" })
			})
		}
		switch g.tab {
		case 0:
			g.settingsView(c)
		case 1:
			g.testView(c)
		case 2:
			g.logsView(c)
		}
		ui.Row(c).Padding(9, 18).Gap(12).Children(func() {
			ui.Text(c, g.status).FontSize(12).Grow(1)
			ui.Text(c, fmt.Sprintf("Claude：%s · Codex：%s", actName(g.activity["claude"]), actName(g.activity["codex"]))).FontSize(12).TextColor(c.Theme().TextMuted)
		})
	})
	g.customDialog(c)
	// NumberInput 在一次构建结束时同步外部数值；再绘一帧展示新文本。
	if g.displaySync {
		g.displaySync = false
		c.Invalidate()
	}
}

func (g *GUI) settingsView(c *ui.Context) {
	ui.Scroll(c.Key("settings-scroll")).Grow(1).Padding(18).Gap(16).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			ui.Button(c.Key("fetch-now"), "立即刷新使用量").OnClick(func() {
				if g.preview {
					g.message("预览模式不发起网络请求", false)
				} else {
					g.engine.Reload()
				}
			})
		})
		ui.Form(c, func() {
			ui.Field(c, "监控对象", func() {
				ui.Column(c).Gap(8).Children(func() {
					ui.Row(c).Wrap().Gap(12).Children(func() {
						for _, p := range providerOrder {
							enabled := g.draft.providers[p]
							name := p
							ui.Checkbox(c.Key("provider-"+p), &enabled, p).OnChange(func() { g.draft.providers[name] = enabled })
						}
					})
					for i, obj := range g.draft.CustomObjects {
						index := i
						ui.Row(c.Key("custom-" + obj.ID)).Gap(8).Children(func() {
							ui.Checkbox(c.Key("custom-enabled-"+obj.ID), &g.draft.CustomObjects[index].Enabled, obj.Name+" ("+obj.Type+")").Grow(1)
							ui.Button(c.Key("edit-"+obj.ID), "编辑").OnClick(func() { g.editCustom(index) })
							ui.Button(c.Key("remove-"+obj.ID), "删除").OnClick(func() {
								g.draft.CustomObjects = append(g.draft.CustomObjects[:index], g.draft.CustomObjects[index+1:]...)
							})
						})
					}
					ui.Button(c.Key("add-custom"), "新增自定义对象").OnClick(func() { g.editCustom(-1) })
				})
			})
			ui.Field(c, "OLED 亮度", func() {
				ui.Row(c).Gap(10).Children(func() {
					ui.Slider(c.Key("brightness"), &g.draft.brightness, 0, 255).Grow(1).Label("OLED 亮度")
					ui.Textf(c, "%d / 255", int(g.draft.brightness)).Width(70)
				})
			})
			ui.Field(c, "开机启动", func() { ui.Checkbox(c.Key("autostart"), &g.draft.Autostart, "开机时自动启动本程序") })
			ui.Field(c, "桌面悬浮窗", func() { g.overlaySettings(c) })
			ui.Field(c, "运行标志钩子", func() {
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, g.hooksText).FontSize(12).TextColor(c.Theme().TextMuted)
					ui.Row(c).Gap(8).Children(func() {
						ui.Button(c.Key("install-hooks"), "安装").OnClick(func() { g.setHooks(true) })
						ui.Button(c.Key("remove-hooks"), "移除").OnClick(func() { g.setHooks(false) })
					})
				})
			})
		})
		label := "更多 ▾"
		if g.more {
			label = "收起 ▴"
		}
		ui.Button(c.Key("more-settings"), label).FillWidth().OnClick(func() { g.more = !g.more })
		if g.more {
			g.advancedView(c)
		}
		ui.Row(c).Justify(ui.End).Children(func() { ui.PrimaryButton(c.Key("save-settings"), "保存").OnClick(g.saveSettings) })
	})
}

func (g *GUI) advancedView(c *ui.Context) {
	ui.Form(c, func() {
		fields := []struct {
			label string
			value *string
			hint  string
		}{{"串口 Port", &g.draft.Port, "AUTO 或 COM5"}, {"波特率 Baud", &g.draft.baud, "115200"}, {"刷新间隔（成功，秒）", &g.draft.interval, "300"}, {"重试间隔（失败，秒）", &g.draft.retry, "60"}, {"轮播间隔（秒）", &g.draft.page, "4"}, {"代理 Proxy", &g.draft.Proxy, "留空使用 HTTPS_PROXY"}}
		for _, f := range fields {
			ui.Field(c, f.label, func() { ui.TextInput(c.Key("field-"+f.label), f.value).Placeholder(f.hint) })
		}
	})
}

func (g *GUI) overlaySettings(c *ui.Context) {
	live := g.engine.Config()
	ui.Column(c).Gap(10).Children(func() {
		enabled := live.DesktopOverlay
		ui.Checkbox(c.Key("overlay-enabled"), &enabled, "在桌面显示额度（立即生效）").OnChange(func() { g.setOverlayEnabled(enabled) })
		style := overlayDesignFor(live.OverlayStyle).name
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "样式").Width(70)
			ui.Select(c.Key("overlay-style"), &style, overlayStyleNames).Grow(1).Label("悬浮窗样式").OnChange(func() {
				for i, name := range overlayStyleNames {
					if name == style {
						g.setOverlayStyle(overlayStyleIDs[i])
						return
					}
				}
			})
		})
		for _, f := range []struct {
			label            string
			value, low, high int
		}{{"宽度", live.OverlayWidth, int(overlayDesignFor(live.OverlayStyle).minWidth), 1000}, {"高度", live.OverlayHeight, int(overlayDesignFor(live.OverlayStyle).minHeight), 800}, {"不透明度", live.OverlayOpacity, 40, 100}} {
			value := float64(f.value)
			name := f.label
			ui.Row(c.Key("overlay-row-" + name)).Gap(10).Children(func() {
				ui.Text(c, name).Width(70)
				ui.NumberInput(c.Key("overlay-"+name), &value, float64(f.low), float64(f.high), 1).Grow(1).Label("悬浮窗" + name).OnChange(func() {
					cfg := g.engine.Config()
					switch name {
					case "宽度":
						cfg.OverlayWidth = int(value)
					case "高度":
						cfg.OverlayHeight = int(value)
					case "不透明度":
						cfg.OverlayOpacity = int(value)
					}
					g.setOverlayGeometry(cfg.OverlayWidth, cfg.OverlayHeight, cfg.OverlayOpacity)
				})
			})
		}
		layer := "始终置顶"
		if live.OverlayLayer == "bottom" {
			layer = "始终置底"
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "层级").Width(70)
			ui.Select(c.Key("overlay-layer"), &layer, []string{"始终置顶", "始终置底"}).Grow(1).Label("悬浮窗层级").OnChange(func() {
				value := "top"
				if layer == "始终置底" {
					value = "bottom"
				}
				g.setOverlayLayer(value)
			})
		})
		ui.Row(c).Wrap().Gap(8).Children(func() {
			ui.Text(c, "拖动卡片移动，边缘调整宽高").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Button(c.Key("reset-position"), "回到右上角").OnClick(g.resetOverlayPosition)
		})
	})
}

func (g *GUI) editCustom(index int) {
	g.editIndex = index
	g.editObject = CustomObject{ID: newCustomID(), Type: "sub2api", Enabled: true}
	if index >= 0 {
		g.editObject = g.draft.CustomObjects[index]
	}
	g.editError = ""
	g.editing = true
}
func (g *GUI) submitCustom() {
	obj := g.editObject
	obj.Name = strings.TrimSpace(obj.Name)
	obj.BaseURL = strings.TrimRight(strings.TrimSpace(obj.BaseURL), "/")
	obj.APIKey = strings.TrimSpace(obj.APIKey)
	if err := obj.validate(); err != nil {
		g.editError = err.Error()
		return
	}
	for i, other := range g.draft.CustomObjects {
		if i != g.editIndex && strings.EqualFold(obj.Name, other.Name) {
			g.editError = "名称已存在，请使用不同名称"
			return
		}
	}
	if g.editIndex < 0 {
		g.draft.CustomObjects = append(g.draft.CustomObjects, obj)
	} else {
		g.draft.CustomObjects[g.editIndex] = obj
	}
	g.editing = false
}
func (g *GUI) customDialog(c *ui.Context) {
	ui.Modal(c.Key("custom-dialog"), &g.editing, func() {
		ui.Text(c, "自定义监控对象").FontSize(18).Bold()
		ui.Form(c, func() {
			ui.Field(c, "名称", func() { ui.TextInput(c.Key("object-name"), &g.editObject.Name).Label("名称") })
			ui.Field(c, "类型", func() { ui.Select(c.Key("object-type"), &g.editObject.Type, []string{"sub2api", "newapi"}) })
			ui.Field(c, "Base URL", func() {
				ui.TextInput(c.Key("object-base"), &g.editObject.BaseURL).Placeholder("https://relay.example/v1")
			})
			ui.Field(c, "API key", func() { ui.TextInput(c.Key("object-key"), &g.editObject.APIKey).Password() })
		})
		if g.editError != "" {
			ui.Text(c, g.editError).TextColor(c.Theme().Danger)
		}
		ui.Text(c, "点击设置页的保存后生效").FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c.Key("cancel-object"), "取消").OnClick(func() { g.editing = false })
			ui.PrimaryButton(c.Key("submit-object"), "确定").OnClick(g.submitCustom)
		})
	})
}

func (g *GUI) saveSettings() {
	old := g.engine.Config()
	cfg, err := g.draft.savedConfig(old)
	if err != nil {
		g.message(err.Error(), true)
		return
	}
	if err = g.engine.UpdateConfig(cfg); err != nil {
		g.message(err.Error(), true)
		return
	}
	if !g.preview {
		err = setAutostart(cfg.Autostart, g.engine.cfgPath)
	}
	if err != nil {
		g.message("配置已保存，开机启动设置失败："+err.Error(), true)
	} else {
		g.message("配置已保存", false)
	}
	if cfg.Port != old.Port || cfg.Baud != old.Baud {
		g.engine.ReconnectSerial()
	}
}
func (g *GUI) setHooks(install bool) {
	if g.preview {
		g.message("预览模式不修改客户端钩子", false)
		return
	}
	var err error
	if install {
		err = installActivityHooks()
	} else {
		err = removeActivityHooks()
	}
	g.hooksText = activityHooksStatus()
	if err != nil {
		g.message(err.Error(), true)
		return
	}
	text := "工作状态钩子已移除"
	if install {
		text = "钩子已安装。Claude Code 新会话生效；Codex 需在 /hooks 中审核并信任。"
	}
	g.message(text, false)
}
func (g *GUI) testView(c *ui.Context) {
	ui.Column(c).Grow(1).Padding(18).Gap(14).Children(func() {
		ui.Text(c, "发送自定义 Frame 到 ESP32").FontSize(17).Bold()
		ui.Text(c, "输入一行 JSON：百分比页 k=pct，余额页 k=money。").TextColor(c.Theme().TextMuted)
		ui.TextArea(c.Key("test-json"), &g.testJSON).Grow(1).Label("测试 JSON")
		ui.PrimaryButton(c.Key("send-json"), "发送到 ESP32").OnClick(func() {
			raw, err := parseTestJSON(g.testJSON)
			if err != nil {
				g.message(err.Error(), true)
				return
			}
			if g.preview {
				g.message("JSON 校验通过；预览模式不写串口", false)
				return
			}
			g.engine.Send(raw)
			g.engine.log(LogInfo, "test", "已发送测试 Frame (%d 字节)", len(raw)+1)
			g.message("测试 Frame 已加入串口发送队列", false)
		})
	})
}
func (g *GUI) logsView(c *ui.Context) {
	ui.Column(c).Grow(1).Padding(18).Gap(12).Children(func() {
		ui.Row(c).Children(func() {
			ui.Text(c, "活动日志").FontSize(17).Bold().Grow(1)
			ui.Button(c.Key("clear-log"), "清空").OnClick(func() { g.logLines = nil; g.logText = "" })
		})
		ui.TextArea(c.Key("activity-log"), &g.logText).ReadOnly(true).Grow(1).Label("活动日志")
	})
}
