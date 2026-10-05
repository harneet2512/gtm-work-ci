package ctxgraph

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var pg *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	env, err := storetest.Start(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "start test database: %v\n", err)
		return 1
	}
	pg = env
	defer func() {
		if cerr := env.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "close test database: %v\n", cerr)
		}
	}()
	return neo4jtest.Main(m)
}

// resetPostgres empties every table the projection reads.
func resetPostgres(t *testing.T) {
	t.Helper()
	tx, err := pg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`SET LOCAL ghost.purge_knowledge = 'on'`,
		`SET LOCAL ghost.purge_transitions = 'on'`,
		`TRUNCATE accounts, people, products, source_events, knowledge CASCADE`,
	} {
		if _, err := tx.Exec(q); err != nil {
			t.Fatalf("reset postgres: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("reset postgres: %v", err)
	}
}

// newGraph returns a clean Neo4j connection with the schema in place.
func newGraph(t *testing.T) *Graph {
	t.Helper()
	env := neo4jtest.Require(t)
	ctx := context.Background()
	g, err := Open(ctx, Config{URI: env.URI, User: env.User, Password: env.Password, Database: env.Database})
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	t.Cleanup(func() { _ = g.Close(ctx) })
	if err := g.ensureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := g.wipe(ctx); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	return g
}

func newProjector(t *testing.T, g *Graph) *Projector {
	t.Helper()
	p, err := NewProjector(pg.DB, g, Options{WorkerID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ids holds the ids of the seeded world.
type ids map[string]string

func (s ids) expand(q string) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		q = strings.ReplaceAll(q, "$"+k, "'"+s[k]+"'::uuid")
	}
	return q
}

type seedStep struct{ key, sql string }

func runSeed(t *testing.T, s ids, steps []seedStep) {
	t.Helper()
	for _, st := range steps {
		q := s.expand(st.sql)
		if st.key == "" {
			if _, err := pg.DB.Exec(q); err != nil {
				t.Fatalf("seed %.60s: %v", st.sql, err)
			}
			continue
		}
		var id string
		if err := pg.DB.QueryRow(q).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", st.key, err)
		}
		s[st.key] = id
	}
}

const docID = "gdrive:acme-mnda"

func tNow() time.Time { return time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) }

// seedWorld inserts one connected account (Acme) with an opportunity, people, conversations, a shared
// document, claims, a commitment, a signal, a decision episode and knowledge, plus a second account
// (Beta) that must stay out of Acme's projection. It returns the ids by key.
func seedWorld(t *testing.T) ids {
	t.Helper()
	resetPostgres(t)
	s := ids{}
	s["DOC"] = graph.DocumentID(docID)
	runSeed(t, s, worldEntities)
	runSeed(t, s, worldActivities)
	runSeed(t, s, worldRelationships)
	runSeed(t, s, worldClaims)
	runSeed(t, s, worldDecisions)
	// The seed writes trigger outbox jobs (episodes, knowledge); tests start from a clean outbox.
	if _, err := pg.DB.Exec(`DELETE FROM graph_projection_jobs`); err != nil {
		t.Fatal(err)
	}
	return s
}

func se(key, obj string, n int, payload string) seedStep {
	return seedStep{key, fmt.Sprintf(`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('%s','%s','k',repeat('%x',64),'%s') RETURNING id`, sourceSystemOf(key), obj, n, payload)}
}

func sourceSystemOf(key string) string {
	switch key {
	case "SE_CALL":
		return "call"
	case "SE_CRM":
		return "crm"
	case "SE_DOC":
		return "docs"
	}
	return "email"
}

var worldEntities = []seedStep{
	{"A", `INSERT INTO accounts (name, domain) VALUES ('Acme Corp','acme.com') RETURNING id`},
	{"B", `INSERT INTO accounts (name, domain) VALUES ('Beta Inc','beta.io') RETURNING id`},
	{"OPP", `INSERT INTO opportunities (account_id, name, motion) VALUES ($A,'Acme EU expansion','expansion') RETURNING id`},
	{"OPPB", `INSERT INTO opportunities (account_id, name, motion) VALUES ($B,'Beta new logo','new_business') RETURNING id`},
	{"REP", `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee','Dana Rep','dana@vendor.example') RETURNING id`},
	{"CHAMP", `INSERT INTO people (kind, display_name, primary_email, title, account_id) VALUES ('contact','Priya Champion','priya@acme.com','VP Ops',$A) RETURNING id`},
	{"BUYER", `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact','Marco Buyer','marco@acme.com',$A) RETURNING id`},
	{"BCONTACT", `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact','Bea Beta','bea@beta.io',$B) RETURNING id`},
	{"", `UPDATE opportunities SET owner_person_id = $REP WHERE id = $OPP`},
}

