package coalesce_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// accuracyTarget is the HAR-104 acceptance bar for state-field accuracy on fields the fake worker can know.
const accuracyTarget = 0.95

// Regression gate on the headline numbers (percent, one decimal, as reported in wp6.md and the PR). They
// are floors, not targets: they may only be raised. Real extraction quality is measured elsewhere
// (WP3/WP11); these numbers move only when gold, fixtures or the fold logic change.
const (
	minAllFieldAccuracy      = 63.0
	minRequiredFieldAccuracy = 78.0
)

func roundedPercent(c, n int) float64 {
	if n == 0 {
		return 0
	}
	return math.Round(1000*float64(c)/float64(n)) / 10
}

// extraCandidates are worker answers the critical facts do not contain but the gold's stale facts
// require us to survive: an AI claim that CRM later outranks (Acme puzzle 4) and the rep's
// never-accepted review proposal (Acme `next_meeting` stale fact).
var extraCandidates = map[string][]claims.Candidate{
	"world/accounts/acme/events/025_call_commercial_transcript.json": {{
		FieldPath: claims.FieldStage, Value: json.RawMessage(`"Negotiation"`), Confidence: 0.8,
		EvidenceQuote: "we're basically in negotiation now"}},
	"world/accounts/acme/events/044_email_dana_security_review_ask.json": {{
		FieldPath: claims.FieldNextMeeting, Value: json.RawMessage(`"2026-10-01 security review (proposed by the rep, never accepted)"`), Confidence: 0.8,
		EvidenceQuote: "could we book a 60-minute security review for Thursday, October 1?"}},
}

type cpResult struct {
	gold      claimstest.Gold
	state     []byte
	facts     []claimstest.Fact
	bgFacts   []claimstest.Fact
	recompute coalesce.Recompute
}

type run struct {
	golds     []claimstest.Gold
	personID  map[string]string
	activity  map[string]string // event file -> activity id
	fileOf    map[string]string // activity id -> event file
	results   []cpResult
	extractor *claimstest.FakeExtractor
}

// replayGold ingests every account's events in file order, drains the recompute at each gold
// checkpoint, scores the stored state and returns everything the assertions need.
func replayGold(t *testing.T) *run {
	t.Helper()
	fixtures, err := claimstest.FindFixtures()
	if err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("fixtures must exist in CI: %v", err)
		}
		t.Skipf("fixtures not present: %v", err)
	}
	golds, err := claimstest.LoadGold(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	events := loadWorldEvents(t, fixtures)
	r := &run{golds: golds, activity: map[string]string{}, fileOf: map[string]string{}}
	r.personID = seedWorld(t, events, golds)

	derivation, err := claimstest.DeriveCandidates(golds, claimstest.PeopleFrom(golds), extraCandidates)
	if err != nil {
		t.Fatal(err)
	}
	r.extractor = claimstest.NewGoldExtractor(derivation, func(id string) string { return r.fileOf[id] })
	d := &driver{
		t: t, r: r, ingestClock: clock.NewFixed(time.Unix(0, 0)), coalClock: clock.NewFixed(time.Unix(0, 0)),
	}
	d.svc = ingestService(t, d.ingestClock)
	d.co = newService(t, d.coalClock, coalesce.Options{Extractor: r.extractor, WorkerID: "acceptance"})
	for _, account := range []string{"acme", "beta", "northstar"} {
		d.replayAccount(account, ingestOrder(t, events, account, golds), golds)
	}
	t.Logf("recompute wall time at the 12 checkpoints (embedded Postgres, includes claim writes and state persistence): %s", d.timings())
	return r
}

// driver replays one account at a time against the real ingest and coalesce services.
type driver struct {
	t           *testing.T
	r           *run
	svc         *ingest.Service
	co          *coalesce.Service
	ingestClock *clock.Fixed
	coalClock   *clock.Fixed
	durations   []time.Duration
}

func (d *driver) timings() string {
	var parts []string
	var total time.Duration
	for _, x := range d.durations {
		total += x
		parts = append(parts, x.Round(time.Millisecond).String())
	}
	return fmt.Sprintf("%s (sum %s)", strings.Join(parts, " "), total.Round(time.Millisecond))
}

