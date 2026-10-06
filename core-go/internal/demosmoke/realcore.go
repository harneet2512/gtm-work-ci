package demosmoke

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/bucket2run"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
	"github.com/harneet2512/gtm-work/core-go/internal/slackfake"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

// RealCore is the real core API (api.NewHandler) over an embedded Postgres seeded with one demo episode
// (HAR-139): the same Slack surface and driver exercise the actual decision/send/verdict store instead of
// fakecore's fixture. Requests are logged in the same fakecore.Log shape Verify reads.
type RealCore struct {
	Seed       strategytest.Seeded
	URL        string
	db         *sql.DB
	env        *storetest.Env
	srv        *httptest.Server
	logf       *reqLog
	ing        *ingest.Service    // the real ingest — the supervision reply goes through it
	sup        *reactions.Service // the real supervision scan/read (HAR-120)
	tok        string
	strategies *strategystore.Service // the real store, with the Bucket 2 gates attached
}

// StartRealCore launches embedded Postgres, migrates, loads the synthetic sample world, seeds the
// awaiting-choice episode and serves the real core API on loopback. dir gets fakecore-log.json.
func StartRealCore(ctx context.Context, dir, token string, log *slog.Logger) (*RealCore, error) {
	env, err := storetest.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("demosmoke real-core: start database: %w", err)
	}
	rc := &RealCore{env: env, db: env.DB}
	defer func() {
		if err != nil {
			_ = rc.Close()
		}
	}()
	sample, err := ctxfixture.SampleDir()
	if err != nil {
		return nil, err
	}
	world, err := ctxfixture.Load(ctx, env.DB, sample)
	if err != nil {
		return nil, fmt.Errorf("demosmoke real-core: load the sample world: %w", err)
	}
	if rc.Seed, err = strategytest.SeedE(env.DB, world.AccountA); err != nil {
		return nil, err
	}
	// A reachable name in the people directory that an email action cannot use: the send-time
	// re-evaluation blocks a send addressed to them (recipient.exists), which is the refusal the
	// driver repairs.
	if _, err = env.DB.ExecContext(ctx,
		`INSERT INTO people (id, kind, display_name, account_id) VALUES ($1::uuid, 'contact', $2, $3::uuid)`,
		strategytest.NewID(), noEmailName, world.AccountA); err != nil {
		return nil, fmt.Errorf("demosmoke real-core: seed the no-email contact: %w", err)
	}
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension()})
	if err != nil {
		return nil, err
	}
	rc.ing, rc.tok = svc, token
	// Supervision (HAR-120): the lifecycle rules grade what evidence means; the file lives in the repo
	// the smoke runs from.
	rulesPath, err := contractFile("contracts/knowledge/lifecycle.v1.json")
	if err != nil {
		return nil, err
	}
	rules, err := knowledge.LoadRules(rulesPath)
	if err != nil {
		return nil, fmt.Errorf("demosmoke real-core: %w", err)
	}
	if rc.sup, err = reactions.New(env.DB, rules, reactions.Options{}); err != nil {
		return nil, fmt.Errorf("demosmoke real-core: supervision: %w", err)
	}
	reader, err := readmodel.New(env.DB)
	if err != nil {
		return nil, err
	}
	// No delta labeler (HAR-139 fallback): the smoke never reaches a model worker.
	strategies, err := strategystore.New(env.DB, log, strategystore.WithInferrer(replayInferrer{}))
	if err != nil {
		return nil, err
	}
	// The Bucket 2 gates run inside the real path (choose, edit, send, Message 3), judged by the replay stand-in.
	recomp, err := recompute.New(env.DB, clock.Real{})
	if err != nil {
		return nil, err
	}
	gates, err := bucket2run.New(env.DB, strategies, recomp, replayJudge{})
	if err != nil {
		return nil, err
	}
	strategies.SetGates(gates)
	strategies.SetFinalJudge(gates)
	rc.strategies = strategies
	refs, err := surfacemsg.New(env.DB)
	if err != nil {
		return nil, err
	}
	inner, err := api.NewHandler(svc, token, log,
		api.WithReads(reader), api.WithStrategy(strategies), api.WithSurfaceMessages(refs),
		api.WithSupervision(rc.sup))
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	// The operator graph endpoint is Neo4j-backed; the smoke serves the people directory from Postgres.
	mux.HandleFunc("GET /accounts/{account_id}/graph", personGraph(env.DB))
	mux.Handle("/", inner)
	rc.logf = &reqLog{db: env.DB, seed: &rc.Seed, path: filepath.Join(dir, CoreLogFile), logger: log,
		log: fakecore.Log{Version: 1, Entries: []fakecore.Entry{}, Effects: []fakecore.Effect{}, StrategiesSHA256: []string{}}}
	rc.srv = httptest.NewServer(rc.logf.wrap(mux))
	rc.URL = rc.srv.URL
	return rc, nil
}

