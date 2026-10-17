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
	overlayUser32       = windows.NewLazySystemDLL("user32.dll")
	overlayGetRect      = overlayUser32.NewProc("GetWindowRect")
	overlayWorkArea     = overlayUser32.NewProc("SystemParametersInfoW")
	overlaySetPos       = overlayUser32.NewProc("SetWindowPos")
	overlayGetStyle     = overlayUser32.NewProc("GetWindowLongW")
	overlaySetStyle     = overlayUser32.NewProc("SetWindowLongW")
	overlayGetDPI       = overlayUser32.NewProc("GetDpiForWindow")
	overlaySetAlpha     = overlayUser32.NewProc("SetLayeredWindowAttributes")
	overlayGetCursor    = overlayUser32.NewProc("GetCursorPos")
	overlayMonitor      = overlayUser32.NewProc("MonitorFromRect")
	overlayMonitorInfo  = overlayUser32.NewProc("GetMonitorInfoW")
	overlaySetRegion    = overlayUser32.NewProc("SetWindowRgn")
	overlayGDI32        = windows.NewLazySystemDLL("gdi32.dll")
	overlayRoundRegion  = overlayGDI32.NewProc("CreateRoundRectRgn")
	overlayDeleteObject = overlayGDI32.NewProc("DeleteObject")
)

type overlayRect struct{ Left, Top, Right, Bottom int32 }

// 使用主显示器的工作区（避开任务栏），物理像素取自 HWND，兼容 DPI 缩放。
func positionDesktopOverlay(w fyne.Window, layer string, opacity int, savedX, savedY int32, positioned bool, design string) error {
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
		// TOPMOST 由 SetWindowPos 管理，直接写该样式会导致窗口层级与样式不一致。
		style = (style | 0x00080000 | 0x00000080 | 0x08000000) &^ 0x00040000 // LAYERED | TOOLWINDOW | NOACTIVATE
		overlaySetStyle.Call(c.HWND, uintptr(index), style)
		if r, _, err := overlaySetAlpha.Call(c.HWND, 0, uintptr(max(0, min(100, opacity))*255/100), 2); r == 0 { // LWA_ALPHA；卡片背景不使用颜色键。
			result = fmt.Errorf("设置悬浮窗不透明度: %w", err)
			return
		}
		margin := int32(16)
		radius := int32(overlayDesignFor(design).radius)
		if overlayGetDPI.Find() == nil {
			if dpi, _, _ := overlayGetDPI.Call(c.HWND); dpi > 0 {
				margin = int32(16 * dpi / 96)
				radius = int32(uintptr(radius) * dpi / 96)
			}
		}
		region, _, err := overlayRoundRegion.Call(0, 0, uintptr(rect.Right-rect.Left+1), uintptr(rect.Bottom-rect.Top+1), uintptr(radius*2), uintptr(radius*2))
		if region == 0 {
			result = fmt.Errorf("创建悬浮窗圆角: %w", err)
			return
		}
		if r, _, err := overlaySetRegion.Call(c.HWND, region, 1); r == 0 {
			overlayDeleteObject.Call(region)
			result = fmt.Errorf("设置悬浮窗圆角: %w", err)
			return
		}
		x := work.Right - (rect.Right - rect.Left) - margin
		if x < work.Left {
			x = work.Left
		}
		y := work.Top + margin
		if positioned {
			x, y = savedX, savedY
			candidate := overlayRect{x, y, x + rect.Right - rect.Left, y + rect.Bottom - rect.Top}
			// 保留跨屏和负坐标；显示器拔掉导致整个窗口不可见时再恢复到最近屏幕。
			if monitor, _, _ := overlayMonitor.Call(uintptr(unsafe.Pointer(&candidate)), 0); monitor == 0 {
				monitor, _, _ = overlayMonitor.Call(uintptr(unsafe.Pointer(&candidate)), 2)
				info := struct {
					Size          uint32
					Monitor, Work overlayRect
					Flags         uint32
				}{}
				info.Size = uint32(unsafe.Sizeof(info))
				if r, _, _ := overlayMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); r != 0 {
					x = max(info.Work.Left, min(x, info.Work.Right-(rect.Right-rect.Left)))
					y = max(info.Work.Top, min(y, info.Work.Bottom-(rect.Bottom-rect.Top)))
				}
			}
		}
		after := ^uintptr(0) // HWND_TOPMOST
		if layer == "bottom" {
			after = 1 // HWND_BOTTOM，同时取消置顶；NOACTIVATE 防止点击后抢到前面。
		}
		// SWP_NOSIZE | SWP_NOACTIVATE：尺寸始终由 Fyne 管理，避免原生边框更新干扰渲染视口。
		if r, _, err := overlaySetPos.Call(c.HWND, after, uintptr(x), uintptr(y), 0, 0, 0x0011); r == 0 {
			result = fmt.Errorf("定位悬浮窗: %w", err)
		}
	})
	return result
}

func desktopOverlayCursor() (overlayPoint, error) {
	var cursor overlayPoint
	if r, _, err := overlayGetCursor.Call(uintptr(unsafe.Pointer(&cursor))); r == 0 {
		return cursor, err
	}
	return cursor, nil
}

func withDesktopOverlayHandle(w fyne.Window, fn func(uintptr) error) error {
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
		result = fn(c.HWND)
	})
	return result
}

func beginDesktopOverlayDrag(w fyne.Window) (overlayDragOrigin, error) {
	var origin overlayDragOrigin
	err := withDesktopOverlayHandle(w, func(hwnd uintptr) error {
		var rect overlayRect
		if r, _, err := overlayGetRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); r == 0 {
			return err
		}
		if r, _, err := overlayGetCursor.Call(uintptr(unsafe.Pointer(&origin.Cursor))); r == 0 {
			return err
		}
		origin.Window = overlayPoint{rect.Left, rect.Top}
		return nil
	})
	return origin, err
}

func moveDesktopOverlayDrag(w fyne.Window, origin overlayDragOrigin) (overlayPoint, error) {
	var point overlayPoint
	err := withDesktopOverlayHandle(w, func(hwnd uintptr) error {
		var cursor overlayPoint
		if r, _, err := overlayGetCursor.Call(uintptr(unsafe.Pointer(&cursor))); r == 0 {
			return err
		}
		point = overlayPoint{origin.Window.X + cursor.X - origin.Cursor.X, origin.Window.Y + cursor.Y - origin.Cursor.Y}
		if r, _, err := overlaySetPos.Call(hwnd, 0, uintptr(point.X), uintptr(point.Y), 0, 0, 0x0015); r == 0 {
			return err
		}
		return nil
	})
	return point, err
}
