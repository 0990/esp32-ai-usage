//go:build !windows

package main

import "fyne.io/fyne/v2"

const desktopOverlaySupported = false

func positionDesktopOverlay(fyne.Window) error { return nil }
