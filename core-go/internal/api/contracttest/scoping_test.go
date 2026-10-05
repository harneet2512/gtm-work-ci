package contracttest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
)

var allTools = []string{"state", "recent_diffs", "evidence", "activities", "people", "commitments"}

func toolQuery(tool string) string {
	if tool == "evidence" {
		return "field_path=stage&limit=20"
	}
	return "limit=20"
}

func TestARunTokenReadsOnlyItsOwnAccount(t *testing.T) {
	s := newStack(t)
	accounts := map[string]string{s.world.AccountA: "", s.world.AccountB: ""}
	for account := range accounts {
		other := s.world.AccountA
		if account == s.world.AccountA {
			other = s.world.AccountB
		}
		_, token := s.token(account)
		for _, tool := range allTools {
			r := s.pull(token, tool, toolQuery(tool))
			p := decodePacket(t, r)
			for _, raw := range p.Items {
				text := string(raw)
				if strings.Contains(text, other) {
					t.Fatalf("%s pull of a run of %s leaked the other account's id %s: %.300s", tool, account, other, text)
				}
			}
			var ids string
			if err := env.DB.QueryRow(`SELECT returned_ids::text FROM context_access_log WHERE id = $1`, p.AccessID).Scan(&ids); err != nil {
				t.Fatal(err)
			}
			var returned []string
			_ = json.Unmarshal([]byte(ids), &returned)
			for _, id := range returned {
				foreign := scalar(t, `SELECT count(*)::text FROM (
 SELECT account_id FROM activities WHERE id = $1::uuid UNION ALL SELECT account_id FROM claims WHERE id = $1::uuid
 UNION ALL SELECT account_id FROM people WHERE id = $1::uuid UNION ALL SELECT account_id FROM state_diffs WHERE id = $1::uuid) x
 WHERE account_id IS NOT NULL AND account_id <> $2::uuid`, id, account)
				if foreign != "0" {
					t.Fatalf("%s: returned id %s belongs to another account", tool, id)
				}
			}
		}
	}
}

func TestNoQueryParameterCanNameAnAccountOrRun(t *testing.T) {
	s := newStack(t)
	runA, token := s.token(s.world.AccountA)
	before := scalar(t, `SELECT count(*)::text FROM context_access_log WHERE agent_run_id = $1::uuid`, runA)
	for _, q := range []string{
		"account_id=" + s.world.AccountB, "run_id=" + s.world.RunB, "account=" + s.world.AccountB,
		"limit=5&account_id=" + s.world.AccountB, "field_path=stage&field_path=health", "limit=1&limit=2", "x=1",
	} {
		r := s.pull(token, "state", q)
		if r.status != 400 {
			t.Errorf("query %q: status %d, want 400 (%s)", q, r.status, clip(r.body))
		}
	}
	for _, q := range []string{"limit=0", "limit=21", "limit=-3", "limit=abc", "limit=", "field_path=bogus", "field_path=account_id", "field_path=%27%3B+DROP+TABLE+claims%3B--"} {
		if r := s.pull(token, "state", q); r.status != 400 && !(q == "limit=" && r.status == 200) {
			t.Errorf("query %q: status %d, want 400", q, r.status)
		}
	}
	if after := scalar(t, `SELECT count(*)::text FROM context_access_log WHERE agent_run_id = $1::uuid`, runA); after != before && after != "1" {
		t.Errorf("rejected requests were logged as pulls: %s -> %s", before, after)
	}
}

