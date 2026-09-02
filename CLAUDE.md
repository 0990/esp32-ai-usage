# CLAUDE.md

本文件指导 Claude Code 在本仓库工作。先读这里，再动手。

## 项目是什么

把 **Claude** 和 **Codex** 的「5 小时」「1 周」剩余额度，实时显示在一块通过 USB 连到电脑的
**ESP32 + SSD1306 OLED** 上（桌面物理「额度表」）。两部分组成：

- `host/`：电脑端 **Go** 程序。读本机已登录的 OAuth token，调用各客户端**内部用的同一个 usage API**
  取数（不跑模型、不消耗额度），整理成一行 JSON 通过 USB 串口发给 ESP32。
- `firmware/`：**ESP32-C3/S3** 固件（PlatformIO + Arduino + U8g2 + ArduinoJson）。只读串口、解析、显示，不联网。

```
[chatgpt.com/.../wham/usage]   \
                                >- HTTPS -> [host (Go)] -USB串口(NDJSON)-> [ESP32-C3/S3] -I2C-> [SSD1306]
[<claude relay>/api/oauth/usage]/     ^ 读 ~/.codex/auth.json + ~/.claude/.credentials.json
```

## 仓库结构

```
host/                     # Go 程序（Fyne 桌面 GUI，module: github.com/local/esp32-ai-usage-host）
  main.go                 # config 加载/保存(saveConfig)、固定路径 ~/.esp32-ai-usage/config.json、入口 RunGUI、newHTTPClient
  engine.go               # 后台引擎：取数循环 fetchLoop/fetchOnce(更新缓存)、显示循环 displayLoop(按页轮播下发)、单点写串口 serialWriterLoop、串口管理 serialManagerLoop(断线重连)、Reload()
  gui.go                  # Fyne 界面：三个 Tab（设置/测试命令/活动日志）+ 底部串口状态 + 系统托盘
  singleinstance.go       # 单实例：本地回环端口当锁兼 IPC；后启动实例唤起已有窗口后退出
  watcher.go              # fsnotify 监听 ~/.codex、~/.claude 变动 → 催 Reload() 立即刷新
  autostart_windows.go    # 开机启动：写 HKCU\...\Run 注册表项（golang.org/x/sys/windows/registry）
  autostart_other.go      # 非 Windows 的 setAutostart 空实现（//go:build !windows）
  common.go               # Window/Provider/Relay/Frame 类型 + pctFrame/moneyFrame 构造、时间格式化、百分比换算
  codex.go                # loadCodexAuth/loadCodexToken + fetchCodex（官方订阅）+ codexIsRelay 检测
  codexrelay.go           # 中转站余额（codex+claude 共用）：sub2api(/usage) / new-api(billing) 取数；claude 侧按 relayBaseCandidates 先试 <base>/v1 再试原始 base
  claude.go               # loadClaudeToken + fetchClaude（官方 OAuth）+ loadClaudeRelaySettings/claudeIsRelay（中转检测，env 取 ANTHROPIC_API_KEY/AUTH_TOKEN）
  ccswitch.go             # cc-switch 本地代理回溯：settings.json 的 base_url 指回环地址时，从其 SQLite 库读当前供应商的真实 base/key
  serialport.go           # desiredPort 选目标串口（Espressif VID 0x303A 优先，唯一 USB 串口兜底；COM1 类主板口不兜底）+ openSerial
  util.go                 # parseProxy / logErr / truncate（logErr/truncate 仍被 codex.go/claude.go 用）
  icon.png                # 窗口 / 托盘图标（gui.go 用 //go:embed 嵌入）
  config.example.json     # 仅字段示例；实际配置在 ~/.esp32-ai-usage/config.json
firmware/
  platformio.ini          # 默认 env=esp32-c3，另有 esp32-s3
  src/main.cpp            # 串口收行 + JSON 解析 + 渲染一帧（pct/money 两种页型；不含翻页逻辑）
README.md                 # 面向普通用户的开源首页（亮点/三步上手；技术细节看 PROTOCOL.md 和本文件）
docs/                     # 动手教程：材料表.md、硬件安装.md、软件使用.md（刷固件+exe 使用）+ images/
```

## 常用命令

```bash
# 电脑端（Go 1.21+；Fyne 需 CGO + MinGW gcc 在 PATH 上）
cd host
set CGO_ENABLED=1                                          # PowerShell: $env:CGO_ENABLED=1
go build -ldflags "-s -w -H=windowsgui" -o bin/esp32-ai-usage.exe .   # 或直接跑 build.bat（-s -w 瘦身约 43→24MB）
go vet ./...
go run .                               # 开发期直接跑（会弹界面）

# 固件（PlatformIO）
cd firmware
pio run -e esp32-c3                    # 仅编译
pio run -e esp32-c3 -t upload         # 编译并烧录（S3 用 -e esp32-s3）
pio device monitor                    # 看串口日志
```

