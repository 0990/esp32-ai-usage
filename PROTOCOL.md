# 串口显示协议（Host → ESP32）

电脑上的 host 程序通过 USB 串口把数据发给 ESP32，ESP32 在 128×32 OLED 上显示
Claude / Codex / Kimi 的额度以及中转站余额。

**设计原则：固件只负责「渲染」，不含任何切换/翻页/选择逻辑。**
host 每发一行显示 JSON 就是「一整屏」(Frame)，固件收到就照着画。要显示哪一页、什么时候翻页、
数据是否过期，全部由 host 决定——这样 host 可以灵活控制显示，固件保持极简。

固件解析实现见 [firmware/src/main.cpp](firmware/src/main.cpp)。

---

## 1. 传输方式

| 项目 | 值 |
|------|----|
| 接口 | USB-CDC 串口（C3/S3 一根 USB 线即可） |
| 波特率 | **115200** |
| 格式 | **NDJSON**：一行一个 Frame 或显示设置命令（JSON 对象），以 `\n`（换行）结尾 |
| 编码 | ASCII（`\r` 会被忽略） |
| 单行长度 | < 600 字节（超长行会被丢弃） |

> 每发一行就整屏刷新一次。非法 JSON、空行（<2 字符）会被静默忽略。
> 翻页由 host 主动驱动：host 每隔约 4 秒发下一页（`all` 监控对象时在多页间轮播）。
> 固件不保存历史页、不自己翻页；它永远只显示「最近收到的那一帧」。
> 兜底：超过 **90 秒**没收到新的显示帧，固件会本地标 `old`（数据过期）。

---

## 1.1 OLED 亮度设置

设置页的亮度滑块范围为 0–255，默认 143（原 SSD1306 初始化亮度）。点「保存」后 host 即时发送：

```json
{"cmd":"display","brightness":80}
```

这是显示设置命令，不替换画面、不改变工作状态或数据过期计时；即使正在显示 Waiting，也能调整亮度。串口重连时会重新发送，普通显示帧也携带当前 `brightness`，设备重启后持续同步。旧帧未携带此字段时保持现有亮度。

固件调用 `setContrast`，并仅在亮度变化时写入 NVS，ESP32 断电重启后可恢复上次亮度。电脑端配置存为 `oled_brightness`，缺失时使用 143，显式 0 保留；超出范围时钳制到 0–255。

---

## 2. Frame：一整屏

每一帧用 `k` 字段区分两种页型：

| `k` | 含义 | 用途 |
|-----|------|------|
| `"pct"`   | 百分比页 | Claude / Codex / Kimi 官方订阅的 5 小时 + 1 周剩余额度 |
| `"money"` | 金额页   | sub2api / newapi 的余额 |

通用字段：

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `ic` | string | — | 图标。`pct` 页：`"claude"` / `"codex"` / `"kimi"`（内置位图）；`money` 页：`"sub2api"` / `"newapi"`（24×24 内置位图） |
| `k`  | string | `"pct"` | 页型，见上表 |
| `l1` | string | — | 第 1 行标签（最长 ~13 字符） |
| `l2` | string | — | 第 2 行标签 |
| `brightness` | int | 保留当前亮度 | OLED 对比度/亮度，范围 0–255，0 为最低亮度（不等于关闭） |
| `old`| bool   | `false` | 数据过期 → 固件叠加显示 `old` |
| `a`  | int    | `0` | 该页对应 CLI 的工作状态：`0` 空闲（不画）、`1` 工作中（图标下方三个点轮流跑动，RGB 灯蓝色呼吸）、`2` 需要你（图标右上角一枚脉动的「!」角标，RGB 灯紫色快闪）。见 §2.1 |

### 2.1 `a` 工作状态从哪来

host 往 Claude Code（`~/.claude/settings.json`）和 Codex CLI（`~/.codex/hooks.json`）安装 hook，
两家 CLI 在「提交提示 / 回合结束 / 工具调用 / 等待授权 / 会话开始结束」时把事件 POST 到 host 的
本地端口 `127.0.0.1:52736`（`host/activity.go`）。host 按会话记录状态、按 provider 取最高优先级
（需要你 > 工作中 > 空闲），再随每一帧下发。`claude` 与 `reclaude` 页共用 Claude Code 的状态；`kimi` 页恒为 `0`。

```json
{"ic":"codex","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30","a":1}
```

