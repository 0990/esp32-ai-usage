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
  activity.go             # 「工作状态」：本地 HTTP(127.0.0.1:52736) 接收 Claude Code/Codex 的 hook 回调，按会话记状态、按 provider 聚合成 Frame.Act
  hooks.go                # 往 ~/.claude/settings.json(http hook) 与 ~/.codex/hooks.json(curl 命令 hook) 安装/移除指向 activityAddr 的钩子
  activity_test.go        # 状态机 / HTTP handler / hook 文件合并 / curl 命令端到端 的测试（go test ./...）
  watcher.go              # fsnotify 监听 ~/.codex、~/.claude 变动 → 催 Reload() 立即刷新
  autostart_windows.go    # 开机启动：写 HKCU\...\Run 注册表项（golang.org/x/sys/windows/registry）
  autostart_other.go      # 非 Windows 的 setAutostart 空实现（//go:build !windows）
  common.go               # Window/Provider/Relay/Frame 类型 + pctFrame/moneyFrame 构造、时间格式化、百分比换算
  codex.go                # loadCodexAuth/loadCodexToken + fetchCodex（官方订阅）
  codexrelay.go           # 自定义对象余额：sub2api(/usage) / newapi(billing) 取数，类型必须手动指定
  custom.go               # CustomObject 配置、输入验证、接口根地址补 /v1、按对象和凭据隔离缓存
  claude.go               # loadClaudeToken + fetchClaude（OAuth 百分比额度）
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
- `k`=`money`：中转站金额页。自定义对象 `v1`=余额（含货币符号）、`ic`=`sub2api`/`newapi`（S2/N 位图），OLED 只显示余额；旧两行金额帧仍兼容。
- `a`（可选）：该页 CLI 的工作状态 0 空闲/1 工作中/2 需要你（等授权）。固件 `drawActivity`：1 画图标下方跑动的三个点，2 画图标右上角脉动的「!」角标（`drawAttentionBadge`，实心/空心每 400ms 交替）；RGB 灯 1 蓝呼吸、2 紫闪。`a≠0` 时固件 150ms 重画一次驱动动画。
- 改协议时**两端要一起改**：`common.go` 的 `Frame`（+ `pctFrame`/`moneyFrame` 构造）与 `firmware/src/main.cpp` 的 `parseLine`/`drawPctFrame`/`drawMoneyFrame`。

## 取数关键事实（重新发现成本高，改前先看）

- **没有可脚本化的 `codex status` / `claude usage` 命令**——它们只是 TUI 内的 `/status`、`/usage` 面板。
  所以本项目走「读本地 token + 调内部 usage API」，**不要**去尝试解析 TUI 输出。
- **Codex 只查官方订阅额度**：
  - **官方订阅 ✅**：`auth.json` 有 `tokens.access_token`+`account_id`；`GET https://chatgpt.com/backend-api/wham/usage`，
    头 `Authorization: Bearer`、`chatgpt-account-id`；响应 `rate_limit` 有 `primary_window`/`secondary_window` 两窗口，
    字段 `used_percent` + `reset_at`(unix 秒)/`reset_after_seconds` + `limit_window_seconds`(窗口时长)。下发 `codex`（百分比）。
    ⚠️ **不按 primary/secondary 位置判定 5h/周**（OpenAI 两个数据源命名相反，且部分账号已取消 5h 窗口，会把周额度塞进 5h 行）：
    `codex.go` 的 `codexClassify` 按 `limit_window_seconds` 判定——小=5h、大=周；仅一个窗口时以 21600s(6h) 为阈值；
    窗口缺失(零值)时该行经 `leftFromUsed(0)` 显示 100%。做法照搬参照工程 `D:\workspace\ai\sub2api-goai` 的 `Normalize()`。
