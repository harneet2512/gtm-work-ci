package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCodespaceServeAnswersHealthzAndAuthenticatedStatusThenStopsCleanly drives the real wiring: the control service
// that the web server calls, on a free loopback port, with this boot's control token.
func TestCodespaceServeAnswersHealthzAndAuthenticatedStatusThenStopsCleanly(t *testing.T) {
	codespaceRepo(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	t.Setenv("GHOST_DEMO_PORT_CONTROL", strconv.Itoa(port))

	rt, closeFlow, err := loadCodespace(io.Discard, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFlow()
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- codespaceServe(ctx, rt, &out) }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control service never answered: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if resp, err := http.Get(base + "/status"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without a token = %v %v", resp, err)
	}
	// the core API token (in secrets.env) is not accepted: only this boot's control token is
	secrets, err := os.ReadFile(filepath.Join(rt.Cfg.Layout.Dir(), "secrets.env"))
	if err == nil {
		for _, line := range strings.Split(string(secrets), "\n") {
			if v, ok := strings.CutPrefix(line, "GHOST_API_TOKEN="); ok {
				apiReq, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
				apiReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(v))
				if resp, err := http.DefaultClient.Do(apiReq); err != nil || resp.StatusCode != http.StatusUnauthorized {
					t.Fatalf("the core API token must not open the control service: %v %v", resp, err)
				}
			}
		}
	}
	api := rt.ControlToken
	req, _ := http.NewRequest(http.MethodGet, base+"/status", nil)
	req.Header.Set("Authorization", "Bearer "+api)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status with this boot's control token = %v %v", resp, err)
	}
	_ = resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop")
	}
	if !strings.Contains(out.String(), "control service listening on 127.0.0.1:") {
		t.Fatalf("output = %q", out.String())
	}
}
