package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CustomObject struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

type customResult struct {
	Relay Relay
	At    time.Time
}

func newCustomID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", id)
}

// 缓存不保存凭据；更换地址、类型或 key 后不能显示原账号的余额。
func (o CustomObject) cacheKey() string {
	raw, _ := json.Marshal([]string{o.ID, o.Type, o.BaseURL, o.APIKey})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (o CustomObject) validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return fmt.Errorf("请填写名称")
	}
	if o.Type != "sub2api" && o.Type != "newapi" {
		return fmt.Errorf("请选择 sub2api 或 newapi")
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Base URL 必须是有效的 HTTP/HTTPS 地址，不能包含账号、查询参数或片段")
	}
	if strings.TrimSpace(o.APIKey) == "" || strings.ContainsAny(o.APIKey, "\r\n") {
		return fmt.Errorf("请填写有效的 API key")
	}
	return nil
}

func fetchCustomObject(ctx context.Context, client *http.Client, o CustomObject) (Relay, error) {
	if err := o.validate(); err != nil {
		return Relay{}, err
	}
	base := strings.TrimRight(o.BaseURL, "/")
	// 同一种明确指定的 API 支持站点根地址和带 /v1 的地址。
	bases := []string{base}
	if !strings.HasSuffix(base, "/v1") {
		bases = []string{base + "/v1", base}
	}
	var last error
	for _, b := range bases {
		r, err := fetchRelayTyped(ctx, client, o.Type, b, o.APIKey)
		if err == nil {
			return r, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return Relay{}, last
}
