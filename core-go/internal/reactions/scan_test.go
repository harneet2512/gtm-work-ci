package reactions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// All tests share one account, so each episode's send_decided_at is pinned to a strictly increasing
// value: (send, next send) partitions the timeline deterministically no matter what earlier tests
// left behind. testNow + N*24h is that per-test send anchor.

// TestReplyIsAReactionAndRaisesKnowledge is the HAR-120 acceptance at package level: the customer
// answers the sent action, the scan writes one 'replied' row, and the knowledge the decision used
// earns decision_episode + human_decision + customer_reaction evidence (support_count 0 -> 1).
func TestReplyIsAReactionAndRaisesKnowledge(t *testing.T) {
	sendAt := testNow
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{Clock: clock.NewFixed(sendAt.Add(2 * time.Hour))})

	addActivity(t, w.Acct, "EmailReply", "Thanks Dana — confirmed for Thursday.", sendAt.Add(time.Hour),
		withPart(w.Seeded.MarcoEmail(), "from", w.Seeded.Marco),
		withPart("dana@ghostvendor.com", "to", ""))

	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Episodes < 1 || res.Reactions != 1 || res.Outcomes != 0 {
		t.Fatalf("scan = %s, want one new reaction and no outcomes", res)
	}
	rows := reactionRows(t, w.Ep)
	if len(rows) != 1 || rows[0] != [2]string{"replied", "positive"} {
		t.Fatalf("reactions = %v, want one positive replied", rows)
	}
	var runID, epID, refs string
	if err := env.DB.QueryRow(`SELECT agent_run_id::text, decision_episode_id::text, evidence_refs::text
 FROM customer_reactions WHERE decision_episode_id = $1::uuid`, w.Ep).Scan(&runID, &epID, &refs); err != nil {
		t.Fatalf("read reaction row: %v", err)
	}
	if runID != w.Run || epID != w.Ep {
		t.Fatalf("reaction links run=%s ep=%s, want %s/%s", runID, epID, w.Run, w.Ep)
	}
	if refs == "" || refs == "[]" {
		t.Error("the reaction must carry its evidence_refs")
	}
	support, positive, negative, _ := knowledgeCounts(t, w.Seeded.KnowledgeID)
	if support != 1 || positive != 1 || negative != 0 {
		t.Fatalf("knowledge support=%d positive=%d negative=%d, want 1/1/0", support, positive, negative)
	}
	kinds := evidenceKinds(t, w.Seeded.KnowledgeID)
	if !sameStrings(kinds, []string{"customer_reaction", "decision_episode", "human_decision"}) {
		t.Fatalf("evidence kinds = %v, want the three HAR-120 kinds", kinds)
	}
	// Idempotent: a second scan is a no-op.
	again, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if again.Reactions != 0 || again.Outcomes != 0 || again.Evidence != 0 {
		t.Fatalf("rescan wrote %s, want a no-op", again)
	}
}

