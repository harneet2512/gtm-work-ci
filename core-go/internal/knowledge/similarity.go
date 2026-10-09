package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// ErrInvalidSimilarity is wrapped by every similarity-rules error.
var ErrInvalidSimilarity = errors.New("invalid knowledge similarity rules")

// Similarity is contracts/knowledge/similarity.v1.json (knowledge_similarity.v1.json): the closest-match
// retrieval of learned knowledge (ADR-0013 amendment 2). Deterministic, no model.
type Similarity struct {
	Version              string          `json:"version"`
	Description          string          `json:"description,omitempty"`
	AppliesToCreatedFrom []string        `json:"applies_to_created_from"`
	Threshold            float64         `json:"threshold"`
	MinComparable        int             `json:"min_comparable_features"`
	MinComparableWeight  float64         `json:"min_comparable_weight"`
	Features             []SimFeatureDef `json:"features"`
}

// SimFeatureDef is one comparable feature and its weight.
type SimFeatureDef struct {
	ID        string  `json:"id"`
	Weight    float64 `json:"weight"`
	Rationale string  `json:"rationale"`
}

// Similarity feature ids (knowledge_similarity.v1.json).
const (
	FeatTransition       = "transition"
	FeatSignals          = "signals"
	FeatTopic            = "topic"
	FeatStage            = "stage"
	FeatMotion           = "motion"
	FeatRelationship     = "relationship_state"
	FeatTransitionStatus = "transition_status"
	FeatChampionStatus   = "champion_status"
	FeatHealth           = "health"
)

// scalarStateFeatures are the features read from one scalar AccountState field of the same name.
var scalarStateFeatures = []string{FeatStage, FeatMotion, FeatChampionStatus, FeatHealth}

var knownFeatures = map[string]bool{FeatTransition: true, FeatSignals: true, FeatTopic: true, FeatStage: true, FeatMotion: true,
	FeatRelationship: true, FeatTransitionStatus: true, FeatChampionStatus: true, FeatHealth: true}

// Decisions of one lesson against one case.
const (
	DecisionApplicable           = "applicable"
	DecisionBelowThreshold       = "below_threshold"
	DecisionInsufficientFeatures = "insufficient_features"
	DecisionExceptionBlocked     = "exception_blocked"
)

// LoadSimilarity reads and validates a similarity rules file.
func LoadSimilarity(path string) (Similarity, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Similarity{}, fmt.Errorf("%w: %v", ErrInvalidSimilarity, err)
	}
	return ParseSimilarity(raw)
}

// ParseSimilarity decodes strictly (unknown keys are errors) and checks the weights.
func ParseSimilarity(raw []byte) (Similarity, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Similarity
	if err := dec.Decode(&s); err != nil {
		return Similarity{}, fmt.Errorf("%w: %v", ErrInvalidSimilarity, err)
	}
	if dec.More() {
		return Similarity{}, fmt.Errorf("%w: trailing data after the rules object", ErrInvalidSimilarity)
	}
	return s, s.check()
}

func (s Similarity) check() error {
	if s.Version == "" || len(s.AppliesToCreatedFrom) == 0 || len(s.Features) == 0 {
		return fmt.Errorf("%w: version, applies_to_created_from and features are required", ErrInvalidSimilarity)
	}
	if s.Threshold <= 0 || s.Threshold > 1 || s.MinComparable < 1 {
		return fmt.Errorf("%w: threshold must be in (0,1] and min_comparable_features >= 1", ErrInvalidSimilarity)
	}
	seen := map[string]bool{}
	for _, f := range s.Features {
		if !knownFeatures[f.ID] || seen[f.ID] || f.Weight <= 0 || f.Rationale == "" {
			return fmt.Errorf("%w: feature %q must be a known, unique id with a positive weight and a rationale", ErrInvalidSimilarity, f.ID)
		}
		seen[f.ID] = true
	}
	if s.MinComparable > len(s.Features) {
		return fmt.Errorf("%w: min_comparable_features exceeds the number of features", ErrInvalidSimilarity)
	}
	total := 0.0
	for _, f := range s.Features {
		total += f.Weight
	}
	if s.MinComparableWeight <= 0 || s.MinComparableWeight > total {
		return fmt.Errorf("%w: min_comparable_weight must be in (0, total weight %v]", ErrInvalidSimilarity, total)
	}
	return nil
}

