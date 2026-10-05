package ctxgraph

import (
	"errors"
	"testing"
	"time"
)

// A knowledge status change is a write the graph snapshot reads: it must keep the barrier closed for every
// account the knowledge touches until it is projected, and the projected node must show the new status.
func TestAKnowledgeWriteReopensTheBarrierUntilProjected(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)
	b := NewBarrier(pg.DB)
	if done, _ := b.Complete(bg, s["A"]); !done {
		t.Fatal("the world is projected, the barrier must be open")
	}

	if _, err := pg.DB.Exec(`UPDATE knowledge SET status = 'confirmed' WHERE id = $1::uuid`, s["K1"]); err != nil {
		t.Fatal(err)
	}
	if done, _ := b.Complete(bg, s["A"]); done {
		t.Fatal("a knowledge status change left the barrier open: the graph is stale")
	}
	if done, _ := b.Complete(bg, s["B"]); !done {
		t.Fatal("an account the knowledge does not touch must stay open")
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	if done, _ := b.Complete(bg, s["A"]); !done {
		t.Fatal("projected, the barrier must open")
	}
	recs, err := g.read(bg, "MATCH (k:Knowledge {id: $id}) RETURN k.status AS s", map[string]any{"id": s["K1"]})
	if err != nil || len(recs) != 1 || recs[0]["s"] != "confirmed" {
		t.Fatalf("knowledge node = %v %v", recs, err)
	}
}

func TestEpisodeAndRunWritesEnqueueTheirAccount(t *testing.T) {
	s := seedWorld(t)
	if _, err := pg.DB.Exec(`DELETE FROM graph_projection_jobs`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.DB.Exec(`UPDATE decision_episodes SET state_version = 2 WHERE id = $1::uuid`, s["EP"]); err != nil {
		t.Fatal(err)
	}
	var reasons string
	if err := pg.DB.QueryRow(`SELECT array_to_string(reasons, ',') FROM graph_projection_jobs WHERE account_id = $1::uuid AND completed_at IS NULL`, s["A"]).Scan(&reasons); err != nil || reasons != "episode" {
		t.Fatalf("episode write enqueued %q %v", reasons, err)
	}
	if _, err := pg.DB.Exec(`UPDATE agent_runs SET knowledge_refs_used = '[]'::jsonb WHERE id = $1::uuid`, s["RUN"]); err != nil {
		t.Fatal(err)
	}
	if err := pg.DB.QueryRow(`SELECT array_to_string(reasons, ',') FROM graph_projection_jobs WHERE account_id = $1::uuid AND completed_at IS NULL`, s["A"]).Scan(&reasons); err != nil || reasons != "episode" {
		t.Fatalf("an empty knowledge list must not add a reason: %q %v", reasons, err)
	}
}

// The diff is found through any event it touched, even when the job's activity list does not name it.
func TestEventDiffIsFoundThroughTheEventsItsChangesCiteEvenWithoutActivityIDs(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	if err := Enqueue(bg, pg.DB, s["A"], nil, ReasonManual, time.Now()); err != nil { // a burst that names no activity
		t.Fatal(err)
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	d, err := DiffForEvent(bg, pg.DB, s["SE_CALL"])
	if err != nil {
		t.Fatal(err)
	}
	if !d.Projected || findChange(d, "node", OpAdded, LabelConversation, s["ACT_CALL"]) == nil {
		t.Fatalf("the call's event must reach its own additions: %+v", d.Summary)
	}
}

func TestDriftFlagsElementsOutsideTheOntologyAndTotalMismatch(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)
	if _, err := g.write(bg, `CREATE (:Widget {x: 1}), (:Account {name: "no id, no hash"})`, nil); err != nil {
		t.Fatal(err)
	}
	rep, err := p.Drift(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.OutsideOntology, "label:Widget") || !rep.CountMismatch || rep.Drift < 2 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestAProjectionIsOneNeo4jTransaction(t *testing.T) {
	g := newGraph(t)
	boom := errors.New("boom")
	err := g.inTx(bg, func(run exec) error {
		if _, err := run(bg, `CREATE (:Account {id: "tx-1"})`, nil); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if n, _, _ := g.Counts(bg); n != 0 {
		t.Fatalf("a failed projection left %d nodes: it must roll back as a whole", n)
	}
}

// While a rebuild holds the lock no projection job is claimed, and its manual jobs keep the barrier closed.
func TestRebuildHoldsTheClaimLockAndTheBarrierUntilDone(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)
	if err := Enqueue(bg, pg.DB, s["A"], nil, ReasonRecompute, time.Now()); err != nil {
		t.Fatal(err)
	}
	conn, err := pg.DB.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(bg, `SELECT pg_advisory_lock($1)`, rebuildLockKey); err != nil {
		t.Fatal(err)
	}
	if job, err := claimJob(bg, pg.DB, "w", time.Now(), time.Minute); err != nil || job != nil {
		t.Fatalf("a job was claimed during a rebuild: %+v %v", job, err)
	}
	if _, err := conn.ExecContext(bg, `SELECT pg_advisory_unlock($1)`, rebuildLockKey); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if job, err := claimJob(bg, pg.DB, "w", time.Now(), -time.Second); err != nil || job == nil { // claimed with an already expired lease
		t.Fatalf("claiming must resume after the rebuild: %+v %v", job, err)
	}

	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	rep, err := p.Rebuild(bg, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Nodes == 0 {
		t.Fatal("empty rebuild")
	}
	if done, _ := NewBarrier(pg.DB).Complete(bg, s["A"]); !done {
		t.Fatal("the barrier must be open once the rebuild is drained")
	}
	var manual int
	if err := pg.DB.QueryRow(`SELECT count(*) FROM graph_projection_jobs WHERE 'manual' = ANY(reasons) AND completed_at IS NOT NULL`).Scan(&manual); err != nil || manual < 2 {
		t.Fatalf("the rebuild must have enqueued and completed a manual job per account: %d %v", manual, err)
	}
}
