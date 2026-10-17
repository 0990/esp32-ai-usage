package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 「工作状态」钩子的安装 / 移除：往两家 CLI 的 hook 配置里写入指向本程序 activityAddr 的回调。
//
//   - Claude Code：~/.claude/settings.json 的 "hooks"，用 type=http 直接 POST 到本地端口（无需脚本）。
//   - Codex CLI ：~/.codex/hooks.json 的 "hooks"，Codex 不支持 http 型，用 curl 命令转发 stdin。
//     Codex 对新增/改动的钩子要求用户在 TUI 里执行 /hooks 审核并信任一次，否则不会执行。
//
// 写入策略：读原文件 → 只增删「属于我们的」条目（handler 的 url/command 含 activityAddr）→
// 缩进写回。其它键与别人的钩子原样保留（键序可能变化，JSON 语义不变）。

func hookURL(provider string) string {
	return "http://" + activityAddr + hookPathPrefix + provider
}

// claudeHookEvents / codexHookEvents 是要挂的事件。Claude 的等待授权走 Notification（按
// notification_type 判定），Codex 走 PermissionRequest；其余事件两家同名。
var claudeHookEvents = []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "PreToolUse", "PostToolUse", "Notification"}
var codexHookEvents = []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "Stop", "PreToolUse", "PostToolUse", "PermissionRequest"}

func claudeSettingsPath() string { return filepath.Join(expandHome("~/.claude"), "settings.json") }
func codexHooksPath() string     { return filepath.Join(codexHome(), "hooks.json") }

// claudeHookHandler 是写进 settings.json 的 handler：同步 HTTP，本程序秒回，超时给 5s 兜底。
func claudeHookHandler() map[string]any {
	return map[string]any{"type": "http", "url": hookURL("claude"), "timeout": 5}
}

// codexHookCommand 不含引号，避免 Codex 在不同 shell 下拆参数时出偏差；服务端不要求 Content-Type。
func codexHookCommand() string {
	return "curl -s -m 2 -X POST --data-binary @- " + hookURL("codex")
}

// codexHookHandler：async 让 Codex 不等待命令结束（也不解析输出）。
func codexHookHandler() map[string]any {
	return map[string]any{"type": "command", "command": codexHookCommand(), "timeout": 5, "async": true}
}

// isOurHandler 判断一个 handler 对象是否指向本程序。
func isOurHandler(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range []string{"url", "command"} {
		if s, ok := m[k].(string); ok && (strings.Contains(s, activityAddr) || strings.Contains(s, "127.0.0.1:52736"+hookPathPrefix)) {
			return true
		}
	}
	return false
}

// isOurGroup 判断一个 matcher 组里是否有本程序的 handler。
func isOurGroup(g any) bool {
	m, ok := g.(map[string]any)
	if !ok {
		return false
	}
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		if isOurHandler(h) {
			return true
		}
	}
	return false
}

// readJSONObject 读 JSON 对象文件；不存在返回空对象。
func readJSONObject(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("解析 %s 失败（不是合法 JSON，未改动）: %w", path, err)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	return obj, nil
}

func writeJSONObject(path string, obj map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

// updateHookFile 对一个 hook 配置文件做安装（install=true）或移除。
// 每个事件下先剔除本程序的旧组，安装时再追加一组 {hooks:[handler]}。
func updateHookFile(path string, events []string, handler map[string]any, install bool) error {
	obj, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := obj["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, ev := range events {
		groups, _ := hooks[ev].([]any)
		kept := groups[:0:0]
		for _, g := range groups {
			if !isOurGroup(g) {
				kept = append(kept, g)
			}
		}
		if install {
			kept = append(kept, map[string]any{"hooks": []any{handler}})
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	// 移除时也顺手清掉其它事件下残留的本程序条目（比如旧版本挂过别的事件）。
	if !install {
		for ev, v := range hooks {
			groups, _ := v.([]any)
			kept := groups[:0:0]
			for _, g := range groups {
				if !isOurGroup(g) {
					kept = append(kept, g)
				}
			}
			if len(kept) == 0 {
				delete(hooks, ev)
			} else {
				hooks[ev] = kept
			}
		}
	}
	if len(hooks) == 0 {
		delete(obj, "hooks")
	} else {
		obj["hooks"] = hooks
	}
	return writeJSONObject(path, obj)
}

// hookFileInstalled 判断文件里是否已有本程序的钩子（按 activityAddr 子串粗判，足够可靠）。
func hookFileInstalled(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), activityAddr)
}

// installActivityHooks 给 Claude Code 与 Codex 都装上钩子；任一失败不影响另一边，错误合并返回。
func installActivityHooks() error {
	return setActivityHooks(true)
}

// removeActivityHooks 移除两家配置里本程序的钩子。
func removeActivityHooks() error {
	return setActivityHooks(false)
}

func setActivityHooks(install bool) error {
	var errs []error
	if err := updateHookFile(claudeSettingsPath(), claudeHookEvents, claudeHookHandler(), install); err != nil {
		errs = append(errs, fmt.Errorf("Claude: %w", err))
	}
	if err := updateHookFile(codexHooksPath(), codexHookEvents, codexHookHandler(), install); err != nil {
		errs = append(errs, fmt.Errorf("Codex: %w", err))
	}
	return errors.Join(errs...)
}

// activityHooksStatus 返回界面用的一句话状态，如 "Claude: 已安装 · Codex: 未安装"。
func activityHooksStatus() string {
	yn := func(ok bool) string {
		if ok {
			return "已安装"
		}
		return "未安装"
	}
	return fmt.Sprintf("Claude: %s · Codex: %s", yn(hookFileInstalled(claudeSettingsPath())), yn(hookFileInstalled(codexHooksPath())))
}
