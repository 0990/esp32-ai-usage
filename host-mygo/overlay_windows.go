//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

var overlayUser32 = windows.NewLazySystemDLL("user32.dll")
var overlayGetRect = overlayUser32.NewProc("GetWindowRect")
var overlaySetPos = overlayUser32.NewProc("SetWindowPos")
var overlayGetStyle = overlayUser32.NewProc("GetWindowLongW")
var overlaySetStyle = overlayUser32.NewProc("SetWindowLongW")
var overlayWorkArea = overlayUser32.NewProc("SystemParametersInfoW")
var overlayGetDPI = overlayUser32.NewProc("GetDpiForWindow")
var overlayMonitor = overlayUser32.NewProc("MonitorFromRect")
var overlayMonitorInfo = overlayUser32.NewProc("GetMonitorInfoW")
var overlaySetRegion = overlayUser32.NewProc("SetWindowRgn")
var overlayGDI32 = windows.NewLazySystemDLL("gdi32.dll")
var overlayRoundRegion = overlayGDI32.NewProc("CreateRoundRectRgn")
var overlayDeleteObject = overlayGDI32.NewProc("DeleteObject")
var overlayDwm = windows.NewLazySystemDLL("dwmapi.dll")
var overlayDwmAttribute = overlayDwm.NewProc("DwmSetWindowAttribute")

type overlayRect struct{ Left, Top, Right, Bottom int32 }

func nativeOverlayPosition(w *mygo.Window) (int32, int32, error) {
	var r overlayRect
	if n, _, err := overlayGetRect.Call(w.NativeHandle(), uintptr(unsafe.Pointer(&r))); n == 0 {
		return 0, 0, err
	}
	return r.Left, r.Top, nil
}

func positionNativeOverlay(w *mygo.Window, c Config, reposition, toolWindow bool) error {
	hwnd := w.NativeHandle()
	if hwnd == 0 {
		return fmt.Errorf("悬浮窗尚未创建")
	}
	var rect, work overlayRect
	if n, _, err := overlayGetRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); n == 0 {
		return err
	}
	index := int32(-20)
	style, _, _ := overlayGetStyle.Call(hwnd, uintptr(index))
	if toolWindow {
		style = (style | 0x80 | 0x08000000) &^ 0x00040000
	} else {
		style = (style | 0x00040000) &^ (0x80 | 0x08000000)
	}
	overlaySetStyle.Call(hwnd, uintptr(index), style) // 工具窗且不抢焦点；不使用 WebView 或颜色键透明。
	dpi := uintptr(96)
	if overlayGetDPI.Find() == nil {
		if value, _, _ := overlayGetDPI.Call(hwnd); value > 0 {
			dpi = value
		}
	}
	radius := uintptr(overlayDesignFor(c.OverlayStyle).radius) * dpi / 96
	// Windows 11 的系统圆角保留 DWM 阴影与隐藏的缩放边缘；旧系统再用窗口区域。
	var corner uint32 = 2 // DWMWCP_ROUND
	hr, _, _ := overlayDwmAttribute.Call(hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	if int32(hr) == 0 {
		overlaySetRegion.Call(hwnd, 0, 1)
	} else {
		region, _, err := overlayRoundRegion.Call(0, 0, uintptr(rect.Right-rect.Left+1), uintptr(rect.Bottom-rect.Top+1), radius*2, radius*2)
		if region == 0 {
			return err
		}
		if n, _, err := overlaySetRegion.Call(hwnd, region, 1); n == 0 {
			overlayDeleteObject.Call(region)
			return err
		}
	}
	x, y := rect.Left, rect.Top
	flags := uintptr(0x0013) // NOSIZE | NOMOVE | NOACTIVATE
	if reposition {
		flags = 0x0011
		if c.OverlayPositionSet {
			x, y = c.OverlayX, c.OverlayY
		} else {
			if n, _, err := overlayWorkArea.Call(0x0030, 0, uintptr(unsafe.Pointer(&work)), 0); n == 0 {
				return err
			}
			margin := int32(16 * dpi / 96)
			x = max(work.Left, work.Right-(rect.Right-rect.Left)-margin)
			y = work.Top + margin
		}
		candidate := overlayRect{x, y, x + rect.Right - rect.Left, y + rect.Bottom - rect.Top}
		if monitor, _, _ := overlayMonitor.Call(uintptr(unsafe.Pointer(&candidate)), 0); monitor == 0 {
			monitor, _, _ = overlayMonitor.Call(uintptr(unsafe.Pointer(&candidate)), 2)
			info := struct {
				Size          uint32
				Monitor, Work overlayRect
				Flags         uint32
			}{}
			info.Size = uint32(unsafe.Sizeof(info))
			if n, _, _ := overlayMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); n != 0 {
				x = max(info.Work.Left, min(x, info.Work.Right-(rect.Right-rect.Left)))
				y = max(info.Work.Top, min(y, info.Work.Bottom-(rect.Bottom-rect.Top)))
			}
		}
	}
	after := ^uintptr(0)
	if c.OverlayLayer == "bottom" {
		after = 1
	}
	if n, _, err := overlaySetPos.Call(hwnd, after, uintptr(x), uintptr(y), 0, 0, flags); n == 0 {
		return err
	}
	return nil
}
