package main

import (
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
type codexAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
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

// loadCodexToken 读取 ~/.codex/auth.json（尊重 CODEX_HOME）。
func loadCodexToken() (token, account string, err error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = expandHome("~/.codex")
	}
	path := filepath.Join(home, "auth.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var a codexAuth
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", "", fmt.Errorf("解析 auth.json 失败: %w", err)
	}
	if a.Tokens.AccessToken == "" {
		return "", "", fmt.Errorf("auth.json 中没有 tokens.access_token（未用 ChatGPT 账号登录？）")
	}
	return a.Tokens.AccessToken, a.Tokens.AccountID, nil
}

// fetchCodex 取 Codex 的 5h/1周额度。失败返回 OK:false 的 Provider 和 error。
func fetchCodex(client *http.Client, endpoint string, debug bool) Provider {
	if endpoint == "" {
		endpoint = defaultCodexEndpoint
	}
	token, account, err := loadCodexToken()
	if err != nil {
		logErr("codex", err)
		return failedProvider()
	}

	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("chatgpt-account-id", account)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-credits/1.0")

	resp, err := client.Do(req)
	if err != nil {
		logErr("codex", fmt.Errorf("请求 %s 失败: %w", endpoint, err))
		return failedProvider()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][codex] GET %s -> %d\n%s\n", endpoint, resp.StatusCode, truncate(body, 2000))
	}
	if resp.StatusCode != http.StatusOK {
		logErr("codex", fmt.Errorf("HTTP %d", resp.StatusCode))
		return failedProvider()
	}

	var u codexUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		logErr("codex", fmt.Errorf("解析响应失败: %w", err))
		return failedProvider()
	}
	p := Provider{
		H5: codexWindowToField(u.RateLimit.Primary, "h5"),
		Wk: codexWindowToField(u.RateLimit.Secondary, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][codex] parsed: %+v\n", p)
	}
	return p
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
