package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// MutationEvidence is the evidence a mutation is read from.
type MutationEvidence struct {
	Kind  string  `json:"kind"`
	RefID string  `json:"ref_id"`
	Note  *string `json:"note"`
}

// KnowledgeMutation is knowledge_mutation.v1.json.
type KnowledgeMutation struct {
	ID                     string           `json:"id"`
	Operation              string           `json:"operation"`
	KnowledgeID            string           `json:"knowledge_id"`
	KnowledgeKey           *string          `json:"knowledge_key"`
	Title                  string           `json:"title"`
	Scope                  string           `json:"scope"`
	Preconditions          json.RawMessage  `json:"preconditions"`
	ApplicabilityCondition json.RawMessage  `json:"applicability_conditions"`
	Exceptions             json.RawMessage  `json:"exceptions"`
	EvidenceEpisodeID      string           `json:"evidence_episode_id"`
	Evidence               MutationEvidence `json:"evidence"`
	HumanVerdict           string           `json:"human_verdict"`
	HumanConfirmed         bool             `json:"human_confirmed"`
	Version                int              `json:"version"`
	Status                 string           `json:"status"`
	StatusBefore           *string          `json:"status_before"`
	OccurredAt             time.Time        `json:"occurred_at"`
}

// KnowledgeMutations is GET /episodes/{id}/knowledge-mutations.
type KnowledgeMutations struct {
	EpisodeID string              `json:"episode_id"`
	Items     []KnowledgeMutation `json:"items"`
}

// Operations of a derived mutation (knowledge_mutation.v1.json operation). REFINE and NARROW are reserved: the
// lifecycle only counts evidence and moves status, so a derived mutation never carries them.
const (
	opCreate         = "CREATE"
	opSupport        = "SUPPORT"
	opCounterexample = "COUNTEREXAMPLE"
	opPromote        = "PROMOTE"
	opDemote         = "DEMOTE"
	opDispute        = "DISPUTE"
	opStale          = "STALE"
)

// mutationNS is the namespace of the stable mutation ids (a fixed uuid: changing it changes every id).
const mutationNS = "6b0c1f4e-7a52-4d8b-9c3e-0f6d2a1b8e47"

type evidenceRow struct {
	id, knowledgeID, kind, refID string
	note                         *string
	at                           time.Time
}

type historyRow struct {
	from, to     *string
	evidenceKind *string
	evidenceRef  *string
	knowledgeID  string
}

type knowledgeRow struct {
	id, title, status             string
	key                           *string
	signature, applic, exceptions []byte
	createdAt                     time.Time
	provenanceNote                *string
}

// KnowledgeMutations derives what the episode did to company knowledge from the lifecycle's own records. ErrNotFound:
// no such episode. An episode that changed no knowledge has an empty list.
func (r *Reader) KnowledgeMutations(ctx context.Context, episodeID string) (KnowledgeMutations, error) {
	out := KnowledgeMutations{EpisodeID: episodeID, Items: []KnowledgeMutation{}}
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		items, err := buildMutations(ctx, db, episodeID)
		out.Items = items
		return err
	})
	return out, err
}

