package mockstrava

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	var out bytes.Buffer
	addrFile := filepath.Join(t.TempDir(), "addr")
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunConfig{Listen: "127.0.0.1:0", AddrFile: addrFile, Stdout: &out, Ready: func(u string) { ready <- u }}, Options{})
	}()
	var base string
	select {
	case base = <-ready:
	case err := <-done:
		t.Fatalf("run ended early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	resp, err := http.Get(base + "/_mock/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", resp.StatusCode)
	}
	if !strings.HasPrefix(out.String(), "listening on "+base) {
		t.Errorf("stdout = %q", out.String())
	}
	if b, _ := os.ReadFile(addrFile); strings.TrimSpace(string(b)) != base {
		t.Errorf("addr file = %q", b)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestRun_Errors(t *testing.T) {
	ctx := context.Background()
	if err := Run(ctx, RunConfig{}, Options{}); err == nil {
		t.Error("empty listen: want error")
	}
	if err := Run(ctx, RunConfig{Listen: "256.0.0.1:x"}, Options{}); err == nil {
		t.Error("bad listen: want error")
	}
	if err := Run(ctx, RunConfig{Listen: "127.0.0.1:0", AddrFile: filepath.Join(t.TempDir(), "no", "dir", "addr"), Stdout: &bytes.Buffer{}}, Options{}); err == nil {
		t.Error("unwritable addr file: want error")
	}
}
