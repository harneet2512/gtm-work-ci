package claimstest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// Person is a gold person with the identities the fake extractor hands to core.
type Person struct {
	Key         string
	Email       string
	DisplayName string
}

// People maps gold person keys to their identities.
type People map[string]Person

// PeopleFrom collects every person entity of the gold checkpoints with its email.
func PeopleFrom(golds []Gold) People {
	out := People{}
	for _, g := range golds {
		for _, e := range g.Expected.Entities {
			if e.Kind != "person" {
				continue
			}
			p := out[e.Key]
			p.Key, p.DisplayName = e.Key, e.DisplayName
			for _, id := range e.SourceIdentities {
				if addr, ok := strings.CutPrefix(id, "email:"); ok && p.Email == "" {
					p.Email = addr
				}
			}
			out[e.Key] = p
		}
	}
	return out
}

// Derivation is the result of deriving fake-worker candidates from gold.
type Derivation struct {
	// ByFile holds the candidates the fake returns for the activity of each event file (path relative to fixtures/).
	ByFile map[string][]claims.Candidate
	// Skipped explains every critical fact that produced no candidate.
	Skipped []string
}

// roleVocabulary is the candidate role enum.
var roleVocabulary = map[string]bool{
	"champion": true, "economic_buyer": true, "technical_evaluator": true, "security": true, "legal": true,
	"executive_sponsor": true, "user": true, "influencer": true, "blocker": true, "procurement": true,
}

// DeriveCandidates turns the critical facts of every checkpoint into the candidates a perfect
// worker would return for each evidence activity, plus candidates for the gold conflicts
// (contradicting_quote). It tests adjudication, folding and coalescing, not LLM quality.
//
// Facts without an evidence quote are structured CRM facts: the rule extractors must produce them,
// the fake does not. The same fact appearing in several checkpoints becomes one candidate.
func DeriveCandidates(golds []Gold, people People, extras map[string][]claims.Candidate) (Derivation, error) {
	d := Derivation{ByFile: map[string][]claims.Candidate{}}
	seen := map[string]bool{}
	add := func(file string, c claims.Candidate) {
		key := file + "|" + string(c.FieldPath) + "|" + c.SubjectIdentity + "|" + claims.CanonicalValue(c.Value)
		if !seen[key] {
			seen[key] = true
			d.ByFile[file] = append(d.ByFile[file], c)
		}
	}
	for _, g := range golds {
		for _, cf := range g.Expected.CriticalFacts {
			if cf.EvidenceQuote == nil {
				d.Skipped = append(d.Skipped, fmt.Sprintf("%s cp%d %s: structured evidence (rule extractors)", g.Account, g.Checkpoint, cf.Field))
				continue
			}
			c, err := candidateFor(cf, people)
			if err != nil {
				return Derivation{}, fmt.Errorf("claimstest: %s cp%d fact %s: %w", g.Account, g.Checkpoint, cf.Field, err)
			}
			add(cf.EvidenceEventFile, c)
		}
		for _, gc := range g.Expected.Conflicts {
			add(gc.ContradictingEventFile, claims.Candidate{
				FieldPath: claims.FieldPath(gc.Field), Value: gc.ContradictingValue, Confidence: 0.8, EvidenceQuote: gc.ContradictingQuote,
			})
		}
	}
	files := make([]string, 0, len(extras))
	for f := range extras {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, c := range extras[f] {
			add(f, c)
		}
	}
	return d, nil
}

func candidateFor(cf CriticalFact, people People) (claims.Candidate, error) {
	c := claims.Candidate{FieldPath: claims.FieldPath(cf.Field), Value: cf.Value, Confidence: 0.9, EvidenceQuote: *cf.EvidenceQuote}
	if !c.FieldPath.Valid() {
		return c, fmt.Errorf("unknown field path %q", cf.Field)
	}
	switch c.FieldPath {
	case claims.FieldChampion, claims.FieldEconomicBuyer, claims.FieldBuyingGroupMember:
		return personCandidate(c, people)
	case claims.FieldStakeholderRole:
		return roleCandidate(c, cf.Subject, people)
	case claims.FieldDelegation:
		return delegationCandidate(c, people)
	}
	return c, nil
}

func emailOf(people People, key string) (string, error) {
	p, ok := people[key]
	if !ok || p.Email == "" {
		return "", fmt.Errorf("no email known for %q", key)
	}
	return p.Email, nil
}

// personCandidate hands the person's email to core as both subject and value; "unknown" stays as is.
func personCandidate(c claims.Candidate, people People) (claims.Candidate, error) {
	var key string
	if err := json.Unmarshal(c.Value, &key); err != nil {
		return c, fmt.Errorf("value must be a string: %w", err)
	}
	if !strings.HasPrefix(key, "person:") {
		return c, nil
	}
	addr, err := emailOf(people, key)
	if err != nil {
		return c, err
	}
	c.Value, c.SubjectIdentity = claims.MustJSON(addr), addr
	return c, nil
}

func roleCandidate(c claims.Candidate, subject string, people People) (claims.Candidate, error) {
	addr, err := emailOf(people, subject)
	if err != nil {
		return c, err
	}
	role, _ := claims.StringValue(c.Value)
	c.SubjectIdentity = addr
	if roleVocabulary[role] {
		c.Role = role
	}
	return c, nil
}

func delegationCandidate(c claims.Candidate, people People) (claims.Candidate, error) {
	var d struct{ From, To, Scope string }
	if err := json.Unmarshal(c.Value, &d); err != nil {
		return c, fmt.Errorf("delegation value: %w", err)
	}
	from, err := emailOf(people, d.From)
	if err != nil {
		return c, err
	}
	to, err := emailOf(people, d.To)
	if err != nil {
		return c, err
	}
	c.Value = claims.MustJSON(map[string]string{"from": from, "to": to, "scope": d.Scope})
	c.SubjectIdentity = from
	return c, nil
}

// NewGoldExtractor returns a fake worker that answers with the derived candidates of an activity's
// event file. fileOf maps an activity id to its event file ("" when unknown).
func NewGoldExtractor(d Derivation, fileOf func(activityID string) string) *FakeExtractor {
	return &FakeExtractor{Model: "gold-fake", Candidates: func(req claims.ExtractRequest) []claims.Candidate {
		return append([]claims.Candidate(nil), d.ByFile[fileOf(req.Activity.ID)]...)
	}}
}
