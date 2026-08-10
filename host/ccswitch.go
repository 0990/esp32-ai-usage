package main

// cc-switch（Claude Code 供应商切换工具）开启「本地代理」后，会把
// ~/.claude/settings.json 的 ANTHROPIC_BASE_URL 改写成本机回环代理地址
// （http://127.0.0.1:<port>，token 为 PROXY_xxx）。该代理只转发对话接口、
// 不转发余额接口（billing 路径 404），所以取余额必须回溯到真实上游：
// cc-switch 把它存在自己的 SQLite 库 ~/.cc-switch/cc-switch.db 的
// providers 表（app_type='claude' 且 is_current=1 的 settings_config.env，
// 结构与 ~/.claude/settings.json 同构，可复用 claudeSettings 解析）。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// isLoopbackBase 判断 base_url 是否指向本机回环代理（127.x、localhost、::1）。
func isLoopbackBase(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.")
}

// loadCCSwitchRelay 从 cc-switch 的 SQLite 库读取当前 Claude 供应商的 base_url + key。
// 库不存在/无当前供应商/字段缺失均返回 error（调用方回退 settings.json 的原始值）。
func loadCCSwitchRelay() (apiKey, baseURL string, err error) {
	dbPath := filepath.Join(expandHome("~/.cc-switch"), "cc-switch.db")
	// 只读打开（cc-switch 在写，加 busy_timeout 容忍短时锁）；URI 需正斜杠路径。
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		return "", "", err
	}
	defer db.Close()

	var cfg string
	if err := db.QueryRow(
		"SELECT settings_config FROM providers WHERE app_type='claude' AND is_current=1 LIMIT 1",
	).Scan(&cfg); err != nil {
		return "", "", fmt.Errorf("cc-switch 无当前 claude 供应商: %w", err)
	}

	var s claudeSettings // settings_config 与 settings.json 同构（env 段）
	if err := json.Unmarshal([]byte(cfg), &s); err != nil {
		return "", "", fmt.Errorf("解析 cc-switch settings_config 失败: %w", err)
	}
	key := s.Env.AnthropicAPIKey
	if key == "" {
		key = s.Env.AnthropicAuthToken
	}
	if key == "" || s.Env.AnthropicBaseURL == "" {
		return "", "", fmt.Errorf("cc-switch 当前供应商缺少 ANTHROPIC_BASE_URL 或 key")
	}
	return key, s.Env.AnthropicBaseURL, nil
}
