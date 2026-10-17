package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

const defaultOverlayStyle = "horizontal"

var overlayStyleIDs = []string{"paper", "obsidian", "strip", "horizontal", "vertical"}
var overlayStyleNames = []string{"雾白卡片", "曜石仪表", "极简窄条", "横向一行", "竖向四行"}

type overlayDesign struct {
	name                                                string
	width, height, minWidth, minHeight, radius          float32
	background, text, muted, track, accent, line, badge ui.Color
}

func rgb(hex uint32) ui.Color { return ui.RGB(byte(hex>>16), byte(hex>>8), byte(hex)) }
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
		return overlayDesign{"雾白卡片", 296, 166, 264, 158, 16, rgb(0xffffff), rgb(0x26332f), rgb(0x77827d), rgb(0xedf1ef), rgb(0x509f80), rgb(0xedf0ee), rgb(0xeaf3ee)}
	case "obsidian":
		return overlayDesign{"曜石仪表", 348, 150, 264, 146, 14, rgb(0x232827), rgb(0xedf3ef), rgb(0xa1ada6), rgb(0x3c4640), rgb(0x9bcbb0), rgb(0x3b4440), rgb(0x35453b)}
	case "strip":
		return overlayDesign{"极简窄条", 330, 132, 264, 128, 12, rgb(0xffffff), rgb(0x26332f), rgb(0x77827d), rgb(0xedf1ef), rgb(0x6d8ba6), rgb(0xedf0ee), rgb(0xeaf0f5)}
	case "vertical":
		return overlayDesign{"竖向四行", 44, 88, 44, 88, 6, rgb(0xffffff), rgb(0x293b35), rgb(0x6c7b74), rgb(0xe8eee9), rgb(0x4b9474), rgb(0xdce7de), rgb(0xeef4ef)}
	default:
		return overlayDesign{"横向一行", 136, 24, 136, 24, 6, rgb(0xffffff), rgb(0x293b35), rgb(0x6c7b74), rgb(0xe8eee9), rgb(0x4b9474), rgb(0xdce7de), rgb(0xeef4ef)}
	}
}

func compactOverlayStyle(style string) bool { return style == "horizontal" || style == "vertical" }
func normalizeOverlayAppearance(scale int, layer string) (int, string) {
	if scale == 0 {
		scale = 100
	}
	if layer != "bottom" {
		layer = "top"
	}
	return max(70, min(160, scale)), layer
}
func normalizeOverlaySize(style string, width, height, opacity int) (int, int, int) {
	d := overlayDesignFor(style)
	if width == 0 {
		width = int(d.width)
	}
	if height == 0 {
		height = int(d.height)
	}
	if opacity == 0 {
		opacity = 100
	}
	return max(int(d.minWidth), min(1000, width)), max(int(d.minHeight), min(800, height)), max(40, min(100, opacity))
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
	}
	if strings.TrimSpace(source) == "" {
		return "AI 额度"
	}
	return source
}
func overlayStatus(f Frame, has bool) string {
	switch {
	case !has:
		return "正在查询使用量…"
	case f.Old:
		return "数据已过期 · 正在重试更新"
	case f.Kind != "money" && f.P1 < 0 && f.P2 < 0:
		return "查询失败 · 请查看活动日志"
	case f.Kind == "money" && (f.V1 == "ERR" || f.V2 == "ERR"):
		return "查询失败 · 请查看活动日志"
	}
	return ""
}
