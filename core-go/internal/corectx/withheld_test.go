package corectx_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

const (
	secretCommitment = "SECRET-FLOOR: CFO approved a 30% floor"
	secretSummary    = "SECRET-SUMMARY: internal margin discussion"
)

func recompute(t *testing.T, account, activity string) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids)
 VALUES ($1::uuid, now() - interval '1 minute', now() - interval '2 minutes', ARRAY[$2::uuid])
 ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE SET due_at = excluded.due_at`, account, activity); err != nil {
		t.Fatal(err)
	}
	co, err := coalesce.New(env.DB, coalesce.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := co.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestAnOwnerOnlyActivityThatWinsFieldsLeaksNowhere is the HAR-129 "every claim has a trace" guard:
// an owner_only internal email wins a commitment and a scalar field of the state, yet its text is in no
// tool's packet, no logged pull and no trace stub.
func TestAnOwnerOnlyActivityThatWinsFieldsLeaksNowhere(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	account := w.AccountB
	at := time.Now().Add(2 * time.Hour).UTC()
	activity := scalar(t, `WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', 'owner-only-cfo', 'sent', encode(sha256(convert_to('owner-only-cfo', 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance, permissions)
 SELECT id, 'EmailSent', 'email', 'owner-only-cfo', $2, $1::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb,
        '{"visibility":"owner_only"}'::jsonb FROM ev RETURNING id::text`, account, at)
	for field, value := range map[string]string{
		"commitment": `{"text":"` + secretCommitment + `","status":"open"}`,
		"summary":    `"` + secretSummary + `"`,
	} {
		if _, err := env.DB.Exec(`INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($1::uuid, $2, $3::jsonb, 'human_approved', 1, $4::uuid, 'internal', $5, 'test')`, account, field, value, activity, at); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = env.DB.Exec(`DELETE FROM claims WHERE source_activity_id = $1::uuid`, activity)
		_, _ = env.DB.Exec(`UPDATE activities SET account_id = NULL, opportunity_id = NULL WHERE id = $1::uuid`, activity)
		recompute(t, account, activity)
	})
	recompute(t, account, activity)
	if stored := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, account); !strings.Contains(stored, secretCommitment) || !strings.Contains(stored, secretSummary) {
		t.Fatalf("setup: the owner_only claims did not win the stored state: %.400s", stored)
	}

	run := fresh(t, account)
	var logged []int64
	pulls := []struct {
		tool corectx.Tool
		p    corectx.Params
	}{
		{corectx.ToolState, corectx.Params{Limit: 20}},
		{corectx.ToolState, corectx.Params{FieldPath: "summary"}},
		{corectx.ToolState, corectx.Params{FieldPath: "commitment"}},
		{corectx.ToolCommitments, corectx.Params{Limit: 20}},
		{corectx.ToolPeople, corectx.Params{Limit: 20}},
		{corectx.ToolRecentDiffs, corectx.Params{Limit: 20}},
		{corectx.ToolEvidence, corectx.Params{FieldPath: "summary", Limit: 20}},
		{corectx.ToolEvidence, corectx.Params{FieldPath: "commitment", Limit: 20}},
		{corectx.ToolActivities, corectx.Params{Limit: 20}},
	}
	for _, c := range pulls {
		packet := pull(t, run, c.tool, c.p)
		logged = append(logged, packet.AccessID)
		body := string(mustJSON(t, packet))
		for _, leak := range []string{"SECRET-", "30%", activity} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s %+v leaks %q: %.300s", c.tool, c.p, leak, body)
			}
		}
	}

	summary := pull(t, run, corectx.ToolState, corectx.Params{FieldPath: "summary"})
	got := decode(t, summary.Items[0])
	if got["known"] != false || got["value"] != nil || got["withheld"] != corectx.WithheldVisibility || !summary.Truncated {
		t.Fatalf("the withheld scalar field = %v (truncated=%v)", got, summary.Truncated)
	}
	commitments := pull(t, run, corectx.ToolState, corectx.Params{FieldPath: "commitment"})
	if c := decode(t, commitments.Items[0]); c["withheld"] != corectx.WithheldVisibility || !commitments.Truncated {
		t.Fatalf("the withheld commitment field = %v", c)
	}

	reader, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := reader.Trace(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	stubs := string(mustJSON(t, map[string]any{"accesses": trace.ContextAccesses, "refs": trace.Run.InputContextRefs}))
	for _, leak := range []string{"SECRET-", "30%", activity} {
		if strings.Contains(stubs, leak) {
			t.Fatalf("the trace's context pulls leak %q: %.300s", leak, stubs)
		}
	}
	if len(trace.ContextAccesses) != len(logged)+2 { // plus the two pulls that inspect the withheld markers
		t.Fatalf("trace shows %d pulls, made %d", len(trace.ContextAccesses), len(logged))
	}
}