// Matches reports whether k is matched by similarity (its provenance is one of applies_to_created_from).
func (s *Similarity) Matches(k Knowledge) bool {
	return s != nil && hasString(s.AppliesToCreatedFrom, k.Provenance.CreatedFrom)
}

// featureValue is one feature of a situation: a scalar or a set (signal types). Absent = unknown.
type featureValue struct {
	scalar string
	set    []string
}

func (v featureValue) text() string {
	if v.set != nil {
		return strings.Join(v.set, ", ")
	}
	return v.scalar
}

// Features is the known features of one situation, by feature id. An unknown feature has no entry.
type Features map[string]featureValue

func knownScalar(s string) (string, bool) {
	k := claims.ItemKey(s)
	return k, k != "" && k != claims.Unknown
}

// CaseFeatures reads the similarity features the current case knows (unknown ones are left out).
func CaseFeatures(s Situation) Features {
	out := Features{}
	for _, name := range scalarStateFeatures {
		v, ok := s.Fields[name]
		if !ok || !v.Known || v.List {
			continue
		}
		if str, isStr := v.Scalar.(string); isStr {
			if k, ok := knownScalar(str); ok {
				out[name] = featureValue{scalar: k}
			}
		}
	}
	if k, ok := knownScalar(s.RelationshipState); ok {
		out[FeatRelationship] = featureValue{scalar: k}
	}
	if t := s.Transition; t != nil {
		if k, ok := knownScalar(t.Status); ok {
			out[FeatTransitionStatus] = featureValue{scalar: k}
		}
		from, fok := knownScalar(t.FromState)
		to, tok := knownScalar(t.ToState)
		if fok && tok {
			out[FeatTransition] = featureValue{scalar: from + " > " + to}
		}
	}
	// Signals are always known: the situation carries the account's signals, so "none open" is a fact (the lesson's
	// trigger does not hold, agreement 0), never an unknown.
	types := []string{}
	for typ := range openSignals(s) {
		types = append(types, typ)
	}
	sort.Strings(types)
	out[FeatSignals] = featureValue{set: types}
	if len(s.Topics) > 0 {
		out[FeatTopic] = featureValue{set: append([]string{}, s.Topics...)}
	}
	return out
}

// LessonFeatures reads the similarity features a lesson's scope states: its eq conditions on the feature fields
// and its exists conditions on signals. A scope never states an unknown value, and one found in older
// knowledge is ignored.
func LessonFeatures(k Knowledge) Features {
	out := Features{}
	var from, to string
	var types, topics []string
	for _, group := range [][]Condition{k.SituationSignature, k.ApplicabilityConditions} {
		for _, c := range group {
			if strings.HasPrefix(c.Field, "diff.") && c.Op == OpExists {
				types = append(types, strings.TrimPrefix(c.Field, "diff."))
				continue
			}
			if strings.HasPrefix(c.Field, "topic.") && c.Op == OpExists {
				topics = append(topics, strings.TrimPrefix(c.Field, "topic."))
				continue
			}
			str, isStr := c.Value.(string)
			if c.Op != OpEq || !isStr {
				continue
			}
			v, ok := knownScalar(str)
			if !ok {
				continue
			}
			switch c.Field {
			case "relationship_state":
				out[FeatRelationship] = featureValue{scalar: v}
			case "transition.status":
				out[FeatTransitionStatus] = featureValue{scalar: v}
			case "transition.from_state":
				from = v
			case "transition.to_state":
				to = v
			default:
				if hasString(scalarStateFeatures, c.Field) {
					out[c.Field] = featureValue{scalar: v}
				}
			}
		}
	}
	if from != "" && to != "" {
		out[FeatTransition] = featureValue{scalar: from + " > " + to}
	}
	if len(types) > 0 {
		sort.Strings(types)
		out[FeatSignals] = featureValue{set: types}
	}
	if len(topics) > 0 {
		sort.Strings(topics)
		out[FeatTopic] = featureValue{set: topics}
	}
	return out
}

// FeatureResult is one compared feature.
type FeatureResult struct {
	ID        string  `json:"id"`
	Weight    float64 `json:"weight"`
	Agreement float64 `json:"agreement"`
	Lesson    string  `json:"lesson"`
	Case      string  `json:"case"`
}

// Render is the plain-words form of a compared feature ("stage: negotiation").
func (f FeatureResult) Render() string {
	if f.Lesson == f.Case {
		return f.ID + ": " + f.Lesson
	}
	return fmt.Sprintf("%s: lesson %s, case %s", f.ID, f.Lesson, f.Case)
}

