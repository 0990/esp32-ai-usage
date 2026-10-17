package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Kimi（Kimi Code 订阅）取数已对本机实测确认：
//
//	GET https://api.kimi.com/coding/v1/usages
//	头 Authorization: Bearer <access_token>
//	响应（数字都是字符串）：
//	  usage  = 每周窗口（resetTime 约 7 天后）→ pct 页下行 1w
//	  limits = 滚动窗口数组，window.duration×timeUnit ≤ 6h 的那条是 5h 窗口 → 上行 5h
//
// 凭据在 ~/.kimi-code/credentials/kimi-code.json（尊重 KIMI_CODE_HOME）。
// ⚠️ access token 寿命仅 15 分钟（expires_in=900），刷新由 kimi CLI 自己进行并写回凭据文件；
// 这里只读 access_token（同 claude/codex 的哲学）：过期会 401 → 显示 ERR，
// 用户任意使用一次 kimi CLI 即刷新恢复（watcher 监听凭据目录，刷新后立即重新取数）。
const defaultKimiEndpoint = "https://api.kimi.com/coding/v1/usages"

// kimiH5MaxWindowSeconds = 6h。滚动窗口时长 <= 此值判为 5h 窗口（对齐 codex 的分类阈值）。
const kimiH5MaxWindowSeconds = 21600

// kimiHome 返回 ~/.kimi-code（尊重 KIMI_CODE_HOME）。
func kimiHome() string {
	if h := os.Getenv("KIMI_CODE_HOME"); h != "" {
		return h
	}
	return expandHome("~/.kimi-code")
}

// kimiCreds 对应 credentials/kimi-code.json 里我们需要的字段。
type kimiCreds struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

// loadKimiToken 读取 access_token；过期则报带提示的错误（日志更可读）。
func loadKimiToken() (string, error) {
	path := filepath.Join(kimiHome(), "credentials", "kimi-code.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	var c kimiCreds
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("解析 kimi-code.json 失败: %w", err)
	}
	if c.AccessToken == "" {
		return "", fmt.Errorf("kimi-code.json 中没有 access_token（未登录 kimi CLI？）")
	}
	if c.ExpiresAt > 0 && time.Now().Unix() >= c.ExpiresAt {
		return "", fmt.Errorf("token 已过期（15 分钟有效），请使用一次 kimi CLI 刷新")
	}
	return c.AccessToken, nil
}

// kimiUsageResp 是 /coding/v1/usages 的响应（只取用到的字段；数字是字符串）。
type kimiUsageResp struct {
	Usage  kimiDetail  `json:"usage"`  // 每周窗口
	Limits []kimiLimit `json:"limits"` // 滚动窗口（含 5h）
}

type kimiDetail struct {
	Used      string `json:"used"`
	ResetTime string `json:"resetTime"`
}

type kimiLimit struct {
	Window struct {
		Duration int64  `json:"duration"`
		TimeUnit string `json:"timeUnit"`
	} `json:"window"`
	Detail kimiDetail `json:"detail"`
}

// fetchKimi 取 Kimi 的 5h/1周额度。失败返回 OK:false 的 Provider 和 error。
// ctx 取消时请求立即中止（退出程序时不会卡在 in-flight 请求上）。
func fetchKimi(ctx context.Context, client *http.Client, endpoint string, debug bool) (Provider, error) {
	if endpoint == "" {
		endpoint = defaultKimiEndpoint
	}
	token, err := loadKimiToken()
	if err != nil {
		return failedProvider(), err
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-usage/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return failedProvider(), err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][kimi] GET %s -> %d\n%s\n", endpoint, resp.StatusCode, truncate(body, 2000))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return failedProvider(), fmt.Errorf("HTTP 401（token 已过期，使用一次 kimi CLI 即刷新恢复）")
	}
	if resp.StatusCode != http.StatusOK {
		return failedProvider(), fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var u kimiUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		logErr("kimi", fmt.Errorf("解析响应失败: %w", err))
		return failedProvider(), fmt.Errorf("json ummarshal failed:%w", err)
	}
	p := Provider{
		H5: kimiWindowToField(kimiClassifyH5(u.Limits), "h5"),
		Wk: kimiWindowToField(u.Usage, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][kimi] parsed: %+v\n", p)
	}
	return p, nil
}

// kimiClassifyH5 从滚动窗口里挑出 5h 窗口（时长 <= 6h），参照 codexClassify 的思路。
// 没有匹配项时返回零值 kimiDetail，经 leftFromUsed(0) 显示为 100%、复位 "--"。
func kimiClassifyH5(limits []kimiLimit) kimiDetail {
	for _, l := range limits {
		secs := kimiWindowSeconds(l.Window.Duration, l.Window.TimeUnit)
		if secs > 0 && secs <= kimiH5MaxWindowSeconds {
			return l.Detail
		}
	}
	return kimiDetail{}
}

// kimiWindowSeconds 把 duration×timeUnit 换算成秒；未知单位返回 0（跳过该条）。
func kimiWindowSeconds(duration int64, unit string) int64 {
	if duration <= 0 {
		return 0
	}
	switch unit {
	case "TIME_UNIT_SECOND":
		return duration
	case "TIME_UNIT_MINUTE":
		return duration * 60
	case "TIME_UNIT_HOUR":
		return duration * 3600
	}
	return 0
}

func kimiWindowToField(d kimiDetail, kind string) Window {
	used, _ := strconv.ParseFloat(d.Used, 64)
	reset := "--"
	if d.ResetTime != "" {
		reset = fmtReset(d.ResetTime, kind)
	}
	return Window{Left: leftFromUsed(used), Reset: reset}
}
