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

type Payload struct {
	Claude Provider `json:"claude"`
	Codex  Provider `json:"codex"`
}

func failedProvider() Provider {
	return Provider{
		H5: Window{Left: -1, Reset: "--"},
		Wk: Window{Left: -1, Reset: "--"},
		OK: false,
	}
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
	return ts.Format("01-02")
}

func fmtResetIn(secs float64, kind string) string {
	if secs <= 0 {
		return "--"
	}
	ts := time.Now().Add(time.Duration(secs) * time.Second)
	if kind == "h5" {
		return ts.Format("15:04")
	}
	return ts.Format("01-02")
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
