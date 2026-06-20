package main

import (
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
const defaultClaudeBase = "https://gate.takemoon.com"

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

func claudeBaseURL(cfgBase string) string {
	if cfgBase != "" {
		return strings.TrimRight(cfgBase, "/")
	}
	if env := os.Getenv("ANTHROPIC_BASE_URL"); env != "" {
		return strings.TrimRight(env, "/")
	}
	return defaultClaudeBase
}

func fetchClaude(client *http.Client, cfgBase string, debug bool) Provider {
	token, err := loadClaudeToken()
	if err != nil {
		logErr("claude", err)
		return failedProvider()
	}
	endpoint := claudeBaseURL(cfgBase) + "/api/oauth/usage"

	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "esp32-ai-credits/1.0")

	resp, err := client.Do(req)
	if err != nil {
		logErr("claude", fmt.Errorf("request %s failed: %w", endpoint, err))
		return failedProvider()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][claude] GET %s -> %d\n%s\n", endpoint, resp.StatusCode, truncate(body, 2000))
	}
	if resp.StatusCode != http.StatusOK {
		logErr("claude", fmt.Errorf("HTTP %d", resp.StatusCode))
		return failedProvider()
	}

	var u claudeUsageResp
	if err := json.Unmarshal(body, &u); err != nil {
		logErr("claude", fmt.Errorf("parse response failed: %w", err))
		return failedProvider()
	}
	p := Provider{
		H5: claudeWindowToField(u.FiveHour, "h5"),
		Wk: claudeWindowToField(u.SevenDay, "wk"),
		OK: true,
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[debug][claude] parsed: %+v\n", p)
	}
	return p
}

func claudeWindowToField(w claudeWindow, kind string) Window {
	reset := "--"
	if w.ResetsAt != nil {
		reset = fmtReset(w.ResetsAt, kind)
	}
	return Window{Left: leftFromUsed(w.Utilization), Reset: reset}
}