入口是 GUI（无 CLI flag）。可选参数：第一个位置参数为配置文件路径（默认 `config.json`）。
排错改用界面里的「活动日志」；想单独验证固件显示，用界面的「发送自定义 Frame」发一帧。

## 串口协议（host → ESP32，一行 = 一整屏 Frame，以 `\n` 结尾，115200）

**固件只渲染，不含切换逻辑**：host 每发一行就是「一整屏」，翻页/选哪页/过期判定全在 host
（`engine.go` 的 `displayLoop`，约 4s 翻一页）。完整协议见 [PROTOCOL.md](PROTOCOL.md)。

```json
{"ic":"claude","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30"}
{"ic":"$","k":"money","l1":"Today","v1":"$1.2","l2":"Left","v2":"$880"}
```

- `k`=`pct`：百分比页（claude/codex）。`p1/p2`=剩余百分比(`-1`→`ERR`)，`s1/s2`=重置时间。
- `k`=`money`：中转站金额页。`v1/v2`=已格式化金额文本（含货币符号），`ic`=货币符号（画在硬币上）。
- 改协议时**两端要一起改**：`common.go` 的 `Frame`（+ `pctFrame`/`moneyFrame` 构造）与 `firmware/src/main.cpp` 的 `parseLine`/`drawPctFrame`/`drawMoneyFrame`。

## 取数关键事实（重新发现成本高，改前先看）

- **没有可脚本化的 `codex status` / `claude usage` 命令**——它们只是 TUI 内的 `/status`、`/usage` 面板。
  所以本项目走「读本地 token + 调内部 usage API」，**不要**去尝试解析 TUI 输出。
- **Codex 分两种**（`codexIsRelay` 看 auth.json 自动判定，可用 `codex_mode` 强制）：
  - **官方订阅 ✅**：`auth.json` 有 `tokens.access_token`+`account_id`；`GET https://chatgpt.com/backend-api/wham/usage`，
    头 `Authorization: Bearer`、`chatgpt-account-id`；响应 `rate_limit` 有 `primary_window`/`secondary_window` 两窗口，
    字段 `used_percent` + `reset_at`(unix 秒)/`reset_after_seconds` + `limit_window_seconds`(窗口时长)。下发 `codex`（百分比）。
    ⚠️ **不按 primary/secondary 位置判定 5h/周**（OpenAI 两个数据源命名相反，且部分账号已取消 5h 窗口，会把周额度塞进 5h 行）：
    `codex.go` 的 `codexClassify` 按 `limit_window_seconds` 判定——小=5h、大=周；仅一个窗口时以 21600s(6h) 为阈值；
    窗口缺失(零值)时该行经 `leftFromUsed(0)` 显示 100%。做法照搬参照工程 `D:\workspace\ai\sub2api-goai` 的 `Normalize()`。
  - **中转站**（`auth.json` 只有 `OPENAI_API_KEY` 即 `sk-`）：用该 key 调中转接口，取「已用 + 余额」转成 `money` 帧（见 PROTOCOL.md §4）。
    base_url 始终来自 `~/.codex/config.toml` 的 `model_providers.<model_provider>.base_url`（用 BurntSushi/toml 解析；不再有 base_url 覆盖项）。
    类型由 `codex_relay_types`（主机名→类型映射，如 `{"goai.im":"sub2api"}`）决定，按 base_url 主机名子串匹配；未命中则自动先试 sub2api 再 new-api。
    **sub2api 展示「今日用量」**（`Relay.Label="Today"`）：`GET <base>/usage`，`remaining`=余额、`usage.today.actual_cost`(兜底 `cost`)=今日用量，`unit` USD→`$`；
    **new-api 展示「累计已用」**（`Relay.Label="Used"`）：billing 仿真 `<base>/dashboard/billing/subscription`(`hard_limit_usd`)−`/usage`(`total_usage` 美分,累计)，余额=总额−累计。
    ⚠️ new-api **没有可用 API key 调的「按时间段聚合」接口**（`SumUsedQuota` 只挂在 `/api/log/self/stat`、`/api/log/stat`，需网页登录态/用户 access token；`sk-` key 只能调 billing、`/api/usage/token`、`/api/log/token`，全是累计/原始日志），故展示累计已用而非今日。源码在 `D:\workspace\ai\new-api`（`controller/billing.go`、`router/*`、`common.QuotaPerUnit=500000`）。
    已实测：goai.im=sub2api；muyuan.do=new-api（余额 880−累计=`$879.58`、已用 `$0.42`；该 key `unlimited_quota` 故 `/api/usage/token` 余额不可用，余额取 billing）。
