package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Claude usage endpoint:
//
//	GET <base>/api/oauth/usage
//
// Headers: Authorization: Bearer <accessToken>, anthropic-beta: oauth-2025-04-20,
// anthropic-version: 2023-06-01.
//
// 本机 Claude 多为中转 token（sk-reclaude-），默认 api.anthropic.com 会 401；
// 需把端点指向中转地址：config 的 claude_endpoint（完整 usage URL）或环境变量
// ANTHROPIC_BASE_URL（基址，自动补 /api/oauth/usage）。

const (
	claudeUsagePath   = "/api/oauth/usage"
	defaultClaudeBase = "https://api.anthropic.com"
)

// resolveClaudeEndpoint 把配置/环境变量解析成完整 usage URL。
// 优先级：config 的 claude_endpoint > 环境变量 ANTHROPIC_BASE_URL > 默认基址。
// 取到的值既可以是基址（自动补 /api/oauth/usage），也可以是已带该路径的完整 URL。
func resolveClaudeEndpoint(endpoint string) string {
	base := endpoint
	if base == "" {
		base = os.Getenv("ANTHROPIC_BASE_URL")
	}
	if base == "" {
		base = defaultClaudeBase
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, claudeUsagePath) {
		return base
	}
	return base + claudeUsagePath
}

type claudeCreds struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    any     `json:"resets_at"`
}

type claudeUsageResp struct {
	FiveHour claudeWindow `json:"five_hour"`
	SevenDay claudeWindow `json:"seven_day"`
}

func loadClaudeToken() (string, error) {
	path := filepath.Join(expandHome("~/.claude"), ".credentials.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s failed: %w", path, err)
	}
	var c claudeCreds
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("parse .credentials.json failed: %w", err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf(".credentials.json missing claudeAiOauth.accessToken")
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// claudeSettings 取 ~/.claude/settings.json 里 env 段我们需要的字段。
// 中转站模式下 Claude 用这里的 ANTHROPIC_API_KEY 走 ANTHROPIC_BASE_URL。
type claudeSettings struct {
	Env struct {
		AnthropicAPIKey  string `json:"ANTHROPIC_API_KEY"`
		AnthropicBaseURL string `json:"ANTHROPIC_BASE_URL"`
	} `json:"env"`
}

// loadClaudeRelaySettings 读取 ~/.claude/settings.json，返回中转站 apiKey + base_url。
// 文件不存在/解析失败返回 error；字段缺失则返回空串（由 claudeIsRelay 判定）。
func loadClaudeRelaySettings() (apiKey, baseURL string, err error) {
	path := filepath.Join(expandHome("~/.claude"), "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read %s failed: %w", path, err)
	}
	var s claudeSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", "", fmt.Errorf("parse settings.json failed: %w", err)
	}
	return s.Env.AnthropicAPIKey, s.Env.AnthropicBaseURL, nil
}

// claudeIsRelay 判断 Claude 是否走中转：settings.json 的 env 同时有
// ANTHROPIC_API_KEY 与 ANTHROPIC_BASE_URL 即为中转站（对齐 codexIsRelay 语义）。
func claudeIsRelay() bool {
	key, base, err := loadClaudeRelaySettings()
	return err == nil && key != "" && base != ""
}

// ctx 取消时请求立即中止（退出程序时不会卡在 in-flight 请求上）。
func fetchClaude(ctx context.Context, client *http.Client, endpoint string, debug bool) (Provider, error) {
	token, err := loadClaudeToken()
	if err != nil {
		return failedProvider(), err
	}
	endpoint = resolveClaudeEndpoint(endpoint)

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-usage/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return failedProvider(), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][claude] GET %s -> %d\n%s\n", endpoint, resp.StatusCode, truncate(body, 2000))
	}
	if resp.StatusCode != http.StatusOK {
		return failedProvider(), fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var u claudeUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		logErr("claude", fmt.Errorf("parse response failed: %w", err))
		return failedProvider(), fmt.Errorf("json ummarshal failed:%w", err)
	}
	p := Provider{
		H5: claudeWindowToField(u.FiveHour, "h5"),
		Wk: claudeWindowToField(u.SevenDay, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][claude] parsed: %+v\n", p)
	}
	return p, nil
}

func claudeWindowToField(w claudeWindow, kind string) Window {
	reset := "--"
	if w.ResetsAt != nil {
		reset = fmtReset(w.ResetsAt, kind)
	}
	return Window{Left: leftFromUsed(w.Utilization), Reset: reset}
}
