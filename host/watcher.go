package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchCredentials 监听 ~/.codex、~/.claude 与 ~/.kimi-code/credentials 目录里凭据/配置文件的变动，
// 一旦变化（debounce 后）就催引擎立即用新配置重新取数显示。
// 监听目录而非具体文件：很多客户端是「写临时文件再 rename」，只有看目录才抓得到。
//
// 只认「内容真的变了」：每个关心的文件记一个指纹（fileFingerprint），debounce 到期时重算，
// 指纹没变就不触发。否则像 ReClaude daemon 每分钟重写 state.json 心跳这种无意义写入，
// 会让取数从 5 分钟一次变成每分钟一次，把网关打到 429。
func watchCredentials(ctx context.Context, e *Engine) {
	defer recoverLog("watchCredentials")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		e.log(LogWarn, "watch", "无法创建文件监听: %v", err)
		return
	}
	defer w.Close()

	dirs := []string{codexHome(), expandHome("~/.claude"), filepath.Join(kimiHome(), "credentials")}
	// ReClaude's daemon port is written to state.json when it starts/restarts.
	// Only add the directory when installed so normal users do not get a noisy
	// "directory not found" warning.
	reclaudeDir := reclaudeHome()
	if info, err := os.Stat(reclaudeDir); err == nil && info.IsDir() {
		dirs = append(dirs, reclaudeDir)
	}
	watched := 0
	for _, d := range dirs {
		if err := w.Add(d); err != nil {
			e.log(LogWarn, "watch", "监听 %s 失败: %v", d, err)
			continue
		}
		watched++
	}
	if watched == 0 {
		return
	}
	e.log(LogInfo, "watch", "已监听凭据目录变动（~/.codex、~/.claude、~/.kimi-code/credentials、~/.reclaude）")

	// 关心的文件名（不区分大小写）：变动这些才触发刷新。
	interesting := func(p string) bool {
		switch strings.ToLower(filepath.Base(p)) {
		case "auth.json", "config.toml", ".credentials.json", "settings.json", "kimi-code.json", "state.json", "ca.pem":
			return true
		}
		return false
	}

	// 启动时先记下现有文件的指纹，避免第一次无意义写入就触发一次刷新。
	fingerprints := map[string]string{}
	for _, d := range dirs {
		entries, _ := os.ReadDir(d)
		for _, ent := range entries {
			p := filepath.Join(d, ent.Name())
			if !ent.IsDir() && interesting(p) {
				fingerprints[strings.ToLower(p)] = fileFingerprint(p)
			}
		}
	}

	pending := map[string]string{} // debounce 期间攒下的待检查路径（小写 key → 原路径）
	var debounce *time.Timer
	fire := func() {
		var changed []string
		for key, p := range pending {
			fp := fileFingerprint(p)
			if fingerprints[key] != fp {
				fingerprints[key] = fp
				changed = append(changed, filepath.Base(p))
			}
			delete(pending, key)
		}
		if len(changed) == 0 {
			return // 只是被重写了一遍，内容/关键字段没变（如 state.json 心跳）
		}
		e.log(LogInfo, "watch", "检测到凭据/配置变动（%s），立即刷新", strings.Join(changed, ", "))
		e.Reload()
		if e.OnCredsChange != nil {
			e.OnCredsChange()
		}
	}

	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			if !interesting(ev.Name) {
				continue
			}
			pending[strings.ToLower(ev.Name)] = ev.Name
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(800*time.Millisecond, fire)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			e.log(LogWarn, "watch", "文件监听错误: %v", err)
		}
	}
}

// fileFingerprint 给一个凭据/配置文件算「是否真的变了」的指纹：
//   - ReClaude 的 state.json 只看 daemon 端口与 running（其余是每分钟更新的心跳/健康检查字段）；
//   - 其它文件取内容 sha256（原样重写不算变）。
//
// 文件不存在/读不到返回 "missing"，与之前存在时的指纹不同，删除也能触发刷新。
func fileFingerprint(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	if strings.EqualFold(filepath.Base(path), "state.json") {
		var s reclaudeState
		if json.Unmarshal(raw, &s) == nil {
			running := "?"
			if s.Daemon.Running != nil {
				running = fmt.Sprint(*s.Daemon.Running)
			}
			return fmt.Sprintf("port=%d last=%d running=%s", s.Daemon.Port, s.LastPort, running)
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
