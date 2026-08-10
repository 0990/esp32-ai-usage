package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// LogLevel 是日志级别。
type LogLevel int

const (
	LogInfo LogLevel = iota
	LogWarn
	LogError
)

func (l LogLevel) String() string {
	switch l {
	case LogWarn:
		return "WARN"
	case LogError:
		return "ERR"
	default:
		return "INFO"
	}
}

// LogEntry 是一条结构化日志，供界面渲染。
type LogEntry struct {
	Time  time.Time
	Level LogLevel
	Tag   string
	Msg   string
}

// defaultPageInterval 是 all 模式下 host 主动翻页的默认间隔（固件不再自己翻页）；
// 可由配置 PageIntervalSeconds 覆盖。
const defaultPageInterval = 4 * time.Second

// serialReconnectInterval 是串口未连接时的重连尝试间隔。
const serialReconnectInterval = 3 * time.Second

// pageInterval 返回当前轮播翻页间隔（读配置 PageIntervalSeconds，非法则回退默认）。
func (e *Engine) pageInterval() time.Duration {
	secs := e.Config().PageIntervalSeconds
	if secs <= 0 {
		return defaultPageInterval
	}
	return time.Duration(secs) * time.Second
}

// Engine 是不依赖 GUI 的后台引擎：按 interval 拉取额度并缓存，由独立的显示循环
// 主动驱动翻页、把「一屏」(Frame) 单点写给串口。固件只渲染收到的最新 Frame。
// 通过回调把日志/状态推给界面。所有 UI 改动由界面侧包进 fyne.Do。
type Engine struct {
	mu      sync.RWMutex // 保护 cfg
	cfg     Config
	cfgPath string
	client  *http.Client

	portMu   sync.Mutex  // 保护 port/portName
	port     serial.Port // 可能为 nil（未连接）
	portName string      // 当前端口名（断开为 ""），AUTO 模式用于目标比对

	writeCh   chan []byte   // 唯一的串口写入入口
	reload    chan struct{} // 通知取数循环立即重新拉取
	refresh   chan struct{} // 通知显示循环：缓存已更新，立即重画当前页
	reconnect chan struct{} // 通知串口管理循环：端口已断开/配置已变，立即重连

	// 取数结果缓存（供显示循环按页渲染），由 dataMu 保护。
	dataMu        sync.Mutex
	claude        Provider  // 官方百分比额度
	codex         Provider  // 官方百分比额度
	kimi          Provider  // 官方百分比额度
	relay         *Relay    // 中转站余额（codex 走中转时）；nil=非中转
	claudeRelay   *Relay    // 中转站余额（claude 走中转时）；nil=非中转
	claudeAt      time.Time // 最近一次成功取数时间（用于过期判定）
	codexAt       time.Time
	kimiAt        time.Time
	relayAt       time.Time
	claudeRelayAt time.Time
	started       bool // 是否已完成首轮取数（用于首屏「等待」判定）
	pageIdx       int  // all 模式当前页索引

	// 回调由界面设置；界面实现体须包进 fyne.Do。
	OnLog    func(LogEntry)
	OnStatus func(string)
	// OnCredsChange 在 watcher 检测到 ~/.codex、~/.claude 凭据/配置变动时调用
	// （已 debounce），供界面刷新依赖凭据的展示（如中转站域名列表）。
	OnCredsChange func()

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine 构造引擎（尚未启动 goroutine）。
func NewEngine(cfg Config, cfgPath string) *Engine {
	return &Engine{
		cfg:     cfg,
		cfgPath: cfgPath,
		client:  newHTTPClient(cfg.Proxy),
		writeCh:   make(chan []byte, 8),
		reload:    make(chan struct{}, 1),
		refresh:   make(chan struct{}, 1),
		reconnect: make(chan struct{}, 1),
	}
}

func (e *Engine) log(level LogLevel, tag, format string, args ...any) {
	entry := LogEntry{Time: time.Now(), Level: level, Tag: tag, Msg: fmt.Sprintf(format, args...)}
	// 持久化到文件，便于事后排查（界面没开/已崩溃时也有据可查）。
	writeLogLine(fmt.Sprintf("%s [%s][%s] %s", entry.Time.Format("2006-01-02 15:04:05"), level, tag, entry.Msg))
	if e.OnLog != nil {
		e.OnLog(entry)
	}
}

func (e *Engine) status(s string) {
	if e.OnStatus != nil {
		e.OnStatus(s)
	}
}

// Config 返回当前配置的副本。
func (e *Engine) Config() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// UpdateConfig 应用整套新配置：更新内存、重建 HTTP client（代理可能变）、写盘、催一次刷新。
// 注意：串口（port/baud）的重连由界面在保存后另行处理。
func (e *Engine) UpdateConfig(cfg Config) error {
	cfg.Provider = normalizeProvider(cfg.Provider)
	e.mu.Lock()
	e.cfg = cfg
	e.client = newHTTPClient(cfg.Proxy)
	e.mu.Unlock()

	if err := saveConfig(e.cfgPath, cfg); err != nil {
		e.log(LogError, "engine", "保存配置失败: %v", err)
		return err
	}
	e.log(LogInfo, "engine", "配置已保存（provider=%s, interval=%ds, retry=%ds）", cfg.Provider, cfg.IntervalSeconds, cfg.RetryIntervalSeconds)
	e.Reload()
	return nil
}

// Reload 非阻塞地催取数循环立即重新拉取（reload 容量 1，已有挂起信号则忽略）。
func (e *Engine) Reload() {
	select {
	case e.reload <- struct{}{}:
	default:
	}
}

// signalRefresh 非阻塞地通知显示循环：缓存已更新，立即重画当前页。
func (e *Engine) signalRefresh() {
	select {
	case e.refresh <- struct{}{}:
	default:
	}
}

// setPort 设置/更换串口；关闭旧的并刷新状态。name 为当前端口名，传 "" 表示断开。
func (e *Engine) setPort(p serial.Port, name, status string) {
	e.portMu.Lock()
	old := e.port
	e.port, e.portName = p, name
	e.portMu.Unlock()
	if old != nil {
		old.Close()
	}
	e.status(status)
}

// ReconnectSerial 关闭当前串口并催串口管理循环立即按最新配置重连
// （界面在 port/baud 变更后调用）。信号容量 1，已有挂起信号则合并。
func (e *Engine) ReconnectSerial() {
	e.setPort(nil, "", "串口: 重连中…")
	select {
	case e.reconnect <- struct{}{}:
	default:
	}
}

// signalReconnect 非阻塞地催串口管理循环立即重连（写失败判定断开时用）。
func (e *Engine) signalReconnect() {
	select {
	case e.reconnect <- struct{}{}:
	default:
	}
}

// Send 把一行 JSON 投到串口写入队列（自动补 \n）。取数循环与测试按钮都走这里。
func (e *Engine) Send(line []byte) {
	if len(line) == 0 {
		return
	}
	if line[len(line)-1] != '\n' {
		buf := make([]byte, len(line)+1)
		copy(buf, line)
		buf[len(line)] = '\n'
		line = buf
	}
	select {
	case e.writeCh <- line:
	case <-e.ctx.Done():
	}
}

// Start 启动串口管理、串口写入、取数、显示四个 goroutine，外加凭据文件监听。
func (e *Engine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.wg.Add(4)
	go e.serialManagerLoop()
	go e.serialWriterLoop()
	go e.fetchLoop()
	go e.displayLoop()
	go watchCredentials(e.ctx, e) // 监听 ~/.codex、~/.claude 变动（feature 5）
}

// Stop 取消上下文、等待 goroutine 退出、关闭串口。可重复调用。
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()
	e.portMu.Lock()
	if e.port != nil {
		e.port.Close()
		e.port, e.portName = nil, ""
	}
	e.portMu.Unlock()
}

