package knowledgestore

import (
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// columns are the JSON-encoded knowledge columns (contract -> SQL mapping, migration 0007 comment).
type columns struct {
	signature, applicability, do, dont, counts, counterexamples, exceptions string
	evidenceClasses, usedBy, provenance                                     string
}

func encode(k knowledge.Knowledge) (columns, error) {
	var c columns
	fields := []struct {
		dst *string
		v   any
	}{
		{&c.signature, k.SituationSignature}, {&c.applicability, orEmptySlice(k.ApplicabilityConditions)},
		{&c.do, orEmptySlice(k.Guidance.Do)}, {&c.dont, orEmptySlice(k.Guidance.Dont)}, {&c.counts, k.Counts},
		{&c.counterexamples, orEmptySlice(k.Counterexamples)}, {&c.exceptions, orEmptySlice(k.Exceptions)},
		{&c.evidenceClasses, orEmptySlice(k.EvidenceClasses)}, {&c.usedBy, orEmptySlice(k.UsedByEvaluators)},
		{&c.provenance, k.Provenance},
	}
	for _, f := range fields {
		b, err := json.Marshal(f.v)
		if err != nil {
			return columns{}, fmt.Errorf("encode knowledge %s: %w", k.ID, err)
		}
		*f.dst = string(b)
	}
	return c, nil
}

// rawColumns receive the JSON columns of a knowledge row.
type rawColumns struct {
	signature, applicability, do, dont, counts, counterexamples, exceptions []byte
	evidenceClasses, usedBy, provenance                                     []byte
}

func (r rawColumns) decodeInto(k *knowledge.Knowledge) error {
	targets := []struct {
		src []byte
		dst any
	}{
		{r.signature, &k.SituationSignature}, {r.applicability, &k.ApplicabilityConditions},
		{r.do, &k.Guidance.Do}, {r.dont, &k.Guidance.Dont}, {r.counts, &k.Counts},
		{r.counterexamples, &k.Counterexamples}, {r.exceptions, &k.Exceptions},
		{r.evidenceClasses, &k.EvidenceClasses}, {r.usedBy, &k.UsedByEvaluators}, {r.provenance, &k.Provenance},
	}
	for _, t := range targets {
		if err := json.Unmarshal(t.src, t.dst); err != nil {
			return err
		}
	}
	return nil
}

// orEmptySlice encodes a nil slice as [] (the columns are NOT NULL JSON arrays).
func orEmptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
