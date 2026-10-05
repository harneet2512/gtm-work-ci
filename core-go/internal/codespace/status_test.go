package codespace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

var startOrder = []string{"neo4j", "postgres", "worker", "core", "slackbot", "web"}

func rows(unhealthy ...string) func(context.Context) []demorun.StatusRow {
	return func(context.Context) []demorun.StatusRow {
		out := make([]demorun.StatusRow, 0, len(startOrder))
		for _, n := range startOrder {
			r := demorun.StatusRow{Name: n, State: demorun.StateRunning, Healthy: true}
			for _, u := range unhealthy {
				if u == n {
					r.Healthy = false
				}
			}
			out = append(out, r)
		}
		return out
	}
}

func allPresent(string) bool { return true }

func newSource(r *rig, unhealthy ...string) *StatusSource {
	return &StatusSource{
		Ops: r.ops, Rows: rows(unhealthy...), Required: startOrder,
		Secrets: []string{"OPENROUTER_API_KEY", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CHANNEL_ID"}, Present: allPresent,
		Invisibility: func(context.Context, string) (string, error) { return "withheld", nil },
		Now:          func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
	}
}

func TestStatusIsReadyOnlyWhenEverythingIsUpSeededAndWithheld(t *testing.T) {
	r := newRig(t)
	r.seeded()
	if err := WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1); err != nil {
		t.Fatal(err)
	}
	st := newSource(r).Compute(context.Background())
	if !st.Ready || st.Phase != PhaseReady || st.Message != "All systems ready" {
		t.Fatalf("status = %+v", st)
	}
	if st.ActiveCase != SlotCase1 || len(st.Services) != 6 || len(st.Cases) != 2 {
		t.Fatalf("status = %+v", st)
	}
	c1, c2 := st.Cases[0], st.Cases[1]
	if !c1.Active || c1.ManifestID != "man-1" || c1.AccountID != "acct-1" || c1.Invisibility != "withheld" {
		t.Errorf("case 1 = %+v", c1)
	}
	if c2.Active || c2.Invisibility != "" || c2.ManifestID != "man-2" {
		t.Errorf("case 2 = %+v: the invisibility assertion is only read for the active case", c2)
	}
	// After Play the world is released: still ready (the operator may be mid-demo).
	src := newSource(r)
	src.Invisibility = func(context.Context, string) (string, error) { return "released", nil }
	if st := src.Compute(context.Background()); !st.Ready {
		t.Fatalf("released = %+v", st)
	}
}

func TestStatusNamesTheFirstServiceThatIsNotHealthy(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	st := newSource(r, "worker", "web").Compute(context.Background())
	if st.Ready || st.Phase != PhaseStarting || st.Message != "Starting worker..." {
		t.Fatalf("status = %+v", st)
	}
	if st.Services[2].Name != "worker" || st.Services[2].Healthy {
		t.Fatalf("services = %+v", st.Services)
	}
}

func TestStatusMostBlockingCauseWins(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	// Nothing seeded: the first-time setup has not finished, whatever else is wrong.
	src := newSource(r, "neo4j")
	if st := src.Compute(ctx); st.Phase != PhaseSetup || !strings.Contains(st.Message, "MedTech Advances") || !strings.Contains(st.Message, "EcoLite Innovations") {
		t.Fatalf("unseeded = %+v", st)
	}
	r.seeded()

	// A missing secret outranks a service that is still starting.
	src.Present = func(n string) bool { return n != "SLACK_APP_TOKEN" }
	st := src.Compute(ctx)
	if st.Phase != PhaseNeedsSecrets || len(st.MissingSecrets) != 1 || st.MissingSecrets[0] != "SLACK_APP_TOKEN" || !strings.Contains(st.Message, "SLACK_APP_TOKEN") {
		t.Fatalf("missing secret = %+v", st)
	}
	src.Present = nil
	if st := src.Compute(ctx); len(st.MissingSecrets) != 4 {
		t.Fatalf("without a presence check every secret is missing: %v", st.MissingSecrets)
	}
	src.Present = allPresent
	src.Rows = rows() // everything is healthy from here on

	// A running job outranks everything.
	src.Job = func() *JobStatus {
		return &JobStatus{Op: "Reset demo", Step: "Restoring MedTech Advances to Event N-1", Running: true}
	}
	if st := src.Compute(ctx); st.Phase != PhaseBusy || st.Message != "Reset demo: Restoring MedTech Advances to Event N-1" || st.Ready {
		t.Fatalf("busy = %+v", st)
	}
	src.Job = func() *JobStatus { return &JobStatus{Op: "Reset demo", Step: "Reset complete"} } // finished: not busy
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	if st := src.Compute(ctx); !st.Ready || st.Job == nil || st.Job.Running {
		t.Fatalf("after the job = %+v", st)
	}
}

func TestStatusActiveCaseAndInvisibilityEdgeCases(t *testing.T) {
	r := newRig(t)
	r.seeded()
	ctx := context.Background()
	_ = os.Remove(r.ops.Paths.ActiveFile()) // the baseline names case 1 active; this test starts with none
	src := newSource(r)

	if st := src.Compute(ctx); st.Phase != PhaseStarting || st.Message != "Choosing the active case..." {
		t.Fatalf("no active case = %+v", st)
	}
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase2)
	src.Invisibility = func(context.Context, string) (string, error) { return "leaked", nil }
	if st := src.Compute(ctx); st.Phase != PhaseAttention || st.Ready || !strings.Contains(st.Message, "EcoLite Innovations") {
		t.Fatalf("leaked = %+v", st)
	}
	src.Forget()
	src.Invisibility = func(context.Context, string) (string, error) { return "", errors.New("core down") }
	if st := src.Compute(ctx); st.Ready || st.Cases[1].Invisibility != "unknown" || st.Phase != PhaseStarting {
		t.Fatalf("core not answering = %+v", st)
	}
	src.Invisibility = nil
	if st := src.Compute(ctx); st.Cases[1].Invisibility != "unknown" {
		t.Fatalf("no checker = %+v", st.Cases[1])
	}
}

func TestStatusCachesTheInvisibilityAssertionBrieflyAndForgetClearsIt(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	calls := 0
	src := newSource(r)
	src.Now = func() time.Time { return now }
	src.Invisibility = func(context.Context, string) (string, error) { calls++; return "withheld", nil }
	for i := 0; i < 3; i++ {
		src.Compute(context.Background())
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 within the TTL", calls)
	}
	now = now.Add(invisibilityTTL + time.Second)
	src.Compute(context.Background())
	if calls != 2 {
		t.Fatalf("calls = %d, want a fresh read after the TTL", calls)
	}
	src.Forget()
	src.Compute(context.Background())
	if calls != 3 {
		t.Fatalf("calls = %d, want a fresh read after Forget", calls)
	}
}

func TestStatusUsesTheSeededCaseNameAndLeaksNothingSensitive(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	r.admin.calls = nil
	st := newSource(r).Compute(context.Background())
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"pid", "env", "token", "password", "postgres://", "url"} {
		if strings.Contains(strings.ToLower(string(raw)), banned) {
			t.Errorf("status JSON mentions %q: %s", banned, raw)
		}
	}
	for _, key := range []string{`"ready"`, `"phase"`, `"message"`, `"services"`, `"cases"`, `"active_case"`, `"manifest_id"`, `"account_id"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("status JSON lacks %s: %s", key, raw)
		}
	}
}
