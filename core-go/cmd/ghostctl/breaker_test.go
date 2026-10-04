package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
)

type stubIngest struct{ api.IngestService }

func TestBreakerUsage(t *testing.T) {
	for _, args := range [][]string{{"breaker", "bogus"}, {"breaker", "reset", "now"}} {
		if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestBreakerStatusAndResetTalkToTheRunningCore(t *testing.T) {
	b, err := providerbreaker.New(1, time.Minute, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(stubIngest{}, "ctl-token", nil, api.WithBreaker(b))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	t.Setenv("DATABASE_URL", "postgres://unused/db")
	t.Setenv("CORE_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("GHOST_API_TOKEN", "ctl-token")

	b.Failure("worker 424 provider_unavailable_nonretryable")
	var out bytes.Buffer
	if err := run([]string{"breaker"}, &out); err != nil || !strings.Contains(out.String(), "open") || !strings.Contains(out.String(), "provider_unavailable_nonretryable") {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"breaker", "reset"}, &out); err != nil || !strings.Contains(out.String(), "reset") || !strings.Contains(out.String(), "closed") || b.State() != providerbreaker.Closed {
		t.Fatalf("reset: %v\n%s", err, out.String())
	}
	t.Setenv("GHOST_API_TOKEN", "wrong")
	if err := run([]string{"breaker", "reset"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a wrong token must be refused: %v", err)
	}
}
