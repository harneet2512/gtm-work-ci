package codespace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// bucket1Gates lists B1 to B9: Bucket 1's context-layer gates.
var bucket1Gates = []string{"B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"}

// ExpectedGates is every gate whose row the record run waits for before it moves on or resets: the gates run in the
// background (a Slack click must be acknowledged at once), so without the wait the recorder could reset and stop the
// stack while their model calls are still in flight and unrecorded.
func ExpectedGates() []string {
	return append(append([]string{}, bucket1Gates...), bucket2.Gates...)
}

// GateProbe reads which gates have a persisted row for a decision episode.
type GateProbe interface {
	Persisted(ctx context.Context, c Case, episodeID string) ([]string, error)
}

// DBGateProbe is GateProbe over the case databases.
type DBGateProbe struct {
	DSNFor func(Case) (string, error)
}

// Persisted implements GateProbe.
func (p DBGateProbe) Persisted(ctx context.Context, c Case, episodeID string) ([]string, error) {
	db, err := DBProbe(p).open(ctx, c)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT gate FROM gate_results WHERE decision_episode_id = $1::uuid`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list the gate rows of episode %s: %w", episodeID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// missingGates is the expected gates absent from have, in flow order.
func missingGates(have []string) []string {
	got := map[string]bool{}
	for _, g := range have {
		got[g] = true
	}
	var out []string
	for _, g := range ExpectedGates() {
		if !got[g] {
			out = append(out, g)
		}
	}
	return out
}

// awaitGates blocks until every expected gate has a row for the episode and the worker has stopped answering calls
// (the count is stable across two polls), so no background gate call outlives the step.
func (r Recorder) awaitGates(ctx context.Context, c Case, episodeID string) error {
	if r.Gates == nil {
		return nil
	}
	var last []string
	err := r.waitUntil(ctx, fmt.Sprintf("the gates of %s were not all persisted", c.Label), func() (bool, error) {
		have, err := r.Gates.Persisted(ctx, c, episodeID)
		if err != nil {
			return false, err
		}
		last = missingGates(have)
		return len(last) == 0, nil
	})
	if err != nil {
		return fmt.Errorf("%w (missing: %s)", err, strings.Join(last, ", "))
	}
	return r.awaitQuiet(ctx, c)
}

// awaitQuiet waits until the cache call counter has not moved for two consecutive polls.
func (r Recorder) awaitQuiet(ctx context.Context, c Case) error {
	if r.CacheCalls == nil {
		return nil
	}
	prev, stable := -1, 0
	return r.waitUntil(ctx, fmt.Sprintf("the model calls of %s's gates did not settle", c.Label), func() (bool, error) {
		n, err := r.CacheCalls()
		if err != nil {
			return false, err
		}
		if n == prev {
			stable++
		} else {
			prev, stable = n, 0
		}
		return stable >= 2, nil
	})
}

// CacheCounters are the worker cache's counters from cache-stats.json.
type CacheCounters struct {
	Hits     int `json:"hits"`
	Recorded int `json:"recorded"`
	Misses   int `json:"misses"`
}

// Calls is every cache lookup so far, answered or not.
func (c CacheCounters) Calls() int { return c.Hits + c.Recorded + c.Misses }

// ReadCacheCounters reads the counters; a missing file is a cache nothing has used yet.
func ReadCacheCounters(cfg demorun.Config) (CacheCounters, error) {
	return readCacheCountersAt(filepath.Join(cfg.CacheDir(), "cache-stats.json"))
}

func readCacheCountersAt(path string) (CacheCounters, error) {
	var c CacheCounters
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// checkNoMisses fails when the strict cache refused a request since the run began. A gate that hits a miss only
// marks itself "not measured" and the run goes on, so this is what makes the strict replay fail on any miss,
// background gate calls included.
func (r Recorder) checkNoMisses(startMisses int) error {
	if r.Misses == nil {
		return nil
	}
	now, err := r.Misses()
	if err != nil {
		return fmt.Errorf("read the cache misses: %w", err)
	}
	if now > startMisses {
		return fmt.Errorf("the strict cache refused %d request(s) with no recording: the chronology is not deterministic (a key that varies between runs); see logs/worker.log", now-startMisses)
	}
	return nil
}
