//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// 开机启动通过当前用户的 Run 注册表项实现（无需管理员权限）。
const (
	autostartName       = "ESP32AICredits"
	legacyAutostartName = "ESP32AICreditsMyGo"
	autostartRunPath    = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// setAutostart 写入/删除 Run 注册表项，指向当前可执行文件。
func setAutostart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := k.SetStringValue(autostartName, `"`+exe+`"`); err != nil { // 路径带空格需加引号
			return err
		}
	} else if err := k.DeleteValue(autostartName); err != nil && err != registry.ErrNotExist {
		return err
	}
	// 两种 UI 版本共用一个自启项，清理旧 MyGo 名称以免重复启动。
	if err := k.DeleteValue(legacyAutostartName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}
