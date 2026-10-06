package evalreport

// inference.go — the human-vs-inference agreement metric: Ghost infers WHY the human chose what they
// chose (judgment_inferences) and the human answers confirmed or corrected; every accepted answer is
// appended to judgment_verdicts (migration 0027). That answer is the most direct grader-vs-human signal
// in the schema — the human grading Ghost's own inference — so the report reads it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// inferenceRow is one judgment_inferences row with its verdict history.
type inferenceRow struct {
	ID, EpisodeID string
	Agreement     string   // "agreed" | "overrode": did the human pick Ghost's preferred candidate
	Verdict       string   // the human's current answer: pending | confirmed | corrected | no_learning
	History       []string // judgment_verdicts.verdict in insert order
}

// latest is the human's current answer: the newest history row, else the inference column (rows
// answered before migration 0027 have no history).
func (r *inferenceRow) latest() string {
	if n := len(r.History); n > 0 {
		return r.History[n-1]
	}
	return r.Verdict
}

// revised reports whether the human changed their answer (the history holds more than one verdict).
func (r *inferenceRow) revised() bool {
	for _, v := range r.History {
		if v != r.History[0] {
			return true
		}
	}
	return false
}

// InferenceAgreement is the share of Ghost's inferences the human confirmed.
type InferenceAgreement struct {
	Inferences       int                       `json:"n_inferences"`
	Answered         int                       `json:"n_answered"`
	Confirmed        int                       `json:"n_confirmed"`
	Corrected        int                       `json:"n_corrected"`
	Pending          int                       `json:"n_pending"`
	NoLearning       int                       `json:"n_no_learning"`          // the human opted out of learning from the episode
	ConfirmRate      *float64                  `json:"confirm_rate,omitempty"` // confirmed / answered
	VerdictRows      int                       `json:"n_verdict_rows"`         // judgment_verdicts history rows
	Revised          int                       `json:"n_revised"`              // inferences whose answer changed
	ByAgentAgreement map[string]InferenceSlice `json:"by_agent_agreement"`     // agreed / overrode
	ThisVersion      InferenceSlice            `json:"this_version_episodes"`  // inferences on episodes the version judged
	Basis            string                    `json:"basis"`
}

// InferenceSlice is a confirm-rate slice of the inferences.
type InferenceSlice struct {
	N           int      `json:"n"`
	Answered    int      `json:"n_answered"`
	Confirmed   int      `json:"n_confirmed"`
	ConfirmRate *float64 `json:"confirm_rate,omitempty"`
}

func (s *InferenceSlice) add(r *inferenceRow) {
	s.N++
	switch r.latest() {
	case "confirmed":
		s.Answered++
		s.Confirmed++
	case "corrected":
		s.Answered++
	}
}

func (s *InferenceSlice) rate() {
	if s.Answered > 0 {
		r := float64(s.Confirmed) / float64(s.Answered)
		s.ConfirmRate = &r
	}
}

const inferenceBasis = "judgment_inferences are Ghost's inference of why the human chose what they chose; " +
	"the human's confirmed/corrected answer (latest row of the append-only judgment_verdicts history, else " +
	"the inference column) is the direct human grade of that inference. this_version_episodes slices the " +
	"inferences on episodes the reported version judged. no_learning is the human opting out of learning, not a " +
	"grade of the inference: it is counted in n_no_learning and leaves the confirm_rate denominator, so an " +
	"inference first confirmed and later opted out stops counting as a confirmation (its history rows stay in " +
	"n_verdict_rows)"

// inferenceAgreement computes the metric; byEpisode (the version's judged episodes) fills the slice.
func (w *world) inferenceAgreement(byEpisode map[string][]*evalRow) Metric[InferenceAgreement] {
	if len(w.inferences) == 0 {
		return na[InferenceAgreement]("no judgment_inferences recorded — the human has not been asked to grade any inference")
	}
	a := InferenceAgreement{ByAgentAgreement: map[string]InferenceSlice{}, Basis: inferenceBasis}
	byAgree := map[string]*InferenceSlice{}
	for _, r := range w.inferences {
		a.Inferences++
		a.VerdictRows += len(r.History)
		if r.revised() {
			a.Revised++
		}
		switch r.latest() {
		case "confirmed":
			a.Confirmed++
		case "corrected":
			a.Corrected++
		case "no_learning":
			a.NoLearning++
		default:
			a.Pending++
		}
		s := byAgree[r.Agreement]
		if s == nil {
			s = &InferenceSlice{}
			byAgree[r.Agreement] = s
		}
		s.add(r)
		if _, judged := byEpisode[r.EpisodeID]; judged {
			a.ThisVersion.add(r)
		}
	}
	a.Answered = a.Confirmed + a.Corrected
	if a.Answered == 0 {
		return na[InferenceAgreement](fmt.Sprintf("none of the %d judgment_inferences has a human verdict yet (all pending)", a.Inferences))
	}
	r := float64(a.Confirmed) / float64(a.Answered)
	a.ConfirmRate = &r
	for k, s := range byAgree {
		s.rate()
		a.ByAgentAgreement[k] = *s
	}
	a.ThisVersion.rate()
	return avail(a)
}

func (w *world) loadInferences(ctx context.Context, db claimstore.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT ji.id::text, ji.decision_episode_id::text, ji.agreement, ji.human_verdict,
 COALESCE((SELECT string_agg(jv.verdict, ',' ORDER BY jv.created_at, jv.id) FROM judgment_verdicts jv
   WHERE jv.judgment_inference_id = ji.id), '')
FROM judgment_inferences ji ORDER BY ji.created_at, ji.id`)
	if err != nil {
		return fmt.Errorf("evalreport: load inferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r inferenceRow
		var history string
		if err := rows.Scan(&r.ID, &r.EpisodeID, &r.Agreement, &r.Verdict, &history); err != nil {
			return fmt.Errorf("evalreport: scan inference: %w", err)
		}
		if history != "" {
			r.History = strings.Split(history, ",")
		}
		w.inferences[r.EpisodeID] = &r
	}
	return rows.Err()
}
