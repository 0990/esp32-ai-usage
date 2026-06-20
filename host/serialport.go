package main

import (
	"fmt"
	"os"
	"strings"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// Espressif 原生 USB 的厂商 ID（ESP32-C3 / S3 自带 USB-CDC）。
const espressifVID = "303A"

// findPort 决定串口名：configured 非 AUTO 时直接用；否则按 Espressif VID 自动识别。
func findPort(configured string) (string, error) {
	if configured != "" && !strings.EqualFold(configured, "AUTO") {
		return configured, nil
	}
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, espressifVID) {
			return p.Name, nil // 命中 ESP32-C3/S3 原生 USB
		}
		candidates = append(candidates, p.Name)
	}
	if len(candidates) == 1 {
		return candidates[0], nil // 只有一个串口就用它
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("未发现任何串口；请插好 ESP32 或在 config 里写明 port")
	}
	fmt.Fprintf(os.Stderr, "[warn] 未能自动识别 ESP32（找 VID=%s）。可用串口: %s\n",
		espressifVID, strings.Join(candidates, ", "))
	return "", fmt.Errorf("请在 config.json 的 port 写明具体串口（如 COM5）")
}

// openSerial 打开串口。
func openSerial(portName string, baud int) (serial.Port, error) {
	port, err := serial.Open(portName, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("打开串口 %s 失败: %w", portName, err)
	}
	return port, nil
}
