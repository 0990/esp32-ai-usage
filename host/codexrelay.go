package main

// 自定义监控对象使用手动填写的类型、Base URL 和 API key 查询余额。
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("余额请求地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-usage/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("余额请求失败（网络、代理或超时）")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func fetchRelayTyped(ctx context.Context, client *http.Client, typ, base, apiKey string) (Relay, error) {
	switch typ {
	case "sub2api":
		return fetchRelaySub2api(ctx, client, base, apiKey)
	case "newapi":
		return fetchRelayNewAPI(ctx, client, base, apiKey)
	default:
		return Relay{}, fmt.Errorf("请选择 sub2api 或 newapi")
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
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if fields["remaining"] == nil && fields["balance"] == nil && fields["quota"] == nil {
		return Relay{}, fmt.Errorf("sub2api 响应缺少余额字段")
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
		HardLimitUSD *float64 `json:"hard_limit_usd"`
	}
	if err := json.Unmarshal(subBody, &sub); err != nil {
		return Relay{}, fmt.Errorf("解析 subscription 失败: %w", err)
	}

	usageBody, err := relayGet(ctx, client, base+"/dashboard/billing/usage", apiKey)
	if err != nil {
		return Relay{}, err
	}
	var usage struct {
		TotalUsage *float64 `json:"total_usage"` // 累计已用，美分
	}
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		return Relay{}, fmt.Errorf("解析 usage 失败: %w", err)
	}

	if sub.HardLimitUSD == nil || usage.TotalUsage == nil {
		return Relay{}, fmt.Errorf("newapi 响应缺少额度或用量字段")
	}
	usedUSD := *usage.TotalUsage / 100.0
	return Relay{
		Used:    usedUSD,
		Balance: *sub.HardLimitUSD - usedUSD,
		Unit:    "$",
		Label:   "Used", // new-api 展示累计已用
		OK:      true,
	}, nil
}
