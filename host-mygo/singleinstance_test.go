package main

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestSingleInstanceSecondaryUsesFallback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	shown := make(chan struct{}, 1)
	go serveSingleInstance(ln, func() { shown <- struct{}{} })
	listener, primary := acquireSingleInstanceAt([]string{"invalid address", ln.Addr().String()}, true)
	if primary || listener != nil {
		t.Fatal("secondary should notify the existing instance and exit")
	}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("existing window was not notified")
	}
}

func TestSingleInstanceAutostartDoesNotShowWindow(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	shown := make(chan struct{}, 1)
	go serveSingleInstance(ln, func() { shown <- struct{}{} })
	listener, primary := acquireSingleInstanceAt([]string{ln.Addr().String()}, false)
	if listener != nil {
		defer listener.Close()
	}
	if primary || listener != nil {
		t.Fatal("autostart must reuse the running instance")
	}
	select {
	case <-shown:
		t.Fatal("autostart must not show the existing window")
	default:
	}
	// A subsequent manual launch must still be able to open settings.
	if err := notifySingleInstance(ln.Addr().String(), true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("manual launch did not show the existing window")
	}
}

func TestSingleInstanceUnrelatedServiceUsesFallback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(c, "unrelated service\n")
			c.Close()
		}
	}()
	listener, primary := acquireSingleInstanceAt([]string{ln.Addr().String(), "127.0.0.1:0"}, true)
	if listener != nil {
		defer listener.Close()
	}
	if !primary || listener == nil {
		t.Fatal("an unrelated service must not prevent startup on a fallback port")
	}
}

func TestSingleInstanceUnavailablePortStillStarts(t *testing.T) {
	listener, primary := acquireSingleInstanceAt([]string{"invalid address"}, true)
	if !primary || listener != nil {
		t.Fatal("a bind failure without an acknowledged instance must not prevent startup")
	}
}

func TestSingleInstanceIgnoresInvalidRequest(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	shown := false
	go func() {
		handleSingleInstance(server, func() { shown = true })
		close(done)
	}()
	_, _ = io.WriteString(client, "unrelated request\n")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalid request was not rejected")
	}
	if shown {
		t.Fatal("invalid request must not activate the window")
	}
}
