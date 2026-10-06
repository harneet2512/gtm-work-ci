package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// HAR-97 B9: promotion and advance stamp the replay world time; a replay without --at is refused before any
// database is dialed, and a malformed --at is an explicit error.
func TestLearnReplayFlagsAreValidatedBeforeTheDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused/db")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"replay without at (promote)", []string{"promote", "--evaluator", "grounding", "--version", "2", "--replay"}, "--replay needs --at"},
		{"replay without at (advance)", []string{"advance", "--evaluator", "grounding", "--version", "2", "--replay"}, "--replay needs --at"},
		{"replay without at (backtest)", []string{"backtest", "--evaluator", "grounding", "--version", "2", "--replay"}, "--replay needs --at"},
		{"malformed at", []string{"promote", "--evaluator", "grounding", "--version", "2", "--at", "yesterday"}, "--at must be RFC 3339"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runLearnTo(tc.args, &out, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseReplayTime(t *testing.T) {
	at, replay, err := parseReplayTime("2026-03-10T09:00:00Z", true)
	if err != nil || !replay || !at.Equal(time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v %v", at, replay, err)
	}
	if at, replay, err := parseReplayTime("", false); err != nil || replay || !at.IsZero() {
		t.Fatalf("live: %v %v %v", at, replay, err)
	}
}