// Close stops the server and the embedded database.
func (r *RealCore) Close() error {
	if r.srv != nil {
		r.srv.Close()
	}
	if r.env != nil {
		return r.env.Close()
	}
	return nil
}

// noEmailName is the display name of the seeded contact with no email; the edit modal resolves people by
// name, so the driver can address them and the send-time eval blocks.
const noEmailName = "No Email (seed)"

// flow returns the flow identifiers and edit texts for the seeded episode: the seeded people are matched
// by their synthetic emails or names, and the human picks the rank-2 candidate, overriding Ghost's
// preference. The repaired edit (editTo...) is what passes the send-time evaluation.
func (r *RealCore) flow() flow {
	return flow{accountID: r.Seed.AccountID, runID: r.Seed.RunID, episodeID: r.Seed.EpisodeID,
		chooseCandidate: r.Seed.Candidates[1], otherCandidate: r.Seed.Candidates[2], blockTo: noEmailName,
		editTo:      "Marco (seed) <" + r.Seed.MarcoEmail() + ">",
		editCC:      r.Seed.PriyaEmail(),
		editSubject: "Repaired subject", editBody: "Hi Marco,\n\nLet us know what timing suits.\n\nBest,\nDana",
		correction: "The sponsor owns the timing", note: "real-core smoke correction"}
}

