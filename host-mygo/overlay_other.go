//go:build !windows

package main

import "github.com/egoist/mygo"

func nativeOverlayPosition(w *mygo.Window) (int32, int32, error) {
	x, y := w.Position()
	return int32(x), int32(y), nil
}
func positionNativeOverlay(w *mygo.Window, c Config, reposition, toolWindow bool) error {
	if reposition {
		if c.OverlayPositionSet {
			w.SetPosition(int(c.OverlayX), int(c.OverlayY))
		} else {
			d := mygo.Screen.PrimaryDisplay()
			w.SetPosition(d.WorkArea.X+d.WorkArea.Width-c.OverlayWidth-16, d.WorkArea.Y+16)
		}
	}
	return nil
}
