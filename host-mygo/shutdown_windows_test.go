package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// The native event loop must run on the main goroutine, in its own process.
func TestMain(m *testing.M) {
	if path := os.Getenv("ESP32_MYGO_COMPACT_HELPER_CONFIG"); path != "" {
		mygo.App.WhenReady(func() {
			g := newGUI(loadConfig(path), path, true)
			g.applyOverlay()
			for _, style := range []string{"horizontal", "paper", "strip", "vertical", "horizontal", "vertical"} {
				if err := g.engine.SetOverlayStyle(style); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				g.overlay.apply(g.engine.Config(), false)
				w, h := g.overlay.win.Size()
				d := overlayDesignFor(style)
				if w != int(d.width) || h != int(d.height) {
					index := int32(-16)
					nativeStyle, _, _ := overlayGetStyle.Call(g.overlay.win.NativeHandle(), uintptr(index))
					fmt.Fprintf(os.Stderr, "native style: %#x; content: %+v\n", nativeStyle, g.overlay.win.ContentBounds())
					fmt.Fprintf(os.Stderr, "%s: native window is %dx%d, want %.0fx%.0f\n", style, w, h, d.width, d.height)
					os.Exit(1)
				}
				if compactOverlayStyle(style) && g.overlay.tip.hwnd == 0 {
					fmt.Fprintln(os.Stderr, "native details tooltip was not created")
					os.Exit(1)
				}
				if compactOverlayStyle(style) {
					content := g.overlay.win.ContentBounds()
					if content.Width != int(d.width) || content.Height != int(d.height) {
						fmt.Fprintf(os.Stderr, "%s: borders consume compact content: %+v\n", style, content)
						os.Exit(1)
					}
					if count, _, _ := overlaySendMessage.Call(g.overlay.tip.hwnd, 0x400+13, 0, 0); count != 1 { // TTM_GETTOOLCOUNT
						fmt.Fprintln(os.Stderr, "tooltip is not registered on the native UI surface")
						os.Exit(1)
					}
				} else if g.overlay.tip.hwnd != 0 {
					fmt.Fprintln(os.Stderr, "switching to a regular style did not remove the compact tooltip")
					os.Exit(1)
				}
			}
			g.overlay.update(Frame{Source: "codex", Kind: "pct", P1: 94, P2: 42, Old: true})
			if w, h := g.overlay.win.Size(); w != 44 || h != 88 {
				fmt.Fprintln(os.Stderr, "stale data expanded the compact window")
				os.Exit(1)
			}
			g.quitting = true
			mygo.App.Quit()
		})
		if err := mygo.App.Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if path := os.Getenv("ESP32_MYGO_QUIT_HELPER_CONFIG"); path != "" {
		quitCompleted := false
		mygo.App.OnQuit(func() { quitCompleted = true })
		mygo.App.WhenReady(func() {
			// This callback is queued after RunGUI has created its windows.
			mygo.App.WhenReady(func() {
				foundMain := false
				for _, w := range mygo.Windows() {
					if w.Title() == "ESP32 AI 额度表 · MyGo" {
						foundMain = true
						w.Close()
						if w.IsDestroyed() {
							fmt.Fprintln(os.Stderr, "ordinary close destroyed the main window")
							os.Exit(1)
						}
					}
				}
				if !foundMain {
					fmt.Fprintln(os.Stderr, "native main window was not created")
					os.Exit(1)
				}
				mygo.App.Quit() // Same entry point as the tray's Exit action.
			})
		})
		RunGUI(loadConfig(path), path, nil, os.Getenv("ESP32_MYGO_QUIT_HELPER_PREVIEW") == "1")
		if !quitCompleted || len(mygo.Windows()) != 0 {
			fmt.Fprintln(os.Stderr, "native quit did not finish closing the windows")
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

func TestNativeCompactWindowDimensions(t *testing.T) {
	if os.Getenv("ESP32_NATIVE_UI_TESTS") != "1" {
		t.Skip("set ESP32_NATIVE_UI_TESTS=1 on a Windows desktop to test native sizing")
	}
	cfg := defaultConfig()
	cfg.DesktopOverlay = true
	path := filepath.Join(t.TempDir(), "config.json")
	if err := saveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "ESP32_MYGO_COMPACT_HELPER_CONFIG="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native compact sizing failed: %v\n%s", err, output)
	}
}

func TestNativeQuitExitsProcess(t *testing.T) {
	if os.Getenv("ESP32_NATIVE_UI_TESTS") != "1" {
		t.Skip("set ESP32_NATIVE_UI_TESTS=1 on a Windows desktop to test native shutdown")
	}
	for _, tc := range []struct {
		name    string
		overlay bool
		preview bool
	}{
		{"main_only", false, true},
		{"with_overlay", true, true},
		{"with_engine", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Provider, cfg.Port = "none", "COM9999"
			cfg.DesktopOverlay = tc.overlay
			path := filepath.Join(t.TempDir(), "config.json")
			if err := saveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0])
			preview := "0"
			if tc.preview {
				preview = "1"
			}
			cmd.Env = append(os.Environ(), "ESP32_MYGO_QUIT_HELPER_CONFIG="+path, "ESP32_MYGO_QUIT_HELPER_PREVIEW="+preview)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native quit failed (%v): %v\n%s", ctx.Err(), err, output)
			}
			if loadConfig(path).DesktopOverlay != tc.overlay {
				t.Fatal("application quit changed the saved overlay toggle")
			}
		})
	}
}
