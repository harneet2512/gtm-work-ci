package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFakecoreListensOnLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.168.1.5:80", "example.com:80", "nonsense"} {
		if loopbackOnly(addr) == nil {
			t.Errorf("%q was accepted", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		if err := loopbackOnly(addr); err != nil {
			t.Errorf("%q refused: %v", addr, err)
		}
	}
}

func TestFakecoreRefusesToStartWithoutAToken(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), nil, func(string) string { return "" }, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "token") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}

func TestFakecoreRejectsBadArguments(t *testing.T) {
	env := func(string) string { return "tok-for-the-test-123456" }
	for name, args := range map[string][]string{
		"unknown flag":   {"--nope"},
		"public address": {"--addr", "0.0.0.0:0"},
		"missing repo":   {"--repo", "this/does/not/exist"},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, env, &out, &errOut); code != 2 {
			t.Errorf("%s: exit code %d, want 2 (stderr %q)", name, code, errOut.String())
		}
		if strings.Contains(errOut.String(), "tok-for-the-test") {
			t.Errorf("%s: the token reached stderr", name)
		}
	}
}

func TestFakecoreFailsWhenItCannotListen(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--addr", "127.0.0.1:99999"}, func(string) string { return "tok-for-the-test-123456" }, &out, &errOut); code != 1 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
}

func TestFakecoreServesUntilItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--addr", "127.0.0.1:0"}, func(string) string { return "tok-for-the-test-123456" }, out, &bytes.Buffer{})
	}()
	var url string
	for i := 0; i < 200 && url == ""; i++ {
		if _, after, ok := strings.Cut(out.String(), "listening on "); ok {
			url = strings.TrimSpace(after)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		t.Fatal("fakecore never announced its address")
	}
	resp, err := http.Get(url + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d after a clean shutdown", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fakecore did not stop")
	}
}
