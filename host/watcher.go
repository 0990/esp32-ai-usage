package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchCredentials 监听 ~/.codex 与 ~/.claude 目录里凭据/配置文件的变动，
// 一旦变化（debounce 后）就催引擎立即用新配置重新取数显示。
// 监听目录而非具体文件：很多客户端是「写临时文件再 rename」，只有看目录才抓得到。
func watchCredentials(ctx context.Context, e *Engine) {
	defer recoverLog("watchCredentials")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		e.log(LogWarn, "watch", "无法创建文件监听: %v", err)
		return
	}
	defer w.Close()

	dirs := []string{codexHome(), expandHome("~/.claude")}
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
	e.log(LogInfo, "watch", "已监听凭据目录变动（~/.codex、~/.claude）")

	// 关心的文件名（不区分大小写）：变动这些才触发刷新。
	interesting := func(p string) bool {
		name := strings.ToLower(filepath.Base(p))
		switch name {
		case "auth.json", "config.toml", ".credentials.json":
			return true
		}
		return false
	}

	var debounce *time.Timer
	fire := func() {
		e.log(LogInfo, "watch", "检测到凭据/配置变动，立即刷新")
		e.Reload()
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
