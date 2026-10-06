package bucket2run

import (
	"encoding/json"
	"strings"
)

// The minimal shapes of the stored documents the runner reads (strategy_set, eval_bundle,
// human_strategy_decision, judgment_inference). Unknown fields are ignored.

type recipient struct {
	PersonID string `json:"person_id"`
	Role     string `json:"role"`
	Why      string `json:"why"`
}

type candidateDoc struct {
	ID            string          `json:"candidate_id"`
	StrategyType  string          `json:"strategy_type"`
	Title         string          `json:"title"`
	ActionClass   string          `json:"action_class"`
	ActionType    string          `json:"action_type"`
	Rationale     string          `json:"rationale"`
	Ranking       int             `json:"ranking"`
	Preferred     bool            `json:"preferred_by_agent"`
	StateRefs     []string        `json:"state_refs"`
	KnowledgeRefs []string        `json:"knowledge_refs"`
	EvidenceRefs  []evidenceRef   `json:"evidence_refs"`
	To            []recipient     `json:"to"`
	CC            []recipient     `json:"cc"`
	DraftIndex    int             `json:"draft_index"`
	FiveQuestions json.RawMessage `json:"five_questions"`
	Artifact      artifactDoc     `json:"full_action_artifact"`
}

type evidenceRef struct {
	ActivityID string `json:"activity_id"`
}

type artifactDoc struct {
	Channel string  `json:"channel"`
	Subject *string `json:"subject"`
	Body    string  `json:"body"`
}

type setDoc struct {
	ID           string         `json:"id"`
	EpisodeID    string         `json:"decision_episode_id"`
	RunID        string         `json:"agent_run_id"`
	AccountID    string         `json:"account_id"`
	NoAcceptable bool           `json:"no_acceptable_candidate"`
	Candidates   []candidateDoc `json:"candidates"`
}

type bundleDoc struct {
	ID              string `json:"id"`
	CandidateID     string `json:"strategy_candidate_id"`
	CandidatePolicy *struct {
		TransitionStatus string `json:"transition_status"`
		Status           string `json:"status"`
	} `json:"candidate_policy"`
	Items []struct {
		EvalType string `json:"eval_type"`
		Verdict  string `json:"verdict"`
		Result   *struct {
			ID       string `json:"id"`
			Blocking bool   `json:"blocking"`
		} `json:"result"`
	} `json:"items"`
}

type decisionDoc struct {
	ID         string          `json:"id"`
	EpisodeID  string          `json:"decision_episode_id"`
	RunID      string          `json:"agent_run_id"`
	Selected   string          `json:"selected_candidate_id"`
	Preferred  string          `json:"original_agent_preference"`
	Edits      json.RawMessage `json:"edits"`
	Send       string          `json:"send_decision"`
	FinalTo    []recipient     `json:"final_to"`
	FinalCC    []recipient     `json:"final_cc"`
	FinalArt   *artifactDoc    `json:"final_artifact"`
	EditsCount int             `json:"-"`
}

type inferenceDoc struct {
	ID    string `json:"id"`
	Delta struct {
		Statement string   `json:"statement"`
		Labels    []string `json:"semantic_labels"`
		Classes   []string `json:"edit_class"`
		Signal    string   `json:"signal_strength"`
		Unknown   bool     `json:"unknown"`
	} `json:"inferred_semantic_delta"`
	Evidence struct {
		Refs []evidenceRef `json:"evidence_refs"`
	} `json:"evidence"`
	Model string `json:"model"`
}

func (c candidateDoc) evidenceIDs() []string {
	var out []string
	for _, r := range c.EvidenceRefs {
		out = append(out, "activity:"+r.ActivityID)
	}
	for _, k := range c.KnowledgeRefs {
		out = append(out, "knowledge:"+k)
	}
	for _, s := range c.StateRefs {
		out = append(out, "state:"+s)
	}
	return out
}

// strip turns an artifact into text a judge can read; an empty artifact is reported as such.
func (a artifactDoc) text() string {
	subject := ""
	if a.Subject != nil {
		subject = *a.Subject
	}
	return strings.TrimSpace(subject + "\n" + a.Body)
}
