package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestTracker() (*ActivityTracker, *time.Time, *[]string) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.Local)
	var changes []string
	t := NewActivityTracker(func(p string, s int) { changes = append(changes, p+":"+actName(s)) })
	t.now = func() time.Time { return now }
	return t, &now, &changes
}

func TestActivityTransitions(t *testing.T) {
	tr, now, changes := newTestTracker()
	ev := func(sid, name string) hookEvent { return hookEvent{SessionID: sid, HookEventName: name} }

	tr.Handle("claude", ev("s1", "SessionStart"))
	if tr.State("claude") != ActIdle {
		t.Fatalf("after SessionStart want idle, got %d", tr.State("claude"))
	}
	tr.Handle("claude", ev("s1", "UserPromptSubmit"))
	if tr.State("claude") != ActBusy {
		t.Fatalf("after UserPromptSubmit want busy")
	}
	// SessionStart（compact/resume 也会发）不能把工作中的会话打回空闲。
	tr.Handle("claude", ev("s1", "SessionStart"))
	if tr.State("claude") != ActBusy {
		t.Fatalf("SessionStart must not reset busy session")
	}
	// 第二个会话在等授权：聚合取最高优先级。
	tr.Handle("claude", hookEvent{SessionID: "s2", HookEventName: "Notification", NotificationType: "permission_prompt"})
	if tr.State("claude") != ActAttention {
		t.Fatalf("want attention when any session waits for permission")
	}
	tr.Handle("claude", ev("s2", "PostToolUse")) // 授权后工具跑完 → 回到工作中
	if tr.State("claude") != ActBusy {
		t.Fatalf("after PostToolUse want busy")
	}
	tr.Handle("claude", ev("s1", "Stop"))
	if tr.State("claude") != ActBusy { // s2 仍在工作
		t.Fatalf("s2 still busy → want busy")
	}
	tr.Handle("claude", ev("s2", "SessionEnd"))
	if tr.State("claude") != ActIdle {
		t.Fatalf("all done → want idle")
	}
	// 无关事件不改变状态、不触发回调。
	before := len(*changes)
	if tr.Handle("claude", hookEvent{SessionID: "s1", HookEventName: "Notification", NotificationType: "auth_success"}) {
		t.Fatalf("unrelated notification must not change state")
	}
	if len(*changes) != before {
		t.Fatalf("unrelated notification must not fire onChange")
	}
	// provider 之间互不影响。
	tr.Handle("codex", ev("c1", "PermissionRequest"))
	if tr.State("codex") != ActAttention || tr.State("claude") != ActIdle {
		t.Fatalf("providers must be independent")
	}
	// TTL：工作中的会话长时间没事件 → 视为空闲。
	tr.Handle("codex", ev("c1", "PreToolUse"))
	*now = now.Add(busyTTL + time.Minute)
	if tr.State("codex") != ActIdle {
		t.Fatalf("busy session past TTL must be idle")
	}
	want := []string{"claude:工作中", "claude:需要你", "claude:工作中", "claude:空闲", "codex:需要你", "codex:工作中"}
	if strings.Join(*changes, ",") != strings.Join(want, ",") {
		t.Fatalf("onChange sequence = %v, want %v", *changes, want)
	}
}

func TestActivityHandler(t *testing.T) {
	tr, _, _ := newTestTracker()
	srv := httptest.NewServer(tr.Handler())
	defer srv.Close()

	post := func(path, body string) *http.Response {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp
	}
	if resp := post("/hook/claude", `{"session_id":"abc","hook_event_name":"UserPromptSubmit","user_input":"secret"}`); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if tr.State("claude") != ActBusy {
		t.Fatalf("handler did not record busy")
	}
	// 非法 JSON 也要 200，别让 CLI 报错。
	if resp := post("/hook/codex", `not json`); resp.StatusCode != 200 {
		t.Fatalf("invalid json should still be 200, got %d", resp.StatusCode)
	}
	if resp, _ := http.Get(srv.URL + "/hook/claude"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET should be 405, got %d", resp.StatusCode)
	}
}

func TestHookFileInstallRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// 原文件里有别人的配置与别人的钩子，必须原样保留。
	orig := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo done"}]}]}}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateHookFile(path, claudeHookEvents, claudeHookHandler(), true); err != nil {
		t.Fatal(err)
	}
	// 重复安装应幂等。
	if err := updateHookFile(path, claudeHookEvents, claudeHookHandler(), true); err != nil {
		t.Fatal(err)
	}
	obj, err := readJSONObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if obj["model"] != "opus" {
		t.Fatalf("other keys must be preserved: %v", obj)
	}
	hooks := obj["hooks"].(map[string]any)
	for _, ev := range claudeHookEvents {
		groups, _ := hooks[ev].([]any)
		ours := 0
		for _, g := range groups {
			if isOurGroup(g) {
				ours++
			}
		}
		if ours != 1 {
			t.Fatalf("event %s: want exactly 1 of our groups, got %d (%v)", ev, ours, groups)
		}
	}
	if stop := hooks["Stop"].([]any); len(stop) != 2 || isOurGroup(stop[0]) {
		t.Fatalf("foreign Stop hook must be kept first: %v", stop)
	}
	if !hookFileInstalled(path) {
		t.Fatalf("hookFileInstalled should be true")
	}

	if err := updateHookFile(path, claudeHookEvents, claudeHookHandler(), false); err != nil {
		t.Fatal(err)
	}
	obj, _ = readJSONObject(path)
	hooks = obj["hooks"].(map[string]any)
	if len(hooks) != 1 || len(hooks["Stop"].([]any)) != 1 {
		t.Fatalf("after remove only the foreign Stop hook should remain: %v", hooks)
	}
	if hookFileInstalled(path) {
		t.Fatalf("hookFileInstalled should be false after remove")
	}

	// 不存在的文件：安装后创建；移除后 hooks 键消失。
	p2 := filepath.Join(dir, "hooks.json")
	if err := updateHookFile(p2, codexHookEvents, codexHookHandler(), true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p2)
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil || !strings.Contains(string(raw), "curl") {
		t.Fatalf("codex hooks.json malformed: %s", raw)
	}
	if err := updateHookFile(p2, codexHookEvents, codexHookHandler(), false); err != nil {
		t.Fatal(err)
	}
	obj, _ = readJSONObject(p2)
	if _, has := obj["hooks"]; has {
		t.Fatalf("hooks key should be dropped when empty: %v", obj)
	}

	// 非法 JSON：报错且不改动文件。
	p3 := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(p3, []byte("{oops"), 0o644)
	if err := updateHookFile(p3, claudeHookEvents, claudeHookHandler(), true); err == nil {
		t.Fatalf("invalid json must error")
	}
	if raw, _ := os.ReadFile(p3); string(raw) != "{oops" {
		t.Fatalf("invalid file must be left untouched")
	}
}

// TestCodexHookCommandCurl 使用隔离的随机端口，绝不向正在运行的 host 发送测试事件。
func TestCodexHookCommandCurl(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}
	tr, _, _ := newTestTracker()
	srv := httptest.NewServer(tr.Handler())
	defer srv.Close()
	command := strings.Replace(codexHookCommand(), hookURL("codex"), srv.URL+hookPathPrefix+"codex", 1)
	args := strings.Fields(command)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(`{"session_id":"x1","hook_event_name":"UserPromptSubmit","cwd":"C:/x"}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("curl failed: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "{}" {
		t.Fatalf("hook response should be {} (Codex Stop hook expects JSON on stdout), got %q", out)
	}
	if tr.State("codex") != ActBusy {
		t.Fatalf("codex should be busy after curl-delivered UserPromptSubmit")
	}
}
