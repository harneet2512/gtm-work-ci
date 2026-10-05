package corectx_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func service(t *testing.T) *corectx.Service {
	t.Helper()
	s, err := corectx.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fresh returns a new open run of the account (earlier tests may have finished or cancelled the
// world's runs).
func fresh(t *testing.T, account string) string {
	t.Helper()
	return ctxfixture.FreshRun(t, env.DB, account, "context_built")
}

func pull(t *testing.T, run string, tool corectx.Tool, p corectx.Params) corectx.Packet {
	t.Helper()
	packet, err := service(t).Pull(context.Background(), run, tool, p)
	if err != nil {
		t.Fatalf("pull %s %+v: %v", tool, p, err)
	}
	return packet
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("item is not an object: %v", err)
	}
	return m
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := corectx.New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestStateToolReturnsTheRunsRealAccountState(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	p := pull(t, fresh(t, w.AccountA), corectx.ToolState, corectx.Params{Limit: corectx.MaxLimit})
	if len(p.Items) < 2 {
		t.Fatalf("state packet has %d items", len(p.Items))
	}
	header := decode(t, p.Items[0])
	if header["kind"] != "state_header" || header["account_id"] != w.AccountA {
		t.Fatalf("header = %v, want the run's account %s", header, w.AccountA)
	}
	version := scalar(t, `SELECT version::text FROM account_state WHERE account_id = $1::uuid`, w.AccountA)
	if got := scalar(t, `SELECT $1::jsonb ->> 'version'`, string(p.Items[0])); got != version {
		t.Fatalf("header version = %s, account_state version = %s", got, version)
	}
	sawKnown := false
	for _, raw := range p.Items[1:] {
		item := decode(t, raw)
		if _, ok := item["field_path"].(string); !ok {
			t.Fatalf("state field item without field_path: %v", item)
		}
		sawKnown = sawKnown || item["known"] == true
	}
	if !sawKnown {
		t.Fatal("no known field in the state of the busiest sample account")
	}
	if p.Tool != corectx.ToolState || p.Limits.MaxItems != corectx.MaxLimit || p.Limits.MaxBytes != corectx.MaxPacketBytes {
		t.Fatalf("packet metadata: %+v", p)
	}
}

func TestStateToolWithFieldPathReturnsOnlyThatField(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	for field, want := range map[string]string{"stage": "stage", "commitment": "commitment", "blockers": "blockers"} {
		p := pull(t, fresh(t, w.AccountA), corectx.ToolState, corectx.Params{FieldPath: field})
		if len(p.Items) != 1 {
			t.Fatalf("%s: %d items", field, len(p.Items))
		}
		if got := decode(t, p.Items[0])["field_path"]; got != want {
			t.Fatalf("%s: field_path = %v", field, got)
		}
	}
	group := pull(t, fresh(t, w.AccountA), corectx.ToolState, corectx.Params{FieldPath: "buying_group.member"})
	for _, raw := range group.Items {
		if _, ok := decode(t, raw)["person_id"]; !ok {
			t.Fatalf("buying group item without person_id: %s", raw)
		}
	}
}

func TestPeopleAndCommitmentsComeFromTheState(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	people := pull(t, fresh(t, w.AccountA), corectx.ToolPeople, corectx.Params{})
	want := scalar(t, `SELECT jsonb_array_length(state -> 'buying_group')::text FROM account_state WHERE account_id = $1::uuid`, w.AccountA)
	if want != "0" && len(people.Items) == 0 {
		t.Fatalf("buying group has %s members but people returned none", want)
	}
	for _, raw := range people.Items {
		if decode(t, raw)["person_id"] == nil {
			t.Fatalf("people item without person_id: %s", raw)
		}
	}
	commitments := pull(t, fresh(t, w.AccountA), corectx.ToolCommitments, corectx.Params{})
	for _, raw := range commitments.Items {
		if decode(t, raw)["text"] == nil {
			t.Fatalf("commitment without text: %s", raw)
		}
	}
}

func TestEvidenceNeedsAFieldPathAndOnlyReturnsTheRunsClaims(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	if _, err := service(t).Pull(context.Background(), fresh(t, w.AccountA), corectx.ToolEvidence, corectx.Params{}); !errors.Is(err, corectx.ErrBadParams) {
		t.Fatalf("evidence without field_path: %v", err)
	}
	// amount is a deal-scoped claim path (ADR-0016), not an account-state field the evidence tool serves.
	field := scalar(t, `SELECT field_path FROM claims WHERE account_id = $1::uuid AND field_path <> 'amount' GROUP BY field_path ORDER BY count(*) DESC, field_path LIMIT 1`, w.AccountA)
	p := pull(t, fresh(t, w.AccountA), corectx.ToolEvidence, corectx.Params{FieldPath: field, Limit: 20})
	if len(p.Items) == 0 {
		t.Fatalf("no claims for %s", field)
	}
	for _, raw := range p.Items {
		item := decode(t, raw)
		owner := scalar(t, `SELECT account_id::text FROM claims WHERE id = $1::uuid`, item["claim_id"].(string))
		if owner != w.AccountA || item["field_path"] != field {
			t.Fatalf("claim %v belongs to %s / %v, not account A / %s", item["claim_id"], owner, item["field_path"], field)
		}
		if item["activity_id"] == nil || item["standing"] == nil {
			t.Fatalf("claim item lacks provenance: %v", item)
		}
	}
}

func TestEveryReturnedIDBelongsToTheRunsAccount(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	for _, c := range []struct{ run, account string }{{fresh(t, w.AccountA), w.AccountA}, {fresh(t, w.AccountB), w.AccountB}} {
		for _, tool := range []corectx.Tool{corectx.ToolState, corectx.ToolActivities, corectx.ToolPeople, corectx.ToolRecentDiffs} {
			p := pull(t, c.run, tool, corectx.Params{Limit: corectx.MaxLimit})
			var ids string
			if err := env.DB.QueryRow(`SELECT returned_ids::text FROM context_access_log WHERE id = $1`, p.AccessID).Scan(&ids); err != nil {
				t.Fatal(err)
			}
			var returned []string
			_ = json.Unmarshal([]byte(ids), &returned)
			for _, id := range returned {
				// An id is an activity, a claim, a person of this account, or an employee (the rep/owner).
				other := scalar(t, `SELECT count(*)::text FROM (
 SELECT account_id FROM activities WHERE id = $1::uuid UNION ALL SELECT account_id FROM claims WHERE id = $1::uuid
 UNION ALL SELECT account_id FROM people WHERE id = $1::uuid UNION ALL SELECT account_id FROM state_diffs WHERE id = $1::uuid) x
 WHERE account_id IS NOT NULL AND account_id <> $2::uuid`, id, c.account)
				if other != "0" {
					t.Fatalf("%s pull of run for %s returned id %s of another account", tool, c.account, id)
				}
			}
		}
	}
}

func TestActivitiesPutTheTriggerFirstAndHideRestrictedOnes(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "context_built")
	trigger := scalar(t, `SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, run)
	p := pull(t, run, corectx.ToolActivities, corectx.Params{Limit: corectx.MaxLimit})
	if len(p.Items) == 0 || decode(t, p.Items[0])["activity_id"] != trigger || decode(t, p.Items[0])["is_trigger"] != true {
		t.Fatalf("first activity is not the run's trigger %s: %s", trigger, p.Items)
	}

	other := scalar(t, `SELECT id::text FROM activities WHERE account_id = $1::uuid AND id <> $2::uuid ORDER BY occurred_at DESC LIMIT 1`, w.AccountA, trigger)
	for _, visibility := range []string{"restricted", "owner_only", "team", "weird"} {
		if _, err := env.DB.Exec(`UPDATE activities SET permissions = jsonb_build_object('visibility', $2::text) WHERE id = $1::uuid`, other, visibility); err != nil {
			t.Fatal(err)
		}
		again := pull(t, run, corectx.ToolActivities, corectx.Params{Limit: corectx.MaxLimit})
		for _, raw := range again.Items {
			if decode(t, raw)["activity_id"] == other {
				t.Fatalf("%s activity %s was served to the agent", visibility, other)
			}
		}
	}
	if _, err := env.DB.Exec(`UPDATE activities SET permissions = '{"visibility":"org"}' WHERE id = $1::uuid`, other); err != nil {
		t.Fatal(err)
	}
}

func TestRecentDiffsAreEmptyBeforeHAR106AndFilterWhenPresent(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountB, "context_built")
	if _, err := env.DB.Exec(`DELETE FROM state_diffs WHERE account_id = $1::uuid AND to_version >= 1000`, w.AccountB); err != nil {
		t.Fatal(err)
	}
	empty := pull(t, run, corectx.ToolRecentDiffs, corectx.Params{})
	if empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("diffs = %v, want an empty list", empty.Items)
	}
	raw, _ := json.Marshal(empty)
	if !json.Valid(raw) || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("empty packet must serialize items as []: %s", raw)
	}

	insertDiff(t, w.AccountB, 1000, true, `[{"field":"blockers","op":"added","material":true}]`)
	insertDiff(t, w.AccountB, 1001, false, `[{"field":"summary","op":"changed","material":false}]`)
	insertDiff(t, w.AccountB, 1002, true, `[{"field":"champion","op":"changed","material":true}]`)
	// The run was built on the newest of these versions: diffs of later versions (that share its as_of) are not its world.
	if _, err := env.DB.Exec(`UPDATE agent_runs SET state_version = 1002 WHERE id = $1::uuid`, run); err != nil {
		t.Fatal(err)
	}
	all := pull(t, run, corectx.ToolRecentDiffs, corectx.Params{})
	if len(all.Items) != 2 || decode(t, all.Items[0])["to_version"] != float64(1002) {
		t.Fatalf("material diffs, newest first = %s", all.Items)
	}
	only := pull(t, run, corectx.ToolRecentDiffs, corectx.Params{FieldPath: "blockers"})
	if len(only.Items) != 1 || decode(t, only.Items[0])["to_version"] != float64(1000) {
		t.Fatalf("blockers diffs = %s", only.Items)
	}
}

func TestNonOrgActivitiesNeverReachTheAgentThroughEvidenceOrStateRefs(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	field := scalar(t, `SELECT field_path FROM claims WHERE account_id = $1::uuid AND field_path <> 'amount' GROUP BY field_path ORDER BY count(*) DESC, field_path LIMIT 1`, w.AccountA)
	source := scalar(t, `SELECT source_activity_id::text FROM claims WHERE account_id = $1::uuid AND field_path = $2 ORDER BY id LIMIT 1`, w.AccountA, field)
	t.Cleanup(func() {
		_, _ = env.DB.Exec(`UPDATE activities SET permissions = '{"visibility":"org"}' WHERE id = $1::uuid`, source)
	})
	before := pull(t, fresh(t, w.AccountA), corectx.ToolEvidence, corectx.Params{FieldPath: field, Limit: 20})
	if !strings.Contains(string(mustJSON(t, before.Items)), source) {
		t.Fatalf("setup: claim source %s is not in the evidence of %s", source, field)
	}
	for _, visibility := range []string{"restricted", "owner_only", "team", "mystery"} {
		if _, err := env.DB.Exec(`UPDATE activities SET permissions = jsonb_build_object('visibility', $2::text) WHERE id = $1::uuid`, source, visibility); err != nil {
			t.Fatal(err)
		}
		run := fresh(t, w.AccountA)
		for _, c := range []struct {
			tool corectx.Tool
			p    corectx.Params
		}{
			{corectx.ToolEvidence, corectx.Params{FieldPath: field, Limit: 20}},
			{corectx.ToolState, corectx.Params{Limit: 20}},
			{corectx.ToolCommitments, corectx.Params{Limit: 20}},
			{corectx.ToolPeople, corectx.Params{Limit: 20}},
			{corectx.ToolRecentDiffs, corectx.Params{Limit: 20}},
		} {
			packet := pull(t, run, c.tool, c.p)
			if strings.Contains(string(mustJSON(t, packet.Items)), source) {
				t.Fatalf("%s activity %s reached the agent through the %s tool", visibility, source, c.tool)
			}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// insertDiff writes a diff (and the history row its FK needs) the way HAR-106 will.
func insertDiff(t *testing.T, account string, toVersion int, material bool, changes string) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, $2, as_of, state FROM account_state WHERE account_id = $1::uuid ON CONFLICT DO NOTHING`, account, toVersion); err != nil {
		t.Fatalf("history: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes)
 VALUES ($1::uuid, 0, $2, $3, $4::jsonb)`, account, toVersion, material, changes); err != nil {
		t.Fatalf("diff: %v", err)
	}
}
