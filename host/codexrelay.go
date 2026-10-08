package main

// 当 Codex 不是官方订阅、而是中转站（供应商配置、环境变量或 auth.json 提供 key）时，
// 用这个 sk- key 调中转站的余额接口，取「已用 + 总余额」(Relay)。
// 支持两种中转站：sub2api（/v1/usage）与 new-api（OpenAI billing 仿真）。
//   - sub2api：展示「今日用量」（Relay.Label="Today"）。
//   - new-api：展示「累计已用」（Relay.Label="Used"）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// codexConfigToml 取 ~/.codex/config.toml 里我们需要的字段（中转站 base_url）。
type codexConfigToml struct {
	ModelProvider  string                        `toml:"model_provider"`
	ModelProviders map[string]codexModelProvider `toml:"model_providers"`
}

type codexModelProvider struct {
	BaseURL                 string `toml:"base_url"`
	EnvKey                  string `toml:"env_key"`
	ExperimentalBearerToken string `toml:"experimental_bearer_token"`
	RequiresOpenAIAuth      bool   `toml:"requires_openai_auth"`
}

// loadCodexRelayBaseURL 从 config.toml 读取当前 model_provider 的 base_url（如 https://muyuan.do/v1）。
func loadCodexRelayBaseURL() (string, error) {
	p, err := loadCodexModelProvider()
	return p.BaseURL, err
}

func loadCodexModelProvider() (codexModelProvider, error) {
	path := filepath.Join(codexHome(), "config.toml")
	var c codexConfigToml
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return codexModelProvider{}, fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if c.ModelProvider == "" {
		return codexModelProvider{}, fmt.Errorf("config.toml 缺少 model_provider")
	}
	p, ok := c.ModelProviders[c.ModelProvider]
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if !ok || p.BaseURL == "" {
		return codexModelProvider{}, fmt.Errorf("config.toml 中 model_providers.%s.base_url 为空", c.ModelProvider)
	}
	return p, nil
}

// loadCodexRelaySettings 不要求 auth.json 存在：优先使用当前供应商显式配置的凭据。
// env_key 缺失时直接报错，避免把其它供应商的 key 发到当前站点。
func loadCodexRelaySettings() (apiKey, base string, err error) {
	p, err := loadCodexModelProvider()
	if err != nil {
		return "", "", err
	}
	if p.EnvKey != "" {
		key := strings.TrimSpace(os.Getenv(p.EnvKey))
		if key == "" {
			return "", "", fmt.Errorf("Codex 中转站配置的环境变量 %s 未设置或为空", p.EnvKey)
		}
		return key, p.BaseURL, nil
	}
	if key := strings.TrimSpace(p.ExperimentalBearerToken); key != "" {
		return key, p.BaseURL, nil
	}
	a, authErr := loadCodexAuth()
	if authErr != nil && !errors.Is(authErr, os.ErrNotExist) {
		return "", "", authErr
	}
	if key := strings.TrimSpace(a.OpenAIAPIKey); key != "" {
		return key, p.BaseURL, nil
	}
	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		return key, p.BaseURL, nil
	}
	return "", "", fmt.Errorf("Codex 中转站缺少 API key：请配置供应商 env_key/experimental_bearer_token、auth.json 的 OPENAI_API_KEY 或环境变量 OPENAI_API_KEY")
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
	req.Header.Set("User-Agent", "esp32-ai-usage/1.0")

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

// resolveCodexRelay 决定 Codex 是否走中转：cfg.CodexMode 显式优先，否则自动检测。
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
	apiKey, base, err := loadCodexRelaySettings()
	if err != nil {
		return Relay{}, err
	}

	return fetchRelayTyped(ctx, client, resolveCodexRelayType(cfg, base), base, apiKey)
}

// fetchRelayTyped 按类型取中转站「已用 + 余额」；typ 为 ""（自动）时先试 sub2api 再试 new-api。
func fetchRelayTyped(ctx context.Context, client *http.Client, typ, base, apiKey string) (Relay, error) {
	switch typ {
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

// fetchClaudeRelay 取 Claude 中转站「已用 + 余额」。apiKey/base 来自
// ~/.claude/settings.json 的 env（ANTHROPIC_API_KEY 或 ANTHROPIC_AUTH_TOKEN /
// ANTHROPIC_BASE_URL）；类型复用 cfg.CodexRelayTypes（主机名→类型），未配置则自动。
// Claude 的 base_url 是 Anthropic 风格、通常不带 /v1，而余额接口挂在 OpenAI 风格
// 的 /v1 根下，故按 relayBaseCandidates 逐个候选根尝试。
func fetchClaudeRelay(ctx context.Context, client *http.Client, cfg Config) (Relay, error) {
	apiKey, base, err := loadClaudeRelaySettings()
	if err != nil {
		return Relay{}, err
	}
	if apiKey == "" || base == "" {
		return Relay{}, fmt.Errorf("settings.json 缺少 ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN 或 ANTHROPIC_BASE_URL（不是中转站？）")
	}

	typ := resolveCodexRelayType(cfg, base)
	var errs []string
	for _, b := range relayBaseCandidates(base) {
		if r, e := fetchRelayTyped(ctx, client, typ, b, apiKey); e == nil {
			return r, nil
		} else {
			errs = append(errs, fmt.Sprintf("%s(%v)", b, e))
		}
	}
	return Relay{}, fmt.Errorf("拉取中转站余额失败：%s", strings.Join(errs, "；"))
}

// relayBaseCandidates 返回余额接口的候选 API 根。Claude 的 ANTHROPIC_BASE_URL 通常
// 不带 /v1（Claude Code 自己拼 /v1/messages），而中转站余额接口挂在 /v1 根下，
// 故优先补 /v1，原始 base 作兜底；已带 /v1 则直接用。
func relayBaseCandidates(base string) []string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return []string{base}
	}
	return []string{base + "/v1", base}
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
