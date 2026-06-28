# esp32-ai-usage

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
- **ESP32** 只读串口、解析、显示收到的「当前页」，不联网、不含切换逻辑。
- 翻页由电脑端主动驱动：约每 4 秒在 **CLAUDE 页 / CODEX（或中转站）页**之间切换。百分比页两行（5h / 1w）：大字百分比 + 进度条 + 重置时间。

---

## 目录结构

```
esp32-ai-usage/
├─ host/                     # 电脑端 Go 程序（Fyne 桌面界面）
│  ├─ main.go                # 配置加载/保存、入口
│  ├─ engine.go              # 后台引擎：取数循环 + 单点串口写入
│  ├─ gui.go                 # Fyne 界面：三个 Tab（测试命令/设置/活动日志）+ 托盘
│  ├─ autostart_windows.go   # 开机启动（写 HKCU Run 注册表项）
│  ├─ autostart_other.go     # 非 Windows 的空实现
│  ├─ common.go  codex.go  claude.go  serialport.go  util.go
│  ├─ icon.png               # 窗口 / 托盘图标
│  ├─ go.mod  go.sum  build.bat
│  └─ config.example.json    # 仅作字段示例；实际配置在 ~/.esp32-ai-usage/config.json
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

电脑端是一个 **Fyne 桌面程序**：双击运行后弹出窗口，后台自动按间隔取数并下发串口。

**构建前提（重要）**：Fyne 依赖 OpenGL，需要 **Go 1.21+** 与 **CGO + C 编译器**。
Windows 上装 **MinGW-w64 gcc** 并确保 `gcc` 在 PATH 上（如 `winget install BrechtSanders.WinLibs.POSIX.UCRT`）。

```bash
cd host

# 构建（Windows）：会生成 bin/esp32-ai-usage.exe（-H=windowsgui 去掉黑窗口）
build.bat
# 或手动：set CGO_ENABLED=1 && go build -ldflags "-H=windowsgui" -o bin/esp32-ai-usage.exe .

