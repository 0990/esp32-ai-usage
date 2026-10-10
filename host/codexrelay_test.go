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
	"time"
)

func TestCustomRelayTypes(t *testing.T) {
	for _, typ := range []string{"sub2api", "newapi"} {
		t.Run(typ, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer manual-key" {
					t.Error("incorrect manual key")
				}
				switch r.URL.Path {
				case "/v1/usage":
					if typ != "sub2api" {
						t.Error("must not try another type")
					}
					fmt.Fprint(w, `{"remaining":42,"unit":"USD","usage":{"today":{"actual_cost":1.5}}}`)
				case "/v1/dashboard/billing/subscription":
					if typ != "newapi" {
						t.Error("must not try another type")
					}
					fmt.Fprint(w, `{"hard_limit_usd":44}`)
				case "/v1/dashboard/billing/usage":
					fmt.Fprint(w, `{"total_usage":200}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			for _, base := range []string{server.URL, server.URL + "/v1/"} {
				obj := CustomObject{ID: "manual", Name: "station", Type: typ, BaseURL: base, APIKey: "manual-key", Enabled: true}
				r, err := fetchCustomObject(context.Background(), server.Client(), obj)
				if err != nil || !r.OK || r.Balance != 42 {
					t.Fatalf("relay result %+v, %v", r, err)
				}
				if typ == "sub2api" && (r.Used != 1.5 || r.Label != "Today") {
					t.Fatal(r)
				}
				if typ == "newapi" && (r.Used != 2 || r.Label != "Used") {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestCustomObjectsEngineAndCache(t *testing.T) {
	calls := 0
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.Header.Get("Authorization"), "disabled") {
			t.Error("disabled object queried")
		}
		if fail {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"remaining":42,"usage":{"today":{"cost":2}}}`)
	}))
	defer server.Close()
	cfg := defaultConfig()
	cfg.Provider = "none"
	cfg.CustomObjects = []CustomObject{
		{ID: "one", Name: "first", Type: "sub2api", BaseURL: server.URL + "/v1", APIKey: "manual-key", Enabled: true},
		{ID: "two", Name: "second", Type: "sub2api", BaseURL: server.URL + "/v1", APIKey: "other-key", Enabled: true},
		{ID: "three", Name: "disabled", Type: "sub2api", BaseURL: server.URL + "/v1", APIKey: "disabled-key"},
	}
	path := filepath.Join(t.TempDir(), "config.json")
	e := NewEngine(cfg, path)
	if !e.fetchOnce(context.Background(), server.Client(), cfg) || calls != 2 {
		t.Fatalf("success/calls: %d", calls)
	}
	frames := e.buildFrames()
	if len(frames) != 2 || frames[0].Kind != "money" || frames[0].V1 != "$42" {
		t.Fatal(frames)
	}
	raw, err := os.ReadFile(cachePath(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "manual-key") || strings.Contains(string(raw), "other-key") {
		t.Fatal("cache contains key")
	}
	restored := NewEngine(cfg, path)
	restored.loadCache()
	if got := restored.buildFrames(); len(got) != 2 || got[1].V1 != "$42" {
		t.Fatal(got)
	}
	fail = true
	if e.fetchOnce(context.Background(), server.Client(), cfg) {
		t.Fatal("unexpected success")
	}
	if e.buildFrames()[0].V1 != "$42" {
		t.Fatal("failed refresh discarded good result")
	}
	cfg.CustomObjects[0].APIKey = "bad-key"
	e.cfg = cfg
	if e.buildFrames()[0].V1 != "ERR" {
		t.Fatal("old credentials reused")
	}
	cfg.CustomObjects[0].Enabled = false
	e.cfg = cfg
	if len(e.buildFrames()) != 1 {
		t.Fatal("disabled page shown")
	}
	cfg.CustomObjects = nil
	e.cfg = cfg
	if len(e.buildFrames()) != 0 {
		t.Fatal("removed page shown")
	}
}

