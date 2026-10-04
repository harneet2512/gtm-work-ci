package ctxgraph

import (
	"context"
	"testing"
	"time"
)

var bg = context.Background()

func projectAll(t *testing.T, s ids, p *Projector) {
	t.Helper()
	for _, a := range []string{s["A"], s["B"]} {
		if err := Enqueue(bg, pg.DB, a, nil, ReasonManual, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func graphDigest(t *testing.T, g *Graph) (string, int64, int64) {
	t.Helper()
	nodes, edges, err := g.storedAll(bg)
	if err != nil {
		t.Fatal(err)
	}
	return Digest(hashesOf(nodes, edges)), int64(len(nodes)), int64(len(edges))
}

func TestProjectionWritesTheSnapshotAndDriftIsZero(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)

	snap := buildAcme(t, s)
	nodes, edges, err := g.storedForAccount(bg, s["A"], snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != len(snap.Nodes) || len(edges) != len(snap.Edges) {
		t.Fatalf("neo4j holds %d nodes %d edges for Acme, snapshot has %d and %d", len(nodes), len(edges), len(snap.Nodes), len(snap.Edges))
	}
	rep, err := p.Drift(bg, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drift != 0 {
		t.Fatalf("drift = %d: missing %v extra %v mismatched %v tampered %v", rep.Drift, rep.Missing, rep.Extra, rep.Mismatched, rep.Tampered)
	}
	var cp string
	if err := pg.DB.QueryRow(`SELECT snapshot_hash FROM graph_projection_checkpoints WHERE account_id = $1::uuid`, s["A"]).Scan(&cp); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if cp != Digest(snap.Hashes()) {
		t.Error("the checkpoint digest is not the snapshot digest")
	}
}

func TestProjectingTheSameOutboxItemTwiceGivesAnIdenticalGraph(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)
	first, n1, e1 := graphDigest(t, g)

	// The same account projected again (a redelivered outbox item) changes nothing and writes nothing.
	res, err := p.ProjectAccount(bg, s["A"])
	if err != nil {
		t.Fatal(err)
	}
	if !res.Diff.Empty() {
		t.Fatalf("re-projecting an unchanged account produced a diff: %+v", res.Diff.Summary())
	}
	second, n2, e2 := graphDigest(t, g)
	if first != second || n1 != n2 || e1 != e2 {
		t.Fatalf("graph changed on re-projection: %s/%d/%d -> %s/%d/%d", first, n1, e1, second, n2, e2)
	}
	// Two enqueues coalesce into one job, and draining it twice is a no-op.
	for i := 0; i < 2; i++ {
		if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_CALL"]}, ReasonRecompute, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var pending int
	if err := pg.DB.QueryRow(`SELECT count(*) FROM graph_projection_jobs WHERE claimed_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending jobs = %d, %v; enqueues must coalesce", pending, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := p.Drain(bg); err != nil {
			t.Fatal(err)
		}
	}
	if third, _, _ := graphDigest(t, g); third != first {
		t.Fatal("draining a coalesced job changed the graph")
	}
}

func TestACrashedProjectionIsRetriedInPlaceAndKeepsItsRecordedDiff(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_CALL"]}, ReasonRecompute, time.Now()); err != nil {
		t.Fatal(err)
	}
	job, err := claimJob(bg, pg.DB, "crasher", time.Now(), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	// The first attempt wrote the graph and recorded the diff, then died before completing.
	snap := buildAcme(t, s)
	nodes, edges, _ := g.storedForAccount(bg, s["A"], snap)
	diff := computeDiff(snap, nodes, edges)
	if err := recordDiff(bg, pg.DB, *job, s["A"], diff); err != nil {
		t.Fatal(err)
	}
	if err := p.apply(bg, snap, diff); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.DB.Exec(`UPDATE graph_projection_jobs SET lease_expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	res, claimed, err := p.RunOnce(bg)
	if err != nil || !claimed || res.JobID != job.ID {
		t.Fatalf("retry = %+v claimed=%v err=%v, want the same job %d", res, claimed, err, job.ID)
	}
	if !res.Diff.Empty() {
		t.Error("the retry saw the graph already written")
	}
	var added int
	if err := pg.DB.QueryRow(`SELECT (summary ->> 'added')::int FROM graph_projection_diffs WHERE job_id = $1`, job.ID).Scan(&added); err != nil || added == 0 {
		t.Fatalf("recorded diff lost its additions: %d %v", added, err)
	}
	var done bool
	if err := pg.DB.QueryRow(`SELECT completed_at IS NOT NULL FROM graph_projection_jobs WHERE id = $1`, job.ID).Scan(&done); err != nil || !done {
		t.Fatalf("job not completed: %v %v", done, err)
	}
}

func TestHistoryStaysQueryableInTheGraph(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	projectAll(t, s, newProjector(t, g))
	recs, err := g.read(bg, "MATCH (p:Person)-[e:CHAMPION_FOR]->(o:Opportunity {id: $o}) RETURN p.id AS p, e.status AS status, e.valid_from AS vf, e.valid_to AS vt ORDER BY e.valid_from, e.status", map[string]any{"o": s["OPP"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("champion_for edges in the graph = %d, want the open one and the closed one", len(recs))
	}
	var sawClosed bool
	for _, r := range recs {
		if r["status"] == "closed" && r["vt"] != nil && r["p"] == s["BUYER"] {
			sawClosed = true
		}
	}
	if !sawClosed {
		t.Fatalf("closed champion edge with valid_to not found: %v", recs)
	}
	old, err := g.read(bg, "MATCH (c:Claim {id: $id}) RETURN c.status AS status, c.valid_to AS vt", map[string]any{"id": s["C_OLD"]})
	if err != nil || len(old) != 1 || old[0]["status"] != "superseded" || old[0]["vt"] == nil {
		t.Fatalf("superseded claim not queryable with its end: %v %v", old, err)
	}
}

func TestRebuildFromEmptyNeo4jRestoresTheSameCanonicalGraph(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)
	digest, nodes, edges := graphDigest(t, g)
	if nodes == 0 || edges == 0 {
		t.Fatal("empty graph")
	}
	if err := g.wipe(bg); err != nil {
		t.Fatal(err)
	}
	if n, e, _ := g.Counts(bg); n != 0 || e != 0 {
		t.Fatalf("wipe left %d nodes %d edges", n, e)
	}
	rep, err := p.Rebuild(bg, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digest != digest || rep.Nodes != nodes || rep.Edges != edges {
		t.Fatalf("rebuild = %s/%d/%d, original %s/%d/%d", rep.Digest, rep.Nodes, rep.Edges, digest, nodes, edges)
	}
	drift, err := p.Drift(bg, "")
	if err != nil || drift.Drift != 0 {
		t.Fatalf("drift after rebuild = %+v %v", drift, err)
	}
}

func TestDriftNamesMissingExtraMismatchedAndTamperedElementsAndProjectionRepairsThem(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	projectAll(t, s, p)

	mutations := []string{
		// missing: a claim node disappears
		`MATCH (c:Claim {id: "` + s["C_DOC"] + `"}) DETACH DELETE c`,
		// extra: a node Postgres never had, scoped to the account
		`CREATE (:Claim {id: "99999999-9999-4999-8999-999999999999", account_id: "` + s["A"] + `", h: "x"})`,
		// tampered: a property changes but the stored hash does not
		`MATCH (o:Opportunity {id: "` + s["OPP"] + `"}) SET o.name = "Renamed behind our back"`,
	}
	for _, m := range mutations {
		if _, err := g.write(bg, m, nil); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	rep, err := p.Drift(bg, s["A"])
	if err != nil {
		t.Fatal(err)
	}
	wantMissing := NodeKey(LabelClaim, s["C_DOC"])
	wantTampered := NodeKey(LabelOpportunity, s["OPP"])
	if !contains(rep.Missing, wantMissing) || len(rep.Extra) != 1 || !contains(rep.Tampered, wantTampered) {
		t.Fatalf("drift report = %+v", rep)
	}
	if rep.Drift != len(rep.Missing)+2 { // the missing node and its edges, the extra node (also tampered), the tampered opportunity
		t.Fatalf("drift = %d, want each drifting element counted once: %+v", rep.Drift, rep)
	}
	if _, err := p.ProjectAccount(bg, s["A"]); err != nil {
		t.Fatal(err)
	}
	if rep, err = p.Drift(bg, s["A"]); err != nil || rep.Drift != 0 {
		t.Fatalf("projection did not repair the graph: %+v %v", rep, err)
	}
}

func TestOutboxClaimsOneJobPerAccountAndReclaimsExpiredLeases(t *testing.T) {
	s := seedWorld(t)
	now := time.Now()
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_MAIL"]}, ReasonIngest, now); err != nil {
		t.Fatal(err)
	}
	first, err := claimJob(bg, pg.DB, "w1", now, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("claim: %v %v", first, err)
	}
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_CALL"]}, ReasonRecompute, now); err != nil {
		t.Fatal(err)
	}
	if got, err := claimJob(bg, pg.DB, "w2", now, time.Minute); err != nil || got != nil {
		t.Fatalf("a second job of an account in flight was claimed: %+v %v", got, err)
	}
	if got, err := claimJob(bg, pg.DB, "w2", now.Add(2*time.Minute), time.Minute); err != nil || got == nil || got.ID != first.ID || got.Attempts != 2 {
		t.Fatalf("expired lease not reclaimed in place: %+v %v", got, err)
	}
}

func TestBarrierHoldsUntilTheProjectionIsComplete(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	p := newProjector(t, g)
	b := NewBarrier(pg.DB)
	if done, err := b.Complete(bg, s["A"]); err != nil || !done {
		t.Fatalf("nothing queued: complete = %v %v", done, err)
	}
	if err := Enqueue(bg, pg.DB, s["A"], []string{s["ACT_CALL"]}, ReasonRecompute, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if done, _ := b.Complete(bg, s["A"]); done {
		t.Fatal("the barrier let a run through before the projection ran")
	}
	if done, _ := b.Complete(bg, s["B"]); !done {
		t.Fatal("another account's barrier must be unaffected")
	}
	lag, err := ReadLag(bg, pg.DB, time.Now())
	if err != nil || lag.PendingJobs != 1 || lag.OldestUnfinishedSeconds < 59 {
		t.Fatalf("lag = %+v %v", lag, err)
	}
	waitCtx, cancel := context.WithTimeout(bg, 200*time.Millisecond)
	defer cancel()
	if err := b.Wait(waitCtx, s["A"], 20*time.Millisecond); err == nil {
		t.Fatal("Wait returned before the projection completed")
	}
	if _, err := p.Drain(bg); err != nil {
		t.Fatal(err)
	}
	if done, _ := b.Complete(bg, s["A"]); !done {
		t.Fatal("the barrier stayed closed after the projection completed")
	}
	if err := b.Wait(bg, s["A"], 10*time.Millisecond); err != nil {
		t.Fatalf("Wait after completion: %v", err)
	}
	lag, err = ReadLag(bg, pg.DB, time.Now())
	if err != nil || lag.PendingJobs != 0 || lag.InFlightJobs != 0 || lag.LastProjectedAt == nil || lag.LatencyP95Seconds < 59 {
		t.Fatalf("lag after drain = %+v %v", lag, err)
	}
}

func TestIdentifiersOutsideTheOntologyAreRefused(t *testing.T) {
	for _, bad := range []string{"Account) DETACH DELETE (n", "NotALabel", "", "WORKS_AT`] DELETE e //"} {
		if _, err := ident(bad); err == nil {
			t.Errorf("ident(%q) accepted a name outside the ontology", bad)
		}
	}
	if q, err := ident("WORKS_AT"); err != nil || q != "`WORKS_AT`" {
		t.Errorf("ident(WORKS_AT) = %q %v", q, err)
	}
}