func (d *driver) replayAccount(account string, order []worldEvent, golds []claimstest.Gold) {
	t := d.t
	files := make([]string, len(order))
	for i, e := range order {
		files[i] = e.Rel
	}
	boundaries := map[string]claimstest.Gold{}
	var accountGolds []claimstest.Gold
	for _, g := range golds {
		if g.Account != account {
			continue
		}
		if claimstest.Index(files, g.AfterEventFile) < 0 {
			t.Fatalf("%s cp%d: after_event_file %s is not among the account's events", account, g.Checkpoint, g.AfterEventFile)
		}
		boundaries[g.AfterEventFile] = g
		accountGolds = append(accountGolds, g)
	}
	var maxClock time.Time
	sinceLast := 0
	for _, e := range order {
		d.ingestClock.Set(e.At)
		if e.At.After(maxClock) {
			maxClock = e.At
		}
		if d.ingestOne(e) {
			sinceLast++
		}
		g, isBoundary := boundaries[e.Rel]
		if !isBoundary {
			continue
		}
		d.checkLiveFiles(g, files, e.Rel)
		d.coalClock.Set(maxClock.Add(time.Minute))
		rec := d.drainOnce(account, g.Checkpoint, sinceLast)
		sinceLast = 0
		d.r.results = append(d.r.results, d.r.score(t, g, accountGolds, files, rec))
	}
}

// ingestOne ingests an event and reports whether it created a new activity (not a duplicate delivery).
func (d *driver) ingestOne(e worldEvent) bool {
	res, err := d.svc.Ingest(context.Background(), e.Event)
	if err != nil {
		d.t.Fatalf("ingest %s: %v", e.Rel, err)
	}
	d.r.activity[e.Rel] = res.ActivityID
	if _, seen := d.r.fileOf[res.ActivityID]; seen {
		if !res.Duplicate {
			d.t.Fatalf("%s: activity %s reused without being a duplicate", e.Rel, res.ActivityID)
		}
		return false
	}
	d.r.fileOf[res.ActivityID] = e.Rel
	return true
}

// checkLiveFiles asserts that, when the gold declares its live files, the live files ingested up to and
// including the boundary are exactly that list, in that order.
func (d *driver) checkLiveFiles(g claimstest.Gold, files []string, boundary string) {
	if len(g.LiveFiles) == 0 {
		return
	}
	var got []string
	for _, f := range files[:claimstest.Index(files, boundary)+1] {
		if strings.HasPrefix(f, "live/") {
			got = append(got, f)
		}
	}
	if strings.Join(got, ",") != strings.Join(g.LiveFiles, ",") {
		d.t.Fatalf("%s cp%d declares live_files %v but the ingest order up to %s has %v", g.Account, g.Checkpoint, g.LiveFiles, boundary, got)
	}
}

func (d *driver) drainOnce(account string, cp, sinceLast int) coalesce.Recompute {
	start := time.Now()
	drain, err := d.co.Drain(context.Background())
	d.durations = append(d.durations, time.Since(start))
	if err != nil || len(drain.Recomputes) != 1 {
		d.t.Fatalf("%s cp%d: drain = %+v, %v (want exactly one coalesced recompute)", account, cp, drain, err)
	}
	rec := drain.Recomputes[0]
	if len(rec.ActivityIDs) != sinceLast {
		d.t.Fatalf("%s cp%d: recompute covered %d activities, ingested %d new ones", account, cp, len(rec.ActivityIDs), sinceLast)
	}
	return rec
}

