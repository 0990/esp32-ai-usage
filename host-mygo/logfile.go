package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// 把日志与 panic 落盘到 ~/.esp32-ai-usage/app.log。
// 由于发布版用 -H=windowsgui（无控制台），崩溃/日志在界面外是看不到的；
// 这个文件让活动日志与任何 panic 堆栈都持久化，便于事后排查。

const maxLogFileBytes = 1 << 20 // 超过 1MB 就清空重来，避免无限增长

var (
	logMu   sync.Mutex
	logFile *os.File
)

func logFilePath() string {
	return filepath.Join(filepath.Dir(defaultConfigPath()), "app.log")
}

// initFileLog 打开（必要时创建）日志文件；超大则先清空。可重复调用。
func initFileLog() {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		return
	}
	p := logFilePath()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogFileBytes {
		_ = os.Remove(p)
	}
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		logFile = f
		fmt.Fprintf(f, "\n%s [INFO][log] ==== 启动，日志文件: %s ====\n", time.Now().Format("2006-01-02 15:04:05"), p)
	}
}

// writeLogLine 追加一行到日志文件（线程安全；未初始化则忽略）。
func writeLogLine(s string) {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return
	}
	fmt.Fprintln(logFile, s)
}

// recoverLog 作为 goroutine/主流程的 defer：捕获 panic、把堆栈写入日志文件，避免整程序静默消失。
// where 标识出事地点。recover 后该 goroutine 会正常返回（其余部分继续运行）。
func recoverLog(where string) {
	if r := recover(); r != nil {
		writeLogLine(fmt.Sprintf("%s [PANIC][%s] %v\n%s",
			time.Now().Format("2006-01-02 15:04:05"), where, r, debug.Stack()))
	}
}