- **Claude 两个分支**（`fetchOnce` 按 `claudeIsRelay()` 选）：
  - **中转站**：`~/.claude/settings.json` 的 env 有 `ANTHROPIC_BASE_URL` 且 `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` 任一非空 →
    `fetchClaudeRelay`（与 codex 共用 sub2api/new-api 逻辑，取「已用 + 余额」转 `money` 帧）。
    Claude 的 base_url 是 Anthropic 风格、通常不带 /v1，而余额接口挂在 /v1 根下，故按 `relayBaseCandidates` 先试 `<base>/v1` 再试原始 base。
    若 base_url 是本机回环代理（cc-switch 本地代理模式，token 为 `PROXY_xxx`），代理不转发余额接口（404），
    须由 `ccswitch.go` 从 `~/.cc-switch/cc-switch.db`（mattn/go-sqlite3；providers 表 `app_type='claude' AND is_current=1` 的 `settings_config.env`）回溯真实上游。
  - **官方/网关 token**（无上述 env）：token 在 `~/.claude/.credentials.json` → `claudeAiOauth.accessToken`；
    `GET <base>/api/oauth/usage`，头还要 `anthropic-beta: oauth-2025-04-20`、`anthropic-version: 2023-06-01`。
    响应 `five_hour`/`seven_day`，字段 `utilization` + `resets_at`。
    中转 token（`sk-reclaude-` 一类）默认 `api.anthropic.com` 会 401；
    必须把端点指向中转地址（config `claude_endpoint` 或环境变量 `ANTHROPIC_BASE_URL`），且该中转需代理了这个 usage 接口。
  - **ReClaude 包装器**（监控对象 `reclaude`）：先运行 `reclaude` 启动本机 daemon；host 只读取 `~/.reclaude/device.json` 的 `sk`，从 `~/.reclaude/state.json` 读取本地代理端口，信任 `~/.reclaude/ca.pem`，经本地代理请求 `https://api.anthropic.com/api/oauth/usage`，复用上述百分比解析。
- 数值格式可能因环境而异：`fetchClaude`/`fetchCodex` 的 `debug` 参数现固定传 `false`（旧 `--debug` 已随 CLI 移除）；
  排错看界面「活动日志」里的 `HTTP 4xx` 等错误，或临时把 `engine.go:fetchOnce` 里的 `false` 改成 `true` 打原始响应。
  `fetchCodex`/`fetchClaude` 均返回 `(Provider, error)`；某边失败时该 provider `ok:false`，不影响另一边。

## 约定与注意

- **凭据即密码**：`~/.codex/auth.json`、`~/.claude/.credentials.json`、`~/.esp32-ai-usage/config.json` 绝不打印 token、绝不入库。
  配置已移出仓库（放用户主目录），日志里如需展示请脱敏。
- **C3/S3 原生 USB-CDC**：一根 USB 线既供电又当串口；`platformio.ini` 用 `-DARDUINO_USB_CDC_ON_BOOT=1`。
- **OLED 引脚**：`platformio.ini` 的 `-DPIN_SDA/-DPIN_SCL`（默认 8/9），I2C 地址默认 `0x3C`。
- **改完必须验证**：host 改动跑 `set CGO_ENABLED=1 && go build -ldflags "-H=windowsgui" ./... && go vet ./...`；固件改动跑 `pio run -e esp32-c3`（已配置可用）。
- 配置键：`port baud interval_seconds retry_interval_seconds page_interval_seconds provider proxy claude_endpoint codex_endpoint codex_mode codex_relay_types autostart`（见 `config.example.json`；实际存 `~/.esp32-ai-usage/config.json`）。`page_interval_seconds` 是多选时轮播翻页间隔（默认 4，秒）。`provider` 是监控对象：`all` 或逗号分隔子集（如 `codex,kimi`，可选 codex/claude/kimi；默认 codex；旧值 both 归一成 all），`parseProviders`/`joinProviders`/`Config.wants` 负责解析与判断，设置页用横向 CheckGroup 多选（全勾存为 all，至少勾一个）。`codex_relay_types` 是 `{主机名:类型}` 映射（旧的 `codex_relay_type`/`codex_relay_base_url` 已移除）；设置页展示为两列列表（域名|类型下拉 auto/sub2api/new-api），域名自动检测（`detectRelayHosts`：codex 走 sk- key 中转时读 config.toml base_url；claude 走中转时读 settings.json env 的 ANTHROPIC_BASE_URL，回环代理经 cc-switch 回溯），已保存类型的旧域名换站后仍保留行，选 auto 即从映射删除；watcher 检测到凭据变动时经 `Engine.OnCredsChange`→`g.refreshRelayRows` 自动重建列表（合并未保存选择）。设置页顶部只放「立即刷新使用量」（调 `engine.Reload()`）、监控对象（provider）、开机启动、中转站类型，其余项折叠在「更多」下（普通按钮切换 Show/Hide，展开/收起时 `fitWindowHeight` 直接调窗口高度，设置页不用滚动容器），保存是独立按钮（不再用 Form.OnSubmit）。`codex_mode` 界面已无设置项，仅手改配置文件生效（保存时透传旧值）。`autostart` 默认 false，需用户手动勾选。
- 取数间隔自适应：`interval_seconds`（默认 300，成功后用）与 `retry_interval_seconds`（默认 60，失败时用）。启动立即取一次，`fetchOnce` 返回是否成功（任一 provider 取到数据即成功），`fetchLoop` 据此选下次间隔。

