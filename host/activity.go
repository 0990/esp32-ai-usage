package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 「工作状态」：Claude Code / Codex CLI 是否正在干活。
//
// 数据来源是两家 CLI 自带的 hook 机制（安装逻辑见 hooks.go）：用户提交提示、回合结束、
// 工具调用、等待授权、会话开始/结束时，CLI 会把事件 JSON POST 到本地端口 activityAddr。
// 这里按 (provider, session_id) 记录每个会话的状态，再按 provider 聚合成一个值交给显示层
// （Frame.Act），固件据此画运行动画 / 闪烁提醒。
//
// 状态优先级：需要你（等待授权）> 工作中 > 空闲。多会话并存时取最高优先级。

// activityAddr 是接收 hook 回调的本地回环地址（单实例锁用的是 52735，这里用 52736）。
const activityAddr = "127.0.0.1:52736"

// hookPathPrefix 是回调路径前缀，后接 provider 名（claude / codex）。
const hookPathPrefix = "/hook/"

const (
	ActIdle      = 0 // 空闲
	ActBusy      = 1 // 工作中（模型在生成 / 在跑工具）
	ActAttention = 2 // 需要你（在等授权 / 等输入）
)

// 无新事件时的过期时间。工作中的会话每次工具调用都会来事件，超过 busyTTL 没动静视为
// 进程已被直接杀掉（不会发 Stop/SessionEnd）；等待授权可能合法地等很久，TTL 给长一些；
// 空闲会话超过 idleTTL 直接忘掉，避免 map 无限增长。
const (
	busyTTL      = 15 * time.Minute
	attentionTTL = 2 * time.Hour
	idleTTL      = 24 * time.Hour
)

// hookEvent 是两家 CLI hook 事件 JSON 里我们关心的字段（其余忽略；绝不记录 prompt/工具输入）。
type hookEvent struct {
	SessionID        string `json:"session_id"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"` // Claude Notification 事件专用
}

type sessionState struct {
	state int
	at    time.Time
}

// ActivityTracker 记录各 provider 各会话的状态，并在聚合值变化时回调 onChange。
type ActivityTracker struct {
	mu       sync.Mutex
	sessions map[string]map[string]sessionState // provider -> session_id -> state
	last     map[string]int                     // provider -> 上次聚合值（变化检测）
	now      func() time.Time                   // 可注入（测试用）
	onChange func(provider string, state int)   // 聚合值变化时回调（HTTP goroutine 里调用）
}

func NewActivityTracker(onChange func(provider string, state int)) *ActivityTracker {
	return &ActivityTracker{
		sessions: map[string]map[string]sessionState{},
		last:     map[string]int{},
		now:      time.Now,
		onChange: onChange,
	}
}

// actName 把状态值转成日志用的中文。
func actName(s int) string {
	switch s {
	case ActBusy:
		return "工作中"
	case ActAttention:
		return "需要你"
	default:
		return "空闲"
	}
}

// eventState 把一个 hook 事件映射成会话的新状态。
// 返回 (state, remove, ok)：remove=true 表示会话结束应删除；ok=false 表示该事件不影响状态。
func eventState(ev hookEvent) (state int, remove, ok bool) {
	switch ev.HookEventName {
	case "SessionStart":
		return ActIdle, false, true // 只登记，不覆盖已有状态（compact/resume 也会触发它）
	case "SessionEnd":
		return ActIdle, true, true
	case "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
		"SubagentStart", "SubagentStop", "PreCompact", "PostCompact":
		return ActBusy, false, true
	case "Stop":
		return ActIdle, false, true
	case "PermissionRequest": // Codex：等待用户授权
		return ActAttention, false, true
	case "Notification": // Claude：按通知类型判定
		switch ev.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			return ActAttention, false, true
		case "idle_prompt":
			return ActIdle, false, true
		}
	}
	return 0, false, false
}

// Handle 处理一个事件；返回该 provider 的聚合状态是否因此改变。
func (t *ActivityTracker) Handle(provider string, ev hookEvent) bool {
	state, remove, ok := eventState(ev)
	if !ok {
		return false
	}
	sid := ev.SessionID
	if sid == "" {
		sid = "_"
	}
	t.mu.Lock()
	m := t.sessions[provider]
	if m == nil {
		m = map[string]sessionState{}
		t.sessions[provider] = m
	}
	switch {
	case remove:
		delete(m, sid)
	case ev.HookEventName == "SessionStart":
		if _, exists := m[sid]; !exists {
			m[sid] = sessionState{state: ActIdle, at: t.now()}
		}
	default:
		m[sid] = sessionState{state: state, at: t.now()}
	}
	agg := t.aggregateLocked(provider)
	prev := t.last[provider] // 未登记过视为空闲：首个 SessionStart 不算变化
	t.last[provider] = agg
	t.mu.Unlock()

	changed := prev != agg
	if changed && t.onChange != nil {
		t.onChange(provider, agg)
	}
	return changed
}

// State 返回某 provider 的当前聚合状态（会先做过期清理）。
func (t *ActivityTracker) State(provider string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.aggregateLocked(provider)
}

// aggregateLocked 取 provider 下所有会话的最高优先级状态，并顺手清理过期会话。须持锁。
func (t *ActivityTracker) aggregateLocked(provider string) int {
	now := t.now()
	agg := ActIdle
	for sid, s := range t.sessions[provider] {
		age := now.Sub(s.at)
		switch s.state {
		case ActBusy:
			if age > busyTTL {
				s.state = ActIdle
				t.sessions[provider][sid] = s
			}
		case ActAttention:
			if age > attentionTTL {
				s.state = ActIdle
				t.sessions[provider][sid] = s
			}
		}
		if s.state == ActIdle && age > idleTTL {
			delete(t.sessions[provider], sid)
			continue
		}
		if s.state > agg {
			agg = s.state
		}
	}
	return agg
}

// Sessions 返回某 provider 当前登记的会话数（界面/日志用）。
func (t *ActivityTracker) Sessions(provider string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions[provider])
}

// Handler 返回处理 hook 回调的 http.Handler：POST /hook/<provider>，请求体为 CLI 的事件 JSON。
// 响应固定为 200 + "{}"（两家 CLI 都把 JSON 输出当「无附加指令」处理），必须快回，
// 因为 Claude 的 HTTP hook 是同步等待的。
func (t *ActivityTracker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(hookPathPrefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		provider := strings.ToLower(strings.Trim(strings.TrimPrefix(r.URL.Path, hookPathPrefix), "/"))
		if provider == "" {
			http.Error(w, "missing provider", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		var ev hookEvent
		_ = json.Unmarshal(body, &ev) // 非法/空 JSON 当作无关事件，仍回 200，别让 CLI 报错
		t.Handle(provider, ev)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	return mux
}

// Serve 在 activityAddr 上监听 hook 回调，直到 ctx 取消。端口被占时返回错误（功能降级，不影响取数显示）。
func (t *ActivityTracker) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", activityAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           t.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(done)
	}()
	err = srv.Serve(ln)
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