// RunRealCore drives the same flow as RunNoHuman, but against the real core API on an embedded Postgres:
// choose, edit and send go through the actual send-time re-evaluation and write the HumanDelta, which this
// run additionally asserts directly in the database (the point of the --real-core mode, HAR-139).
func RunRealCore(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, runTimeout+2*time.Minute) // embedded Postgres and the sample ingest take a while
	defer cancel()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("demosmoke: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	rc, err := StartRealCore(ctx, dir, token, quiet)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	fs := slackfake.New()
	defer fs.Close()
	audit, err := os.OpenFile(filepath.Join(dir, SlackAuditFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("demosmoke: %w", err)
	}
	defer audit.Close()
	cfg := slacksurface.Config{AppToken: "xapp-fake-smoke", BotToken: "xoxb-fake-smoke", ChannelID: smokeChannel}
	api, smc := slacksurface.NewSlackClients(cfg, fs.APIURL())
	poster := slacksurface.NewAuditPoster(slacksurface.NewSlackPoster(api), audit)
	h := slacksurface.NewHandler(slacksurface.NewCoreHTTP(rc.URL, slacksurface.Secret(token), nil), poster, cfg.ChannelID, "https://ghost.example.test", quiet)
	served := make(chan error, 1)
	go func() { served <- slacksurface.Serve(ctx, smc, h, quiet) }()
	d := &driver{ctx: ctx, fs: fs, h: h}
	runErr := d.playReal(rc.flow())
	cancel()
	if err := <-served; err != nil && runErr == nil && ctx.Err() == nil {
		runErr = fmt.Errorf("demosmoke: socket mode: %w", err)
	}
	if runErr != nil {
		return runErr
	}
	if err := rc.checkRealCore(); err != nil {
		return err
	}
	// ctx was cancelled above to stop socket mode; the supervision check gets its own deadline.
	sctx, scancel := context.WithTimeout(context.Background(), time.Minute)
	defer scancel()
	return rc.checkSupervision(sctx)
}

// playReal drives the fakecore flow plus the two refusal paths only the real store can serve (HAR-139):
// a second Choose after the first (409 selection_locked; the stored choice holds) and a Send whose final
// artifact fails a blocking eval (422 blocking_eval; the decision stays pending until the repair sends).
//
//	post M1 and M2, View full, Choose B, Choose C (locked), Edit to a recipient with no email, Send
//	(blocked), Edit back to a reachable recipient, Send, (M3 posts by itself), Edit interpretation
func (d *driver) playReal(f flow) error {
	ctx, cancel := d.step(stepTimeout)
	err := d.fs.WaitConnected(ctx)
	cancel()
	if err != nil {
		return err
	}
	pub := d.h.Publisher()
	if _, err := pub.PostBIForEpisode(d.ctx, f.accountID, f.episodeID); err != nil {
		return fmt.Errorf("demosmoke: post M1: %w", err)
	}
	m2, err := pub.PostChooser(d.ctx, f.runID)
	if err != nil {
		return fmt.Errorf("demosmoke: post M2: %w", err)
	}
	run := slacksurface.Target{RunID: f.runID}
	candB := slacksurface.Target{RunID: f.runID, CandidateID: f.chooseCandidate}
	candC := slacksurface.Target{RunID: f.runID, CandidateID: f.otherCandidate}
	meta := slacksurface.Target{RunID: f.runID, CandidateID: f.chooseCandidate, ChannelID: smokeChannel, MessageTS: m2.TS}
	steps := []struct {
		name string
		do   func() error
	}{
		{"View full", func() error { return d.viewFull(m2.TS, candB) }},
		{"Select B", func() error { return d.chooseB(m2.TS, candB) }},
		{"Select C after B", func() error { return d.chooseLocked(m2.TS, candC) }},
		{"Edit (open)", func() error { return d.editOpen(m2.TS, run) }},
		{"Edit to a no-email recipient", func() error {
			return d.editSubmitAt("V-edit-1", flow{editTo: f.blockTo, editCC: f.editCC,
				editSubject: "Blocked subject", editBody: "Hi,\n\nLet us know what timing suits.\n\nBest,\nDana"}, meta, 3)
		}},
		{"Send (blocked)", func() error { return d.sendBlocked(m2.TS, run) }},
		{"Repair edit (open)", func() error { return d.editOpen(m2.TS, run) }},
		{"Repair edit (submit)", func() error { return d.editSubmitAt("V-edit-2", f, meta, 5) }},
		{"Send", func() error { return d.sendLast(m2.TS, run) }},
		{"Edit interpretation (submit)", func() error { return d.correctAt(f, 7) }},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("demosmoke: %s: %w", s.name, err)
		}
	}
	return nil
}

// chooseLocked clicks a different candidate after the choice was recorded: core answers selection_locked
// and the surface re-renders the stored decision, so the update count moves but the choice does not.
func (d *driver) chooseLocked(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionStrategyChoose, "T-choose-locked", ts, t); err != nil {
		return err
	}
	_, err := d.waitFor("chat.update", 2)
	return err
}

// sendBlocked clicks Send while the final artifact fails a blocking eval: the message is re-rendered with
// the refusal and, unlike a real send, Message 3 is not posted.
func (d *driver) sendBlocked(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionSelectedSend, "T-send-blocked", ts, t); err != nil {
		return err
	}
	if _, err := d.waitFor("chat.update", 4); err != nil {
		return err
	}
	if posts := d.fs.CallsOf("chat.postMessage"); len(posts) != 2 {
		return fmt.Errorf("a blocked send posted message %d; the decision must stay pending", len(posts))
	}
	return nil
}

// sendLast is the Send that succeeds: M2 is re-rendered and M3 (the judgment) posts by itself.
func (d *driver) sendLast(ts string, t slacksurface.Target) error {
	if err := d.click(slacksurface.ActionSelectedSend, "T-send", ts, t); err != nil {
		return err
	}
	if _, err := d.waitFor("chat.update", 6); err != nil {
		return err
	}
	_, err := d.waitFor("chat.postMessage", 3)
	return err
}

