package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
)

// encodeJSON marshals a persisted spec payload.
func encodeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("learning: encode: %w", err)
	}
	return string(b), nil
}

// DeltaSeed is what an unexplained HumanDelta contributes to the learning loop.
type DeltaSeed struct {
	DeltaID   string
	EpisodeID string
	AccountID string
	Criterion Criterion
	Changes   []LiteralChange
	Situation Situation
	At        time.Time // when the evidence arrived (the send's replay-clock time)
	// Replay marks a seed that feeds a replay (HAR-144): the knowledge and the candidate evaluator are
	// stamped with At, the decided episode's world time, and a zero At is refused
	// (knowledgestore.ErrCreatedAtRequired) instead of falling back to the database clock. Live
	// (false) keeps wall-clock created_at.
	Replay bool
}

// SeedResult is what a seed wrote: the candidate knowledge id and the candidate evaluator version
// string ('<eval_type>:v<N>').
type SeedResult struct {
	KnowledgeID string
	EvalType    string
	Version     int
}

// EvalVersion is the '<eval_type>:v<N>' tag results of this candidate carry.
func (r SeedResult) EvalVersion() string { return fmt.Sprintf("%s:v%d", r.EvalType, r.Version) }

// nextVersion is the first free version number of an evaluator. Shipped evaluators run v1 without a
// registry row, so a learned candidate is always v2 or later even when the table has no v1 row.
func nextVersion(ctx context.Context, tx *sql.Tx, evaluator string) (int, error) {
	var v int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version), 1) + 1 FROM evaluator_versions
 WHERE evaluator = $1`, evaluator).Scan(&v); err != nil {
		return 0, fmt.Errorf("learning: next version of %s: %w", evaluator, err)
	}
	return v, nil
}

// SeedDeltaCriterion derives the candidate evaluator version and the candidate knowledge of an
// unexplained delta (HAR-119): the criterion's statement becomes the rubric and the guidance, the
// delta's literal changes become the shadow spec, and the episode's situation scopes the knowledge.
// Both rows land in the caller's transaction. An episode the human declined learning from (no_learning) is
// refused with ErrLearningDeclined. rules nil skips the learned-from-episode evidence write;
// with rules, the episode the criterion was learned from is recorded as decision_episode evidence.
func SeedDeltaCriterion(ctx context.Context, tx *sql.Tx, seed DeltaSeed, rules *knowledge.Rules) (SeedResult, error) {
	if err := refuseDeclined(ctx, tx, seed.EpisodeID); err != nil {
		return SeedResult{}, err
	}
	if seed.Criterion.Statement == "" || seed.Criterion.SuggestedEvalType == "" {
		return SeedResult{}, fmt.Errorf("%w on delta %s", ErrNoCriterion, seed.DeltaID)
	}
	if !ValidAxis(seed.Criterion.SuggestedEvalType) {
		return SeedResult{}, gateReason(false, "delta %s proposes %q, not an eval axis", seed.DeltaID,
			seed.Criterion.SuggestedEvalType)
	}
	if err := requireWorldTime(seed.Replay, seed.At, seed.DeltaID); err != nil {
		return SeedResult{}, err
	}
	version, err := nextVersion(ctx, tx, seed.Criterion.SuggestedEvalType)
	if err != nil {
		return SeedResult{}, err
	}
	res := SeedResult{EvalType: seed.Criterion.SuggestedEvalType, Version: version}
	sig, applicability := seed.Situation.Signature()
	note := fmt.Sprintf("seeded by unexplained human delta %s", seed.DeltaID)
	k := knowledge.Knowledge{
		ID:                      seedUUID("knowledge", seed.DeltaID),
		Title:                   clip("Learned correction: "+seed.Criterion.Statement, 300),
		SituationSignature:      sig,
		ApplicabilityConditions: applicability,
		Guidance:                knowledge.Guidance{Summary: clip(seed.Criterion.Statement, 1000), Do: []string{}, Dont: []string{}},
		Status:                  knowledge.StatusCandidate,
		EvidenceClasses:         []string{"deal_data"},
		UsedByEvaluators:        []string{res.EvalVersion()},
		Provenance: knowledge.Provenance{CreatedFrom: "human_delta",
			SourceDecisionEpisodeID: &seed.EpisodeID, Note: &note},
	}
	k, err = insertKnowledge(ctx, tx, k, "candidate seeded by an unexplained human delta (HAR-119)", seed.At, seed.Replay)
	if err != nil {
		return SeedResult{}, fmt.Errorf("learning: seed knowledge of delta %s: %w", seed.DeltaID, err)
	}
	res.KnowledgeID = k.ID
	if rules != nil && !seed.At.IsZero() {
		_, _, err = knowledgestore.RecordEvidence(ctx, tx, k.ID, knowledge.Evidence{
			Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: seed.At}, *rules)
		if err != nil {
			return SeedResult{}, fmt.Errorf("learning: record seed episode evidence: %w", err)
		}
	}
	spec, err := encodeJSON(Spec{AccountID: seed.AccountID, DeltaID: seed.DeltaID, LiteralChanges: seed.Changes})
	if err != nil {
		return SeedResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, labels, examples, created_from, source_human_delta_id,
  shadow_spec, knowledge_id, account_id, created_at)
 VALUES ($1::eval_type, $2, 'candidate', $3, $4, '{}', '[]', 'human_delta', $5::uuid, $6::jsonb, $7::uuid, $8::uuid,
  coalesce($9::timestamptz, now()))`,
		res.EvalType, res.Version, KindOf(res.EvalType), clip(seed.Criterion.Statement, 8000), seed.DeltaID,
		spec, res.KnowledgeID, seed.AccountID, createdAtArg(seed.Replay, seed.At)); err != nil {
		return SeedResult{}, fmt.Errorf("learning: insert candidate evaluator %s: %w", res.EvalVersion(), err)
	}
	return res, nil
}

