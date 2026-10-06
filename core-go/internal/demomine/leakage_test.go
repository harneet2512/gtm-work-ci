package demomine

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// namingExtractor is a fake worker: for every extractable activity it answers one champion claim naming the
// person pick returns, quoting the first words of the text so the claim passes the verbatim-quote check.
type namingExtractor struct {
	pick func(claims.ExtractRequest) string
	mu   sync.Mutex
	asks []string
}

func (n *namingExtractor) Extract(_ context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	who := n.pick(req)
	n.mu.Lock()
	n.asks = append(n.asks, who)
	n.mu.Unlock()
	firstLine, _, _ := strings.Cut(strings.TrimSpace(req.Text), "\n")
	quote := strings.TrimSpace(firstLine) // a verbatim substring of the body
	value, _ := json.Marshal(who)
	return claims.ExtractResponse{Model: "fake-worker", ExtractorVersion: claims.DefaultExtractorVersion,
		Claims: []claims.Candidate{{FieldPath: claims.FieldChampion, Value: value, Confidence: 0.9, EvidenceQuote: quote, SubjectIdentity: who}}}, nil
}

func (n *namingExtractor) calls() int { n.mu.Lock(); defer n.mu.Unlock(); return len(n.asks) }

// replayUntilAsked replays the sample event by event until the extractor has been asked once, and returns the
// database as it stands at that point of the history.
func replayUntilAsked(t *testing.T, db *sql.DB, ext *namingExtractor) {
	t.Helper()
	snap, err := crmarena.Load(filepath.FromSlash(sampleDir))
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWorld(context.Background(), db, res, filepath.Join(filepath.FromSlash(sampleDir), "User.json"), nil, ext)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Events {
		if _, err := w.Apply(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if ext.calls() > 0 {
			return
		}
	}
	t.Fatal("the sample never reached the extractor")
}

func championClaims(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM claims WHERE field_path = 'champion' AND extractor LIKE 'llm:fake-worker%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Leakage: a worker (or a cassette served by approximate match) may name a person who only enters the
// history later. At this point of the history that person is unknown to the account, so core cannot resolve
// the identity and drops the claim; a person who is known is accepted, so the test is not vacuous.
func TestClaimNamingAPersonNotYetKnownIsDropped(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	snap, err := crmarena.Load(filepath.FromSlash(sampleDir))
	if err != nil {
		t.Fatal(err)
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()

	var future string
	leaky := &namingExtractor{pick: func(claims.ExtractRequest) string {
		for _, c := range snap.Contacts { // the first contact the history has not met yet
			var n int
			if err := env.DB.QueryRow(`SELECT count(*) FROM people WHERE primary_email = lower($1)`, c.Email).Scan(&n); err == nil && n == 0 {
				future = c.Email
				return c.Email
			}
		}
		return "nobody@example.invalid"
	}}
	replayUntilAsked(t, env.DB, leaky)
	if future == "" {
		t.Fatal("the sample has no contact the history has not yet met")
	}
	if got := championClaims(t, env.DB); got != 0 {
		t.Fatalf("a claim naming %s, who is not yet known, was stored (%d champion claims)", future, got)
	}
	var known int
	if err := env.DB.QueryRow(`SELECT count(*) FROM people WHERE primary_email = lower($1)`, future).Scan(&known); err != nil || known != 0 {
		t.Fatalf("the unknown person %s entered the world through the claim (%d, %v)", future, known, err)
	}
}

func TestClaimNamingAKnownPersonIsKept(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	control := &namingExtractor{pick: func(req claims.ExtractRequest) string {
		for _, p := range req.Activity.Participants {
			if p.PersonID != "" && p.RawIdentity != "" {
				return p.RawIdentity
			}
		}
		return "nobody@example.invalid"
	}}
	replayUntilAsked(t, env.DB, control)
	if got := championClaims(t, env.DB); got == 0 {
		t.Fatalf("control: a claim naming a participant known at that point was dropped (asked for %v)", control.asks)
	}
}
