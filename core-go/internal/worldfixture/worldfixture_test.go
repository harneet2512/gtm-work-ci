package worldfixture_test

import (
	"os"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestSeedReplaysFiveEventsIntoFiveVersionsWhoseAsOfIsTheEventTime(t *testing.T) {
	w := worldfixture.Seed(t, env.DB)
	if len(w.Events) != 5 {
		t.Fatalf("events = %d", len(w.Events))
	}
	for _, ev := range w.Events {
		if ev.Version != ev.N {
			t.Errorf("E%d produced version %d, want one version per event", ev.N, ev.Version)
		}
		if got := count(t, `SELECT count(*) FROM state_history WHERE account_id = $1::uuid AND version = $2 AND as_of = $3`, w.Account, ev.Version, ev.At); got != 1 {
			t.Errorf("E%d: state_history.as_of is not the event's occurred_at", ev.N)
		}
		if got := count(t, `SELECT count(*) FROM activities WHERE id = $1::uuid AND occurred_at = $2 AND account_id = $3::uuid`, ev.ActivityID, ev.At, w.Account); got != 1 {
			t.Errorf("E%d: activity not attributed to the account at its time", ev.N)
		}
	}
	if got := count(t, `SELECT count(*) FROM claims WHERE account_id = $1::uuid AND field_path = 'stage'`, w.Account); got != 3 {
		t.Errorf("stage claims = %d, want 3", got)
	}
	if got := count(t, `SELECT count(*) FROM claims WHERE account_id = $1::uuid AND field_path = 'stage' AND status = 'active' AND value = '"Closed Won"'::jsonb`, w.Account); got != 1 {
		t.Errorf("the last stage claim must be the active one")
	}
	if got := count(t, `SELECT count(*) FROM relationships WHERE id = $1::uuid AND valid_to = $2`, w.EdgeChamp, w.At(3)); got != 1 {
		t.Errorf("the champion edge must close at E3")
	}
	if got := count(t, `SELECT count(*) FROM signals WHERE id = $1::uuid AND occurred_at = $2`, w.SignalID, w.At(4)); got != 1 {
		t.Errorf("the signal must be emitted at E4")
	}
}

func TestNewRunIsTriggeredByTheChosenEvent(t *testing.T) {
	w := worldfixture.Seed(t, env.DB)
	run := w.NewRun(t, 3)
	if got := count(t, `SELECT count(*) FROM agent_runs WHERE id = $1::uuid AND trigger_activity_ids = ARRAY[$2::uuid] AND status = 'context_built'`, run, w.Event(3).ActivityID); got != 1 {
		t.Fatalf("run %s is not triggered by E3", run)
	}
	again := w.NewRun(t, 1)
	if again == run || count(t, `SELECT count(*) FROM agent_runs WHERE id = $1::uuid AND status = 'cancelled'`, run) != 1 {
		t.Fatal("a new run must cancel the previous open one")
	}
}
