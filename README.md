# esp32-ai-credits

把 **Claude** 和 **Codex** 的「5 小时」「1 周」剩余额度，实时显示在一块通过 USB 连到电脑的
**ESP32 + SSD1306 OLED** 上，做成桌面物理「额度表」。

```
[chatgpt.com/.../wham/usage]    \
                                 >-- HTTPS --> [PC: Go 程序 host] --USB串口--> [ESP32-C3/S3] --I2C--> [SSD1306]
[<claude base>/api/oauth/usage] /        ^
                                  读 ~/.codex/auth.json + ~/.claude/.credentials.json
```

- **电脑端 Go 程序**负责联网取数：读取本机已登录的 OAuth token，调用各客户端**内部用的同一个 usage API**
  （不跑模型、不消耗额度），把结果整理成一行 JSON 通过串口发给 ESP32。
- **ESP32** 只读串口、解析、翻页显示，不联网。
- OLED 每约 4 秒在 **CLAUDE 页 / CODEX 页**之间自动翻页，每页两行（5h / 1w）：大字百分比 + 进度条 + 重置时间。

---

## 目录结构

```
esp32-ai-credits/
├─ host/                     # 电脑端 Go 程序
│  ├─ main.go  common.go  codex.go  claude.go  serialport.go  util.go
│  ├─ go.mod  go.sum
│  └─ config.example.json    # 复制为 config.json 后按需改
└─ firmware/                 # ESP32 固件（PlatformIO + Arduino）
   ├─ platformio.ini
   └─ src/main.cpp
```

---

## 一、硬件接线

| OLED (SSD1306 I2C) | ESP32-C3 / S3 |
|---|---|
| VCC | 3V3 |
| GND | GND |
| SDA | GPIO8（`PIN_SDA`） |
| SCL | GPIO9（`PIN_SCL`） |

- 引脚在 `firmware/platformio.ini` 的 `build_flags` 里用 `-DPIN_SDA` / `-DPIN_SCL` 定义，按实际接线改。
- OLED I2C 地址默认 `0x3C`。
- 电脑 ↔ ESP32 用**一根 USB 数据线**：C3/S3 原生 USB，既供电又当串口。

---

## 二、烧录固件

需要 [PlatformIO](https://platformio.org/)。

```bash
cd firmware
pio run -e esp32-c3 -t upload      # ESP32-C3
# 或：pio run -e esp32-s3 -t upload  # ESP32-S3
pio device monitor                  # 可选：看串口日志
```

烧录后 OLED 显示 `AI Credits / Waiting for PC...`，等电脑端程序连上。

---

## 三、运行电脑端程序

需要 Go 1.21+。

```bash
cd host
cp config.example.json config.json   # Windows: copy config.example.json config.json

# 1) 先核对取数是否正确（不碰串口）：
go run . --once --debug --no-serial

# 2) 单独测试固件显示（不联网，发假数据）：
go run . --simulate

# 3) 正常运行（自动找 ESP32 串口，每 60s 刷新）：
go run .
# 或编译后运行：go build -o ai-credits.exe . && ./ai-credits.exe
```

### 命令行参数

| 参数 | 说明 |
|---|---|
| `--once` | 取一次后退出 |
| `--debug` | 打印两边 usage API 的原始响应（核对字段用） |
| `--simulate` | 不联网，向串口发会变化的假数据 |
| `--no-serial` | 不打开串口，只把 JSON 打到 stdout |
| `--port COM5` | 指定串口（优先级高于 config） |
| `--interval 60` | 轮询间隔（秒） |
| `--config path` | 指定配置文件，默认 `config.json` |

### 配置文件 config.json

```json
{
  "port": "AUTO",              // "AUTO"=按 Espressif VID 自动找；或写 "COM5"
  "baud": 115200,
  "interval_seconds": 60,
  "proxy": "",                  // 需要代理访问 chatgpt.com 时填，如 http://127.0.0.1:7890
  "claude_base_url": "",        // 见下方 Claude 说明
  "codex_endpoint": "https://chatgpt.com/backend-api/wham/usage"
}
```

---

## 四、取数说明（重要）

> 注意：`codex status` / `claude usage` 这类「可脚本化打印文本」的命令在当前版本**并不存在**
> （只有 TUI 内的 `/status`、`/usage` 面板）。本项目改为读取本地 OAuth token、调用客户端内部的 usage API。

### Codex —— 已实测可用 ✅

- token：`~/.codex/auth.json` → `tokens.access_token` + `tokens.account_id`
- 接口：`GET https://chatgpt.com/backend-api/wham/usage`
- 字段：`rate_limit.primary_window`=5 小时、`secondary_window`=1 周，各含 `used_percent` + `reset_at`
- 剩余 = `100 - used_percent`

### Claude —— 需指向你的中转地址 ⚠️

本机的 Claude Code 用的是**中转 (relay) token**（前缀 `sk-reclaude-`），不是 Anthropic 官方 token，
所以**默认的 `api.anthropic.com` 会返回 401**。要取到数，必须把 base URL 指向你的中转服务：

- 方式 A：在 `config.json` 里设 `"claude_base_url": "https://你的中转域名"`
- 方式 B：设置环境变量 `ANTHROPIC_BASE_URL=https://你的中转域名` 后再运行

程序会请求 `<base>/api/oauth/usage`（头 `anthropic-beta: oauth-2025-04-20`），
解析 `five_hour` / `seven_day` 的 `utilization` + `resets_at`。
**前提是你的中转服务代理了这个 usage 接口**；若没代理，Claude 这一页会显示 `ERR`，但不影响 Codex 页正常显示。

> 用 `go run . --once --debug --no-serial` 看 Claude 的真实响应，据此微调
> `host/claude.go` 里的字段映射即可（数值是 0–100 还是 0–1、`resets_at` 是秒还是 ISO，代码已做兼容）。

---

## 五、串口协议

电脑端每轮发一行 JSON（`\n` 结尾，115200 baud）：

```json
{"claude":{"h5":{"left":94,"reset":"15:47"},"wk":{"left":38,"reset":"06-25"},"ok":true},
 "codex":{"h5":{"left":91,"reset":"20:48"},"wk":{"left":35,"reset":"06-25"},"ok":true}}
```

- `h5`=5 小时、`wk`=1 周；`left`=剩余百分比（取数失败为 `-1`），`reset`=重置时间短串。
- 某个 provider `ok:false` 时，OLED 对应页显示 `ERR`。
- ESP32 收到非法行直接忽略。

---

## 六、排错

| 现象 | 处理 |
|---|---|
| 找不到串口 / 没自动识别 | 在 `config.json` 写明 `"port": "COM5"`，或用 `--port COM5` |
| OLED 一直 Waiting | 确认电脑端程序在跑且串口选对；`pio device monitor` 看 ESP32 是否收到行 |
| OLED 不亮 | 检查 SDA/SCL 接线与 `PIN_SDA/PIN_SCL`、I2C 地址（多数模块 0x3C，少数 0x3D） |
| Codex 页 ERR | 确认 Codex 已登录（`~/.codex/auth.json` 存在）、本机能访问 chatgpt.com（必要时配 proxy） |
| Claude 页 ERR | 见上「Claude 需指向你的中转地址」 |
| 访问 chatgpt.com 超时 | 在 `config.json` 设 `proxy`，或设 `HTTPS_PROXY` 环境变量 |

---

## 安全提示

`~/.codex/auth.json`、`~/.claude/.credentials.json` 等同密码。本程序只读取、绝不打印 token，
也已在 `.gitignore` 忽略 `config.json` 与各类凭据文件。不要把它们提交到仓库或分享。
