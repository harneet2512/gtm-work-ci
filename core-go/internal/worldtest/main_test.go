// Package worldtest holds the leak tests of the world-time reads (ADR-0019, HAR-129 section 16.5): it
// replays E1..E5 through the real pipeline and asserts, for T = occurred_at(Ek), that nothing derived
// from Ek..E5 appears in the state, graph, timeline, evidence or run-scoped context reads. It holds only
// tests. The world is replayed once per test binary; tests only read it, and those that add rows remove them again.
package worldtest

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	code := storetest.Main(m, func(e *storetest.Env) { env = e })
	neo4jtest.Stop() // the parity test starts Neo4j on demand where it is available
	os.Exit(code)
}

var (
	seedOnce sync.Once
	world    *worldfixture.World
)

// seed replays the story into a clean database once per test binary (a replay takes seconds) and returns
// it. The tests only read it; the ones that add rows remove them again (see insertBulk), and the ones that
// open runs open new ones, which cancel the previous.
func seed(t *testing.T) *worldfixture.World {
	t.Helper()
	seedOnce.Do(func() { world = worldfixture.Seed(t, env.DB) })
	if world == nil {
		t.Fatal("the shared world could not be seeded")
	}
	return world
}

// ids lists the activity ids of the events from..to (1-based, inclusive).
func ids(w *worldfixture.World, from, to int) []string {
	var out []string
	for n := from; n <= to; n++ {
		out = append(out, w.Event(n).ActivityID)
	}
	return out
}

// sourceEvents lists the source event ids of the events from..to.
func sourceEvents(w *worldfixture.World, from, to int) []string {
	var out []string
	for n := from; n <= to; n++ {
		out = append(out, w.Event(n).SourceEventID)
	}
	return out
}

// assertNoneOf fails when body mentions any of the needles; what names the read.
func assertNoneOf(t *testing.T, what, body string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if n != "" && strings.Contains(body, n) {
			t.Errorf("%s leaked %q:\n%.1500s", what, n, body)
		}
	}
}

// laterMarkers are the values the story introduces at each event, so a leak of the After value is visible
// as text. markersFrom returns those of events k..5.
func markersFrom(k int) []string {
	all := [][]string{
		{worldfixture.StageDiscovery},
		{worldfixture.BlockerSecurity},
		{worldfixture.StageNegotiation},
		{worldfixture.BlockerBudget},
		{worldfixture.StageClosedWon},
	}
	var out []string
	for n := k; n <= len(all); n++ {
		out = append(out, all[n-1]...)
	}
	return out
}

// justAfter is the smallest time that includes an event at t (timestamptz has microsecond resolution).
func justAfter(t time.Time) time.Time { return t.Add(time.Microsecond) }