// checkRealCore asserts in the database what only the real store can prove: the locked-out choice never
// landed, the refused send wrote no human decision, the repaired send wrote its explained HumanDelta, and
// the verdict history got its append-only rows.
func (r *RealCore) checkRealCore() error {
	one := func(q string, args ...any) string {
		var s string
		if err := r.db.QueryRow(q, args...).Scan(&s); err != nil {
			return "error:" + err.Error()
		}
		return s
	}
	if got := one(`SELECT selected_candidate_id::text FROM human_strategy_decisions WHERE decision_episode_id = $1::uuid`, r.Seed.EpisodeID); got != r.Seed.Candidates[1] {
		return fmt.Errorf("demosmoke: selection_locked let the choice move to %s", got)
	}
	if got := one(`SELECT decision FROM human_decisions WHERE agent_run_id = $1::uuid`, r.Seed.RunID); got != "edit" {
		return fmt.Errorf("demosmoke: human decision = %q, want one 'edit' (the blocked send recorded none)", got)
	}
	var linked bool
	err := r.db.QueryRow(`SELECT EXISTS (
  SELECT 1 FROM decision_episodes e JOIN human_deltas d ON d.id = e.human_delta_id WHERE e.id = $1::uuid AND NOT d.unexplained
    AND EXISTS (SELECT 1 FROM human_delta_explanations x WHERE x.human_delta_id = d.id))`,
		r.Seed.EpisodeID).Scan(&linked)
	if err != nil {
		return fmt.Errorf("demosmoke: read the human delta: %w", err)
	}
	if !linked {
		return errors.New("demosmoke: the repaired send wrote no explained human_delta linked to the episode")
	}
	for _, table := range []string{"judgment_verdicts", "judgment_notes"} {
		if got := one(`SELECT count(*)::text FROM `+table+` h JOIN judgment_inferences i ON i.id = h.judgment_inference_id WHERE i.decision_episode_id = $1::uuid`, r.Seed.EpisodeID); got != "1" {
			return fmt.Errorf("demosmoke: %s rows = %s, want the one submission in history", table, got)
		}
	}
	if err := r.checkBucket2(); err != nil {
		return err
	}
	// The request log must show the two refusals the surface absorbed: the locked choose and the blocked
	// send, both answered by the real store.
	for _, code := range []string{"selection_locked", "blocking_eval"} {
		if !r.logf.hasCode(code) {
			return fmt.Errorf("demosmoke: core never answered %s", code)
		}
	}
	return nil
}

// contractFile finds a repo-relative contracts file by walking up from the working directory — the
// smoke always runs inside the repo, the same assumption ctxfixture.SampleDir makes.
func contractFile(rel string) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, rel)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		return "", fmt.Errorf("demosmoke: %s not found above the working directory", rel)
	}
}

// personGraph serves just enough of GET /accounts/{id}/graph for the Slack surface's People(): the
// account's people as person nodes with data.email.
func personGraph(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(),
			`SELECT id::text, display_name, COALESCE(primary_email, '') FROM people WHERE account_id = $1::uuid ORDER BY display_name`,
			r.PathValue("account_id"))
		if err != nil {
			http.Error(w, `{"error":{"code":"internal_error","message":"graph read failed"}}`, http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		nodes := []map[string]any{}
		for rows.Next() {
			var id, name, email string
			if err := rows.Scan(&id, &name, &email); err != nil {
				http.Error(w, `{"error":{"code":"internal_error","message":"graph read failed"}}`, http.StatusInternalServerError)
				return
			}
			nodes = append(nodes, map[string]any{"id": id, "type": "person", "label": name,
				"data": map[string]any{"email": email}, "evidence_refs": []any{}, "source_event_ids": []any{}})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"account_id": r.PathValue("account_id"),
			"nodes": nodes, "edges": []any{}, "sections": map[string]any{}, "truncated": false})
	}
}
