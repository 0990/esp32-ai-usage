//go:build !windows

package main

import "fyne.io/fyne/v2"

const desktopOverlaySupported = false

func positionDesktopOverlay(fyne.Window, string, int, int32, int32, bool, string) error { return nil }
func desktopOverlayCursor() (overlayPoint, error)                                       { return overlayPoint{}, nil }
func beginDesktopOverlayDrag(fyne.Window) (overlayDragOrigin, error)                    { return overlayDragOrigin{}, nil }
func moveDesktopOverlayDrag(fyne.Window, overlayDragOrigin) (overlayPoint, error) {
	return overlayPoint{}, nil
}
