package strategystore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// FinalArtifactJudge is the model half of D8 (HAR-97 D8, live and required "immediately before Send"). The
// worker's decision-judge endpoint answers it through bucket2run.Runner. It judges the exact final artifact.
type FinalArtifactJudge interface {
	JudgeFinalArtifact(ctx context.Context, in FinalArtifactInput) (bucket2.Result, error)
}

// FinalArtifactInput is the exact final artifact about to be delivered: its recipients, subject, body (the CTA
// is part of the body) and channel, plus the intent and evidence of the candidate it came from.
type FinalArtifactInput struct {
	EpisodeID, RunID, CandidateID string
	ActionType, Intent, Rationale string
	To, CC                        []string // person ids
	Artifact                      Artifact
	KnowledgeRefs, EvidenceIDs    []string
}

// SetFinalJudge sets the D8 model judge after construction (the runner reads this service, so it cannot exist first).
// Without one, only the deterministic D8 checks run before a send; the model part is then measured after it.
func (s *Service) SetFinalJudge(j FinalArtifactJudge) { s.finalJudge = j }

// preSendD8 is gate D8 on the exact final artifact, run inside the send-time evaluation before anything is
// delivered. It returns the deterministic results (recipients, stale content, policy gates) and the model
// judgment. A model judge that fails leaves D8 UNKNOWN with the reason; it never blocks and never reads as a pass.
func (s *Service) preSendD8(ctx context.Context, tx *sql.Tx, ep episode, runID string, cands map[string]candidate, chosen candidate, final Draft, sev sendEval) ([]bucket2.Result, error) {
	people, internal, err := accountPeople(ctx, tx, ep.AccountID)
	if err != nil {
		return nil, err
	}
	var others []string
	for id, c := range cands {
		if id != chosen.ID {
			others = append(others, c.Draft.Artifact.Body)
		}
	}
	to, cc := personIDs(final.To), personIDs(final.CC)
	evals := make([]bucket2.SendEval, 0, len(sev.Results))
	for _, r := range sev.Results {
		evals = append(evals, bucket2.SendEval{ID: r.ID, EvalType: string(r.EvalType), Verdict: r.Verdict, Blocking: r.Blocking})
	}
	out := bucket2.FinalArtifactResults(bucket2.FinalArtifact{EpisodeID: ep.ID, CandidateID: chosen.ID, To: to, CC: cc,
		AccountPeople: people, InternalOnly: internal, Body: final.Artifact.Body, SelectedBody: chosen.Draft.Artifact.Body,
		OtherBodies: others, SendEvals: evals})
	if s.finalJudge == nil {
		return out, nil
	}
	in := FinalArtifactInput{EpisodeID: ep.ID, RunID: runID, CandidateID: chosen.ID, ActionType: chosen.ActionType,
		Intent: chosen.Description, Rationale: chosen.Rationale, To: to, CC: cc, Artifact: final.Artifact,
		KnowledgeRefs: chosen.KnowledgeRefs, EvidenceIDs: candidateEvidence(chosen)}
	res, err := s.finalJudge.JudgeFinalArtifact(ctx, in)
	if err != nil {
		s.log.WarnContext(ctx, "the pre-send D8 model judgment failed; D8 is unknown and the send is not blocked", "run_id", runID, "error", err)
		res = bucket2.Result{Gate: "D8", SubGate: "model", JudgedType: "FinalArtifact", JudgedID: chosen.ID,
			SpanID: "recomputed_action:" + ep.ID, Grader: bucket2.Deterministic, Verdict: bucket2.Unknown,
			EvidenceRefs: []string{"candidate:" + chosen.ID}, Observed: "the model judgment of the final artifact did not complete",
			Why: "the model judge could not be reached before the send: " + err.Error()}.Finalize()
	}
	return append(out, res), nil
}

// candidateEvidence are the ids a D8 judgment may cite: the candidate itself, its activities and its knowledge.
func candidateEvidence(c candidate) []string {
	out := []string{"candidate:" + c.ID}
	for _, r := range c.EvidenceRefs {
		out = append(out, "activity:"+r.ActivityID)
	}
	for _, k := range c.KnowledgeRefs {
		out = append(out, "knowledge:"+k)
	}
	return out
}

func personIDs(rs []Recipient) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.PersonID)
	}
	return out
}

// accountPeople are the people of the account (or our org) and which of them are internal-only.
func accountPeople(ctx context.Context, tx *sql.Tx, accountID string) (all, internal map[string]bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text, internal_only FROM people WHERE account_id = $1::uuid OR kind = 'employee'`, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("strategystore: people of account %s: %w", accountID, err)
	}
	defer rows.Close()
	all, internal = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id string
		var only bool
		if err := rows.Scan(&id, &only); err != nil {
			return nil, nil, err
		}
		all[id], internal[id] = true, only
	}
	return all, internal, rows.Err()
}

// d8AsBlockingEvals appends to blocked the D8 results the catalog lets block, as blocking eval results, so a D8
// refusal takes the same refusal path and message as a blocking eval. A type already in blocked is not repeated.
func d8AsBlockingEvals(blocked []deterministic.EvalResult, d8 []bucket2.Result) []deterministic.EvalResult {
	names := bucket2.D8BlockingEvals()
	have := map[deterministic.EvalType]bool{}
	for _, b := range blocked {
		have[b.EvalType] = true
	}
	out := append([]deterministic.EvalResult{}, blocked...)
	for _, b := range bucket2.D8Blockers(d8) {
		t := deterministic.EvalType(names[b.SubGate])
		if !have[t] {
			have[t] = true
			out = append(out, deterministic.EvalResult{EvalType: t, Verdict: "fail", Blocking: true, Reason: b.Why})
		}
	}
	return out
}
