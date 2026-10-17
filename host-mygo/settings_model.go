package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const sampleJSON = `{"ic":"claude","k":"pct","l1":"5h","p1":50,"s1":"15:47","l2":"1w","p2":30,"s2":"06-25 14:30"}`

type settingsDraft struct {
	Config
	providers                   map[string]bool
	baud, interval, retry, page string
	brightness                  float64
}

func newSettingsDraft(c Config) settingsDraft {
	c.CustomObjects = append([]CustomObject(nil), c.CustomObjects...)
	return settingsDraft{Config: c, providers: parseProviders(c.Provider), baud: strconv.Itoa(c.Baud), interval: strconv.Itoa(c.IntervalSeconds), retry: strconv.Itoa(c.RetryIntervalSeconds), page: strconv.Itoa(c.PageIntervalSeconds), brightness: float64(c.OLEDBrightness)}
}
func (d *settingsDraft) savedConfig(live Config) (Config, error) {
	c := d.Config
	fields := []struct {
		value, label string
		dst          *int
	}{{d.baud, "波特率", &c.Baud}, {d.interval, "成功刷新间隔", &c.IntervalSeconds}, {d.retry, "失败重试间隔", &c.RetryIntervalSeconds}, {d.page, "轮播间隔", &c.PageIntervalSeconds}}
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f.value))
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("%s必须是正整数", f.label)
		}
		*f.dst = n
	}
	selected := false
	names := map[string]bool{}
	for _, obj := range c.CustomObjects {
		if err := obj.validate(); err != nil {
			return Config{}, err
		}
		name := strings.ToLower(strings.TrimSpace(obj.Name))
		if names[name] {
			return Config{}, fmt.Errorf("名称已存在：%s", obj.Name)
		}
		names[name] = true
		selected = selected || obj.Enabled
	}
	for _, on := range d.providers {
		selected = selected || on
	}
	if !selected {
		return Config{}, fmt.Errorf("请至少勾选一个监控对象")
	}
	c.Provider = joinProviders(d.providers)
	c.Port = strings.TrimSpace(c.Port)
	if c.Port == "" {
		c.Port = "AUTO"
	}
	c.Proxy = strings.TrimSpace(c.Proxy)
	c.ClaudeEndPoint = strings.TrimSpace(c.ClaudeEndPoint)
	c.CodexEndpoint = strings.TrimSpace(c.CodexEndpoint)
	c.KimiEndpoint = strings.TrimSpace(c.KimiEndpoint)
	c.OLEDBrightness = clampOLEDBrightness(int(d.brightness))
	c.CustomObjects = append([]CustomObject(nil), c.CustomObjects...)
	copyOverlayConfig(&c, live)
	return c, nil
}
func copyOverlayConfig(dst *Config, c Config) {
	dst.DesktopOverlay = c.DesktopOverlay
	dst.OverlayStyle = c.OverlayStyle
	dst.OverlayScale = c.OverlayScale
	dst.OverlayLayer = c.OverlayLayer
	dst.OverlayWidth = c.OverlayWidth
	dst.OverlayHeight = c.OverlayHeight
	dst.OverlayOpacity = c.OverlayOpacity
	dst.OverlayX = c.OverlayX
	dst.OverlayY = c.OverlayY
	dst.OverlayPositionSet = c.OverlayPositionSet
}
func parseTestJSON(raw string) ([]byte, error) {
	var f Frame
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &f); err != nil {
		return nil, fmt.Errorf("JSON 无效：%w", err)
	}
	return json.Marshal(f)
}
