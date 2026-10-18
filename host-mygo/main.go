// esp32-ai-usage host：把 Claude / Codex 的 5小时 与 1周 剩余额度，
// 通过 USB 串口推送给 ESP32 + SSD1306 OLED 显示。
//
// 取数方式：读取本机已登录的 OAuth token（~/.codex/auth.json、~/.claude/.credentials.json），
// 调用各客户端内部用的同一个 usage API（不跑模型、不消耗额度）。
//
// 运行方式：直接双击/运行可执行文件，打开 MyGo 原生桌面界面。后台引擎按 interval
// 自动拉取并下发串口；界面里可切换监控对象、查看日志、最小化到托盘、发送测试 JSON。
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
	OLEDBrightness  int    `json:"oled_brightness"`  // 0–255，默认 143（SSD1306 原始亮度）
	Port            string `json:"port"`             // "AUTO" 或具体串口名（COM5）
	Baud            int    `json:"baud"`             // 默认 115200
	IntervalSeconds int    `json:"interval_seconds"` // 取数成功后的正常轮询间隔，默认 300
	// RetryIntervalSeconds 是取数失败时的重试间隔，默认 60。
	// 启动先立即取一次：失败按此间隔重试，首次成功后改用 IntervalSeconds。
	RetryIntervalSeconds int `json:"retry_interval_seconds"`
	// PageIntervalSeconds 是 all 模式下轮播翻页的间隔（秒），默认 4。
	PageIntervalSeconds int    `json:"page_interval_seconds"`
	Provider            string `json:"provider"`        // 监控对象："all" 或逗号分隔子集（如 "codex,reclaude,kimi"），默认 codex；旧值 both 等同 all
	Proxy               string `json:"proxy"`           // 显式代理；留空则用 HTTPS_PROXY 环境变量
	ClaudeEndPoint      string `json:"claude_endpoint"` // 留空则用 ANTHROPIC_BASE_URL 或默认
	CodexEndpoint       string `json:"codex_endpoint"`  // 官方订阅的 wham/usage，留空用默认
	KimiEndpoint        string `json:"kimi_endpoint"`   // Kimi 订阅的 /coding/v1/usages，留空用默认
	Autostart           bool   `json:"autostart"`       // 开机启动，默认 false（需用户在设置里手动开启）
	DesktopOverlay      bool   `json:"desktop_overlay"` // Windows 右上角额度悬浮窗，默认关闭
	OverlayScale        int    `json:"overlay_scale"`   // 悬浮窗大小百分比，默认 100
	OverlayLayer        string `json:"overlay_layer"`   // top（置顶）或 bottom（置底）
	OverlayStyle        string `json:"overlay_style"`   // paper / obsidian / strip / horizontal（默认）/ vertical
	OverlayWidth        int    `json:"overlay_width"`
	OverlayHeight       int    `json:"overlay_height"`
	OverlayOpacity      int    `json:"overlay_opacity"` // 整体不透明度，40–100，默认 100
	OverlayX            int32  `json:"overlay_x"`       // 屏幕物理像素，可为负（副屏）
	OverlayY            int32  `json:"overlay_y"`
	OverlayPositionSet  bool   `json:"overlay_position_set"`

	CustomObjects []CustomObject `json:"custom_objects,omitempty"`
}

func defaultConfig() Config {
	d := overlayDesignFor(defaultOverlayStyle)
	return Config{OLEDBrightness: defaultOLEDBrightness, Port: "AUTO", Baud: 115200, IntervalSeconds: 300, RetryIntervalSeconds: 60, PageIntervalSeconds: 4, Provider: "codex", Autostart: false, OverlayScale: 100, OverlayLayer: "top", OverlayStyle: defaultOverlayStyle, OverlayWidth: int(d.width), OverlayHeight: int(d.height), OverlayOpacity: 100}
}

// defaultConfigPath 返回独立配置路径 ~/.esp32-ai-usage-mygo/config.json。
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "config.json"
	}
	return filepath.Join(home, ".esp32-ai-usage-mygo", "config.json")
}

// providerOrder 是监控对象的固定顺序（界面勾选框与存储串都按此序）。
var providerOrder = []string{"codex", "claude", "reclaude", "kimi"}

