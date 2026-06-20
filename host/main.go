// esp32-ai-credits host：把 Claude / Codex 的 5小时 与 1周 剩余额度，
// 通过 USB 串口推送给 ESP32 + SSD1306 OLED 显示。
//
// 取数方式：读取本机已登录的 OAuth token（~/.codex/auth.json、~/.claude/.credentials.json），
// 调用各客户端内部用的同一个 usage API（不跑模型、不消耗额度）。
//
// 常用命令：
//
//	go run . --once --debug --no-serial   # 取一次并打印两边原始响应（核对字段）
//	go run . --simulate                   # 不联网，发会变化的假数据联调固件
//	go run .                              # 正常运行（默认每 60s 刷新）
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.bug.st/serial"
)

// Config 来自 config.json，所有字段都有默认值。
type Config struct {
	Port            string `json:"port"`             // "AUTO" 或具体串口名（COM5）
	Baud            int    `json:"baud"`             // 默认 115200
	IntervalSeconds int    `json:"interval_seconds"` // 轮询间隔，默认 60
	Proxy           string `json:"proxy"`            // 显式代理；留空则用 HTTPS_PROXY 环境变量
	ClaudeBaseURL   string `json:"claude_base_url"`  // 留空则用 ANTHROPIC_BASE_URL 或默认
	CodexEndpoint   string `json:"codex_endpoint"`   // 留空则用默认 wham/usage
}

func defaultConfig() Config {
	return Config{Port: "AUTO", Baud: 115200, IntervalSeconds: 60}
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
		cfg.IntervalSeconds = 60
	}
	if cfg.Port == "" {
		cfg.Port = "AUTO"
	}
	return cfg
}

func main() {
	var (
		configPath = flag.String("config", "config.json", "配置文件路径")
		once       = flag.Bool("once", false, "取一次后退出")
		debug      = flag.Bool("debug", false, "打印 usage API 原始响应")
		simulate   = flag.Bool("simulate", false, "不联网，发假数据测试固件")
		noSerial   = flag.Bool("no-serial", false, "不打开串口，只打印 JSON")
		portFlag   = flag.String("port", "", "覆盖串口（如 COM5），优先级高于 config")
		interval   = flag.Int("interval", 0, "覆盖轮询间隔（秒）")
	)
	flag.Parse()

	cfg := loadConfig(*configPath)
	if *portFlag != "" {
		cfg.Port = *portFlag
	}
	if *interval > 0 {
		cfg.IntervalSeconds = *interval
	}

	// 串口（--once 且非 simulate 时默认不开，方便纯核对）。
	var port serial.Port
	wantSerial := !*noSerial && !(*once && !*simulate)
	if wantSerial {
		name, err := findPort(cfg.Port)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			if !*simulate {
				os.Exit(2)
			}
		} else if p, err := openSerial(name, cfg.Baud); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			if !*simulate {
				os.Exit(2)
			}
		} else {
			port = p
			defer port.Close()
			fmt.Fprintf(os.Stderr, "[info] 已连接串口 %s @ %d\n", name, cfg.Baud)
			time.Sleep(2 * time.Second) // 等 ESP32 复位就绪
		}
	}

	if *simulate {
		runSimulate(port, cfg.IntervalSeconds)
		return
	}

	client := newHTTPClient(cfg.Proxy)
	for {
		payload := Payload{
			Claude: fetchClaude(client, cfg.ClaudeBaseURL, *debug),
			Codex:  fetchCodex(client, cfg.CodexEndpoint, *debug),
		}
		send(port, payload)
		if *once {
			return
		}
		time.Sleep(time.Duration(cfg.IntervalSeconds) * time.Second)
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
	return &http.Client{Timeout: 20 * time.Second, Transport: tr}
}

// send 把 payload 序列化为一行 JSON，写串口并回显到 stdout。
func send(port serial.Port, payload Payload) {
	line, _ := json.Marshal(payload)
	line = append(line, '\n')
	if port != nil {
		if _, err := port.Write(line); err != nil {
			fmt.Fprintf(os.Stderr, "[error] 写串口失败: %v\n", err)
		}
	}
	os.Stdout.Write(line)
}

// runSimulate 不联网，循环发会变化的假数据，用于单独验证固件显示。
func runSimulate(port serial.Port, intervalSec int) {
	steps := []struct{ ch5, cwk, xh5, xwk int }{
		{94, 38, 88, 60}, {72, 38, 60, 55}, {50, 21, 33, 40}, {12, 9, 100, 100},
	}
	interval := time.Duration(min(intervalSec, 3)) * time.Second
	i := 0
	for {
		s := steps[i%len(steps)]
		send(port, Payload{
			Claude: Provider{H5: Window{s.ch5, "15:47"}, Wk: Window{s.cwk, "06-25"}, OK: true},
			Codex:  Provider{H5: Window{s.xh5, "12:10"}, Wk: Window{s.xwk, "06-22"}, OK: true},
		})
		i++
		time.Sleep(interval)
	}
}
