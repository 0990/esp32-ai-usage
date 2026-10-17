package main

import (
	"fmt"
	"strings"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// Espressif 原生 USB 的厂商 ID（ESP32-C3 / S3 自带 USB-CDC）。
const espressifVID = "303A"

// desiredPort 计算目标串口：configured 非 AUTO 时直接用配置值（不做存在性检查，由打开重试兜底）。
// AUTO 时枚举：优先 Espressif VID（ESP32-C3/S3 原生 USB）；否则仅在「恰好一个 USB 串口」时
// 用它兜底（CH340/CP210x 等）。COM1 这类主板自带口不是 USB 设备，绝不兜底选中——
// 否则会把数据写到错误的口上，且写不报错、占住不释放，重插 ESP32 也切不回来。
// candidates 返回枚举到的全部串口名，供日志提示。
func desiredPort(configured string) (name string, ok bool, candidates []string) {
	if configured != "" && !strings.EqualFold(configured, "AUTO") {
		return configured, true, nil
	}
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", false, nil
	}
	var usb []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, espressifVID) {
			return p.Name, true, nil // 命中 ESP32-C3/S3 原生 USB
		}
		if p.IsUSB {
			usb = append(usb, p.Name)
		}
		candidates = append(candidates, p.Name)
	}
	if len(usb) == 1 {
		return usb[0], true, candidates // 只有一个 USB 串口就用它
	}
	return "", false, candidates
}

// openSerial 打开串口。
func openSerial(portName string, baud int) (serial.Port, error) {
	port, err := serial.Open(portName, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("打开串口 %s 失败: %w", portName, err)
	}
	return port, nil
}
