package main

// 当 Codex 不是官方订阅、而是中转站（auth.json 只有 OPENAI_API_KEY）时，
// 用这个 sk- key 调中转站的余额接口，取「已用 + 总余额」(Relay)。
// 支持两种中转站：sub2api（/v1/usage）与 new-api（OpenAI billing 仿真）。
//   - sub2api：展示「今日用量」（Relay.Label="Today"）。
//   - new-api：展示「累计已用」（Relay.Label="Used"）。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// codexConfigToml 取 ~/.codex/config.toml 里我们需要的字段（中转站 base_url）。
type codexConfigToml struct {
	ModelProvider  string `toml:"model_provider"`
	ModelProviders map[string]struct {
		BaseURL string `toml:"base_url"`
	} `toml:"model_providers"`
}

// loadCodexRelayBaseURL 从 config.toml 读取当前 model_provider 的 base_url（如 https://muyuan.do/v1）。
func loadCodexRelayBaseURL() (string, error) {
	path := filepath.Join(codexHome(), "config.toml")
	var c codexConfigToml
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if c.ModelProvider == "" {
		return "", fmt.Errorf("config.toml 缺少 model_provider")
	}
	p, ok := c.ModelProviders[c.ModelProvider]
	if !ok || p.BaseURL == "" {
		return "", fmt.Errorf("config.toml 中 model_providers.%s.base_url 为空", c.ModelProvider)
	}
	return p.BaseURL, nil
}

// currencySymbol 把货币代码/符号规范成固件能渲染的短前缀（最长 3 字符，优先 ASCII）。
func currencySymbol(unit string) string {
	u := strings.TrimSpace(unit)
	switch strings.ToUpper(u) {
	case "", "USD", "$":
		return "$"
	}
	if len(u) == 1 && u[0] >= 0x20 && u[0] < 0x7f { // 已是单个 ASCII 符号
		return u
	}
	return "$"
}

