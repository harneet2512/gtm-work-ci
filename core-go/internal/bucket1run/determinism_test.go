package bucket1run

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

var uuidRE = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// cacheKeyText mimics the worker cache: the request text with every uuid replaced by its order of first appearance.
func cacheKeyText(t *testing.T, payload map[string]any, ids []string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"payload": payload, "evidence_ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	return uuidRE.ReplaceAllStringFunc(string(raw), func(u string) string {
		if _, ok := seen[u]; !ok {
			seen[u] = len(seen)
		}
		return fmt.Sprintf("<<id-%d>>", seen[u])
	})
}

func uid(run string, n int) string { return fmt.Sprintf("%s0000-0000-4000-8000-%012d", run, n) }

// episodeOf builds the same Event N episode with its own run-scoped ids; rev lists the claims in the opposite order,
// as a database tie on the claims' shared time (broken by random id) would.
func episodeOf(run string, rev bool) bucket1.Episode {
	at := time.Date(2023, 11, 9, 9, 0, 0, 0, time.UTC)
	claims := []bucket1.Claim{
		{ID: uid(run, 1), ActivityID: uid(run, 10), Field: "stage", Value: "Legal", Quote: "the DPA is signed", Kind: "fact", Status: "active", OccurredAt: at},
		{ID: uid(run, 2), ActivityID: uid(run, 10), Field: "blocker", Value: "security review", Quote: "waiting on security", Kind: "inference", Status: "active", OccurredAt: at},
		{ID: uid(run, 3), ActivityID: uid(run, 10), Field: "champion", Value: "Marco", Quote: "Marco will push", Kind: "fact", Status: "active", OccurredAt: at},
	}
	prior := []bucket1.Claim{
		{ID: uid(run, 4), Field: "stage", Value: "Discovery", OccurredAt: at.Add(-48 * time.Hour)},
		{ID: uid(run, 5), Field: "budget", Value: "approved", OccurredAt: at.Add(-48 * time.Hour)},
	}
	prec := []bucket1.Precedent{{ID: uid(run, 6), SharedFeatures: []string{"stage"}, Lesson: "x"}, {ID: uid(run, 7), SharedFeatures: []string{"role"}, Lesson: "y"}}
	if rev {
		claims[0], claims[2] = claims[2], claims[0]
		prior[0], prior[1] = prior[1], prior[0]
		prec[0], prec[1] = prec[1], prec[0]
	}
	return bucket1.Episode{ID: uid(run, 99),
		Activities:  []bucket1.Activity{{ID: uid(run, 10), Text: "the DPA is signed", OccurredAt: at}},
		Claims:      claims,
		PriorClaims: prior,
		Beliefs:     []bucket1.Belief{{Kind: "summary", Statement: "legal is the gate"}},
		Precedents:  prec,
	}
}

func TestB1B3B5B8PayloadsGiveTheSameCacheKeyForEquivalentDataWithDifferentIdsAndOrder(t *testing.T) {
	for _, gate := range []string{"B1", "B3", "B5", "B8"} {
		pa, ia, _, oka := judgePayload(episodeOf("aaaa", false), gate)
		pb, ib, _, okb := judgePayload(episodeOf("bbbb", true), gate)
		if !oka || !okb {
			t.Fatalf("%s: nothing to judge", gate)
		}
		if a, b := cacheKeyText(t, pa, ia), cacheKeyText(t, pb, ib); a != b {
			t.Errorf("%s: the cassette key depends on run ids or row order:\n%s\n%s", gate, a, b)
		}
	}
}