func TestCustomConfigValidation(t *testing.T) {
	good := CustomObject{ID: "one", Name: "relay", Type: "sub2api", BaseURL: "https://example.com/v1", APIKey: "secret", Enabled: true}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*CustomObject){
		func(o *CustomObject) { o.Name = " " }, func(o *CustomObject) { o.Type = "auto" }, func(o *CustomObject) { o.APIKey = "" },
		func(o *CustomObject) { o.BaseURL = "file:///etc/passwd" }, func(o *CustomObject) { o.BaseURL = "https://key@example.com" }, func(o *CustomObject) { o.BaseURL = "https://example.com?key=secret" },
	} {
		o := good
		change(&o)
		if o.validate() == nil {
			t.Fatal("invalid object accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":"none","codex_mode":"relay","codex_relay_types":{"example.com":"auto"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(path)
	if cfg.wants("codex") {
		t.Fatal("none must not fall back to codex")
	}
	cfg.CustomObjects = []CustomObject{good}
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	cfg = loadConfig(path)
	if len(cfg.CustomObjects) != 1 || cfg.CustomObjects[0] != good {
		t.Fatal("custom object roundtrip failed")
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "codex_mode") || strings.Contains(string(raw), "codex_relay_types") {
		t.Fatal("obsolete config persisted")
	}
	a := good
	a.Name = "renamed"
	if a.cacheKey() != good.cacheKey() {
		t.Fatal("rename invalidates balance")
	}
	a = good
	a.BaseURL = "https://other.example"
	if a.cacheKey() == good.cacheKey() {
		t.Fatal("changed URL reuses balance")
	}
	e := NewEngine(cfg, path)
	e.started = true
	e.custom = map[string]customResult{good.cacheKey(): {Relay: Relay{OK: true}, At: time.Now().Add(-time.Hour)}}
	if !e.buildFrames()[0].Old {
		t.Fatal("stale custom result not marked old")
	}
}

func TestRelayRejectsWrongResponseAndHidesKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"error":"secret"}`) }))
	defer server.Close()
	for _, typ := range []string{"sub2api", "newapi", "auto"} {
		obj := CustomObject{Name: "relay", Type: typ, BaseURL: server.URL, APIKey: "secret"}
		if _, err := fetchCustomObject(context.Background(), server.Client(), obj); err == nil {
			t.Fatalf("%s accepted wrong response", typ)
		}
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); fmt.Fprint(w, "secret") })
	obj := CustomObject{Name: "relay", Type: "sub2api", BaseURL: server.URL, APIKey: "secret"}
	_, err := fetchCustomObject(context.Background(), server.Client(), obj)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("response leaked key or error was swallowed")
	}
}

func TestUpdateCustomConfigSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	e := NewEngine(defaultConfig(), path)
	cfg := defaultConfig()
	cfg.Provider = "none"
	cfg.CustomObjects = []CustomObject{{ID: "one", Name: "relay", Type: "newapi", BaseURL: "https://example.com", APIKey: "manual", Enabled: true}}
	if err := e.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.CustomObjects[0].APIKey = "changed"
	if e.Config().CustomObjects[0].APIKey != "manual" {
		t.Fatal("caller changed engine config")
	}
	snapshot := e.Config()
	snapshot.CustomObjects[0].Enabled = false
	if !e.Config().CustomObjects[0].Enabled {
		t.Fatal("returned config is not isolated")
	}
	bad := e.Config()
	bad.CustomObjects[0].Type = "auto"
	if err := e.UpdateConfig(bad); err == nil {
		t.Fatal("automatic type accepted")
	}
	if loadConfig(path).CustomObjects[0].Type != "newapi" {
		t.Fatal("invalid update overwrote config")
	}
	e.cfgPath = filepath.Join(path, "invalid.json")
	changed := e.Config()
	changed.CustomObjects[0].Name = "different"
	if err := e.UpdateConfig(changed); err == nil {
		t.Fatal("expected save failure")
	}
	if e.Config().CustomObjects[0].Name != "relay" {
		t.Fatal("failed save applied config")
	}
}

func TestBalanceFrames(t *testing.T) {
	for _, typ := range []string{"sub2api", "newapi"} {
		f := balanceFrame(typ, Relay{Used: 123, Balance: 42.5, Unit: "$", OK: true}, true)
		if f.Icon != typ || f.Kind != "money" || f.L1 != "Balance" || f.V1 != "$42.5" || f.V2 != "" || f.L2 != "" || !f.Old {
			t.Fatalf("unexpected balance page: %+v", f)
		}
		f = balanceFrame(typ, Relay{}, false)
		if f.Icon != typ || f.V1 != "ERR" || f.V2 != "" {
			t.Fatalf("unexpected failed page: %+v", f)
		}
	}
}