// serialWriterLoop 是唯一执行 port.Write 的地方，从结构上杜绝并发写。
// 写失败视为设备已断开（如 ESP32 被拔掉）：清空端口并催管理循环重连，
// 重插后由 serialManagerLoop 自动恢复，无需重启 host。
func (e *Engine) serialWriterLoop() {
	defer e.wg.Done()
	defer recoverLog("serialWriterLoop")
	var lastDropLog time.Time // 「未连接丢弃」日志限流，避免断连期间刷屏
	for {
		select {
		case <-e.ctx.Done():
			return
		case line := <-e.writeCh:
			e.portMu.Lock()
			p := e.port
			e.portMu.Unlock()
			if p == nil {
				if time.Since(lastDropLog) > time.Minute {
					e.log(LogWarn, "serial", "串口未连接，丢弃 %d 字节（同类日志每分钟最多一条）", len(line))
					lastDropLog = time.Now()
				}
				continue
			}
			if _, err := p.Write(line); err != nil {
				e.log(LogError, "serial", "写串口失败: %v（视为已断开，等待重连）", err)
				e.setPort(nil, "", "串口: 已断开，等待重连…")
				e.signalReconnect()
			}
		}
	}
}

// serialManagerLoop 负责串口的连接与断线重连。每 serialReconnectInterval 重新计算
// 「目标串口」（desiredPort）并与当前口比对：
//   - 当前口已不在枚举里（被拔掉）或出现了更优目标（如 AUTO 兜底换成真 ESP32）→ 断开重选；
//   - 未连接且目标存在 → 打开；
//   - 目标不存在 → 状态栏置未连接，继续等。
//
// reconnect 信号（写失败/配置变更）可提前唤醒立即重查；写失败那条快速通道覆盖
// 「同 COM 号快速拔插、枚举名没变但句柄已失效」的情况。这样 ESP32 拔掉再插上、
// 或 host 启动时设备还没插，都能自动识别恢复，无需重启 host。
func (e *Engine) serialManagerLoop() {
	defer e.wg.Done()
	defer recoverLog("serialManagerLoop")
	firstTry := true // 每个断连周期只记一次日志，避免重试刷屏
	for {
		cfg := e.Config()
		desired, ok, candidates := desiredPort(cfg.Port)

		e.portMu.Lock()
		cur, curName := e.port, e.portName
		e.portMu.Unlock()

		switch {
		case cur != nil && (!ok || desired != curName):
			// 当前口被拔掉/目标变了 → 断开，立即重选（不等间隔）
			e.log(LogInfo, "serial", "%s 已不可用，重新选择串口…", curName)
			e.setPort(nil, "", "串口: 未连接")
			firstTry = true
			continue
		case cur == nil && ok:
			e.tryOpen(desired, cfg.Baud, firstTry)
			firstTry = false
		case cur == nil && !ok:
			if firstTry {
				hint := strings.Join(candidates, ", ")
				if hint == "" {
					hint = "（无）"
				}
				e.log(LogWarn, "serial", "未发现 ESP32 串口（找 VID=%s）。可用串口: %s", espressifVID, hint)
			}
			e.status("串口: 未连接")
			firstTry = false
		default: // 已连接且目标未变
			firstTry = true
		}

		timer := time.NewTimer(serialReconnectInterval)
		select {
		case <-e.ctx.Done():
			timer.Stop()
			return
		case <-e.reconnect: // 写失败/配置变更 → 立即重查，不等间隔
			timer.Stop()
		case <-timer.C:
		}
	}
}