// TestWindowBounds: only activity strictly after the send is evidence, and once a newer send exists
// on the account the older episode's window closes at it — follow-on activity feeds the newer one.
func TestWindowBounds(t *testing.T) {
	send1 := testNow.Add(10 * 24 * time.Hour)
	w := sendEpisode(t, send1)
	ctx := context.Background()
	svc := newService(t, Options{})

	// At the send instant and before it: outside the window. Authored by OUR side so they never
	// link to any open episode — this test isolates the time bound, not authorship.
	addActivity(t, w.Acct, "EmailReply", "at send", send1,
		withPart("dana@ghostvendor.com", "from", ""))
	addActivity(t, w.Acct, "EmailReply", "before send", send1.Add(-time.Hour),
		withPart("dana@ghostvendor.com", "from", ""))
	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Reactions != 0 {
		t.Fatalf("boundary scan wrote %d reactions, want 0", res.Reactions)
	}

	// A second send opens a newer episode; the first episode's window ends there.
	send2 := send1.Add(48 * time.Hour)
	w2 := sendEpisode(t, send2)
	if w2.Ep == w.Ep {
		t.Fatal("the second send must open a new episode")
	}
	reply := addActivity(t, w.Acct, "EmailReply", "late", send2.Add(24*time.Hour),
		withPart(w2.Seeded.MarcoEmail(), "from", w2.Seeded.Marco))
	res, err = svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if res.Reactions != 1 {
		t.Fatalf("second scan wrote %d reactions, want 1", res.Reactions)
	}
	if got := reactionRows(t, w.Ep); len(got) != 0 {
		t.Fatalf("episode 1 got %v — a reply after the next send must feed the newer episode", got)
	}
	if got := reactionRows(t, w2.Ep); len(got) != 1 || got[0][0] != "replied" {
		t.Fatalf("episode 2 got %v, want the reply", got)
	}
	var onEp string
	if err := env.DB.QueryRow(`SELECT decision_episode_id::text FROM customer_reactions WHERE activity_id = $1::uuid`,
		reply).Scan(&onEp); err != nil {
		t.Fatalf("reaction owner: %v", err)
	}
	if onEp != w2.Ep {
		t.Fatalf("reply attributed to %s, want %s", onEp, w2.Ep)
	}
}

// TestSilenceWindow: a ghost.clock CustomerWentSilent tick before the window writes nothing; at or
// past it, one 'ignored' (negative) — silence is never inferred from the absence of activity.
func TestSilenceWindow(t *testing.T) {
	sendAt := testNow.Add(20 * 24 * time.Hour)
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{SilenceWindow: 7 * 24 * time.Hour})

	addActivity(t, w.Acct, "CustomerWentSilent", "", sendAt.Add(6*24*time.Hour))
	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Reactions != 0 {
		t.Fatalf("a silence tick before the window wrote %d reactions", res.Reactions)
	}
	addActivity(t, w.Acct, "CustomerWentSilent", "", sendAt.Add(7*24*time.Hour))
	res, err = svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if res.Reactions != 1 {
		t.Fatalf("a silence tick at the window wrote %d reactions, want 1", res.Reactions)
	}
	if got := reactionRows(t, w.Ep); !sameRows(got, [2]string{"ignored", "negative"}) {
		t.Fatalf("reactions = %v, want one negative ignored", got)
	}
}

// TestSilenceConverges: a sustained silence emits daily ghost.clock ticks, but it is ONE negative
// episode — the second tick for the same decision episode writes nothing (customer_reactions_ignored_once).
// Its own send anchor (+60d): TestSilenceWindow owns +20d, and two episodes sent at one instant on the shared
// account split their window by random episode id (see requireDistinctSendInstant).
func TestSilenceConverges(t *testing.T) {
	sendAt := testNow.Add(60 * 24 * time.Hour)
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{SilenceWindow: 7 * 24 * time.Hour})

	addActivity(t, w.Acct, "CustomerWentSilent", "", sendAt.Add(8*24*time.Hour))
	addActivity(t, w.Acct, "CustomerWentSilent", "", sendAt.Add(9*24*time.Hour))
	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Reactions != 1 {
		t.Fatalf("two silence ticks wrote %d reactions, want 1 — one silence episode is one ignored", res.Reactions)
	}
	// A later tick on a rescan still writes nothing.
	addActivity(t, w.Acct, "CustomerWentSilent", "", sendAt.Add(10*24*time.Hour))
	res, err = svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if res.Reactions != 0 {
		t.Fatalf("third tick on rescan wrote %d reactions, want 0", res.Reactions)
	}
}

