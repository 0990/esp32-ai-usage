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