// SeedVerdictCriterion turns a corrected judgment verdict into a candidate criterion of the human_delta
// evaluator (HAR-119): the corrected statement is the human's own account of what the inference missed.
// It lands as a candidate — a verdict annotates the inference; it never promotes company knowledge
// directly. When the episode already carries a human delta the candidate links it as its source.
// replay stamps the knowledge and the candidate with at, the decided episode's world time, and refuses a
// zero at (HAR-144); live (false) keeps wall-clock created_at.
func SeedVerdictCriterion(ctx context.Context, tx *sql.Tx, episodeID, correctedStatement string, at time.Time, replay bool, rules *knowledge.Rules) (SeedResult, error) {
	const evalType = "human_delta"
	if err := refuseDeclined(ctx, tx, episodeID); err != nil {
		return SeedResult{}, err
	}
	if err := requireWorldTime(replay, at, episodeID); err != nil {
		return SeedResult{}, err
	}
	kid := seedUUID("verdict", episodeID, correctedStatement)
	// A repeated identical correction replays the same seed: its deterministic knowledge id already
	// carries a candidate version, so the write is a no-op returning it.
	var have int
	switch err := tx.QueryRowContext(ctx, `SELECT version FROM evaluator_versions
 WHERE knowledge_id = $1::uuid LIMIT 1`, kid).Scan(&have); {
	case err == nil:
		return SeedResult{KnowledgeID: kid, EvalType: evalType, Version: have}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return SeedResult{}, fmt.Errorf("learning: look up verdict seed %s: %w", kid, err)
	}
	version, err := nextVersion(ctx, tx, evalType)
	if err != nil {
		return SeedResult{}, err
	}
	res := SeedResult{EvalType: evalType, Version: version}
	var deltaID, accountID *string
	if err := tx.QueryRowContext(ctx, `SELECT hd.id::text, de.account_id::text FROM decision_episodes de
 LEFT JOIN human_deltas hd ON hd.decision_episode_id = de.id WHERE de.id = $1::uuid`, episodeID).
		Scan(&deltaID, &accountID); err != nil {
		return SeedResult{}, fmt.Errorf("learning: episode of corrected verdict %s: %w", episodeID, err)
	}
	sit, err := EpisodeSituation(ctx, tx, episodeID)
	if err != nil {
		return SeedResult{}, err
	}
	sig, applicability := sit.Signature()
	note := fmt.Sprintf("seeded by a corrected judgment verdict on episode %s", episodeID)
	scope, err := humanScopeNote(ctx, tx, episodeID)
	if err != nil {
		return SeedResult{}, err
	}
	if scope != "" {
		note += `; the human's note: "` + clip(scope, 500) + `"`
	}
	k := knowledge.Knowledge{
		ID:                      kid,
		Title:                   clip("Corrected inference: "+correctedStatement, 300),
		SituationSignature:      sig,
		ApplicabilityConditions: applicability,
		Guidance:                knowledge.Guidance{Summary: clip(correctedStatement, 1000), Do: []string{}, Dont: []string{}},
		Status:                  knowledge.StatusCandidate,
		EvidenceClasses:         []string{"deal_data"},
		UsedByEvaluators:        []string{res.EvalVersion()},
		Provenance:              knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &episodeID, Note: &note},
	}
	k, err = insertKnowledge(ctx, tx, k, "candidate seeded by a corrected judgment verdict (HAR-119)", at, replay)
	if err != nil {
		return SeedResult{}, fmt.Errorf("learning: seed knowledge of verdict on %s: %w", episodeID, err)
	}
	res.KnowledgeID = k.ID
	if rules != nil && !at.IsZero() {
		_, _, err = knowledgestore.RecordEvidence(ctx, tx, k.ID, knowledge.Evidence{
			Kind: knowledge.EvidenceDecisionEpisode, RefID: episodeID, At: at}, *rules)
		if err != nil {
			return SeedResult{}, fmt.Errorf("learning: record verdict episode evidence: %w", err)
		}
	}
	spec, err := encodeJSON(Spec{AccountID: str(accountID), LiteralChanges: []LiteralChange{}})
	if err != nil {
		return SeedResult{}, err
	}
	var srcDelta, srcAccount any
	if deltaID != nil {
		srcDelta = *deltaID
	}
	if accountID != nil {
		srcAccount = *accountID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, labels, examples, created_from, source_human_delta_id,
  shadow_spec, knowledge_id, account_id, created_at)
 VALUES ($1::eval_type, $2, 'candidate', $3, $4, '{}', '[]', 'manual', $5::uuid, $6::jsonb, $7::uuid, $8::uuid,
  coalesce($9::timestamptz, now()))`,
		res.EvalType, res.Version, KindOf(res.EvalType), clip(correctedStatement, 8000), srcDelta, spec,
		res.KnowledgeID, srcAccount, createdAtArg(replay, at)); err != nil {
		return SeedResult{}, fmt.Errorf("learning: insert verdict candidate %s: %w", res.EvalVersion(), err)
	}
	return res, nil
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// requireWorldTime refuses a replay write that carries no world time: the database clock would put the
// row in the wrong era (HAR-144, ADR-0019).
func requireWorldTime(replay bool, at time.Time, subject string) error {
	if replay && at.IsZero() {
		return fmt.Errorf("%w: %s", knowledgestore.ErrCreatedAtRequired, subject)
	}
	return nil
}

// createdAtArg is the created_at bind of a learning row: the world time in a replay, nil (the database
// clock) when live.
func createdAtArg(replay bool, at time.Time) any {
	if !replay {
		return nil
	}
	return at.UTC()
}

// insertKnowledge stores a candidate: stamped with the world time at in a replay, with the database clock
// when live.
func insertKnowledge(ctx context.Context, tx *sql.Tx, k knowledge.Knowledge, reason string, at time.Time, replay bool) (knowledge.Knowledge, error) {
	if !replay {
		return knowledgestore.Insert(ctx, tx, k, reason)
	}
	k.CreatedAt = at.UTC()
	return knowledgestore.InsertReplay(ctx, tx, k, reason)
}

// humanScopeNote is the newest note the human wrote on the episode's judgment inference ("" when none): the
// scope they gave an edited interpretation. It rides the seeded candidate's provenance note.
func humanScopeNote(ctx context.Context, q rowQuerier, episodeID string) (string, error) {
	var note string
	err := q.QueryRowContext(ctx, `SELECT jn.note FROM judgment_notes jn
 JOIN judgment_inferences ji ON ji.id = jn.judgment_inference_id
 WHERE ji.decision_episode_id = $1::uuid ORDER BY jn.created_at DESC, jn.id DESC LIMIT 1`, episodeID).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("learning: read the note on the inference of episode %s: %w", episodeID, err)
	}
	return strings.TrimSpace(note), nil
}