// tryOpen 打开目标串口并设为当前口；logFail 为 false 时失败静默（只更新状态栏）。
func (e *Engine) tryOpen(name string, baud int, logFail bool) {
	p, err := openSerial(name, baud)
	if err != nil {
		if logFail {
			e.log(LogError, "serial", "%v", err)
		}
		e.status("串口: 打开失败")
		return
	}
	select {
	case <-e.ctx.Done(): // 退出中：别留着新开的端口
		p.Close()
		return
	case <-time.After(2 * time.Second): // 等 ESP32 复位就绪
	}
	e.setPort(p, name, fmt.Sprintf("串口: %s @ %d", name, baud))
	e.log(LogInfo, "serial", "已连接 %s @ %d", name, baud)
}

// fetchLoop 启动即立即取一次；之后按结果选间隔：失败用 RetryIntervalSeconds、
// 成功用 IntervalSeconds。provider 改变/凭据变动（reload）或退出（ctx）会提前唤醒。
func (e *Engine) fetchLoop() {
	defer e.wg.Done()
	defer recoverLog("fetchLoop")
	for {
		e.mu.RLock()
		cfg := e.cfg
		client := e.client
		e.mu.RUnlock()

		ok := e.fetchOnce(e.ctx, client, cfg)

		secs := cfg.RetryIntervalSeconds
		if ok {
			secs = cfg.IntervalSeconds
		}
		if secs <= 0 {
			secs = 60
		}

		timer := time.NewTimer(time.Duration(secs) * time.Second)
		select {
		case <-e.ctx.Done():
			timer.Stop()
			return
		case <-e.reload: // provider 改变 / 凭据变动 → 立即重新拉取
			timer.Stop()
		case <-timer.C:
		}
	}
}