func TestMissingExpiredForgedAndWrongKindTokensAreRefused(t *testing.T) {
	s := newStack(t)
	runID, good := s.token(s.world.AccountA)
	other, err := runtoken.NewSigner([]byte("another-signing-key-0123456789abcdef"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := other.Issue(runID, time.Now())
	expired, _ := s.signer.Issue(runID, time.Now().Add(-time.Hour))
	parts := strings.Split(good, ".")
	retargeted := strings.Join([]string{parts[0], s.world.RunB, parts[2], parts[3]}, ".")
	cases := map[string]string{
		"no token":                 "",
		"garbage":                  "garbage",
		"foreign key":              foreign,
		"expired":                  expired,
		"another run id, same sig": retargeted,
		"the operator API token":   apiToken,
		"truncated signature":      good[:len(good)-4],
		"token plus junk":          good + "x",
		"empty-looking bearer":     " ",
	}
	for name, tok := range cases {
		r := s.pull(tok, "state", "")
		if r.status != 401 {
			t.Errorf("%s: status %d, want 401", name, r.status)
		}
		if body := string(r.body); strings.Contains(body, good) || strings.Contains(body, signingKey) {
			t.Errorf("%s: response echoes a secret: %s", name, body)
		}
		if code := errorCode(t, r); code != "unauthorized" {
			t.Errorf("%s: code %q", name, code)
		}
	}
	// A run token is not an operator token: it cannot read account endpoints or the trace.
	for _, path := range []string{"/accounts/" + s.world.AccountA + "/state", "/accounts/" + s.world.AccountB + "/timeline", "/runs/" + runID + "/trace"} {
		if r := s.do("GET", path, "", good, nil); r.status != 401 {
			t.Errorf("run token on %s: %d, want 401", path, r.status)
		}
	}
	if r := s.pull(good, "state", ""); r.status != 200 {
		t.Fatalf("the genuine token stopped working: %d", r.status)
	}
}

func TestATokenStopsWorkingWhenItsRunLeavesTheDraftingStates(t *testing.T) {
	s := newStack(t)
	for status, want := range map[string]int{
		"pending": 200, "context_built": 200, "drafted": 200,
		"awaiting_human": 403, "approved": 403, "edited": 403, "rejected": 403, "ignored": 403,
		"executed": 403, "failed": 403, "cancelled": 403,
	} {
		runID, token := s.token(s.world.AccountB)
		if _, err := env.DB.Exec(`UPDATE agent_runs SET status = $2, run_mode = 'live' WHERE id = $1::uuid`, runID, status); err != nil {
			t.Fatal(err)
		}
		if r := s.pull(token, "people", ""); r.status != want {
			t.Errorf("run %s: status %d, want %d", status, r.status, want)
		}
	}
	// A genuine signature for a run that does not exist is a 403, never a 404 oracle or a 500.
	ghost, _ := s.signer.Issue(missingID, time.Now())
	if r := s.pull(ghost, "state", ""); r.status != 403 {
		t.Errorf("token of an unknown run: %d", r.status)
	}
}

func TestErrorsAndLogsNeverCarryTokensOrInternals(t *testing.T) {
	s := newStack(t)
	runID, token := s.token(s.world.AccountA)
	s.pull(token, "state", "")
	s.pull(token+"x", "state", "")
	s.pull(token, "evidence", "")
	s.do("GET", "/accounts/"+s.world.AccountA+"/state?as_of=garbage", "", apiToken, nil)
	for _, secret := range []string{token, apiToken, signingKey} {
		if strings.Contains(s.logs.String(), secret) {
			t.Fatalf("the server log contains a secret: %.200s", s.logs.String())
		}
	}
	for _, r := range []reply{s.pull(token, "nonsense", ""), s.pull("", "state", ""), s.get("/accounts/"+missingID+"/state", "")} {
		body := strings.ToLower(string(r.body))
		for _, leak := range []string{"sql", "pq:", "pgx", "stack", "panic", "postgres", runID} {
			if strings.Contains(body, leak) {
				t.Errorf("error body leaks %q: %s", leak, body)
			}
		}
	}
}

func TestPacketsAreBoundedOnTheWire(t *testing.T) {
	s := newStack(t)
	account := s.world.AccountB
	for i := 0; i < 25; i++ {
		obj := fmt.Sprintf("wire-huge-%d", i)
		if _, err := env.DB.Exec(`WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', $1::text, 'received', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance, summary, body_text)
 SELECT id, 'EmailReceived', 'email', $1, now() + ($2 || ' seconds')::interval, $3::uuid,
        '{"source_system":"email","source_object_id":"x"}'::jsonb, repeat('s', 900), repeat('b', 30000) FROM ev`, obj, fmt.Sprint(i), account); err != nil {
			t.Fatal(err)
		}
	}
	_, token := s.token(account)
	for _, tool := range allTools {
		r := s.pull(token, tool, toolQuery(tool))
		p := decodePacket(t, r)
		if len(r.body) > 16384 {
			t.Errorf("%s: response of %d bytes exceeds the worker's default 16384-byte cap", tool, len(r.body))
		}
		if p.Bytes > 12<<10 || len(p.Items) > 20 {
			t.Errorf("%s: %d items / %d bytes exceed the bounds", tool, len(p.Items), p.Bytes)
		}
		if p.Limits.MaxItems != 20 || p.Limits.MaxBytes != 12<<10 {
			t.Errorf("%s: limits %+v", tool, p.Limits)
		}
	}
	if p := decodePacket(t, s.pull(token, "activities", "limit=20")); !p.Truncated {
		t.Error("an over-budget activities packet was not marked truncated")
	}
}
