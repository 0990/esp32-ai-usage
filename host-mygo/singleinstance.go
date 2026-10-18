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
// 发一行 "show"（手动启动）或 "ping"（静默自启）并收到确认后才退出。
// 回环端口不会触发防火墙弹窗。
var singleInstanceAddrs = []string{"127.0.0.1:42737", "127.0.0.1:32737", "127.0.0.1:22737"}

const singleInstanceAck = "esp32-ai-usage-mygo:shown\n"
const singleInstancePingAck = "esp32-ai-usage-mygo:running\n"

// acquireSingleInstance 尝试成为主实例。
// 返回 (listener, true) 表示本进程是主实例（需保留 listener）；
// 返回 (nil, false) 表示已有实例在运行，本进程应退出；show 控制是否唤起窗口。
func acquireSingleInstance(show bool) (net.Listener, bool) {
	return acquireSingleInstanceAt(singleInstanceAddrs, show)
}

func acquireSingleInstanceAt(addrs []string, show bool) (net.Listener, bool) {
	// 先检查所有候选端口，避免系统保留范围变化后漏掉使用备用端口的实例。
	for _, addr := range addrs {
		if notifySingleInstance(addr, show) == nil {
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
		if notifySingleInstance(addr, show) == nil {
			return nil, false
		}
		writeLogLine(fmt.Sprintf("[WARN][instance] 单实例端口不可用 %s: %v", addr, err))
	}
	writeLogLine("[WARN][instance] 所有单实例端口均不可用，继续启动界面")
	return nil, true
}

func notifySingleInstance(addr string, show bool) error {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	request, expectedAck := "ping\n", singleInstancePingAck
	if show {
		request, expectedAck = "show\n", singleInstanceAck
	}
	if _, err := io.WriteString(c, request); err != nil {
		return err
	}
	ack, err := bufio.NewReader(io.LimitReader(c, 64)).ReadString('\n')
	if err != nil {
		return err
	}
	if ack != expectedAck {
		return fmt.Errorf("不是额度表实例的响应")
	}
	return nil
}

// serveSingleInstance 接受后续实例的连接，仅 show 请求回调 onShow。
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
	if err != nil {
		return
	}
	switch request {
	case "show\n":
		if onShow != nil {
			onShow()
		}
		_, _ = io.WriteString(conn, singleInstanceAck)
	case "ping\n":
		_, _ = io.WriteString(conn, singleInstancePingAck)
	}
}
