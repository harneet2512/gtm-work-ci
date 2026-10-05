package demorun

import (
	"context"
	"strings"
	"testing"
)

func TestChecklistListsTheClicksInTheDemoOrder(t *testing.T) {
	text := Checklist("C0DEMO")
	order := []string{"View full", "Choose", "Edit", "Send", "Needs correction", "Add note", "demo verify"}
	pos := -1
	for _, want := range order {
		i := strings.Index(text[pos+1:], want)
		if i < 0 {
			t.Fatalf("checklist is missing or misorders %q:\n%s", want, text)
		}
		pos += 1 + i
	}
	if !strings.Contains(text, "#ghost-demo") {
		t.Fatalf("it must say where to look:\n%s", text)
	}
	if strings.Contains(text, "C0DEMO") && !strings.Contains(text, "(C0DEMO)") {
		t.Fatalf("the channel id is shown only as an id hint:\n%s", text)
	}
}

func TestWebURLsPrintOnlyPagesThatExistInThisBuild(t *testing.T) {
	exists := func(_ context.Context, u string) bool { return !strings.HasSuffix(u, "/evals") }
	lines := WebURLs(context.Background(), "http://127.0.0.1:3000", "acct-1", "man-1", "run-1", exists)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"http://127.0.0.1:3000/accounts/acct-1", "http://127.0.0.1:3000/replay", "http://127.0.0.1:3000/replay/man-1", "http://127.0.0.1:3000/runs/run-1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "http://127.0.0.1:3000/runs/run-1/evals") && !strings.Contains(joined, "not in this build") {
		t.Fatalf("an absent evals page must be labelled, not offered:\n%s", joined)
	}
}

func TestWebURLsWithoutARunOmitsRunPages(t *testing.T) {
	lines := WebURLs(context.Background(), "http://x", "a", "m", "", func(context.Context, string) bool { return true })
	if strings.Contains(strings.Join(lines, "\n"), "/runs/") {
		t.Fatalf("no run, no run pages: %v", lines)
	}
}
