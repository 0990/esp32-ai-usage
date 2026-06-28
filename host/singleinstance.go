package main

import (
	"net"
	"time"
)

// 单实例锁兼唤起通道：用一个固定的本地回环端口同时充当「锁」和「IPC」。
// 谁先成功 Listen 谁就是主实例；后启动的实例 Listen 失败，转而 Dial 主实例、
// 发一行 "show" 让其前置窗口，然后自己退出。回环端口不会触发防火墙弹窗。
const singleInstanceAddr = "127.0.0.1:52735"

// acquireSingleInstance 尝试成为主实例。
// 返回 (listener, true) 表示本进程是主实例（需保留 listener）；
// 返回 (nil, false) 表示已有实例在运行（已通知其显示窗口），本进程应退出。
func acquireSingleInstance() (net.Listener, bool) {
	ln, err := net.Listen("tcp", singleInstanceAddr)
	if err == nil {
		return ln, true
	}
	// 端口被占用：大概率是已有实例。通知它把窗口拉到前台。
	if c, e := net.DialTimeout("tcp", singleInstanceAddr, 2*time.Second); e == nil {
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Write([]byte("show\n"))
		_ = c.Close()
	}
	return nil, false
}

// serveSingleInstance 在 listener 上接受后续实例的「唤起」连接，每来一个就回调 onShow。
// 阻塞运行，直到 listener 被关闭（程序退出）。
func serveSingleInstance(ln net.Listener, onShow func()) {
	defer recoverLog("serveSingleInstance")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener 已关闭
		}
		conn.Close()
		if onShow != nil {
			onShow()
		}
	}
}
