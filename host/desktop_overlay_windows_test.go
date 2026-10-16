//go:build windows

package main

import (
	"runtime"
	"testing"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"golang.org/x/sys/windows"
)

type overlayNativeTestWindow struct {
	fyne.Window
	hwnd uintptr
}

func (w overlayNativeTestWindow) RunNative(f func(any)) {
	f(driver.WindowsWindowContext{HWND: w.hwnd})
}

func TestDesktopOverlayWindowsPlacement(t *testing.T) {
	// A fully transparent native window exercises Windows placement/styles without
	// launching the host, reading credentials, or touching the user's windows.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	create := overlayUser32.NewProc("CreateWindowExW")
	hwnd, _, err := create.Call(0x00080080, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 340, 280, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("create hidden window: %v", err)
	}
	defer overlayUser32.NewProc("DestroyWindow").Call(hwnd)
	if r, _, err := overlayUser32.NewProc("SetLayeredWindowAttributes").Call(hwnd, 0, 0, 2); r == 0 {
		t.Fatalf("make test window transparent: %v", err)
	}
	foreground := overlayUser32.NewProc("GetForegroundWindow")
	before, _, _ := foreground.Call()
	overlayUser32.NewProc("ShowWindow").Call(hwnd, 4) // SW_SHOWNOACTIVATE
	if err := positionDesktopOverlay(overlayNativeTestWindow{hwnd: hwnd}); err != nil {
		t.Fatal(err)
	}
	var rect, work overlayRect
	overlayGetRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	overlayWorkArea.Call(0x0030, 0, uintptr(unsafe.Pointer(&work)), 0)
	if rect.Left < work.Left || rect.Right > work.Right || rect.Top < work.Top || rect.Top > work.Top+100 {
		t.Fatalf("overlay outside top-right work area: rect=%+v work=%+v", rect, work)
	}
	if work.Right-rect.Right > 100 || rect.Right-rect.Left != 340 {
		t.Fatalf("overlay not anchored right, or resized: rect=%+v work=%+v", rect, work)
	}
	index := int32(-20)
	style, _, _ := overlayGetStyle.Call(hwnd, uintptr(index))
	const required = 0x00000008 | 0x00000080 | 0x08000000 // TOPMOST | TOOLWINDOW | NOACTIVATE
	if style&required != required || style&0x00040000 != 0 {
		t.Fatalf("incorrect overlay window styles: %x", style)
	}
	after, _, _ := foreground.Call()
	if after != before {
		t.Fatal("positioning stole foreground focus")
	}
}
