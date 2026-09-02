package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// 取数结果落盘：~/.esp32-ai-usage/cache.json。
// 启动时先把上次成功的数据装回缓存并立刻显示（按时间戳自动标 old），首轮取数失败
// （网络没好、网关 429）时屏幕就不会先 ERR 一阵。文件里只有百分比/时间/金额，没有任何凭据。

type dataCache struct {
	Claude, Reclaude, Codex, Kimi         Provider
	ClaudeAt, ReclaudeAt, CodexAt, KimiAt time.Time
	Relay                                 *Relay
	RelayAt                               time.Time
	ClaudeRelay                           *Relay
	ClaudeRelayAt                         time.Time
}

func cachePath(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "cache.json")
}

// loadCache 把磁盘上的上次数据装进引擎缓存；没有文件或解析失败则静默忽略（保持空缓存）。
// 有任何一项数据时把 started 置真，让显示循环立刻出页。须在取数循环启动前调用。
func (e *Engine) loadCache() {
	raw, err := os.ReadFile(cachePath(e.cfgPath))
	if err != nil {
		return
	}
	var c dataCache
	if err := json.Unmarshal(raw, &c); err != nil {
		e.log(LogWarn, "cache", "解析 %s 失败，忽略: %v", cachePath(e.cfgPath), err)
		return
	}
	e.dataMu.Lock()
	defer e.dataMu.Unlock()
	e.claude, e.claudeAt = c.Claude, c.ClaudeAt
	e.reclaude, e.reclaudeAt = c.Reclaude, c.ReclaudeAt
	e.codex, e.codexAt = c.Codex, c.CodexAt
	e.kimi, e.kimiAt = c.Kimi, c.KimiAt
	e.relay, e.relayAt = c.Relay, c.RelayAt
	e.claudeRelay, e.claudeRelayAt = c.ClaudeRelay, c.ClaudeRelayAt
	if c.Claude.OK || c.Reclaude.OK || c.Codex.OK || c.Kimi.OK ||
		(c.Relay != nil && c.Relay.OK) || (c.ClaudeRelay != nil && c.ClaudeRelay.OK) {
		e.started = true
		e.log(LogInfo, "cache", "已载入上次数据，先显示旧值（超期自动标 old）")
	}
}

// saveCache 把当前缓存写盘（每次成功取数后调用）。失败只记日志。
func (e *Engine) saveCache() {
	e.dataMu.Lock()
	c := dataCache{
		Claude: e.claude, ClaudeAt: e.claudeAt,
		Reclaude: e.reclaude, ReclaudeAt: e.reclaudeAt,
		Codex: e.codex, CodexAt: e.codexAt,
		Kimi: e.kimi, KimiAt: e.kimiAt,
		Relay: e.relay, RelayAt: e.relayAt,
		ClaudeRelay: e.claudeRelay, ClaudeRelayAt: e.claudeRelayAt,
	}
	e.dataMu.Unlock()
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	p := cachePath(e.cfgPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		err = os.WriteFile(p, raw, 0o644)
	}
	if err != nil {
		e.log(LogWarn, "cache", "写 %s 失败: %v", p, err)
	}
}
