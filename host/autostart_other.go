//go:build !windows

package main

// 非 Windows 平台暂不支持开机启动，保持空实现以便跨平台编译。
func setAutostart(enable bool) error { return nil }
