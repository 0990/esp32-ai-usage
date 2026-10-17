//go:build !windows

package main

import "github.com/egoist/mygo"

type overlayTooltip struct{}

func (*overlayTooltip) update(*mygo.Window, string) {}
func (*overlayTooltip) destroy()                    {}