// fetchOnce 取一轮额度，更新缓存（成功才更新对应时间戳，失败保留上次好数据）。
// 返回本轮是否「成功」（至少一个被请求的 provider 取到数据），fetchLoop 据此决定下次间隔。
// 取数与下发解耦：实际下发由 displayLoop 按页驱动。ctx 取消（退出中）时跳过取消导致的错误日志。
func (e *Engine) fetchOnce(ctx context.Context, client *http.Client, cfg Config) bool {
	success := false
	if cfg.wants("codex") {
		if resolveCodexRelay(cfg) { // 中转站：取余额
			if r, err := fetchCodexRelay(ctx, client, cfg); err != nil {
				if ctx.Err() == nil {
					e.log(LogError, "relay", "%v", err)
				}
			} else {
				e.dataMu.Lock()
				e.relay = &r
				e.relayAt = time.Now()
				e.dataMu.Unlock()
				e.log(LogInfo, "relay", "%s %s%g / 余额 %s%g", r.Label, r.Unit, r.Used, r.Unit, r.Balance)
				success = true
			}
		} else { // 官方订阅：取百分比额度
			if p, err := fetchCodex(ctx, client, cfg.CodexEndpoint, false); err != nil {
				if ctx.Err() == nil {
					e.log(LogError, "codex", "%v", err)
				}
			} else {
				e.dataMu.Lock()
				e.codex = p
				e.codexAt = time.Now()
				e.relay = nil // 切回官方时清掉中转余额，避免残留中转页
				e.dataMu.Unlock()
				e.log(LogInfo, "codex", "5h 剩 %d%% (复位 %s) / 周 剩 %d%% (复位 %s)",
					p.H5.Left, p.H5.Reset, p.Wk.Left, p.Wk.Reset)
				success = true
			}
		}
	}
	if cfg.wants("claude") {
		if claudeIsRelay() { // 中转站：取余额
			if r, err := fetchClaudeRelay(ctx, client, cfg); err != nil {
				if ctx.Err() == nil {
					e.log(LogError, "claude-relay", "%v", err)
				}
			} else {
				e.dataMu.Lock()
				e.claudeRelay = &r
				e.claudeRelayAt = time.Now()
				e.dataMu.Unlock()
				e.log(LogInfo, "claude-relay", "%s %s%g / 余额 %s%g", r.Label, r.Unit, r.Used, r.Unit, r.Balance)
				success = true
			}
		} else { // 官方订阅：取百分比额度
			if p, err := fetchClaude(ctx, client, cfg.ClaudeEndPoint, false); err != nil {
				if ctx.Err() == nil {
					e.log(LogError, "claude", "%v", err)
				}
			} else {
				e.dataMu.Lock()
				e.claude = p
				e.claudeAt = time.Now()
				e.claudeRelay = nil // 切回官方时清掉中转余额，避免残留中转页
				e.dataMu.Unlock()
				e.log(LogInfo, "claude", "5h 剩 %d%% (复位 %s) / 周 剩 %d%% (复位 %s)",
					p.H5.Left, p.H5.Reset, p.Wk.Left, p.Wk.Reset)
				success = true
			}
		}
	}
	if cfg.wants("kimi") {
		if p, err := fetchKimi(ctx, client, cfg.KimiEndpoint, false); err != nil {
			if ctx.Err() == nil {
				e.log(LogError, "kimi", "%v", err)
			}
		} else {
			e.dataMu.Lock()
			e.kimi = p
			e.kimiAt = time.Now()
			e.dataMu.Unlock()
			e.log(LogInfo, "kimi", "5h 剩 %d%% (复位 %s) / 周 剩 %d%% (复位 %s)",
				p.H5.Left, p.H5.Reset, p.Wk.Left, p.Wk.Reset)
			success = true
		}
	}

	e.dataMu.Lock()
	e.started = true
	e.dataMu.Unlock()
	if ctx.Err() == nil {
		e.signalRefresh() // 催显示循环立即重画
	}
	return success
}

