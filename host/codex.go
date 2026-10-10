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
//   响应 rate_limit 有 primary_window / secondary_window 两个窗口，各含
//        used_percent、reset_at(unix 秒)/reset_after_seconds、limit_window_seconds(窗口时长)。
//   ⚠️ 不要按 primary/secondary 的位置/命名判定 5h 还是周（OpenAI 两个数据源命名相反，
//      且部分账号已取消 5h 窗口）。改按各窗口 limit_window_seconds 判定：小=5h、大=周；
//      仅一个窗口时以 21600s(6h) 为阈值。窗口可能缺失(零值)，缺失的槽位按 100% 显示。
//      参照工程 D:\workspace\ai\sub2api-goai 的 Normalize()（openai_gateway_service.go）。

const defaultCodexEndpoint = "https://chatgpt.com/backend-api/wham/usage"

// codexH5MaxWindowSeconds = 6h。仅一个窗口带时长时，<= 此值判为 5h 窗口。
// 参照 sub2api-goai：window_minutes <= 360，即 360*60 = 21600 秒。
const codexH5MaxWindowSeconds = 21600

// codexAuth 对应 ~/.codex/auth.json 里我们需要的字段。
// 只读取官方订阅的 tokens.access_token，不读取中转站 API key。
type codexAuth struct {
	Tokens struct {
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

// codexUsageResp 是 wham/usage 的响应（只取用到的字段）。
type codexUsageResp struct {
	RateLimit struct {
		Primary   codexWindow `json:"primary_window"`
		Secondary codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAt            int64   `json:"reset_at"`
	ResetAfterSeconds  float64 `json:"reset_after_seconds"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"` // 窗口时长，用于判定 5h vs 周
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
	h5w, wkw := codexClassify(u.RateLimit.Primary, u.RateLimit.Secondary)
	p := Provider{
		H5: codexWindowToField(h5w, "h5"),
		Wk: codexWindowToField(wkw, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][codex] parsed: %+v\n", p)
	}
	return p, nil
}

// codexClassify 按窗口时长(limit_window_seconds)判定哪个是 5h、哪个是周，
// 不信任 primary/secondary 命名（OpenAI 两个数据源命名相反，且 5h 窗口可能被取消）。
// 返回 (h5, wk)；缺失的窗口是零值 codexWindow，经 leftFromUsed(0) 显示为 100%。
func codexClassify(primary, secondary codexWindow) (h5, wk codexWindow) {
	pDur := primary.LimitWindowSeconds > 0
	sDur := secondary.LimitWindowSeconds > 0
	switch {
	case pDur && sDur: // 两个时长都已知：小=5h，大=周
		if primary.LimitWindowSeconds < secondary.LimitWindowSeconds {
			return primary, secondary
		}
		return secondary, primary
	case pDur: // 只有 primary 带时长：阈值判定，另一槽为相反窗口
		if primary.LimitWindowSeconds <= codexH5MaxWindowSeconds {
			return primary, secondary
		}
		return secondary, primary
	case sDur: // 只有 secondary 带时长
		if secondary.LimitWindowSeconds <= codexH5MaxWindowSeconds {
			return secondary, primary
		}
		return primary, secondary
	default: // 都没时长：兜底（参照 sub2api-goai：primary=周、secondary=5h）；实际接口总带该字段，此路仅防御
		return secondary, primary
	}
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