// parseProviders 把配置值解析成监控对象集合：支持逗号分隔多选（如 "codex,reclaude,kimi"）；
// "all"/"both" 等同全选；"none" 不选内置对象；空或全部非法时回退默认 {codex}。
func parseProviders(s string) map[string]bool {
	set := map[string]bool{}
	if strings.TrimSpace(s) == "none" {
		return set
	}
	for _, tok := range strings.Split(s, ",") {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "all", "both":
			for _, p := range providerOrder {
				set[p] = true
			}
		case "codex", "claude", "reclaude", "kimi":
			set[strings.ToLower(strings.TrimSpace(tok))] = true
		}
	}
	if len(set) == 0 {
		set["codex"] = true
	}
	return set
}

// joinProviders 把集合转成规范存储串：全选归一成 "all"，否则按 providerOrder 逗号连接。
func joinProviders(set map[string]bool) string {
	if len(set) == len(providerOrder) {
		return "all"
	}
	var out []string
	for _, p := range providerOrder {
		if set[p] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}

// normalizeProvider 把配置值规范化（全选 → "all"，多选排序去重，非法回退默认）。
func normalizeProvider(s string) string {
	return joinProviders(parseProviders(s))
}

// providerList 把配置值解析成有序列表（供界面勾选框 SetSelected）。
func providerList(s string) []string {
	set := parseProviders(s)
	var out []string
	for _, p := range providerOrder {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// wants 报告某个监控对象是否被配置选中。
func (c Config) wants(name string) bool {
	return parseProviders(c.Provider)[name]
}

func loadConfig(path string) Config {
	cfg := defaultConfig()
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg // 没有配置文件就全用默认
	}
	_ = json.Unmarshal(raw, &cfg)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		if _, exists := fields["overlay_style"]; !exists || cfg.OverlayStyle == "nano" {
			// 无样式的旧配置和已删除的口袋数字使用新默认，保留位置、层级与监控配置。
			cfg.OverlayStyle = defaultOverlayStyle
			cfg.OverlayScale = 100
			cfg.OverlayWidth = 0
			cfg.OverlayHeight = 0
			if !exists {
				cfg.OverlayOpacity = 100
			}
		} else {
			// 显式选择旧样式但省略尺寸时，使用该样式的默认尺寸。
			if _, exists := fields["overlay_width"]; !exists {
				cfg.OverlayWidth = 0
			}
			if _, exists := fields["overlay_height"]; !exists {
				cfg.OverlayHeight = 0
			}
		}
	}
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
	cfg.OLEDBrightness = clampOLEDBrightness(cfg.OLEDBrightness)
	cfg.OverlayScale, cfg.OverlayLayer = normalizeOverlayAppearance(cfg.OverlayScale, cfg.OverlayLayer)
	cfg.OverlayStyle = normalizeOverlayStyle(cfg.OverlayStyle)
	cfg.OverlayWidth, cfg.OverlayHeight, cfg.OverlayOpacity = normalizeOverlaySize(cfg.OverlayStyle, cfg.OverlayWidth, cfg.OverlayHeight, cfg.OverlayOpacity)
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

	cfgPath, preview, startHidden := parseStartupArgs(os.Args[1:])
	if cfgPath == defaultConfigPath() && !preview {
		importLegacyConfig(cfgPath)
	}

	cfg := loadConfig(cfgPath)
	// 每次正常运行都刷新已启用的自启项，迁移旧命令并跟随 exe 改名/移动。
	// 在单实例检测之前执行，新位置的程序即使只唤起已有实例也能修复路径。
	if !preview && cfg.Autostart {
		if err := setAutostart(true, cfgPath); err != nil {
			writeLogLine("[WARN][autostart] 更新开机启动失败: " + err.Error())
		}
	}
	// 手动启动唤起已有窗口；自启只检测实例，不打扰当前窗口状态。
	ln, primary := acquireSingleInstance(!startHidden)
	if !primary {
		return
	}
	if ln != nil {
		defer ln.Close()
	}
	RunGUI(cfg, cfgPath, ln, preview, startHidden)
}

func parseStartupArgs(args []string) (cfgPath string, preview, startHidden bool) {
	cfgPath = defaultConfigPath()
	for _, arg := range args {
		switch arg {
		case "--preview":
			preview = true
		case "--autostart":
			startHidden = true
		case "":
		default:
			cfgPath = arg
		}
	}
	return
}

// 独立配置只在首次启动导入，后续编辑不影响 Fyne 版本。
func importLegacyConfig(path string) {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	legacy := filepath.Join(home, ".esp32-ai-usage", "config.json")
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	cfg := loadConfig(legacy)
	cfg.Autostart = false
	if err := saveConfig(path, cfg); err != nil {
		writeLogLine("[WARN][config] 导入原版设置失败: " + err.Error())
	}
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