# 直接跑（开发期）：
go run .
```

首次运行会自动创建配置文件 **`~/.esp32-ai-usage/config.json`** 并默认开启「开机启动」。
（也可把配置文件路径作为第一个参数传入：`esp32-ai-usage.exe D:\my-config.json`。）

### 界面（三个 Tab）

- **测试命令**：粘贴一行 Frame JSON（一行=一屏），点「发送到 ESP32」把它发到串口，用于单独验证 OLED 显示（非法 JSON 会弹错误框）。
- **设置**：可编辑全部配置项（串口、波特率、刷新间隔、显示对象、代理、Claude/Codex 端点、Codex 模式、中转站类型映射）+「开机启动」开关；点**保存**写入 `~/.esp32-ai-usage/config.json` 并即时生效（改了串口/波特率会自动重连）。
- **活动日志**：实时显示每轮取数结果（5h / 1周 剩余百分比与复位时间）或错误，保留最近 500 行，可一键清空。
- 窗口底部常驻**串口状态**；点窗口关闭按钮 = 收进**系统托盘**（不退出），托盘菜单可「显示/隐藏窗口」「退出」。

### 配置文件（`~/.esp32-ai-usage/config.json`）

由「设置」页保存生成，字段如下（`config.example.json` 仅作示例参考）：

```json
{
  "port": "AUTO",              // "AUTO"=按 Espressif VID 自动找；或写 "COM5"
  "baud": 115200,
  "interval_seconds": 300,        // 取数成功后的正常间隔（秒）
  "retry_interval_seconds": 60,   // 取数失败时的重试间隔（秒）
  "provider": "both",           // "both"=两个都拉都显示并翻页；"codex"/"claude"=只拉只显示这一个
  "proxy": "",                  // 需要代理访问 chatgpt.com 时填，如 http://127.0.0.1:7890
  "claude_endpoint": "",        // 见下方 Claude 说明（留空走 ANTHROPIC_BASE_URL）
  "codex_endpoint": "https://chatgpt.com/backend-api/wham/usage",  // 官方订阅取额度用
  "codex_mode": "",             // ""=自动识别 | "official"=官方订阅 | "relay"=中转站
  "codex_relay_types": {        // 中转站「主机名→类型」映射；不在表内的主机名自动识别
    "goai.im": "sub2api",
    "muyuan.do": "new-api"
  },
  "autostart": true             // 开机启动（写当前用户的 Run 注册表项，仅 Windows）
}
```

- `provider` 设为 `codex` 时：只请求 Codex、OLED 也只显示 Codex（中转站则显示中转页）；`claude` 同理。
- `both`（默认）：都请求，OLED 每约 4 秒自动翻页（Claude → Codex/中转站）。**翻页由电脑端主动驱动**（固件只显示收到的当前页）。
- 取数间隔自适应：**程序启动立即取一次**；失败按 `retry_interval_seconds`（默认 60s）重试，**首次成功后改用** `interval_seconds`（默认 300s）。之后再失败会自动回落到重试间隔，恢复成功后再回到正常间隔。
- `codex_relay_types`：中转站类型映射，键是 base_url 的主机名（子串即可命中，如 `goai.im` 命中 `api.goai.im`），值为 `sub2api` / `new-api`；不配置则自动识别。base_url 始终来自 `~/.codex/config.toml`。
- 程序会**自动监听** `~/.codex`、`~/.claude` 凭据/配置变动（如切换中转站、重新登录），一旦变化立即用新配置刷新显示，无需手动重启。
- 重复双击程序不会再开新窗口：已有实例会被唤起到前台（单实例）。
- 串口未连接时界面仍可正常使用（底部状态显示「未连接」），插上 ESP32 后在「设置」点保存（会触发重连）或重启程序即可。

---

## 四、取数说明（重要）

> 注意：`codex status` / `claude usage` 这类「可脚本化打印文本」的命令在当前版本**并不存在**
> （只有 TUI 内的 `/status`、`/usage` 面板）。本项目改为读取本地 OAuth token、调用客户端内部的 usage API。

### Codex —— 自动区分「官方订阅」和「中转站」

程序读 `~/.codex/auth.json` 判断（也可用 `codex_mode` 强制）：

**A. 官方订阅**（`auth.json` 有 `tokens.access_token` + `tokens.account_id`）✅ 已实测
- 接口：`GET https://chatgpt.com/backend-api/wham/usage`
- 字段：`rate_limit.primary_window`=5 小时、`secondary_window`=1 周，各含 `used_percent` + `reset_at`；剩余 = `100 - used_percent`
- OLED 显示 **Codex 页**（5h / 1周 百分比）。

**B. 中转站**（`auth.json` 只有 `OPENAI_API_KEY`，即 `sk-...`）
- 用这个 key 调中转站接口，取「已用 + 总余额」，OLED 显示 **中转站页**（金额）。
- 中转站地址来自 `~/.codex/config.toml` 的 `model_providers.<model_provider>.base_url`。
- 类型由 `codex_relay_types`（主机名→类型映射）决定，未命中则自动先试 sub2api 再 new-api：
  - **sub2api**：`GET <base>/usage`（Bearer key），`remaining`=余额、`usage.today.actual_cost`=**今日用量**，单位 USD→`$`。OLED 标签为 `Today` / `Left`。
  - **new-api / one-api**：OpenAI billing 仿真，`GET <base>/dashboard/billing/subscription`(总额 `hard_limit_usd`) 与 `.../usage`(累计 `total_usage`，美分)；余额 = 总额 − 累计已用，展示**累计已用**。OLED 标签为 `Used` / `Left`。
    > new-api 没有可用 API key 拉取的「按时间段聚合」接口（`/api/log/.../stat` 需网页登录态），故展示累计已用而非今日用量。已对真实 muyuan.do 实测：已用 `$0.42` / 余额 `$879.58`。
