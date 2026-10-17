package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileFingerprint(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// ReClaude 心跳：只有 last_alive_at_ms 变了 → 指纹不变。
	write(state, `{"daemon":{"running":true,"pid":1,"port":52159,"last_alive_at_ms":1},"last_port":52159}`)
	a := fileFingerprint(state)
	write(state, `{"daemon":{"running":true,"pid":1,"port":52159,"last_alive_at_ms":2},"last_port":52159}`)
	if b := fileFingerprint(state); b != a {
		t.Fatalf("heartbeat-only rewrite must keep fingerprint: %q vs %q", a, b)
	}
	// 端口变了 / daemon 停了 → 指纹变。
	write(state, `{"daemon":{"running":true,"pid":1,"port":52200,"last_alive_at_ms":3},"last_port":52200}`)
	if fileFingerprint(state) == a {
		t.Fatalf("port change must change fingerprint")
	}
	write(state, `{"daemon":{"running":false,"pid":1,"port":52159,"last_alive_at_ms":4},"last_port":52159}`)
	if fileFingerprint(state) == a {
		t.Fatalf("running=false must change fingerprint")
	}
	// 其它文件：内容哈希；原样重写不变，内容变则变；删除 → missing。
	auth := filepath.Join(dir, "auth.json")
	write(auth, `{"OPENAI_API_KEY":"sk-1"}`)
	h1 := fileFingerprint(auth)
	write(auth, `{"OPENAI_API_KEY":"sk-1"}`)
	if fileFingerprint(auth) != h1 {
		t.Fatalf("identical rewrite must keep fingerprint")
	}
	write(auth, `{"OPENAI_API_KEY":"sk-2"}`)
	if fileFingerprint(auth) == h1 {
		t.Fatalf("content change must change fingerprint")
	}
	_ = os.Remove(auth)
	if fileFingerprint(auth) != "missing" {
		t.Fatalf("missing file → \"missing\"")
	}
}
