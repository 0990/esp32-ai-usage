package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexRelayCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, fields, auth, env, wantKey, wantErr string
		wantRelay                                 bool
	}{
		{"inline without auth", `experimental_bearer_token = "inline-key"`, "", "", "inline-key", "", true},
		{"env without auth", `env_key = "TEST_CODEX_RELAY_KEY"`, "", "env-key", "env-key", "", true},
		{"missing configured env", `env_key = "TEST_CODEX_RELAY_KEY"`, `{"OPENAI_API_KEY":"other-key"}`, "", "", "TEST_CODEX_RELAY_KEY", true},
		{"legacy auth", "", `{"OPENAI_API_KEY":"auth-key"}`, "", "auth-key", "", true},
		{"missing all credentials", "", "", "", "", "缺少 API key", true},
		{"null auth key", "", `{"OPENAI_API_KEY":null}`, "", "", "缺少 API key", true},
		{"official oauth", `requires_openai_auth = true`, `{"tokens":{"access_token":"oauth"}}`, "", "", "缺少 API key", false},
		{"inline overrides stale oauth", `experimental_bearer_token = "inline-key"`, `{"tokens":{"access_token":"oauth"}}`, "", "inline-key", "", true},
		{"inline ignores broken auth", `experimental_bearer_token = "inline-key"`, `{broken`, "", "inline-key", "", true},
		{"broken auth reported", "", `{broken`, "", "", "解析 auth.json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CODEX_HOME", dir)
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("TEST_CODEX_RELAY_KEY", tc.env)
			config := "model_provider = \"relay\"\n[model_providers.relay]\nbase_url = \"https://relay.example/v1\"\n" + tc.fields + "\n[model_providers.unselected]\nbase_url = \"https://other.example/v1\"\nexperimental_bearer_token = \"other-key\"\n"
			writeCodexTestFile(t, dir, "config.toml", config)
			if tc.auth != "" {
				writeCodexTestFile(t, dir, "auth.json", tc.auth)
			}
			if got := codexIsRelay(); got != tc.wantRelay {
				t.Fatalf("relay detection = %v, want %v", got, tc.wantRelay)
			}
			key, base, err := loadCodexRelaySettings()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || key != tc.wantKey || base != "https://relay.example/v1" {
				t.Fatal("incorrect credential or base URL resolution", err)
			}
		})
	}
}

func writeCodexTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFetchCodexRelayWithoutAuth(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "fallback-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" || r.Header.Get("Authorization") != "Bearer fallback-key" {
			t.Error("unexpected usage path or authorization")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"remaining":42,"unit":"USD","usage":{"today":{"actual_cost":1.5}}}`)
	}))
	defer server.Close()
	writeCodexTestFile(t, codexHome(), "config.toml", fmt.Sprintf("model_provider = \"relay\"\n[model_providers.relay]\nbase_url = %q\n", server.URL+"/v1"))
	if !resolveCodexRelay(Config{}) || resolveCodexRelay(Config{CodexMode: "official"}) {
		t.Fatal("incorrect automatic or explicit official mode")
	}
	r, err := fetchCodexRelay(context.Background(), server.Client(), Config{})
	if err != nil || !r.OK || r.Balance != 42 || r.Used != 1.5 || r.Label != "Today" {
		t.Fatalf("usage query failed: %+v, %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(codexHome(), "auth.json")); !os.IsNotExist(err) {
		t.Fatal("query must not create auth.json")
	}
}
