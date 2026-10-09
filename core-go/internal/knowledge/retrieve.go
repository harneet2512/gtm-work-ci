package knowledge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// EntrySimilarity is decision_guidance.supporting_knowledge[].similarity: set only on learned knowledge offered
// by closest match, so the agent is told the lesson's status and how similar it is.
type EntrySimilarity struct {
	Status  string   `json:"status"`
	Score   float64  `json:"score"`
	Matched []string `json:"matched"`
	Differs []string `json:"differs"`
}

// Retrieval is what one run persists (detail.knowledge_retrieval, knowledge_similarity.v1.json#/$defs/retrieval):
// the lessons ranked by closeness to the case, each with its decision, and the plain statement the product shows.
type Retrieval struct {
	RulesVersion  string      `json:"rules_version"`
	Threshold     float64     `json:"threshold"`
	MinComparable int         `json:"min_comparable_features"`
	MinWeight     float64     `json:"min_comparable_weight"`
	Candidates    []Candidate `json:"candidates"`
	Message       string      `json:"message"`
}

// RetrieveAll matches every knowledge object against the situation. Knowledge matched by similarity (sim.Matches)
// is scored against the case instead of gated on its scope conditions: at or above the threshold, with enough
// comparable features and no exception firing, it applies. Every other knowledge object keeps the exact matcher.
// sim nil is MatchAll. With similarity rules the Retrieval is always returned (empty candidates say that no lesson exists yet).
func RetrieveAll(ks []Knowledge, s Situation, sim *Similarity) ([]Result, *Retrieval, error) {
	if sim == nil {
		out, err := MatchAll(ks, s)
		return out, nil, err
	}
	e := newEnv(s)
	current := CaseFeatures(s)
	out := make([]Result, 0, len(ks))
	var cands []Candidate
	for _, k := range ks {
		if !sim.Matches(k) {
			r, err := e.match(k)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, r)
			continue
		}
		r, c, err := e.matchBySimilarity(k, *sim, current)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
		cands = append(cands, c)
	}
	if cands == nil {
		cands = []Candidate{}
	}
	rankCandidates(cands)
	return out, &Retrieval{RulesVersion: sim.Version, Threshold: sim.Threshold, MinComparable: sim.MinComparable,
		MinWeight: sim.MinComparableWeight, Candidates: cands, Message: Message(cands, *sim)}, nil
}

func (e env) matchBySimilarity(k Knowledge, sim Similarity, current Features) (Result, Candidate, error) {
	if err := Validate(k); err != nil {
		return Result{}, Candidate{}, err
	}
	sc := sim.ScoreFeatures(LessonFeatures(k), current)
	c := Candidate{KnowledgeID: k.ID, Title: k.Title, Status: k.Status, Score: sc.Value, Comparable: sc.Comparable,
		ComparableWeight: sc.Weight, Matched: nonNil(sc.Matched), Differs: nonNil(sc.Differs), Ignored: IgnoredConditions(k)}
	// A lesson that is not offered keeps the entry text it had before topic existed (ADR-0013 amendment 4), so the guidance
	// of a case whose lesson is not offered does not change; the full record, topic included, is in the Candidate.
	entry := Entry{KnowledgeID: k.ID, MatchedConditions: c.withoutTopic().MatchedText(), UnmatchedConditions: c.withoutTopic().DifferText(),
		ExceptionsChecked: []ExceptionCheck{}}
	switch {
	case sc.Comparable < sim.MinComparable || sc.Weight < sim.MinComparableWeight:
		c.Decision = DecisionInsufficientFeatures
		return Result{Label: LabelDoesNotApply, Entry: entry, Similarity: &c}, c, nil
	case sc.Value < sim.Threshold:
		c.Decision = DecisionBelowThreshold
		return Result{Label: LabelDoesNotApply, Entry: entry, Similarity: &c}, c, nil
	}
	triggered := false
	for _, x := range k.Exceptions {
		check, err := e.checkException(x)
		if err != nil {
			return Result{}, Candidate{}, fmt.Errorf("knowledge %s exception %q: %w", k.ID, x.Description, err)
		}
		triggered = triggered || check.Triggered
		entry.ExceptionsChecked = append(entry.ExceptionsChecked, check)
	}
	if triggered {
		c.Decision = DecisionExceptionBlocked
		return Result{Label: LabelExceptionTriggered, Entry: entry, Similarity: &c}, c, nil
	}
	c.Decision = DecisionApplicable
	entry.Applies = true
	entry.MatchedConditions, entry.UnmatchedConditions = c.MatchedText(), c.DifferText()
	refs, err := e.matchedEvidence(k)
	if err != nil {
		return Result{}, Candidate{}, fmt.Errorf("knowledge %s evidence: %w", k.ID, err)
	}
	entry.CurrentEvidenceRefs = refs
	entry.Similarity = &EntrySimilarity{Status: k.Status, Score: sc.Value, Matched: c.MatchedText(), Differs: c.DifferText()}
	return Result{Label: LabelApplies, Entry: entry, Similarity: &c}, c, nil
}

