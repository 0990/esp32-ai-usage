//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"golang.org/x/sys/windows"
)

const desktopOverlaySupported = true

var (
	overlayUser32   = windows.NewLazySystemDLL("user32.dll")
	overlayGetRect  = overlayUser32.NewProc("GetWindowRect")
	overlayWorkArea = overlayUser32.NewProc("SystemParametersInfoW")
	overlaySetPos   = overlayUser32.NewProc("SetWindowPos")
	overlayGetStyle = overlayUser32.NewProc("GetWindowLongW")
	overlaySetStyle = overlayUser32.NewProc("SetWindowLongW")
	overlayGetDPI   = overlayUser32.NewProc("GetDpiForWindow")
)

type overlayRect struct{ Left, Top, Right, Bottom int32 }

// 使用主显示器的工作区（避开任务栏），物理像素取自 HWND，兼容 DPI 缩放。
func positionDesktopOverlay(w fyne.Window) error {
	native, ok := w.(driver.NativeWindow)
	if !ok {
		return fmt.Errorf("当前驱动不支持原生 Windows 窗口")
	}
	var result error
	native.RunNative(func(ctx any) {
		c, ok := ctx.(driver.WindowsWindowContext)
		if !ok || c.HWND == 0 {
			result = fmt.Errorf("悬浮窗尚未创建")
			return
		}
		var work, rect overlayRect
		if r, _, err := overlayWorkArea.Call(0x0030, 0, uintptr(unsafe.Pointer(&work)), 0); r == 0 { // SPI_GETWORKAREA
			result = fmt.Errorf("读取桌面工作区: %w", err)
			return
		}
		if r, _, err := overlayGetRect.Call(c.HWND, uintptr(unsafe.Pointer(&rect))); r == 0 {
			result = fmt.Errorf("读取悬浮窗大小: %w", err)
			return
		}
		// GWL_EXSTYLE 是 32 位样式字段；Get/SetWindowLongW 在 32/64 位 Windows 均可用。
		index := int32(-20)
		style, _, _ := overlayGetStyle.Call(c.HWND, uintptr(index))
		style = (style | 0x00000008 | 0x00000080 | 0x08000000) &^ 0x00040000 // TOPMOST | TOOLWINDOW | NOACTIVATE，移除 APPWINDOW
		overlaySetStyle.Call(c.HWND, uintptr(index), style)
		margin := int32(16)
		if overlayGetDPI.Find() == nil {
			if dpi, _, _ := overlayGetDPI.Call(c.HWND); dpi > 0 {
				margin = int32(16 * dpi / 96)
			}
		}
		x := work.Right - (rect.Right - rect.Left) - margin
		if x < work.Left {
			x = work.Left
		}
		y := work.Top + margin
		// HWND_TOPMOST，SWP_NOSIZE | SWP_NOACTIVATE | SWP_FRAMECHANGED：
		// 应用扩展样式并定位，更新时不抢键盘焦点。
		if r, _, err := overlaySetPos.Call(c.HWND, ^uintptr(0), uintptr(x), uintptr(y), 0, 0, 0x0031); r == 0 {
			result = fmt.Errorf("定位悬浮窗: %w", err)
		}
	})
	return result
}
