package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Codex 取数已对本机实测确认：
//   GET https://chatgpt.com/backend-api/wham/usage
//   头 Authorization: Bearer <access_token>, chatgpt-account-id: <account_id>
//   响应 rate_limit.primary_window = 5 小时, secondary_window = 1 周
//        每个窗口含 used_percent 与 reset_at(unix 秒) / reset_after_seconds。

const defaultCodexEndpoint = "https://chatgpt.com/backend-api/wham/usage"

// codexAuth 对应 ~/.codex/auth.json 里我们需要的字段。
// 官方订阅有 tokens.access_token；中转站则只有顶层的 OPENAI_API_KEY（sk-...）。
type codexAuth struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// codexHome 返回 ~/.codex（尊重 CODEX_HOME）。
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return expandHome("~/.codex")
}

// loadCodexAuth 读取并解析 ~/.codex/auth.json。
func loadCodexAuth() (codexAuth, error) {
	var a codexAuth
	path := filepath.Join(codexHome(), "auth.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return a, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("解析 auth.json 失败: %w", err)
	}
	return a, nil
}

// codexIsRelay 根据 auth.json 判断是否为中转站（无官方 OAuth token、但有 OPENAI_API_KEY）。
func codexIsRelay() bool {
	a, err := loadCodexAuth()
	if err != nil {
		return false
	}
	return a.Tokens.AccessToken == "" && a.OpenAIAPIKey != ""
}

// codexUsageResp 是 wham/usage 的响应（只取用到的字段）。
type codexUsageResp struct {
	RateLimit struct {
		Primary   codexWindow `json:"primary_window"`
		Secondary codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type codexWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	ResetAt           int64   `json:"reset_at"`
	ResetAfterSeconds float64 `json:"reset_after_seconds"`
}

// loadCodexToken 取官方订阅的 OAuth token + account（中转站没有，会报错）。
func loadCodexToken() (token, account string, err error) {
	a, err := loadCodexAuth()
	if err != nil {
		return "", "", err
	}
	if a.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("auth.json 中没有 tokens.access_token（未用 ChatGPT 账号登录？）")
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}

// fetchCodex 取 Codex 的 5h/1周额度。失败返回 OK:false 的 Provider 和 error。
// ctx 取消时请求立即中止（退出程序时不会卡在 in-flight 请求上）。
func fetchCodex(ctx context.Context, client *http.Client, endpoint string, debug bool) (Provider, error) {
	if endpoint == "" {
		endpoint = defaultCodexEndpoint
	}
	token, account, err := loadCodexToken()
	if err != nil {
		return failedProvider(), err
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("chatgpt-account-id", account)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-usage/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return failedProvider(), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][codex] GET %s -> %d\n%s\n", endpoint, resp.StatusCode, truncate(body, 2000))
	}
	if resp.StatusCode != http.StatusOK {
		return failedProvider(), fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var u codexUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		logErr("codex", fmt.Errorf("解析响应失败: %w", err))
		return failedProvider(), fmt.Errorf("json ummarshal failed:%w", err)
	}
	p := Provider{
		H5: codexWindowToField(u.RateLimit.Primary, "h5"),
		Wk: codexWindowToField(u.RateLimit.Secondary, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][codex] parsed: %+v\n", p)
	}
	return p, nil
}

func codexWindowToField(w codexWindow, kind string) Window {
	reset := "--"
	if w.ResetAt > 0 {
		reset = fmtReset(w.ResetAt, kind)
	} else if w.ResetAfterSeconds > 0 {
		reset = fmtResetIn(w.ResetAfterSeconds, kind)
	}
	return Window{Left: leftFromUsed(w.UsedPercent), Reset: reset}
}