func (r *run) score(t *testing.T, g claimstest.Gold, accountGolds []claimstest.Gold, order []string, rec coalesce.Recompute) cpResult {
	t.Helper()
	acct := scalar(t, `SELECT id::text FROM accounts WHERE lower(name) LIKE $1`, g.Account+" %")
	state := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, acct)
	validate(t, state)
	sc := claimstest.Scorer{Gold: g, AccountGolds: accountGolds, Order: order, PersonID: r.personID}
	facts, err := sc.ScoreState([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	bg, err := sc.ScoreBuyingGroup([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	return cpResult{gold: g, state: []byte(state), facts: facts, bgFacts: bg, recompute: rec}
}

func (r *run) result(account string, cp int) cpResult {
	for _, res := range r.results {
		if res.gold.Account == account && res.gold.Checkpoint == cp {
			return res
		}
	}
	panic(fmt.Sprintf("no result for %s cp%d", account, cp))
}

func (r *run) parsedState(t *testing.T, account string, cp int) reducer.AccountState {
	t.Helper()
	var st reducer.AccountState
	if err := json.Unmarshal(r.result(account, cp).state, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestGoldAcceptance(t *testing.T) {
	r := replayGold(t)
	if len(r.results) != 12 {
		t.Fatalf("scored %d checkpoints, want 12", len(r.results))
	}

	t.Run("state field accuracy against gold", func(t *testing.T) { r.assertAccuracy(t) })
	t.Run("conflicts match the gold (ADR-0008)", func(t *testing.T) { r.assertConflicts(t) })
	t.Run("outranked claims are retained", func(t *testing.T) { r.assertOutrankedRetained(t) })
	t.Run("enrichment never outranks first party", func(t *testing.T) { r.assertEnrichment(t) })
	t.Run("economic buyer stays unknown at Acme", func(t *testing.T) { r.assertAcmeEconomicBuyer(t) })
	t.Run("Northstar champion is delegated to Sam", func(t *testing.T) { r.assertNorthstarDelegation(t) })
	t.Run("derived fields", func(t *testing.T) { r.assertDerived(t) })
}

func (r *run) assertAccuracy(t *testing.T) {
	var all, known, required []claimstest.Fact
	var bgAll []claimstest.Fact
	header := fmt.Sprintf("%-10s %-3s %-22s %-22s %-22s", "account", "cp", "knowable (correct/n)", "all 18 fields", "required 11")
	lines := []string{header}
	for _, res := range r.results {
		c, n := claimstest.Tally(res.facts, true)
		ca, na := claimstest.Tally(res.facts, false)
		var req []claimstest.Fact
		for _, f := range res.facts {
			for _, name := range claimstest.RequiredFields() {
				if f.Field == name {
					req = append(req, f)
				}
			}
		}
		cr, nr := claimstest.Tally(req, false)
		lines = append(lines, fmt.Sprintf("%-10s %-3d %-22s %-22s %-22s", res.gold.Account, res.gold.Checkpoint,
			pct(c, n), pct(ca, na), pct(cr, nr)))
		all = append(all, res.facts...)
		required = append(required, req...)
		bgAll = append(bgAll, res.bgFacts...)
	}
	for _, f := range all {
		if f.Knowable {
			known = append(known, f)
		}
	}
	ck, nk := claimstest.Tally(all, true)
	ca, na := claimstest.Tally(all, false)
	cr, nr := claimstest.Tally(required, false)
	cb, nb := claimstest.Tally(bgAll, false)
	lines = append(lines, fmt.Sprintf("%-10s %-3s %-22s %-22s %-22s", "TOTAL", "", pct(ck, nk), pct(ca, na), pct(cr, nr)))
	lines = append(lines, fmt.Sprintf("buying group + coverage gaps (all facts): %s", pct(cb, nb)))
	t.Log("\n" + strings.Join(lines, "\n"))

	t.Log("MISSES ON KNOWABLE FIELDS (these are bugs):")
	misses := 0
	for _, f := range known {
		if !f.Correct {
			misses++
			t.Logf("  %s cp%d %-26s want=%q got=%q (%s)", f.Account, f.CP, f.Field, f.Want, f.Got, f.Reason)
		}
	}
	t.Log("MISSES ON NON-KNOWABLE FIELDS (the fake cannot know them):")
	for _, f := range all {
		if !f.Knowable && !f.Correct {
			t.Logf("  %s cp%d %-26s want=%q got=%q (%s)", f.Account, f.CP, f.Field, f.Want, f.Got, f.Reason)
		}
	}
	t.Log("LIST ITEM STATUS NOTES:")
	for _, f := range all {
		for _, n := range f.Notes {
			t.Logf("  %s cp%d %-26s %s", f.Account, f.CP, f.Field, n)
		}
	}
	t.Log("BUYING GROUP / COVERAGE MISSES:")
	for _, f := range bgAll {
		if !f.Correct {
			t.Logf("  %s cp%d %-48s want=%q got=%q (%s)", f.Account, f.CP, f.Field, f.Want, f.Got, f.Reason)
		}
	}
	if got := roundedPercent(ca, na); got < minAllFieldAccuracy {
		t.Errorf("all-field accuracy %.1f%% fell below the %.1f%% floor", got, minAllFieldAccuracy)
	}
	if got := roundedPercent(cr, nr); got < minRequiredFieldAccuracy {
		t.Errorf("required-field accuracy %.1f%% fell below the %.1f%% floor", got, minRequiredFieldAccuracy)
	}
	if nk == 0 || float64(ck)/float64(nk) < accuracyTarget {
		t.Fatalf("knowable-field accuracy %s is below the %.0f%% target", pct(ck, nk), accuracyTarget*100)
	}
	if misses > 0 {
		t.Errorf("%d knowable fields are wrong", misses)
	}
	if path := os.Getenv("WP6_REPORT_FILE"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
}

func pct(c, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d = %.1f%%", c, n, 100*float64(c)/float64(n))
}

func (r *run) fieldOf(st reducer.AccountState, name string) reducer.Field {
	return *st.Fields.Field(name)
}