// Score is the similarity of one lesson to one case.
type Score struct {
	Value      float64
	Comparable int
	// Weight is the sum of the weights of the comparable features.
	Weight  float64
	Matched []FeatureResult
	Differs []FeatureResult
}

// agreement of one comparable feature: scalars are equal or not; signals are the share of the lesson's signals
// that are also open in the case (does the lesson's trigger hold now).
func agreement(l, c featureValue) float64 {
	if l.set == nil {
		if l.scalar == c.scalar {
			return 1
		}
		return 0
	}
	in := 0
	for _, t := range l.set {
		if hasString(c.set, t) {
			in++
		}
	}
	return float64(in) / float64(len(l.set))
}

// matchedAgreement is the agreement at or above which a compared feature is listed as matched rather than as differing. It is a
// display rule only: it does not enter the score or the offering decision, and no ADR states it (ADR-0013 amendment 2 fixes
// the weights and the 0.60 threshold, not this split).
const matchedAgreement = 0.5

// ScoreFeatures compares a lesson with a case over the features known on both sides. A feature unknown on
// either side contributes nothing. The value is rounded to four places so it is stable across platforms.
func (s Similarity) ScoreFeatures(lesson, current Features) Score {
	var sc Score
	var num, den float64
	for _, def := range s.Features {
		l, lok := lesson[def.ID]
		c, cok := current[def.ID]
		if !lok || !cok {
			continue
		}
		a := agreement(l, c)
		r := FeatureResult{ID: def.ID, Weight: def.Weight, Agreement: a, Lesson: l.text(), Case: c.text()}
		if a >= matchedAgreement {
			sc.Matched = append(sc.Matched, r)
		} else {
			sc.Differs = append(sc.Differs, r)
		}
		num += def.Weight * a
		den += def.Weight
		sc.Comparable++
		sc.Weight += def.Weight
	}
	if den > 0 {
		sc.Value = math.Round(num/den*10000) / 10000
	}
	return sc
}

// Candidate is the similarity record of one lesson in one case.
type Candidate struct {
	KnowledgeID string  `json:"knowledge_id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	Score       float64 `json:"score"`
	Comparable  int     `json:"comparable"`
	// ComparableWeight is the sum of the weights of the comparable features.
	ComparableWeight float64 `json:"comparable_weight"`
	// Ignored lists the lesson's scope conditions outside the eight features: not scored, reported.
	Ignored  []string        `json:"ignored_conditions,omitempty"`
	Matched  []FeatureResult `json:"matched"`
	Differs  []FeatureResult `json:"differs"`
	Decision string          `json:"decision"`
}

func renderAll(fs []FeatureResult) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Render())
	}
	return out
}

// MatchedText is the matched features in plain words, as the guidance entry and the product show them.
func (c Candidate) MatchedText() []string { return renderAll(c.Matched) }

// DifferText is the differing features in plain words.
func (c Candidate) DifferText() []string { return renderAll(c.Differs) }

// IgnoredConditions lists, in plain words, the lesson's scope conditions that similarity does not score: anything
// that is neither an eq on a feature field nor an exists on a diff signal (neq, in, not_exists, other fields). A
// feature field with an unknown value is not listed here: it is deliberately never a feature.
func IgnoredConditions(k Knowledge) []string {
	var out []string
	for _, group := range [][]Condition{k.SituationSignature, k.ApplicabilityConditions} {
		for _, c := range group {
			if (strings.HasPrefix(c.Field, "diff.") || strings.HasPrefix(c.Field, "topic.")) && c.Op == OpExists {
				continue
			}
			if _, isStr := c.Value.(string); c.Op == OpEq && isStr && isFeatureField(c.Field) {
				continue
			}
			out = append(out, render(c))
		}
	}
	return out
}

func isFeatureField(field string) bool {
	switch field {
	case "relationship_state", "transition.status", "transition.from_state", "transition.to_state":
		return true
	}
	return hasString(scalarStateFeatures, field)
}

// withoutTopic is the candidate without its topic feature (see matchBySimilarity).
func (c Candidate) withoutTopic() Candidate {
	drop := func(in []FeatureResult) []FeatureResult {
		out := make([]FeatureResult, 0, len(in))
		for _, f := range in {
			if f.ID != FeatTopic {
				out = append(out, f)
			}
		}
		return out
	}
	c.Matched, c.Differs = drop(c.Matched), drop(c.Differs)
	return c
}
