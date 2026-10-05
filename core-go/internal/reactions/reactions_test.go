package reactions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var testNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

// rules loads the shipped lifecycle contract; the tests exercise the real thresholds.
func rules(t *testing.T) knowledge.Rules {
	t.Helper()
	r, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatalf("load lifecycle rules: %v", err)
	}
	return r
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(self), "..", "..", "..", filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("repo file %s: %v", rel, err)
	}
	return p
}

func newService(t *testing.T, opts Options) *Service {
	t.Helper()
	svc, err := New(env.DB, rules(t), opts)
	if err != nil {
		t.Fatalf("reactions.New: %v", err)
	}
	return svc
}

// world is a freshly-seeded account with one sent episode.
type world struct {
	Seeded strategytest.Seeded
	Acct   string
	Ep     string
	Run    string
}

// pinSend rewrites the recorded send instant. human_strategy_decisions_check makes a send decision
// final, so the update runs with triggers off inside one transaction (the tests are the schema owner).
func pinSend(t *testing.T, episodeID string, sendAt time.Time) {
	t.Helper()
	tx, err := env.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("pin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(context.Background(), `SET LOCAL session_replication_role = 'replica'`); err != nil {
		t.Fatalf("pin triggers off: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE human_strategy_decisions SET send_decided_at = $2 WHERE decision_episode_id = $1::uuid`,
		episodeID, sendAt); err != nil {
		t.Fatalf("pin send_decided_at: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("pin commit: %v", err)
	}
}

// sendEpisode seeds the strategy world on the shared account, takes the human decision and the send
// through the real strategystore service, then pins send_decided_at so window bounds are deterministic.
func sendEpisode(t *testing.T, sendAt time.Time) world {
	t.Helper()
	ctx := context.Background()
	seed := strategytest.Seed(t, env.DB, ctxfixture.Get(t, env.DB).AccountA)
	strategies, err := strategystore.New(env.DB, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("strategystore.New: %v", err)
	}
	if _, _, err := strategies.RecordDecision(ctx, seed.RunID, strategystore.DecisionRequest{
		SelectedCandidateID: seed.Candidates[1], Surface: "api", ActorLabel: "t",
	}); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if _, err := strategies.Send(ctx, seed.RunID, strategystore.SendRequest{
		Decision: "send", Surface: "api", ActorLabel: "t",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	pinSend(t, seed.EpisodeID, sendAt)
	requireDistinctSendInstant(t, seed.AccountID, seed.EpisodeID, sendAt)
	return world{Seeded: seed, Acct: seed.AccountID, Ep: seed.EpisodeID, Run: seed.RunID}
}

// requireDistinctSendInstant fails the test when another sent episode on the account already sits at
// the same send_decided_at. The tests share one account (and, in CI, one database across packages), and
// an activity feeds exactly one episode (customer_reactions_once is per activity): of several episodes
// at one instant the scan's (send_decided_at, id) order hands the whole window to the id-last one, so
// which test's episode saw a tick would depend on random UUIDs — passing on some runs, failing on others.
func requireDistinctSendInstant(t *testing.T, accountID, episodeID string, sendAt time.Time) {
	t.Helper()
	var clash int
	if err := env.DB.QueryRow(`SELECT count(*) FROM human_strategy_decisions h
 JOIN decision_episodes e ON e.id = h.decision_episode_id
 WHERE e.account_id = $1::uuid AND e.id <> $2::uuid AND h.send_decision = 'send' AND h.send_decided_at = $3`,
		accountID, episodeID, sendAt).Scan(&clash); err != nil {
		t.Fatalf("send instant clash check: %v", err)
	}
	if clash != 0 {
		t.Fatalf("%d other episode(s) on the shared account were sent at %s — give each test its own send anchor",
			clash, sendAt.Format(time.RFC3339))
	}
}

type actOpt func(*actSpec)

type actSpec struct {
	sourceSystem string
	objectID     string
	eventKey     string
	payload      map[string]any
	summary      string
	correlation  string
	causedBy     string
	opportunity  string
	parts        [][3]string // raw_identity, role, person_id ("" allowed)
}

func withPayload(p map[string]any) actOpt { return func(s *actSpec) { s.payload = p } }
func withSummary(v string) actOpt         { return func(s *actSpec) { s.summary = v } }
func withCorrelation(id string) actOpt    { return func(s *actSpec) { s.correlation = id } }
func withCausedBy(id string) actOpt       { return func(s *actSpec) { s.causedBy = id } }
func withOpportunity(id string) actOpt    { return func(s *actSpec) { s.opportunity = id } }
func withEventKey(k string) actOpt        { return func(s *actSpec) { s.eventKey = k } }
func withPart(raw, role, person string) actOpt {
	return func(s *actSpec) { s.parts = append(s.parts, [3]string{raw, role, person}) }
}

// addActivity inserts a source event + activity + participants directly (the scan reads the stored
// rows, so tests control occurred_at and attribution precisely). Returns the activity id.
func addActivity(t *testing.T, acct, typ, body string, occurred time.Time, opts ...actOpt) string {
	t.Helper()
	sp := actSpec{
		sourceSystem: "email",
		objectID:     "msg-" + uuid.NewString()[:8],
		eventKey:     "evt-" + uuid.NewString()[:8],
	}
	for _, o := range opts {
		o(&sp)
	}
	if sp.payload == nil {
		sp.payload = map[string]any{"occurred_at": occurred.Format(time.RFC3339)}
	}
	sum := sha256.Sum256([]byte(sp.sourceSystem + "/" + sp.objectID + "/" + sp.eventKey))
	payload, _ := json.Marshal(sp.payload)
	var eventID, actID string
	err := env.DB.QueryRowContext(context.Background(), `
INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, occurred_at, payload)
VALUES ($1, $2, $3, $4, $5, $6::jsonb) RETURNING id::text`,
		sp.sourceSystem, sp.objectID, sp.eventKey, hex.EncodeToString(sum[:]), occurred, string(payload)).Scan(&eventID)
	if err != nil {
		t.Fatalf("insert source event: %v", err)
	}
	err = env.DB.QueryRowContext(context.Background(), `
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at,
                        account_id, opportunity_id, correlation_id, caused_by_activity_id, summary, body_text, provenance)
VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, NULLIF($7,'')::uuid, NULLIF($8,'')::uuid, NULLIF($9,'')::uuid,
        NULLIF($10,''), NULLIF($11,''), '{}'::jsonb) RETURNING id::text`,
		eventID, typ, sp.sourceSystem, sp.objectID, occurred,
		acct, sp.opportunity, sp.correlation, sp.causedBy, sp.summary, body).Scan(&actID)
	if err != nil {
		t.Fatalf("insert activity: %v", err)
	}
	for _, p := range sp.parts {
		if _, err := env.DB.ExecContext(context.Background(),
			`INSERT INTO activity_participants (activity_id, raw_identity, role, person_id) VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid)`,
			actID, p[0], p[1], p[2]); err != nil {
			t.Fatalf("insert participant: %v", err)
		}
	}
	return actID
}

// reactionRows returns (reaction_type, polarity) pairs for the episode, ordered by type.
func reactionRows(t *testing.T, episodeID string) [][2]string {
	t.Helper()
	rows, err := env.DB.Query(
		`SELECT reaction_type, polarity FROM customer_reactions WHERE decision_episode_id = $1::uuid ORDER BY reaction_type`, episodeID)
	if err != nil {
		t.Fatalf("query reactions: %v", err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var rt, p string
		if err := rows.Scan(&rt, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, [2]string{rt, p})
	}
	return out
}

func outcomeRows(t *testing.T, episodeID string) []string {
	t.Helper()
	rows, err := env.DB.Query(
		`SELECT outcome_type FROM business_outcomes WHERE decision_episode_id = $1::uuid ORDER BY outcome_type`, episodeID)
	if err != nil {
		t.Fatalf("query outcomes: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ot string
		if err := rows.Scan(&ot); err != nil {
			t.Fatal(err)
		}
		out = append(out, ot)
	}
	return out
}

func knowledgeCounts(t *testing.T, knowledgeID string) (support, positive, negative, advanced int64) {
	t.Helper()
	err := env.DB.QueryRow(`
SELECT support_count, (counts->>'positive_reactions')::int,
       (counts->>'negative_reactions')::int, (counts->>'outcomes_advanced')::int
FROM knowledge WHERE id = $1::uuid`, knowledgeID).
		Scan(&support, &positive, &negative, &advanced)
	if err != nil {
		t.Fatalf("read knowledge counts: %v", err)
	}
	return
}

func evidenceKinds(t *testing.T, knowledgeID string) []string {
	t.Helper()
	rows, err := env.DB.Query(
		`SELECT kind FROM knowledge_evidence WHERE knowledge_id = $1::uuid ORDER BY kind`, knowledgeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}
