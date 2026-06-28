package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

type Window struct {
	Left  int    `json:"left"`
	Reset string `json:"reset"`
}

type Provider struct {
	H5 Window `json:"h5"`
	Wk Window `json:"wk"`
	OK bool   `json:"ok"`
}

// Relay 是中转站余额（金额）。Codex 走中转 API key 时用它替代 Codex 百分比。
// 它只是 host 内部数据；下发给固件时会被转换成 Frame（见下）。
type Relay struct {
	Used    float64 // 已用量：sub2api=今日用量，new-api=累计已用
	Balance float64 // 总余额 / 剩余
	Unit    string  // 货币符号前缀，最长 3 字符（如 "$"）
	Label   string  // 第一行标签：sub2api="Today"，new-api="Used"
	OK      bool
}

// Frame 是「一屏」的完整渲染指令，见 PROTOCOL.md。
// host 全权决定显示什么/翻不翻页/是否过期；固件只渲染收到的最新 Frame。
// 两种 kind：
//   - "pct"  ：两行百分比窗口（5h / 1w），icon = "claude"/"codex"，p1/p2 为剩余百分比(-1=ERR)，s1/s2 为重置时间。
//   - "money"：两行金额（Used/Today、Left），icon = 货币符号（画在硬币上），v1/v2 为已格式化金额文本。
type Frame struct {
	Icon string `json:"ic"`            // pct: "claude"/"codex"；money: 货币符号（如 "$"）
	Kind string `json:"k"`             // "pct" | "money"
	L1   string `json:"l1"`            // 第 1 行标签
	L2   string `json:"l2"`            // 第 2 行标签
	P1   int    `json:"p1"`            // pct: 第 1 行剩余百分比（-1=ERR）；money 时为 -1
	P2   int    `json:"p2"`            // pct: 第 2 行剩余百分比（-1=ERR）；money 时为 -1
	S1   string `json:"s1,omitempty"`  // pct: 第 1 行重置时间
	S2   string `json:"s2,omitempty"`  // pct: 第 2 行重置时间
	V1   string `json:"v1,omitempty"`  // money: 第 1 行金额文本
	V2   string `json:"v2,omitempty"`  // money: 第 2 行金额文本
	Old  bool   `json:"old,omitempty"` // 数据过期 → 固件叠加 "old"
}

func failedProvider() Provider {
	return Provider{
		H5: Window{Left: -1, Reset: "--"},
		Wk: Window{Left: -1, Reset: "--"},
		OK: false,
	}
}

// pctFrame 把一个 Provider（claude/codex 官方）转成一帧 pct。失败/无数据时百分比为 -1（固件显示 ERR）。
func pctFrame(icon string, p Provider, stale bool) Frame {
	h5, wk := -1, -1
	if p.OK {
		h5, wk = p.H5.Left, p.Wk.Left
	}
	return Frame{
		Icon: icon, Kind: "pct",
		L1: "5h", P1: h5, S1: p.H5.Reset,
		L2: "1w", P2: wk, S2: p.Wk.Reset,
		Old: stale,
	}
}

// moneyFrame 把中转站余额转成一帧 money。
func moneyFrame(r Relay, stale bool) Frame {
	icon := currencySymbol(r.Unit)
	label := r.Label
	if label == "" {
		label = "Used"
	}
	f := Frame{Icon: icon, Kind: "money", L1: label, L2: "Left", P1: -1, P2: -1, Old: stale}
	if r.OK {
		f.V1 = fmtMoney(r.Unit, r.Used)
		f.V2 = fmtMoney(r.Unit, r.Balance)
	} else {
		f.V1, f.V2 = "ERR", "ERR"
	}
	return f
}

// fmtMoney 整数不带小数，非整数保留 1 位，前缀货币符号。
func fmtMoney(unit string, v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%s%d", unit, int64(v))
	}
	return fmt.Sprintf("%s%.1f", unit, v)
}

// leftFromUsed converts a used percentage in the 0..100 range into a remaining percentage.
func leftFromUsed(used float64) int {
	left := 100 - int(math.Round(used))
	if left < 0 {
		left = 0
	}
	if left > 100 {
		left = 100
	}
	return left
}

func resolveTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case float64:
		return numToTime(int64(t))
	case int64:
		return numToTime(t)
	case int:
		return numToTime(int64(t))
	case string:
		if t == "" {
			return time.Time{}, false
		}
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts, true
		}
		var n int64
		if _, err := fmt.Sscan(t, &n); err == nil {
			return numToTime(n)
		}
	}
	return time.Time{}, false
}

func numToTime(n int64) (time.Time, bool) {
	switch {
	case n > 1e15:
		return time.Unix(0, n), true
	case n > 1e12:
		return time.UnixMilli(n), true
	case n > 1e9:
		return time.Unix(n, 0), true
	default:
		return time.Time{}, false
	}
}

func fmtReset(v any, kind string) string {
	ts, ok := resolveTime(v)
	if !ok {
		return "--"
	}
	ts = ts.Local()
	if kind == "h5" {
		return ts.Format("15:04")
	}
	return ts.Format("01-02 15:04")
}

func fmtResetIn(secs float64, kind string) string {
	if secs <= 0 {
		return "--"
	}
	ts := time.Now().Add(time.Duration(secs) * time.Second)
	if kind == "h5" {
		return ts.Format("15:04")
	}
	return ts.Format("01-02 15:04")
}

func expandHome(p string) string {
	if p == "~" || len(p) >= 2 && p[:2] == "~/" || len(p) >= 2 && p[:2] == "~\\" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
