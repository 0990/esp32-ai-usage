package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

type compactOverlayRow struct {
	label, value, reset string
}

func (o *nativeOverlay) compactRows() []compactOverlayRow {
	f := o.frame
	if o.hasFrame && f.Kind == "money" {
		if f.L1 == "Balance" {
			return []compactOverlayRow{{label: "余额", value: f.V1}}
		}
		return []compactOverlayRow{{label: "已用", value: f.V1}, {label: "余额", value: f.V2}}
	}
	rows := []compactOverlayRow{{label: "5h", value: "—"}, {label: "1w", value: "—"}}
	for i, left := range []int{f.P1, f.P2} {
		if !o.hasFrame || left < 0 {
			continue
		}
		rows[i].value = fmt.Sprintf("%d%%", max(0, min(100, left)))
		rows[i].reset = []string{f.S1, f.S2}[i]
	}
	return rows
}

func (o *nativeOverlay) compactDetails() []string {
	name := "AI 额度"
	if o.hasFrame {
		name = overlayTitle(o.frame.Source)
	}
	details := []string{name}
	for _, row := range o.compactRows() {
		line := row.label + "  " + row.value
		if row.reset != "" && row.reset != "--" {
			line += "  ·  重置 " + row.reset
		}
		details = append(details, line)
	}
	if status := overlayStatus(o.frame, o.hasFrame); status != "" {
		details = append(details, status)
	}
	return details
}

// Pure horizontal/vertical layouts: the whole surface drags, with details in a native tooltip and context menu.
func (o *nativeOverlay) compactView(c *ui.Context, d overlayDesign) {
	w, h := c.Size()
	rows := o.compactRows()
	muted := d.muted
	if overlayStatus(o.frame, o.hasFrame) != "" {
		muted = rgb(0xaa6522)
	}
	ui.Box(c.Key("drag-region")).Fill().Background(d.background).DragWindow().Cursor(ui.CursorGrab).
		Label("拖动悬浮窗").Description(strings.Join(o.compactDetails(), "\n")).ContextMenu(o.contextMenu).Children(func() {
		for i, row := range rows {
			key := fmt.Sprintf("compact-%d-", i)
			if o.cfg.OverlayStyle == "vertical" {
				y := float32(8) + float32(i)*40
				ui.Text(c.Key(key+"label"), row.label).Absolute().Left(6).Top(y).Width(w - 12).FontSize(11).TextColor(muted).TextAlign(ui.Center).SingleLine()
				ui.Text(c.Key(key+"value"), row.value).Absolute().Left(6).Top(y + 14).Width(w - 12).FontSize(12).TextColor(d.text).TextAlign(ui.Center).SingleLine().FontFeatures("tnum")
			} else {
				col := (w - 16 - float32(len(rows)-1)*8) / float32(len(rows))
				x := float32(8) + float32(i)*(col+8)
				labelWidth := float32(16)
				if o.frame.Kind == "money" {
					labelWidth = 24
				}
				ui.Text(c.Key(key+"label"), row.label).Absolute().Left(x).Top((h - 14) / 2).Width(labelWidth).FontSize(11).TextColor(muted).SingleLine()
				ui.Text(c.Key(key+"value"), row.value).Absolute().Left(x + labelWidth + 3).Top((h - 16) / 2).Width(max(0, col-labelWidth-3)).FontSize(12).TextColor(d.text).TextAlign(ui.End).SingleLine().FontFeatures("tnum")
			}
		}
	})
}
