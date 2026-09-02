package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ReClaude runs Claude Code through a local daemon. The daemon is an HTTP
// proxy which terminates TLS with the CA stored in ~/.reclaude/ca.pem, then
// forwards the request through ReClaude's gateway.
const defaultReclaudeEndpoint = defaultClaudeBase + claudeUsagePath

type reclaudeState struct {
	Daemon struct {
		Running *bool `json:"running"`
		Port    int   `json:"port"`
	} `json:"daemon"`
	LastPort int `json:"last_port"`
}

type reclaudeDevice struct {
	SK string `json:"sk"`
}

func reclaudeHome() string {
	return expandHome("~/.reclaude")
}

// loadReclaudeToken reads only the gateway token issued by ReClaude itself.
func loadReclaudeToken() (string, error) {
	devicePath := filepath.Join(reclaudeHome(), "device.json")
	raw, err := os.ReadFile(devicePath)
	if err != nil {
		return "", fmt.Errorf("read %s failed: %w", devicePath, err)
	}
	var d reclaudeDevice
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", fmt.Errorf("parse reclaude device.json failed: %w", err)
	}
	if d.SK == "" {
		return "", fmt.Errorf("reclaude device.json missing sk")
	}
	return d.SK, nil
}

func fetchReclaude(ctx context.Context, client *http.Client, endpoint string, debug bool) (Provider, error) {
	token, err := loadReclaudeToken()
	if err != nil {
		return failedProvider(), err
	}
	return fetchClaudeToken(ctx, client, endpoint, debug, token)
}

func loadReclaudeState() (reclaudeState, error) {
	path := filepath.Join(reclaudeHome(), "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return reclaudeState{}, fmt.Errorf("read %s failed: %w", path, err)
	}
	var s reclaudeState
	if err := json.Unmarshal(raw, &s); err != nil {
		return reclaudeState{}, fmt.Errorf("parse reclaude state.json failed: %w", err)
	}
	if s.Daemon.Port <= 0 {
		s.Daemon.Port = s.LastPort
	}
	if s.Daemon.Port <= 0 {
		return reclaudeState{}, fmt.Errorf("reclaude daemon port is missing")
	}
	if s.Daemon.Running != nil && !*s.Daemon.Running {
		return reclaudeState{}, fmt.Errorf("reclaude daemon is not running (run reclaude first)")
	}
	return s, nil
}

// newReclaudeHTTPClient returns a client that sends HTTPS requests through
// ReClaude's local proxy and trusts only its local CA in addition to system
// roots. The proxy is deliberately discovered from state.json because its
// port can change whenever the daemon restarts.
func newReclaudeHTTPClient() (*http.Client, error) {
	s, err := loadReclaudeState()
	if err != nil {
		return nil, err
	}

	caPath := filepath.Join(reclaudeHome(), "ca.pem")
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read reclaude CA %s failed: %w", caPath, err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("parse reclaude CA %s failed", caPath)
	}

	proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Daemon.Port))}
	transport := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: transport}, nil
}
