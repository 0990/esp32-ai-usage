// esp32-ai-usage host：把 Claude / Codex 的 5小时 与 1周 剩余额度，
// 通过 USB 串口推送给 ESP32 + SSD1306 OLED 显示。
//
// 取数方式：读取本机已登录的 OAuth token（~/.codex/auth.json、~/.claude/.credentials.json），
// 调用各客户端内部用的同一个 usage API（不跑模型、不消耗额度）。
//
// 运行方式：直接双击/运行可执行文件，打开 Fyne 桌面界面。后台引擎按 interval
// 自动拉取并下发串口；界面里可切换显示对象、查看日志、最小化到托盘、发送测试 JSON。
// 配置文件默认 config.json，也可作为第一个参数传入：esp32-ai-usage.exe my.json
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config 来自 config.json，所有字段都有默认值。
type Config struct {
	Port            string `json:"port"`             // "AUTO" 或具体串口名（COM5）
	Baud            int    `json:"baud"`             // 默认 115200
	IntervalSeconds int    `json:"interval_seconds"` // 取数成功后的正常轮询间隔，默认 300
	// RetryIntervalSeconds 是取数失败时的重试间隔，默认 60。
	// 启动先立即取一次：失败按此间隔重试，首次成功后改用 IntervalSeconds。
	RetryIntervalSeconds int    `json:"retry_interval_seconds"`
	// PageIntervalSeconds 是 both 模式下轮播翻页的间隔（秒），默认 4。
	PageIntervalSeconds int    `json:"page_interval_seconds"`
	Provider            string `json:"provider"`        // "both"/"codex"/"claude"：拉取并显示哪个
	Proxy                string `json:"proxy"`           // 显式代理；留空则用 HTTPS_PROXY 环境变量
	ClaudeEndPoint       string `json:"claude_endpoint"` // 留空则用 ANTHROPIC_BASE_URL 或默认
	CodexEndpoint        string `json:"codex_endpoint"`  // 官方订阅的 wham/usage，留空用默认
	Autostart            bool   `json:"autostart"`       // 开机启动，默认 true

	// Codex 取数模式：官方订阅取百分比额度，中转站取余额（relay）。
	CodexMode string `json:"codex_mode"` // ""/auto（看 auth.json）| "official" | "relay"

	// CodexRelayTypes 是「中转站主机名 → 类型」映射（如 {"goai.im":"sub2api"}）。
	// base_url 始终读 ~/.codex/config.toml；按其主机名查此表决定类型，未命中则自动识别。
	CodexRelayTypes map[string]string `json:"codex_relay_types"`
}

func defaultConfig() Config {
	return Config{Port: "AUTO", Baud: 115200, IntervalSeconds: 300, RetryIntervalSeconds: 60, PageIntervalSeconds: 4, Provider: "both", Autostart: true}
}

// defaultConfigPath 返回固定的配置路径 ~/.esp32-ai-usage/config.json。
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.json"
	}
	return filepath.Join(home, ".esp32-ai-usage", "config.json")
}

// normalizeProvider 把配置值规范化为 "both"/"codex"/"claude"，非法值回退 both。
func normalizeProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	default:
		return "both"
	}
}

func loadConfig(path string) Config {
	cfg := defaultConfig()
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg // 没有配置文件就全用默认
	}
	_ = json.Unmarshal(raw, &cfg)
	if cfg.Baud == 0 {
		cfg.Baud = 115200
	}
	if cfg.IntervalSeconds == 0 {
		cfg.IntervalSeconds = 300
	}
	if cfg.RetryIntervalSeconds == 0 {
		cfg.RetryIntervalSeconds = 60
	}
	if cfg.PageIntervalSeconds == 0 {
		cfg.PageIntervalSeconds = 4
	}
	if cfg.Port == "" {
		cfg.Port = "AUTO"
	}
	cfg.Provider = normalizeProvider(cfg.Provider)
	return cfg
}

// saveConfig 把当前配置写回磁盘（缩进 JSON），必要时创建上级目录。
func saveConfig(path string, cfg Config) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func main() {
	initFileLog()            // 尽早开日志文件，崩溃也有据可查
	defer recoverLog("main") // 兜底：主 goroutine（含 UI 回调）panic 时落盘堆栈

	cfgPath := defaultConfigPath()
	if len(os.Args) > 1 && os.Args[1] != "" {
		cfgPath = os.Args[1] // 可选：自定义配置路径
	}

	// 单实例：已有实例在运行则唤起它的窗口并退出，避免重复开窗。
	ln, primary := acquireSingleInstance()
	if !primary {
		return
	}
	RunGUI(loadConfig(cfgPath), cfgPath, ln)
}

// newHTTPClient 构造带超时的 client；proxy 非空则显式走该代理，否则用环境变量。
func newHTTPClient(proxy string) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if proxy != "" {
		if u, err := parseProxy(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: tr}
}