- **自定义中转站对象**：`Config.CustomObjects`，每个对象有 `id/name/type/base_url/api_key/enabled`。
    类型手动选择 `sub2api` 或 `newapi`；地址和 key 不从客户端文件、环境变量或 cc-switch 自动读取。
    站点根地址先尝试 `/v1`，再尝试原始根地址；始终使用指定类型，不尝试另一种类型。
    **sub2api 取数**（API 返回今日用量，OLED 仅展示余额）：`GET <base>/usage`，`remaining`=余额、`usage.today.actual_cost`(兜底 `cost`)=今日用量，`unit` USD→`$`；
    **new-api 取数**（累计已用用于计算余额，OLED 仅展示余额）：billing 仿真 `<base>/dashboard/billing/subscription`(`hard_limit_usd`)−`/usage`(`total_usage` 美分,累计)，余额=总额−累计。
    ⚠️ new-api **没有可用 API key 调的「按时间段聚合」接口**（`SumUsedQuota` 只挂在 `/api/log/self/stat`、`/api/log/stat`，需网页登录态/用户 access token；`sk-` key 只能调 billing、`/api/usage/token`、`/api/log/token`，全是累计/原始日志），故展示累计已用而非今日。源码在 `D:\workspace\ai\new-api`（`controller/billing.go`、`router/*`、`common.QuotaPerUnit=500000`）。
    已实测：goai.im=sub2api；muyuan.do=new-api（余额 880−累计=`$879.58`、已用 `$0.42`；该 key `unlimited_quota` 故 `/api/usage/token` 余额不可用，余额取 billing）。
- **Claude 百分比额度**：
  - **官方/网关 OAuth token**：token 在 `~/.claude/.credentials.json` → `claudeAiOauth.accessToken`；
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
- 配置键：`port baud oled_brightness interval_seconds retry_interval_seconds page_interval_seconds provider proxy claude_endpoint codex_endpoint kimi_endpoint custom_objects autostart`。旧 `codex_mode` / `codex_relay_types` 已移除，保存时不会保留。
  `provider` 控制内置对象（codex/claude/reclaude/kimi）；`all`/`both` 全选内置对象，`none` 不选内置对象。自定义对象分别用 `enabled` 控制。
  设置页的「监控对象」包含内置复选框、自定义对象列表和「新增自定义对象」按钮；添加/编辑弹窗手填名称、类型、Base URL、API key（密码框），删除和勾选后点设置页「保存」才生效，至少启用一个内置或自定义对象。
  缓存按 ID、类型、地址及 key 的 SHA256 区分；更换账号时不显示旧余额，失败保留最后成功值，缓存不含 key。独立自定义金额页不绑定 CLI 工作状态。

- 取数间隔自适应：`interval_seconds`（默认 300，成功后用）与 `retry_interval_seconds`（默认 60，失败时用）。启动立即取一次，`fetchOnce` 返回是否成功（任一 provider 取到数据即成功），`fetchLoop` 据此选下次间隔。

## 易踩的点（GUI 架构）

- **配置路径**：固定 `~/.esp32-ai-usage/config.json`（`defaultConfigPath`）；首次运行（文件不存在）才写默认配置并 `setAutostart(false)`（开机启动默认关）。`loadConfig` 先 `defaultConfig()` 再 `Unmarshal`（JSON 缺键时保持默认值，别改成先零值后 Unmarshal）。
- **保存即应用**：设置页保存走 `engine.UpdateConfig(cfg)`（先写盘，再更新内存和 HTTP client，催显示与取数刷新）；`autostart` 另调 `setAutostart`；仅当 port/baud 变化才调 `engine.ReconnectSerial()` 重连。没有「逐字段即时生效」。
- **OLED 亮度**：配置 `oled_brightness`（0–255，默认 143）在设置页提供滑块，保存后立即发送 `{"cmd":"display","brightness":值}`，串口打开/重连后也发送。普通 Frame 带可选 `brightness`；固件设置命令不改变当前画面、活动状态或过期计时，亮度变化时才写 NVS，断电重启恢复。
- **串口连上即重发**：`tryOpen` 成功后 `signalRefresh()`。启动时首轮取数/缓存装载往往早于串口就绪（打开后还等 2s），首帧会被丢弃；不重发的话要等下一次翻页定时器（轮播间隔可配到 60s）屏幕才离开 `Waiting for PC`。
- **串口热插拔**：`serialManagerLoop` 每 3s 用 `desiredPort` 算目标串口并与当前口比对——当前口消失（拔掉）或出现更优目标就断开重选，未连接且目标存在就打开；写串口失败也会立即触发重查（覆盖同 COM 号快速拔插、句柄失效的情况）。AUTO 模式的「单口兜底」只认 USB 串口，绝不选主板自带 COM1（写它不报错、会一直占住）。
- **UI 线程**：引擎 goroutine 回写界面必须包 `fyne.Do(...)`（见 `gui.go` 的 `OnLog`/`OnStatus`）；按钮/表单回调本就在 UI 线程，调引擎方法即可。
- **串口写入单点化**：只有 `engine.go` 的 `serialWriterLoop` 执行 `port.Write`；显示循环与「发送测试 Frame」都往 `writeCh` 投递，勿在别处直接写串口。
- **取数/显示解耦**：`fetchOnce` 只更新缓存（`dataMu` 保护的 claude/codex/custom + 时间戳，成功才更新、失败保留上次好数据），不直接下发；下发由 `displayLoop` 按页轮播（间隔来自 `pageInterval()`←配置 `page_interval_seconds`，默认 4s；改「轮播间隔」保存后经 reload→refresh 即时生效）。取到新数据会 `signalRefresh()` 让显示循环立刻重画当前页。`buildFrames()` 据 provider+缓存组装当前页列表，首轮取数前返回 nil（固件停在等待屏）。
- **取数间隔自适应**：`fetchOnce` 返回 bool（任一 provider 成功即 true）；`fetchLoop` 失败用 `RetryIntervalSeconds`、成功用 `IntervalSeconds`。启动即取一次，因此「失败 60s 重试、首次成功后 300s」是 result-based 实现（后续再失败也会回落到重试间隔）。
- **provider 取数分支**：内置对象始终查询百分比额度；启用的自定义对象各自查询余额，按列表顺序追加到轮播页。自定义对象失败不影响其它对象。
- **单实例**：`main()` 先 `acquireSingleInstance()`（占本地回环端口）；占用失败说明已有实例，敲门让其 `w.Show()` 后本进程退出。
- **文件监听**：`watchCredentials` 用 fsnotify 看 `~/.codex`、`~/.claude`、`~/.kimi-code/credentials`、`~/.reclaude` 目录，命中 `auth.json/.credentials.json/kimi-code.json/state.json/ca.pem` 变动（debounce 800ms）就 `Reload()` 立即重取。
  **只认内容真变了**：debounce 到期时按 `fileFingerprint` 重算指纹（`state.json` 只看 daemon port/running；其它文件 sha256），没变不触发。
  背景：ReClaude daemon 每分钟重写 `~/.reclaude/state.json` 的 `last_alive_at_ms` 心跳，以前每次都触发 Reload，取数变成每分钟一次，把 ReClaude 网关打到 HTTP 429（成功/失败交替）。
