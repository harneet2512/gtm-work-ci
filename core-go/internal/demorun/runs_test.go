package demorun

import (
	"context"
	"net/http"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// The statement runs on the real migrated schema (a misspelled column fails here, not during the live walkthrough),
// and a Play with no runs yields an empty list without calling core.
func TestDemoCorePlayRunsOnAnEmptyDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	core := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("no run exists, so core must not be called: %s", r.URL.Path)
	})
	runs, err := DemoCore{CoreClient: core, DB: env.DB}.PlayRuns(context.Background(), "00000000-0000-0000-0000-0000000000aa")
	if err != nil || len(runs) != 0 {
		t.Fatalf("PlayRuns = %v, %v", runs, err)
	}
}

// DemoCore must be usable wherever RunPlay wants a PlayCore.
var _ PlayCore = DemoCore{}