var worldActivities = []seedStep{
	se("SE_MAIL", "m1", 1, "{}"), se("SE_CALL", "c1", 2, "{}"), se("SE_CRM", "o1", 3, "{}"),
	se("SE_DOC", "d1", 4, `{"kind":"document","document_id":"`+docID+`"}`), se("SE_HID", "m2", 5, "{}"), se("SE_B", "m3", 6, "{}"),
	{"ACT_MAIL", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance)
 VALUES ($SE_MAIL,'EmailReceived','email','m1','2026-09-01T10:00:00Z',$A,$OPP,'{"source_system":"email","source_object_id":"m1"}') RETURNING id`},
	{"ACT_CALL", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance)
 VALUES ($SE_CALL,'TranscriptReady','call','c1','2026-09-02T10:00:00Z',$A,$OPP,'{"source_system":"call","source_object_id":"c1"}') RETURNING id`},
	{"ACT_CRM", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, provenance)
 VALUES ($SE_CRM,'CRMFieldChanged','crm','o1','2026-09-03T10:00:00Z',$A,$OPP,'{"source_system":"crm","source_object_id":"o1"}') RETURNING id`},
	{"ACT_DOC", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
 VALUES ($SE_DOC,'DocumentShared','docs','d1','2026-09-04T10:00:00Z',$A,'{"source_system":"docs","source_object_id":"d1"}') RETURNING id`},
	{"ACT_HID", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, opportunity_id, permissions, provenance)
 VALUES ($SE_HID,'EmailSent','email','m2','2026-09-05T10:00:00Z',$A,$OPP,'{"visibility":"person"}','{"source_system":"email","source_object_id":"m2"}') RETURNING id`},
	{"ACT_B", `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
 VALUES ($SE_B,'EmailReceived','email','m3','2026-09-05T10:00:00Z',$B,'{"source_system":"email","source_object_id":"m3"}') RETURNING id`},
}

func rel(src, srcID, typ, dst, dstID, standing string, conf float64, act string, validTo string) seedStep {
	to := "NULL"
	if validTo != "" {
		to = "'" + validTo + "'"
	}
	a := "NULL"
	if act != "" {
		a = "$" + act
	}
	return seedStep{"", fmt.Sprintf(`INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, source_activity_id, valid_from, valid_to)
 VALUES ('%s',%s,'%s','%s',%s,'%s',%v,%s,'2026-09-01T00:00:00Z',%s)`, src, srcID, typ, dst, dstID, standing, conf, a, to)}
}

var worldRelationships = []seedStep{
	rel("person", "$CHAMP", "works_at", "account", "$A", "crm_explicit", 0.9, "", ""),
	rel("opportunity", "$OPP", "belongs_to", "account", "$A", "crm_explicit", 1, "", ""),
	rel("person", "$CHAMP", "champion_for", "opportunity", "$OPP", "first_party_ai", 0.8, "ACT_CALL", ""),
	rel("person", "$BUYER", "economic_buyer_for", "opportunity", "$OPP", "crm_explicit", 0.9, "ACT_CRM", ""),
	rel("person", "$BUYER", "champion_for", "opportunity", "$OPP", "first_party_ai", 0.5, "ACT_MAIL", "2026-09-02T00:00:00Z"),
	rel("person", "$CHAMP", "participated_in", "activity", "$ACT_CALL", "first_party_record", 1, "ACT_CALL", ""),
	rel("activity", "$ACT_CALL", "involves", "person", "$CHAMP", "first_party_record", 1, "ACT_CALL", ""),
	rel("activity", "$ACT_CALL", "about", "opportunity", "$OPP", "first_party_record", 1, "ACT_CALL", ""),
	rel("activity", "$ACT_MAIL", "about", "opportunity", "$OPP", "first_party_record", 1, "ACT_MAIL", ""),
	rel("document", "$DOC", "shared_with", "person", "$BUYER", "first_party_record", 1, "ACT_DOC", ""),
	rel("person", "$REP", "owns", "opportunity", "$OPP", "crm_explicit", 1, "", ""),
	rel("person", "$CHAMP", "blocks", "opportunity", "$OPP", "first_party_ai", 0.4, "", ""),
	rel("person", "$CHAMP", "participated_in", "activity", "$ACT_CRM", "first_party_record", 1, "", ""),
	rel("person", "$BCONTACT", "works_at", "account", "$B", "crm_explicit", 0.9, "", ""),
}

var worldClaims = []seedStep{
	{"C_CHAMP", `INSERT INTO claims (account_id, opportunity_id, subject_person_id, speaker_person_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,$CHAMP,$CHAMP,'champion','"Priya"','first_party_ai',0.85,$ACT_CALL,'I will sponsor this','2026-09-02T10:00:00Z','llm:test@v1') RETURNING id`},
	{"C_STAGE", `INSERT INTO claims (account_id, opportunity_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor)
 VALUES ($A,$OPP,'stage','"Negotiation"','crm_explicit',1,$ACT_CRM,'2026-09-03T10:00:00Z','rule:crm@v1') RETURNING id`},
	{"C_OLD", `INSERT INTO claims (account_id, opportunity_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor, status)
 VALUES ($A,$OPP,'stage','"Discovery"','crm_explicit',1,$ACT_MAIL,'2026-09-01T10:00:00Z','rule:crm@v1','outranked') RETURNING id`},
	{"", `UPDATE claims SET status = 'superseded', superseded_by = $C_STAGE WHERE id = $C_OLD`},
	{"C_COMMIT", `INSERT INTO claims (account_id, opportunity_id, subject_person_id, speaker_person_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,$CHAMP,$CHAMP,'commitment','{"item":"send security questionnaire","due":"2026-09-20"}','first_party_ai',0.7,$ACT_CALL,'we will send it by the 20th','2026-09-02T10:00:00Z','llm:test@v1') RETURNING id`},
	{"C_DOC", `INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,'decision_process','"legal review first"','first_party_ai',0.6,$ACT_DOC,'MNDA attached','2026-09-04T10:00:00Z','llm:test@v1') RETURNING id`},
	{"C_HID", `INSERT INTO claims (account_id, opportunity_id, field_path, value, standing, confidence, source_activity_id, evidence_quote, occurred_at, extractor)
 VALUES ($A,$OPP,'blockers','"budget freeze"','first_party_ai',0.6,$ACT_HID,'we are frozen','2026-09-05T10:00:00Z','llm:test@v1') RETURNING id`},
}

var worldDecisions = []seedStep{
	{"", `INSERT INTO state_history (account_id, version, as_of, state) VALUES ($A,1,'2026-09-02T10:00:00Z','{}')`},
	{"DIFF", `INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes, activity_ids)
 VALUES ($A,0,1,true,'[{"field":"champion","op":"set"}]',ARRAY[$ACT_CALL]::uuid[]) RETURNING id`},
	{"SIG", `INSERT INTO signals (account_id, opportunity_id, signal_type, state_diff_id, subject_person_id, subject_claim_id, rule, evidence_refs, occurred_at)
 VALUES ($A,$OPP,'new_stakeholder_entered',$DIFF,$CHAMP,$C_CHAMP,'sig.new_stakeholder@1',jsonb_build_array(jsonb_build_object('activity_id', $ACT_CALL::text)),'2026-09-02T10:00:00Z') RETURNING id`},
	{"K1", `INSERT INTO knowledge (title, guidance, status) VALUES ('Keep the champion in the loop','g','supported') RETURNING id`},
	{"TRIG", `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, state_diff_id) VALUES ($A,'post_interaction_followup',true,'{eligible_meeting_completed}',$DIFF) RETURNING id`},
	{"RUN", `INSERT INTO agent_runs (account_id, opportunity_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids, knowledge_refs_used)
 VALUES ($A,$OPP,'post_interaction_followup','dry_run','awaiting_human',$TRIG,ARRAY[$ACT_CALL]::uuid[],to_jsonb(ARRAY[$K1::text])) RETURNING id`},
	{"", `INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN,1,'account_agent','{}')`},
	{"HD", `INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ($RUN,'approve','web','Dana') RETURNING id`},
	{"EP", `INSERT INTO decision_episodes (agent_run_id, account_id, state_version, state_diff_id, final_draft_index, human_decision_id, human_action)
 VALUES ($RUN,$A,1,$DIFF,1,$HD,'APPROVE_UNCHANGED') RETURNING id`},
	{"", `INSERT INTO knowledge_evidence (knowledge_id, kind, ref_id) VALUES ($K1,'decision_episode',$EP)`},
}

// all returns every key of a snapshot's nodes or edges of one type.
func keysOfType(s Snapshot, kind, typ string) []string {
	var out []string
	if kind == "node" {
		for _, n := range s.Nodes {
			if n.Primary() == typ {
				out = append(out, n.ID)
			}
		}
	} else {
		for _, e := range s.Edges {
			if e.Type == typ {
				out = append(out, e.ID)
			}
		}
	}
	sort.Strings(out)
	return out
}