// TestChannelLinks covers the deterministic links the detector honors: run correlation, caused-by a
// trigger activity, the watched email thread, and account-scope stakeholder events; a reply our
// side authored on the watched thread links but yields no reaction.
func TestChannelLinks(t *testing.T) {
	sendAt := testNow.Add(30 * 24 * time.Hour)
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{})

	corr := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	if _, err := env.DB.ExecContext(ctx, `UPDATE agent_runs SET correlation_id = $1::uuid WHERE id = $2::uuid`, corr, w.Run); err != nil {
		t.Fatalf("set run correlation: %v", err)
	}
	triggerThread := "thread-" + w.Seeded.ActivityID[:8]
	if _, err := env.DB.ExecContext(ctx, `UPDATE source_events se SET payload = payload || jsonb_build_object('thread_id', $2::text)
 FROM activities a WHERE a.source_event_id = se.id AND a.id = $1::uuid`, w.Seeded.ActivityID, triggerThread); err != nil {
		t.Fatalf("set trigger thread: %v", err)
	}

	addActivity(t, w.Acct, "EmailReply", "by correlation", sendAt.Add(time.Hour),
		withCorrelation(corr), withPart("nobody@ext.test", "from", ""))
	addActivity(t, w.Acct, "EmailReply", "by caused-by", sendAt.Add(2*time.Hour),
		withCausedBy(w.Seeded.ActivityID), withPart("nobody@ext.test", "from", ""))
	addActivity(t, w.Acct, "EmailReply", "by thread", sendAt.Add(3*time.Hour),
		withPayload(map[string]any{"thread_id": triggerThread}),
		withPart("nobody@ext.test", "from", ""))
	addActivity(t, w.Acct, "EmailReply", "ours", sendAt.Add(4*time.Hour),
		withPayload(map[string]any{"thread_id": triggerThread}),
		withPart("dana@ghostvendor.com", "from", ""))
	addActivity(t, w.Acct, "ContactAdded", "", sendAt.Add(5*time.Hour),
		withPart("crm:contact:817", "mentioned", ""), withPart("marco@acme.test", "actor", ""))
	// The same types authored by our side write nothing — our CRM hygiene is not a customer reaction.
	addActivity(t, w.Acct, "ContactAdded", "", sendAt.Add(5*time.Hour+time.Minute),
		withPart("crm:contact:818", "mentioned", ""), withPart("dana@ghostvendor.com", "actor", ""))
	addActivity(t, w.Acct, "MeetingAccepted", "", sendAt.Add(5*time.Hour+2*time.Minute),
		withPart("dana@ghostvendor.com", "actor", ""))
	// An unrelated activity on another thread authored by us: never attributed.
	addActivity(t, w.Acct, "EmailReply", "unrelated", sendAt.Add(6*time.Hour),
		withPayload(map[string]any{"thread_id": "elsewhere"}),
		withPart("dana@ghostvendor.com", "from", ""))

	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// correlation reply + caused-by reply + thread reply + ContactAdded -> stakeholder_added; the
	// our-authored replies yield nothing (the thread-matching one links, then classifies empty).
	if res.Reactions != 4 {
		t.Fatalf("scan wrote %d reactions, want 4 (3 replied + 1 stakeholder_added)", res.Reactions)
	}
	got := reactionRows(t, w.Ep)
	want := [][2]string{{"replied", "positive"}, {"replied", "positive"}, {"replied", "positive"}, {"stakeholder_added", "positive"}}
	if !sameRowSet(got, want) {
		t.Fatalf("reactions = %v, want %v", got, want)
	}
}

