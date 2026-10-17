//go:build windows

package main

import (
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

var (
	overlayCreateWindow  = overlayUser32.NewProc("CreateWindowExW")
	overlayDestroyWindow = overlayUser32.NewProc("DestroyWindow")
	overlaySendMessage   = overlayUser32.NewProc("SendMessageW")
	overlayInitControls  = windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx")
	overlayFindChild     = overlayUser32.NewProc("FindWindowExW")
)

// Native tooltips can extend beyond a 24px-high or 44px-wide overlay.
// https://learn.microsoft.com/en-us/windows/win32/controls/tooltip-controls
type overlayTooltip struct {
	hwnd   uintptr
	text   string
	buffer []uint16 // Keep the TOOLINFO text alive while the native control uses it.
	info   overlayToolInfo
}

type overlayToolInfo struct {
	Size, Flags uint32
	Window, ID  uintptr
	Rect        overlayRect
	Instance    uintptr
	Text        *uint16
	Param       uintptr
	Reserved    uintptr
}

const (
	overlayTTMAddTool     = 0x400 + 50
	overlayTTMUpdateText  = 0x400 + 57
	overlayTTMSetMaxWidth = 0x400 + 24
	overlayTTMSetDelay    = 0x400 + 3
)

func (t *overlayTooltip) update(w *mygo.Window, text string) {
	if w == nil || w.IsDestroyed() || (t.hwnd != 0 && t.text == text) {
		return
	}
	buffer, err := windows.UTF16FromString(text)
	if err != nil {
		return
	}
	if t.hwnd == 0 {
		controls := struct{ Size, Classes uint32 }{8, 4} // ICC_BAR_CLASSES includes tooltips.
		if r, _, _ := overlayInitControls.Call(uintptr(unsafe.Pointer(&controls))); r == 0 {
			return
		}
		class := windows.StringToUTF16Ptr("tooltips_class32")
		// WS_EX_NOACTIVATE; WS_POPUP | TTS_ALWAYSTIP | TTS_NOPREFIX.
		t.hwnd, _, _ = overlayCreateWindow.Call(0x08000000, uintptr(unsafe.Pointer(class)), 0, 0x80000003, 0, 0, 0, 0, w.NativeHandle(), 0, 0, 0)
		if t.hwnd == 0 {
			return
		}
		// Mouse messages go to MyGo's child surface rather than its top-level window.
		surfaceClass := windows.StringToUTF16Ptr("MyGoSurface")
		surface, _, _ := overlayFindChild.Call(w.NativeHandle(), 0, uintptr(unsafe.Pointer(surfaceClass)), 0)
		if surface == 0 {
			t.destroy()
			return
		}
		t.info = overlayToolInfo{Flags: 0x11, Window: w.NativeHandle(), ID: surface} // TTF_IDISHWND | TTF_SUBCLASS
		t.info.Size = uint32(unsafe.Sizeof(t.info))
		t.buffer, t.text = buffer, text
		t.info.Text = &t.buffer[0]
		if r, _, _ := overlaySendMessage.Call(t.hwnd, overlayTTMAddTool, 0, uintptr(unsafe.Pointer(&t.info))); r == 0 {
			t.destroy()
			return
		}
		width := uintptr(300)
		if overlayGetDPI.Find() == nil {
			if dpi, _, _ := overlayGetDPI.Call(w.NativeHandle()); dpi > 0 {
				width = width * dpi / 96
			}
		}
		overlaySendMessage.Call(t.hwnd, overlayTTMSetMaxWidth, 0, width)
		overlaySendMessage.Call(t.hwnd, overlayTTMSetDelay, 3, 600) // TTDT_INITIAL
		overlaySetPos.Call(t.hwnd, ^uintptr(0), 0, 0, 0, 0, 0x0013) // TOPMOST, NOSIZE | NOMOVE | NOACTIVATE
		return
	}
	// Update synchronously before releasing the previous text buffer.
	t.info.Text = &buffer[0]
	overlaySendMessage.Call(t.hwnd, overlayTTMUpdateText, 0, uintptr(unsafe.Pointer(&t.info)))
	t.buffer, t.text = buffer, text
}

func (t *overlayTooltip) destroy() {
	if t.hwnd != 0 {
		overlayDestroyWindow.Call(t.hwnd)
	}
	*t = overlayTooltip{}
}