- **取数结果落盘**：`cache.go` 每次成功取数后把缓存写到 `~/.esp32-ai-usage/cache.json`（只有百分比/时间/金额，无凭据）；`Start()` 先 `loadCache()` 装回并置 `started=true`，
  启动即显示上次数据（按时间戳超期自动标 `old`），首轮取数失败时不再先 ERR 一阵。
- **退出**：`fetchCodex/fetchClaude` 已绑 ctx，退出 `cancel()` 后 in-flight 请求立即中止，不会卡 `Stop()`。
- **工作状态（运行标志）**：不是猜进程/看文件 mtime，而是两家 CLI 自带的 hook。`hooks.go` 把回调写进
  `~/.claude/settings.json`（`type:http`，POST 到 `http://127.0.0.1:52736/hook/claude`，同步、秒回）和
  `~/.codex/hooks.json`（`type:command`，`curl -s -m 2 -X POST --data-binary @- .../hook/codex`，`async:true`；Codex 不支持 http 型，
  且**新钩子要用户在 Codex 里 `/hooks` 信任一次**才执行）。事件：SessionStart/SessionEnd/UserPromptSubmit/Stop/PreToolUse/PostToolUse，
  外加 Claude 的 Notification（按 `notification_type`：permission_prompt 等→需要你，idle_prompt→空闲）与 Codex 的 PermissionRequest（→需要你）。
  `activity.go` 的 `eventState` 是事件→状态映射；按 `(provider, session_id)` 记录，聚合取最高优先级（需要你>工作中>空闲）；
  进程被直接杀掉不会发 Stop，靠 TTL 兜底（busy 15min、attention 2h）。SessionStart 只登记不覆盖（compact/resume 也会发它）。
  聚合值变化 → `Engine.activity` 的 onChange → 记日志 + `signalRefresh()` 立刻重发当前页 + `OnActivity` 更新底部状态栏。
  `buildFrames` 把 claude 状态同时给 claude/reclaude 页（同一个 Claude Code CLI），codex 给 codex 页，kimi 恒 0。
  安装/移除只增删「handler 的 url/command 含 activityAddr」的条目，别人的钩子与其它设置键原样保留（`updateHookFile` 幂等）。
  服务端对任何请求都回 200 + `{}`（Codex 的 Stop hook 要求 stdout 是 JSON；非法 body 也回 200，别让 CLI 报错）。
  hook 事件 JSON 里有 prompt/工具输入，只解析 `session_id/hook_event_name/notification_type`，绝不记录其它内容。