// matchedEvidence is the current evidence behind the lesson's scope conditions that hold in this case (the same refs
// the exact matcher attaches), so "why does this lesson apply now" can be answered for an offered lesson.
// A condition the matcher cannot evaluate is an error, never a silently absent ref.
func (e env) matchedEvidence(k Knowledge) ([]EvidenceRef, error) {
	var refs []EvidenceRef
	for _, c := range append(append([]Condition(nil), k.SituationSignature...), k.ApplicabilityConditions...) {
		ok, r, err := e.holds(c)
		if err != nil {
			return nil, err
		}
		if ok {
			refs = append(refs, r...)
		}
	}
	return dedupeRefs(refs), nil
}

func nonNil(in []FeatureResult) []FeatureResult {
	if in == nil {
		return []FeatureResult{}
	}
	return in
}

// rankCandidates orders the candidates closest first: lessons with enough comparable features before those without, then
// by score, comparable features, title and id, so the order is total and reproducible.
func rankCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if ai, bi := a.Decision == DecisionInsufficientFeatures, b.Decision == DecisionInsufficientFeatures; ai != bi {
			return bi
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Comparable != b.Comparable {
			return a.Comparable > b.Comparable
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.KnowledgeID < b.KnowledgeID
	})
}

func names(fs []FeatureResult) string {
	ids := make([]string, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	if len(ids) == 0 {
		return "nothing"
	}
	return strings.Join(ids, ", ")
}

// Message is the plain statement of the closest lesson (candidates ranked): what is applicable and why, or that
// the knowledge base holds nothing similar enough. It never says a lesson influenced anything.
func Message(cs []Candidate, sim Similarity) string {
	if len(cs) == 0 {
		return "No similar knowledge in the knowledge base: no lesson has been learned yet."
	}
	c := cs[0]
	switch c.Decision {
	case DecisionApplicable, DecisionExceptionBlocked:
		blocked := ""
		if c.Decision == DecisionExceptionBlocked {
			blocked = ", but an exception fires so it is not offered"
		}
		return fmt.Sprintf("Closest past lesson: %s (%s), similarity %.2f, matched on %s; differs on %s%s.",
			c.Title, c.Status, c.Score, names(c.Matched), names(c.Differs), blocked)
	case DecisionInsufficientFeatures:
		if c.Comparable >= sim.MinComparable {
			return fmt.Sprintf("No similar knowledge in the knowledge base (closest: %s, %d comparable features but only weight %s, %s needed).",
				c.Title, c.Comparable, num(c.ComparableWeight), num(sim.MinComparableWeight))
		}
		unit := "features"
		if c.Comparable == 1 {
			unit = "feature"
		}
		return fmt.Sprintf("No similar knowledge in the knowledge base (closest: %s, only %d comparable %s, %d needed).",
			c.Title, c.Comparable, unit, sim.MinComparable)
	}
	return fmt.Sprintf("No similar knowledge in the knowledge base (closest: %s, similarity %.2f — below %.2f).",
		c.Title, c.Score, sim.Threshold)
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
