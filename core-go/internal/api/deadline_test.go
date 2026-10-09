package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The route of a long response must show in the log when its write deadline cannot be extended.
func TestExtendWriteDeadlineLogsAWarnWithTheRouteWhenTheWriterCannotMoveItsDeadline(t *testing.T) {
	var buf bytes.Buffer
	s := &server{log: slog.New(slog.NewTextHandler(&buf, nil))}
	r := httptest.NewRequest(http.MethodPost, "/runs/r1/send", nil)
	s.extendWriteDeadline(httptest.NewRecorder(), r, time.Minute) // a recorder has no deadlines
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, `route="POST /runs/r1/send"`) || !strings.Contains(out, "not supported") {
		t.Fatalf("log = %q", out)
	}
}

type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineWriter) SetWriteDeadline(t time.Time) error { d.deadline = t; return nil }

func TestExtendWriteDeadlineMovesTheDeadlineAndStaysQuietWhenItCan(t *testing.T) {
	var buf bytes.Buffer
	s := &server{log: slog.New(slog.NewTextHandler(&buf, nil))}
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	s.extendWriteDeadline(w, httptest.NewRequest(http.MethodPost, "/runs/r1/send", nil), time.Hour)
	if buf.Len() != 0 || time.Until(w.deadline) < 59*time.Minute {
		t.Fatalf("deadline = %v, log = %q", w.deadline, buf.String())
	}
}