func buildMutations(ctx context.Context, db claimstore.DB, episodeID string) ([]KnowledgeMutation, error) {
	e, err := loadEpisodeRow(ctx, db, episodeID)
	if err != nil {
		return nil, err
	}
	mine, err := episodeEvidence(ctx, db, e)
	if err != nil {
		return nil, err
	}
	created, err := createdByEpisode(ctx, db, e.ID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, ev := range mine {
		ids = appendOnce(ids, ev.knowledgeID)
	}
	for _, id := range created {
		ids = appendOnce(ids, id)
	}
	if len(ids) == 0 {
		return []KnowledgeMutation{}, nil
	}
	rows, err := loadKnowledge(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	all, err := loadAllEvidence(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	hist, err := loadHistory(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	verdict, err := loadJudgmentStatus(ctx, db, e.ID)
	if err != nil {
		return nil, err
	}
	base := mutationBase{episode: e, verdict: verdict}
	var out []KnowledgeMutation
	for _, id := range ids {
		k := rows[id]
		out = append(out, base.forKnowledge(k, slices.Contains(created, id), mine, all[id], hist[id])...)
	}
	slices.SortFunc(out, func(a, b KnowledgeMutation) int {
		if c := a.OccurredAt.Compare(b.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func appendOnce(xs []string, x string) []string {
	if slices.Contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

// episodeEvidence is the evidence rows the episode attached: its own decision episode and counterexamples, its human
// decision, the reactions it drew and the outcomes of its run.
func episodeEvidence(ctx context.Context, db claimstore.DB, e episodeRow) ([]evidenceRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT ke.id::text, ke.knowledge_id::text, ke.kind, ke.ref_id::text, ke.note, ke.created_at
 FROM knowledge_evidence ke
 WHERE (ke.kind IN ('decision_episode', 'counterexample') AND ke.ref_id = $1::uuid)
    OR (ke.kind = 'human_decision' AND ke.ref_id = $2::uuid)
    OR (ke.kind = 'customer_reaction' AND ke.ref_id IN (SELECT id FROM customer_reactions WHERE decision_episode_id = $1::uuid))
    OR (ke.kind = 'business_outcome' AND ke.ref_id IN (SELECT id FROM business_outcomes WHERE agent_run_id = $3::uuid))
 ORDER BY ke.created_at, ke.id`, e.ID, e.HumanDecisionID, e.RunID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read episode evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func scanEvidence(rows *sql.Rows) ([]evidenceRow, error) {
	var out []evidenceRow
	for rows.Next() {
		var ev evidenceRow
		if err := rows.Scan(&ev.id, &ev.knowledgeID, &ev.kind, &ev.refID, &ev.note, &ev.at); err != nil {
			return nil, fmt.Errorf("controlplane: scan evidence: %w", err)
		}
		ev.at = ev.at.UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

func createdByEpisode(ctx context.Context, db claimstore.DB, episodeID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM knowledge WHERE provenance ->> 'source_decision_episode_id' = $1 ORDER BY created_at, id`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read knowledge created by the episode: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("controlplane: scan knowledge id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadKnowledge(ctx context.Context, db claimstore.DB, ids []string) (map[string]knowledgeRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, key, title, status, situation_signature, applicability_conditions, exceptions, created_at,
 provenance ->> 'note' FROM knowledge WHERE id = ANY(string_to_array($1, ',')::uuid[])`, strings.Join(ids, ","))
	if err != nil {
		return nil, fmt.Errorf("controlplane: read knowledge: %w", err)
	}
	defer rows.Close()
	out := map[string]knowledgeRow{}
	for rows.Next() {
		var k knowledgeRow
		if err := rows.Scan(&k.id, &k.key, &k.title, &k.status, &k.signature, &k.applic, &k.exceptions, &k.createdAt, &k.provenanceNote); err != nil {
			return nil, fmt.Errorf("controlplane: scan knowledge: %w", err)
		}
		k.createdAt = k.createdAt.UTC()
		out[k.id] = k
	}
	return out, rows.Err()
}

// loadAllEvidence is every evidence row of the knowledge objects, oldest first: the order that gives each its version.
func loadAllEvidence(ctx context.Context, db claimstore.DB, ids []string) (map[string][]evidenceRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, knowledge_id::text, kind, ref_id::text, note, created_at FROM knowledge_evidence
 WHERE knowledge_id = ANY(string_to_array($1, ',')::uuid[]) ORDER BY created_at, id`, strings.Join(ids, ","))
	if err != nil {
		return nil, fmt.Errorf("controlplane: read knowledge evidence: %w", err)
	}
	defer rows.Close()
	list, err := scanEvidence(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]evidenceRow{}
	for _, ev := range list {
		out[ev.knowledgeID] = append(out[ev.knowledgeID], ev)
	}
	return out, nil
}

func loadHistory(ctx context.Context, db claimstore.DB, ids []string) (map[string][]historyRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT knowledge_id::text, from_status, to_status, evidence_kind, evidence_ref_id::text
 FROM knowledge_status_history WHERE knowledge_id = ANY(string_to_array($1, ',')::uuid[]) ORDER BY id`, strings.Join(ids, ","))
	if err != nil {
		return nil, fmt.Errorf("controlplane: read knowledge status history: %w", err)
	}
	defer rows.Close()
	out := map[string][]historyRow{}
	for rows.Next() {
		var h historyRow
		var to string
		if err := rows.Scan(&h.knowledgeID, &h.from, &to, &h.evidenceKind, &h.evidenceRef); err != nil {
			return nil, fmt.Errorf("controlplane: scan status history: %w", err)
		}
		h.to = &to
		out[h.knowledgeID] = append(out[h.knowledgeID], h)
	}
	return out, rows.Err()
}

type mutationBase struct {
	episode episodeRow
	verdict string
}

// forKnowledge derives the mutations of one knowledge object: a CREATE when the episode taught it, then one mutation
// per piece of the episode's evidence, with the status the evidence's own change recorded.
func (b mutationBase) forKnowledge(k knowledgeRow, createdHere bool, mine, all []evidenceRow, hist []historyRow) []KnowledgeMutation {
	statusBefore, statusAfter := statusChain(all, hist)
	var out []KnowledgeMutation
	absorbed := ""
	if createdHere {
		m, id := b.create(k, all, statusAfter, hist)
		absorbed = id
		out = append(out, m)
	}
	for _, ev := range mine {
		if ev.knowledgeID != k.id || ev.id == absorbed {
			continue
		}
		version := 1 + slices.IndexFunc(all, func(x evidenceRow) bool { return x.id == ev.id })
		before := statusBefore[ev.id]
		out = append(out, b.mutation(k, ev, opFor(ev, linkOf(hist, ev)), version, statusAfter[ev.id], &before))
	}
	return out
}

// create is the CREATE mutation of a knowledge object the episode taught. The episode's own decision_episode evidence
// on it (the seed) is part of the CREATE, not a second mutation: its id is returned so the caller skips it.
func (b mutationBase) create(k knowledgeRow, all []evidenceRow, statusAfter map[string]string, hist []historyRow) (KnowledgeMutation, string) {
	status, absorbed := initialStatus(hist), ""
	for _, ev := range all {
		if ev.kind == "decision_episode" && ev.refID == b.episode.ID {
			absorbed, status = ev.id, statusAfter[ev.id]
			break
		}
	}
	ev := evidenceRow{id: "create", knowledgeID: k.id, kind: "decision_episode", refID: b.episode.ID, note: k.provenanceNote, at: k.createdAt}
	return b.mutation(k, ev, opCreate, 1, status, nil), absorbed
}

func (b mutationBase) mutation(k knowledgeRow, ev evidenceRow, op string, version int, status string, before *string) KnowledgeMutation {
	return KnowledgeMutation{
		ID: stableID(mutationNS, k.id, ev.id), Operation: op, KnowledgeID: k.id, KnowledgeKey: k.key, Title: k.title,
		Scope: b.episode.LearningScope, Preconditions: jsonArray(k.signature), ApplicabilityCondition: jsonArray(k.applic),
		Exceptions: jsonArray(k.exceptions), EvidenceEpisodeID: b.episode.ID,
		Evidence:     MutationEvidence{Kind: ev.kind, RefID: ev.refID, Note: ev.note},
		HumanVerdict: b.verdict, HumanConfirmed: b.verdict == "confirmed", Version: version, Status: status, StatusBefore: before, OccurredAt: ev.at,
	}
}

func jsonArray(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
}

func initialStatus(hist []historyRow) string {
	for _, h := range hist {
		if h.from == nil && h.to != nil {
			return *h.to
		}
	}
	return "candidate"
}

// linkOf is the status change the evidence itself caused (nil when it moved no status).
func linkOf(hist []historyRow, ev evidenceRow) *historyRow {
	for i := range hist {
		h := &hist[i]
		if h.evidenceKind != nil && h.evidenceRef != nil && *h.evidenceKind == ev.kind && *h.evidenceRef == ev.refID {
			return h
		}
	}
	return nil
}

// statusChain walks the object's evidence in order: the status before each piece and the one it left. A piece that
// caused a status change carries it; one that did not carries the previous status forward.
func statusChain(all []evidenceRow, hist []historyRow) (before, after map[string]string) {
	before, after = map[string]string{}, map[string]string{}
	current := initialStatus(hist)
	for _, ev := range all {
		before[ev.id] = current
		if link := linkOf(hist, ev); link != nil && link.to != nil {
			current = *link.to
		}
		after[ev.id] = current
	}
	return before, after
}

// ladder orders the statuses a knowledge object climbs; disputed and stale are off it.
var ladder = map[string]int{"candidate": 0, "provisional": 1, "supported": 2, "confirmed": 3}

// demotes: a move down the ladder (both ends on it). Coming back onto the ladder from disputed or stale is a promotion.
func demotes(from, to string) bool {
	f, fok := ladder[from]
	t, tok := ladder[to]
	return fok && tok && t < f
}

// opFor names what the evidence did: a status change wins (DISPUTE, STALE, then PROMOTE or DEMOTE by the direction on
// the ladder), else the evidence kind decides between a counterexample (also a negative reaction) and support.
func opFor(ev evidenceRow, link *historyRow) string {
	if link != nil && link.to != nil {
		switch {
		case *link.to == "disputed":
			return opDispute
		case *link.to == "stale":
			return opStale
		case link.from != nil && demotes(*link.from, *link.to):
			return opDemote
		case link.from == nil || *link.from != *link.to:
			return opPromote
		}
	}
	negative := ev.kind == "customer_reaction" && ev.note != nil && *ev.note == "polarity=negative"
	if ev.kind == "counterexample" || negative {
		return opCounterexample
	}
	return opSupport
}