- 实现见 [host/codexrelay.go](host/codexrelay.go)。

### Claude —— 需指向你的中转地址 ⚠️

本机的 Claude Code 用的是**中转 (relay) token**（前缀 `sk-reclaude-`），不是 Anthropic 官方 token，
所以**默认的 `api.anthropic.com` 会返回 401**。要取到数，必须把 base URL 指向你的中转服务：

- 方式 A：在 `config.json` 里设 `"claude_endpoint": "https://你的中转域名"`（填基址即可，程序自动补 `/api/oauth/usage`；也可直接填完整 URL）
- 方式 B：设置环境变量 `ANTHROPIC_BASE_URL=https://你的中转域名` 后再运行

程序会请求 `<base>/api/oauth/usage`（头 `anthropic-beta: oauth-2025-04-20`），
解析 `five_hour` / `seven_day` 的 `utilization` + `resets_at`。
**前提是你的中转服务代理了这个 usage 接口**；若没代理，Claude 这一页会显示 `ERR`，但不影响 Codex 页正常显示。

> 取数失败时界面「活动日志」会显示对应 provider 的错误（如 `HTTP 401`），据此判断是否中转/代理配置问题；
> 若需微调字段映射见 `host/claude.go`（数值是 0–100 还是 0–1、`resets_at` 是秒还是 ISO，代码已做兼容）。

---

## 五、串口协议

**固件只渲染、不含切换逻辑**：电脑端每发一行 JSON 就是「一整屏」(Frame)，翻页/选哪页全由电脑端决定。
电脑端每约 4 秒发当前页（`\n` 结尾，115200 baud）：

```json
{"ic":"claude","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30"}
{"ic":"$","k":"money","l1":"Today","v1":"$0.4","l2":"Left","v2":"$879.6"}
```

- `k`=`pct`：百分比页（claude/codex）。`p1/p2`=剩余百分比（`-1`→显示 `ERR`）、`s1/s2`=重置时间。
- `k`=`money`：中转站金额页。`v1/v2`=已格式化金额（含货币符号），`ic`=货币符号（画在硬币上）。sub2api 标签 `Today`、new-api 标签 `Used`。
- ESP32 收到一行就整屏刷新；收到非法行直接忽略。超过 90 秒没新行会本地标 `old`。
- 完整协议见 [PROTOCOL.md](PROTOCOL.md)。

---

## 六、排错

| 现象 | 处理 |
|---|---|
| 找不到串口 / 没自动识别 | 在 `config.json` 写明 `"port": "COM5"`（界面状态栏会显示「未连接」） |
| OLED 一直 Waiting | 确认电脑端程序在跑且串口选对；`pio device monitor` 看 ESP32 是否收到行 |
| OLED 不亮 | 检查 SDA/SCL 接线与 `PIN_SDA/PIN_SCL`、I2C 地址（多数模块 0x3C，少数 0x3D） |
| Codex 页 ERR | 确认 Codex 已登录（`~/.codex/auth.json` 存在）、本机能访问 chatgpt.com（必要时配 proxy） |
| 中转站页取数失败 | 看「活动日志」的 `relay` 错误：`config.toml` 的 `base_url` 是否正确、`codex_relay_types` 里类型选对（sub2api/new-api）、key 是否有效 |
| Claude 页 ERR | 见上「Claude 需指向你的中转地址」 |
| 访问 chatgpt.com 超时 | 在 `config.json` 设 `proxy`，或设 `HTTPS_PROXY` 环境变量 |

---

## 安全提示

`~/.codex/auth.json`、`~/.claude/.credentials.json` 等同密码。本程序只读取、绝不打印 token，
也已在 `.gitignore` 忽略 `config.json` 与各类凭据文件。不要把它们提交到仓库或分享。
