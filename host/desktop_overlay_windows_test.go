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
	// Exercise a shown but fully transparent window, like the real splash window.
	if err := positionDesktopOverlay(overlayNativeTestWindow{hwnd: hwnd}, "top", 0, 0, 0, false, "strip"); err != nil {
		t.Fatal(err)
	}
	overlayUser32.NewProc("ShowWindow").Call(hwnd, 4)
	if err := positionDesktopOverlay(overlayNativeTestWindow{hwnd: hwnd}, "top", 0, 0, 0, false, "strip"); err != nil {
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
	if err := positionDesktopOverlay(overlayNativeTestWindow{hwnd: hwnd}, "bottom", 0, 200, 150, true, "obsidian"); err != nil {
		t.Fatal(err)
	}
	style, _, _ = overlayGetStyle.Call(hwnd, uintptr(index))
	if style&0x00000008 != 0 || style&0x08000080 != 0x08000080 {
		t.Fatalf("bottom layer must cancel topmost and preserve no-activate/toolwindow: %x", style)
	}
	overlayGetRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if rect.Left != 200 || rect.Top != 150 {
		t.Fatalf("custom position was ignored: %+v", rect)
	}
	var key, flags uint32
	var alpha byte
	if r, _, err := overlayUser32.NewProc("GetLayeredWindowAttributes").Call(hwnd, uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&alpha)), uintptr(unsafe.Pointer(&flags))); r == 0 || flags != 2 {
		t.Fatalf("solid card must not use color-key transparency: key=%x flags=%x error=%v", key, flags, err)
	}
	region, _, _ := overlayGDI32.NewProc("CreateRectRgn").Call(0, 0, 1, 1)
	defer overlayDeleteObject.Call(region)
	if r, _, err := overlayUser32.NewProc("GetWindowRgn").Call(hwnd, region); r == 0 {
		t.Fatal(err)
	}
	corner, _, _ := overlayGDI32.NewProc("PtInRegion").Call(region, 0, 0)
	center, _, _ := overlayGDI32.NewProc("PtInRegion").Call(region, 170, 140)
	if corner != 0 || center == 0 {
		t.Fatal("native card region must clip corners and retain the center")
	}
	if err := positionDesktopOverlay(overlayNativeTestWindow{hwnd: hwnd}, "top", 0, 200, 150, true, "paper"); err != nil {
		t.Fatal(err)
	}
	style, _, _ = overlayGetStyle.Call(hwnd, uintptr(index))
	if style&required != required {
		t.Fatalf("switching back to top must restore topmost: %x", style)
	}
	origin, err := beginDesktopOverlayDrag(overlayNativeTestWindow{hwnd: hwnd})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate cursor displacement without moving the user's pointer.
	origin.Cursor.X -= 40
	origin.Cursor.Y -= 20
	point, err := moveDesktopOverlayDrag(overlayNativeTestWindow{hwnd: hwnd}, origin)
	if err != nil {
		t.Fatal(err)
	}
	overlayGetRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	if point.X != 240 || point.Y != 170 || rect.Left != point.X || rect.Top != point.Y {
		t.Fatalf("drag must use screen cursor deltas: point=%+v rect=%+v", point, rect)
	}
}