// displayLoop 按 pageInterval() 主动翻页并下发当前页；缓存更新（refresh）时立即重画同一页。
// 翻页逻辑完全在 host 这一侧，固件只渲染收到的最新 Frame。每轮按当前配置重置定时器，
// 使「轮询间隔」改动（保存后经 reload→refresh）立即生效。
func (e *Engine) displayLoop() {
	defer e.wg.Done()
	defer recoverLog("displayLoop")
	timer := time.NewTimer(e.pageInterval())
	defer timer.Stop()
	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(e.pageInterval())
	}
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-timer.C:
			e.renderCurrent(true) // 翻到下一页
			timer.Reset(e.pageInterval())
		case <-e.refresh:
			e.renderCurrent(false) // 新数据/配置变动：重画当前页并按新间隔重置
			resetTimer()
		}
	}
}

// renderCurrent 取当前应显示的页集合，按 advance 决定是否前进页码，下发该页。
// 还没有任何数据时不下发（固件保持「等待」屏）。
func (e *Engine) renderCurrent(advance bool) {
	frames := e.buildFrames()
	if len(frames) == 0 {
		return
	}
	e.dataMu.Lock()
	if advance {
		e.pageIdx++
	}
	if e.pageIdx >= len(frames) {
		e.pageIdx = 0
	}
	idx := e.pageIdx
	e.dataMu.Unlock()

	line, _ := json.Marshal(frames[idx])
	e.Send(line)
}

// buildFrames 依据 provider 配置 + 缓存数据，组装当前要轮播的页列表。
// 首轮取数完成前返回 nil（让固件停留在等待屏）。
func (e *Engine) buildFrames() []Frame {
	cfg := e.Config()
	now := time.Now()
	stale := func(at time.Time) bool {
		thr := 2*time.Duration(cfg.IntervalSeconds)*time.Second + 15*time.Second
		if thr < 90*time.Second {
			thr = 90 * time.Second
		}
		return !at.IsZero() && now.Sub(at) > thr
	}

	e.dataMu.Lock()
	defer e.dataMu.Unlock()
	if !e.started {
		return nil
	}
	var frames []Frame
	if cfg.wants("claude") {
		if e.claudeRelay != nil { // claude 走中转 → 中转页
			frames = append(frames, moneyFrame(*e.claudeRelay, stale(e.claudeRelayAt)))
		} else {
			frames = append(frames, pctFrame("claude", e.claude, stale(e.claudeAt)))
		}
	}
	if cfg.wants("codex") {
		if e.relay != nil { // codex 走中转 → 中转页
			frames = append(frames, moneyFrame(*e.relay, stale(e.relayAt)))
		} else {
			frames = append(frames, pctFrame("codex", e.codex, stale(e.codexAt)))
		}
	}
	if cfg.wants("kimi") {
		frames = append(frames, pctFrame("kimi", e.kimi, stale(e.kimiAt)))
	}
	return frames
}