// TestOutcomes: CRM field changes on the episode's opportunity become business outcomes; evidence
// counts only the advanced ones.
func TestOutcomes(t *testing.T) {
	sendAt := testNow.Add(40 * 24 * time.Hour)
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{})

	// Give the episode's strategy set an opportunity.
	var oppID string
	if err := env.DB.QueryRowContext(ctx, `INSERT INTO opportunities (account_id, name, motion)
 VALUES ($1::uuid, 'Acme Expansion', 'expansion') RETURNING id::text`, w.Acct).Scan(&oppID); err != nil {
		t.Fatalf("insert opportunity: %v", err)
	}
	if _, err := env.DB.ExecContext(ctx, `UPDATE strategy_sets SET opportunity_id = $1::uuid WHERE id = $2::uuid`,
		oppID, w.Seeded.SetID); err != nil {
		t.Fatalf("link opportunity: %v", err)
	}

	addActivity(t, w.Acct, "OpportunityStageChanged", "", sendAt.Add(time.Hour),
		withOpportunity(oppID), withEventKey("field:StageName:Negotiation"),
		withPayload(map[string]any{"object_type": "Opportunity",
			"fields": map[string]any{"StageName": map[string]any{"old": "Commercial Review", "new": "Negotiation"}}}))
	addActivity(t, w.Acct, "CRMFieldChanged", "", sendAt.Add(2*time.Hour),
		withOpportunity(oppID), withEventKey("field:Amount:150000"),
		withPayload(map[string]any{"object_type": "Opportunity",
			"fields": map[string]any{"Amount": map[string]any{"old": 100000, "new": 150000}}}))

	res, err := svc.ScanAccount(ctx, w.Acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Outcomes != 2 {
		t.Fatalf("scan wrote %d outcomes, want stage_advanced + acv_change", res.Outcomes)
	}
	if got := outcomeRows(t, w.Ep); !sameStrings(got, []string{"acv_change", "stage_advanced"}) {
		t.Fatalf("outcomes = %v", got)
	}
	_, _, _, advanced := knowledgeCounts(t, w.Seeded.KnowledgeID)
	if advanced != 1 {
		t.Fatalf("outcomes_advanced = %d, want 1 (acv_change never advances)", advanced)
	}
	// And the episode/run/opportunity links plus evidence_refs survive on both rows.
	var n int
	if err := env.DB.QueryRow(`SELECT count(*)::int FROM business_outcomes
 WHERE decision_episode_id = $1::uuid AND agent_run_id = $2::uuid AND opportunity_id = $3::uuid
   AND jsonb_typeof(evidence_refs) = 'array'`, w.Ep, w.Run, oppID).Scan(&n); err != nil {
		t.Fatalf("outcome links: %v", err)
	}
	if n != 2 {
		t.Fatalf("outcome rows linked to the episode/run/opportunity: %d, want 2", n)
	}
}

// TestSupervisionReadBack: the read surface returns stored rows and empty arrays; unknown or
// malformed ids are ErrNotFound.
func TestSupervisionReadBack(t *testing.T) {
	sendAt := testNow.Add(50 * 24 * time.Hour)
	w := sendEpisode(t, sendAt)
	ctx := context.Background()
	svc := newService(t, Options{})
	addActivity(t, w.Acct, "EmailReply", "hi", sendAt.Add(time.Hour),
		withPart(w.Seeded.MarcoEmail(), "from", w.Seeded.Marco))
	if _, err := svc.ScanAccount(ctx, w.Acct); err != nil {
		t.Fatalf("scan: %v", err)
	}
	sup, err := svc.Supervision(ctx, w.Ep)
	if err != nil {
		t.Fatalf("supervision: %v", err)
	}
	if len(sup.CustomerReactions) != 1 || sup.CustomerReactions[0].ReactionType != "replied" {
		t.Fatalf("supervision = %+v", sup.CustomerReactions)
	}
	if sup.BusinessOutcomes == nil || len(sup.BusinessOutcomes) != 0 {
		t.Fatal("business_outcomes must be the empty array, not null")
	}
	if _, err := svc.Supervision(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed id: %v, want ErrNotFound", err)
	}
	if _, err := svc.Supervision(ctx, "99999999-9999-4999-8999-999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v, want ErrNotFound", err)
	}
	doc, err := svc.SupervisionDoc(ctx, w.Ep)
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	if len(doc) == 0 {
		t.Fatal("empty supervision doc")
	}
	runRows, err := ReactionsForRun(ctx, env.DB, w.Run)
	if err != nil {
		t.Fatalf("run reactions: %v", err)
	}
	if len(runRows) != 1 {
		t.Fatalf("run reactions = %d, want 1", len(runRows))
	}
}

func sameRows(got [][2]string, want [2]string) bool {
	return len(got) == 1 && got[0] == want
}

func sameRowSet(got, want [][2]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