// relayGet 发一个带 Bearer apiKey 的 GET，返回响应体（非 2xx 视为错误）。
func relayGet(ctx context.Context, client *http.Client, url, apiKey string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-credits/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

// resolveCodexRelay 决定 Codex 是否走中转：cfg.CodexMode 显式优先，否则看 auth.json。
func resolveCodexRelay(cfg Config) bool {
	switch strings.ToLower(strings.TrimSpace(cfg.CodexMode)) {
	case "official":
		return false
	case "relay":
		return true
	default:
		return codexIsRelay()
	}
}

// relayHost 从 base_url 取主机名（如 https://goai.im/v1 → goai.im），失败返回空串。
func relayHost(base string) string {
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	return ""
}

// resolveCodexRelayType 按 base_url 的主机名在 cfg.CodexRelayTypes 映射里查类型；
// 命中规则：配置的 key 是主机名的子串（如 "goai.im" 命中 "api.goai.im"）。未命中返回 ""（自动）。
func resolveCodexRelayType(cfg Config, base string) string {
	host := relayHost(base)
	if host == "" {
		return ""
	}
	for k, v := range cfg.CodexRelayTypes {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && strings.Contains(host, k) {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	return ""
}

// fetchCodexRelay 取中转站「已用 + 余额」。base_url 始终来自 ~/.codex/config.toml；
// 类型由 cfg.CodexRelayTypes（主机名→类型）决定，未配置则自动先试 sub2api 再试 new-api。
func fetchCodexRelay(ctx context.Context, client *http.Client, cfg Config) (Relay, error) {
	auth, err := loadCodexAuth()
	if err != nil {
		return Relay{}, err
	}
	apiKey := auth.OpenAIAPIKey
	if apiKey == "" {
		return Relay{}, fmt.Errorf("auth.json 无 OPENAI_API_KEY（不是中转站？）")
	}
	base, err := loadCodexRelayBaseURL()
	if err != nil {
		return Relay{}, err
	}

	switch resolveCodexRelayType(cfg, base) {
	case "sub2api":
		return fetchRelaySub2api(ctx, client, base, apiKey)
	case "new-api", "newapi", "new_api":
		return fetchRelayNewAPI(ctx, client, base, apiKey)
	default: // auto
		if r, e1 := fetchRelaySub2api(ctx, client, base, apiKey); e1 == nil {
			return r, nil
		} else if r2, e2 := fetchRelayNewAPI(ctx, client, base, apiKey); e2 == nil {
			return r2, nil
		} else {
			return Relay{}, fmt.Errorf("自动识别中转站失败：sub2api(%v) / new-api(%v)", e1, e2)
		}
	}
}

// relayUsageBucket 是 sub2api /usage 里 today/total 的金额结构。
type relayUsageBucket struct {
	Cost       float64 `json:"cost"`
	ActualCost float64 `json:"actual_cost"`
}

// cost 取实际扣费，缺省回退原始 cost。
func (b relayUsageBucket) cost() float64 {
	if b.ActualCost != 0 {
		return b.ActualCost
	}
	return b.Cost
}

// fetchRelaySub2api：GET {base}/usage（base 已含 /v1）。金额单位美元。
// sub2api 展示「今日用量」：Used=usage.today.actual_cost，Balance=总剩余（remaining），标签 "Today"。
func fetchRelaySub2api(ctx context.Context, client *http.Client, base, apiKey string) (Relay, error) {
	url := strings.TrimRight(base, "/") + "/usage"
	body, err := relayGet(ctx, client, url, apiKey)
	if err != nil {
		return Relay{}, err
	}
	var u struct {
		Remaining float64 `json:"remaining"` // 两种模式都有：总剩余
		Balance   float64 `json:"balance"`   // 钱包模式
		Unit      string  `json:"unit"`
		Quota     struct {
			Used      float64 `json:"used"`
			Remaining float64 `json:"remaining"`
		} `json:"quota"`
		Usage struct {
			Today relayUsageBucket `json:"today"`
			Total relayUsageBucket `json:"total"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return Relay{}, fmt.Errorf("解析 sub2api /usage 失败: %w", err)
	}
	balance := u.Remaining
	if balance == 0 { // 兜底：钱包模式 / 嵌套字段
		if u.Quota.Remaining != 0 {
			balance = u.Quota.Remaining
		} else if u.Balance != 0 {
			balance = u.Balance
		}
	}
	used := u.Usage.Today.cost() // 今日用量
	return Relay{Used: used, Balance: balance, Unit: currencySymbol(u.Unit), Label: "Today", OK: true}, nil
}

// fetchRelayNewAPI：OpenAI billing 仿真（one-api/new-api）。
// subscription.hard_limit_usd 是总额度（美元）；usage.total_usage 是「累计已用」，单位美分（需 /100）。
// Today=累计已用，Balance=总额−累计。（new-api 无可用 API key 调的「按日」聚合接口，故展示累计。）
func fetchRelayNewAPI(ctx context.Context, client *http.Client, base, apiKey string) (Relay, error) {
	base = strings.TrimRight(base, "/")

	subBody, err := relayGet(ctx, client, base+"/dashboard/billing/subscription", apiKey)
	if err != nil {
		return Relay{}, err
	}
	var sub struct {
		HardLimitUSD float64 `json:"hard_limit_usd"`
	}
	if err := json.Unmarshal(subBody, &sub); err != nil {
		return Relay{}, fmt.Errorf("解析 subscription 失败: %w", err)
	}

	usageBody, err := relayGet(ctx, client, base+"/dashboard/billing/usage", apiKey)
	if err != nil {
		return Relay{}, err
	}
	var usage struct {
		TotalUsage float64 `json:"total_usage"` // 累计已用，美分
	}
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		return Relay{}, fmt.Errorf("解析 usage 失败: %w", err)
	}

	usedUSD := usage.TotalUsage / 100.0
	return Relay{
		Used:    usedUSD,
		Balance: sub.HardLimitUSD - usedUSD,
		Unit:    "$",
		Label:   "Used", // new-api 展示累计已用
		OK:      true,
	}, nil
}