固件收到 `a≠0` 的帧后每 150ms 本地重画一次以驱动动画；状态变化时 host 会立刻重发当前页。

---

## 3. `pct` 百分比页

表示两个窗口的**剩余百分比**（上行 5 小时、下行 1 周）。

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `p1` | int | `-1` | 第 1 行剩余百分比 0–100；`-1` → 显示 `ERR` |
| `s1` | string | `""` | 第 1 行重置时间文本，最长 11 字符（如 `15:47`） |
| `p2` | int | `-1` | 第 2 行剩余百分比；`-1` → `ERR` |
| `s2` | string | `""` | 第 2 行重置时间（如 `06-25 14:30`） |

显示效果（左侧 provider 图标 + 右侧两行：标签 + 进度条 + 大字百分比 + 重置时间）：

```
[icon] 5h  ▆▆▆▆░░  94%
       15:47
       1w  ▆▆░░░░  38%
       06-25 14:30
```

示例：

```json
{"ic":"claude","k":"pct","l1":"5h","p1":94,"s1":"15:47","l2":"1w","p2":38,"s2":"06-25 14:30"}
```

出错 / 无数据时（任一行 `p=-1` 即显示 `ERR`）：

```json
{"ic":"codex","k":"pct","l1":"5h","p1":-1,"l2":"1w","p2":-1}
```

---

## 4. `money` 金额页（中转站）

sub2api / newapi 页只显示余额。host 通过 `ic` 指定类型图标：`sub2api` 为 S2，`newapi` 为 N，均为 24×24 单色位图，位置与 Codex / Claude 图标一致。

| 字段 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `ic` | string | — | `"sub2api"` / `"newapi"` |
| `l1` | string | `"Balance"` | 余额标签 |
| `v1` | string | `"ERR"` | 余额文本，含货币符号，如 `$880` |
| `old` | bool | false | 数据过期标记 |

host 不再下发已用金额，`l2` 为空，`v2` 省略。固件右侧显示小字 Balance 和大字余额，大额余额会自动缩小字号。余额缺失或 `ERR` 时状态灯显示异常；过期显示 `old`。

```
[S2]  Balance
      $880
```

```json
{"ic":"sub2api","k":"money","l1":"Balance","v1":"$880"}
{"ic":"newapi","k":"money","l1":"Balance","v1":"$42.5"}
```

固件仍兼容旧货币图标的两行金额帧。新版自定义余额页需要同时更新电脑端和固件。

---

## 5. host 如何驱动显示

固件没有「监控对象 / 翻页」概念；这些都在 host 侧实现（见 `host/engine.go` 的 `displayLoop`）：

| host 的「监控对象」 | host 轮播的页序列 |
|----|----|
| `claude` | 只发 Claude `pct` 页 |
| `reclaude` | 只发 ReClaude `pct` 页（使用 Claude 图标） |
| `codex`（官方） | 只发 Codex `pct` 页 |
| 启用的自定义对象 | 每个对象发一页 `money` |
| `kimi` | 只发 Kimi `pct` 页 |
| `all`（官方 codex） | Claude `pct` → ReClaude `pct` → Codex `pct` → Kimi `pct`，每 ~4s 翻一页 |
| `all` + 自定义对象 | 内置 `pct` 页后追加启用对象的 `money` 页，每 ~4s 翻一页 |

host 每 ~4 秒发当前页；取到新数据时也会立刻重发当前页。固件只是被动跟随。

---

## 6. 字段长度限制（固件缓冲区）

| 字段 | 最大长度 |
|------|---------|
| `ic` | 9 字符 |
| `l1` / `l2` / `s1` / `s2` | 13 字符 |
| `v1` / `v2` | 15 字符 |
| 整行 JSON | 600 字节 |

超出会被截断，整行超 600 字节会被丢弃。

---

## 7. 本地测试

不依赖 host 程序，可用脚本直接发测试帧：
[firmware/tools/send-test.ps1](firmware/tools/send-test.ps1)

```powershell
# firmware 目录下执行
powershell -ExecutionPolicy Bypass -File tools\send-test.ps1 -Kind pct   -Port COM3
powershell -ExecutionPolicy Bypass -File tools\send-test.ps1 -Kind money -Icon sub2api -V1 '$880'
```

> 脚本独占串口，运行时不能同时开 host 程序。端口号用 `pio device list` 查。
