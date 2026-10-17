package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"time"
)

// 单实例锁兼唤起通道：用本地回环端口同时充当「锁」和「IPC」。
// 系统可能保留某个端口，因此提供备用端口；后启动的实例先查找主实例，
// 发一行 "show" 并收到确认后才退出。回环端口不会触发防火墙弹窗。
var singleInstanceAddrs = []string{"127.0.0.1:42737", "127.0.0.1:32737", "127.0.0.1:22737"}

const singleInstanceAck = "esp32-ai-usage-mygo:shown\n"

// acquireSingleInstance 尝试成为主实例。
// 返回 (listener, true) 表示本进程是主实例（需保留 listener）；
// 返回 (nil, false) 表示已有实例在运行（已通知其显示窗口），本进程应退出。
func acquireSingleInstance() (net.Listener, bool) {
	return acquireSingleInstanceAt(singleInstanceAddrs)
}

func acquireSingleInstanceAt(addrs []string) (net.Listener, bool) {
	// 先检查所有候选端口，避免系统保留范围变化后漏掉使用备用端口的实例。
	for _, addr := range addrs {
		if notifySingleInstance(addr) == nil {
			return nil, false
		}
	}
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			writeLogLine(fmt.Sprintf("[INFO][instance] 单实例监听: %s", addr))
			return ln, true
		}
		// 只有收到本程序的确认才退出；端口也可能被系统保留或被其他程序占用。
		if notifySingleInstance(addr) == nil {
			return nil, false
		}
		writeLogLine(fmt.Sprintf("[WARN][instance] 单实例端口不可用 %s: %v", addr, err))
	}
	writeLogLine("[WARN][instance] 所有单实例端口均不可用，继续启动界面")
	return nil, true
}

func notifySingleInstance(addr string) error {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, "show\n"); err != nil {
		return err
	}
	ack, err := bufio.NewReader(io.LimitReader(c, 64)).ReadString('\n')
	if err != nil {
		return err
	}
	if ack != singleInstanceAck {
		return fmt.Errorf("不是额度表实例的响应")
	}
	return nil
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
		handleSingleInstance(conn, onShow)
	}
}

func handleSingleInstance(conn net.Conn, onShow func()) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	request, err := bufio.NewReader(io.LimitReader(conn, 64)).ReadString('\n')
	if err != nil || request != "show\n" {
		return
	}
	if onShow != nil {
		onShow()
	}
	_, _ = io.WriteString(conn, singleInstanceAck)
}
