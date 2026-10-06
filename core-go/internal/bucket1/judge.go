package bucket1

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Judgment is the recorded output of the ONE model call a gate makes per episode, covering every model
// assertion of that gate. It is replayed from a cassette file; this package never calls a model.
type Judgment struct {
	Gate       string      `json:"gate"`
	Model      string      `json:"model"`
	Assertions []Assertion `json:"assertions"`
}

// LoadJudgments reads a cassette file: {"<episode id>": {"B1": Judgment, ...}}. A missing file is not an
// error (nothing was recorded: every model assertion reads "not measured"); a malformed one is.
func LoadJudgments(path, episodeID string) (map[string]Judgment, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bucket1: read judgments %s: %w", path, err)
	}
	var all map[string]map[string]Judgment
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("bucket1: judgments %s: %w", path, err)
	}
	return all[episodeID], nil
}

// modelAssertion is the recorded judgment of one model assertion, or "not measured". A verdict other than
// unknown that cites no evidence, or evidence that is not in the episode, is unknown (rule R1).
func modelAssertion(ep Episode, js map[string]Judgment, gate, name string) Assertion {
	j, ok := js[gate]
	if !ok {
		return notMeasured(name, gate)
	}
	for _, a := range j.Assertions {
		if a.Name != name {
			continue
		}
		a.Grader = GraderModel
		a.Verdict = NormalizeVerdict(a.Verdict)
		if a.Verdict != Unknown {
			if bad := ep.unknownRefs(a.Refs); len(a.Refs) == 0 || len(bad) > 0 {
				a.Why = fmt.Sprintf("model verdict %s dropped to unknown: %s", a.Verdict, refProblem(a.Refs, bad))
				a.Verdict, a.Observed = Unknown, "not measured"
			}
		}
		if a.Observed == "" {
			a.Observed = j.Model + " judged " + name
		}
		return a
	}
	return notMeasured(name, gate)
}

func refProblem(all, bad []Ref) string {
	if len(all) == 0 {
		return "it cites no evidence"
	}
	ids := make([]string, 0, len(bad))
	for _, r := range bad {
		ids = append(ids, r.ActivityID+r.ClaimID+r.StepID)
	}
	return "it cites evidence that is not in the episode (" + strings.Join(ids, ", ") + ")"
}

// unknownRefs returns the refs that point at nothing in the episode.
func (ep Episode) unknownRefs(refs []Ref) []Ref {
	known := map[string]bool{}
	for _, a := range ep.Activities {
		known[a.ID] = true
	}
	for _, c := range append(append([]Claim(nil), ep.Claims...), ep.PriorClaims...) {
		known[c.ID] = true
	}
	for _, p := range ep.Precedents {
		known[p.ID] = true
	}
	if ep.Attribution != nil {
		known[ep.Attribution.StepID] = true
	}
	var bad []Ref
	for _, r := range refs {
		for _, id := range []string{r.ActivityID, r.ClaimID, r.StepID} {
			if id != "" && !known[id] {
				bad = append(bad, r)
				break
			}
		}
	}
	return bad
}

// JudgePrompt is the single request a gate sends to the model (recorded to a cassette, then replayed): every
// model assertion of the gate and the episode material it reads, never one call per assertion.
func JudgePrompt(ep Episode, gate string, assertions []string) string {
	var b strings.Builder
	info := Gates[gate]
	fmt.Fprintf(&b, "gtm_ai Bucket 1 gate %s: %s\nQuestion: %s\nEpisode %s at %s.\n", gate, info.Name, info.Question, ep.ID, ep.At.Format("2006-01-02T15:04:05Z"))
	b.WriteString("Judge ONLY these assertions, each pass/warn/fail/unknown, citing the activity or claim ids that settle it; ")
	b.WriteString("a verdict that cites nothing is unknown:\n")
	for _, a := range assertions {
		b.WriteString("- " + a + "\n")
	}
	for _, a := range ep.Activities {
		fmt.Fprintf(&b, "activity %s (%s, speaker %s): %s\n", a.ID, a.OccurredAt.Format("2006-01-02"), a.SpeakerID, a.Text)
	}
	for _, c := range ep.Claims {
		fmt.Fprintf(&b, "claim %s [%s] %s = %s (quote %q)\n", c.ID, c.Kind, c.Field, c.Value, c.Quote)
	}
	return b.String()
}