## 易踩的点（GUI 架构）

- **配置路径**：固定 `~/.esp32-ai-usage/config.json`（`defaultConfigPath`）；首次运行（文件不存在）才写默认配置并 `setAutostart(false)`（开机启动默认关）。`loadConfig` 先 `defaultConfig()` 再 `Unmarshal`（JSON 缺键时保持默认值，别改成先零值后 Unmarshal）。
- **保存即应用**：设置页保存走 `engine.UpdateConfig(cfg)`（整套配置入内存+重建 http client+写盘+催 reload）；`autostart` 另调 `setAutostart`；仅当 port/baud 变化才调 `engine.ReconnectSerial()` 重连。没有「逐字段即时生效」。
- **串口热插拔**：`serialManagerLoop` 每 3s 用 `desiredPort` 算目标串口并与当前口比对——当前口消失（拔掉）或出现更优目标就断开重选，未连接且目标存在就打开；写串口失败也会立即触发重查（覆盖同 COM 号快速拔插、句柄失效的情况）。AUTO 模式的「单口兜底」只认 USB 串口，绝不选主板自带 COM1（写它不报错、会一直占住）。
- **UI 线程**：引擎 goroutine 回写界面必须包 `fyne.Do(...)`（见 `gui.go` 的 `OnLog`/`OnStatus`）；按钮/表单回调本就在 UI 线程，调引擎方法即可。
- **串口写入单点化**：只有 `engine.go` 的 `serialWriterLoop` 执行 `port.Write`；显示循环与「发送测试 Frame」都往 `writeCh` 投递，勿在别处直接写串口。
- **取数/显示解耦**：`fetchOnce` 只更新缓存（`dataMu` 保护的 claude/codex/relay + 时间戳，成功才更新、失败保留上次好数据），不直接下发；下发由 `displayLoop` 按页轮播（间隔来自 `pageInterval()`←配置 `page_interval_seconds`，默认 4s；改「轮播间隔」保存后经 reload→refresh 即时生效）。取到新数据会 `signalRefresh()` 让显示循环立刻重画当前页。`buildFrames()` 据 provider+缓存组装当前页列表，首轮取数前返回 nil（固件停在等待屏）。
- **取数间隔自适应**：`fetchOnce` 返回 bool（任一 provider 成功即 true）；`fetchLoop` 失败用 `RetryIntervalSeconds`、成功用 `IntervalSeconds`。启动即取一次，因此「失败 60s 重试、首次成功后 300s」是 result-based 实现（后续再失败也会回落到重试间隔）。
- **provider 取数分支**：`fetchOnce`/`buildFrames` 用 `cfg.wants("xxx")` 判断（`provider` 支持逗号分隔多选，如 `codex,kimi`；`all`/旧值 both 由 `parseProviders` 归一成全选，默认 codex）；codex 走中转时设 `e.relay`，官方时清 `e.relay=nil`（避免残留中转页）；`buildFrames` 据 `e.relay!=nil` 决定 codex 槽位画 `money` 还是 `pct`。
- **单实例**：`main()` 先 `acquireSingleInstance()`（占本地回环端口）；占用失败说明已有实例，敲门让其 `w.Show()` 后本进程退出。
- **文件监听**：`watchCredentials` 用 fsnotify 看 `~/.codex`、`~/.claude` 目录，命中 `auth.json/config.toml/.credentials.json` 变动（debounce 800ms）就 `Reload()` 立即重取。
- **退出**：`fetchCodex/fetchClaude` 已绑 ctx，退出 `cancel()` 后 in-flight 请求立即中止，不会卡 `Stop()`。
