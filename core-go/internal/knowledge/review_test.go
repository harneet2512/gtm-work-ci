package knowledge

import (
	"testing"
	"time"
)

func TestLateEvidenceNeverMovesValidationBackwards(t *testing.T) {
	kn := candidate()
	recent := now
	kn.LastValidatedAt = &recent
	late := Evidence{Kind: EvidenceCustomerReaction, RefID: "r-late", Polarity: "positive", At: now.Add(-30 * day)}
	next, err := ApplyEvidence(kn, late)
	if err != nil {
		t.Fatal(err)
	}
	if !next.LastValidatedAt.Equal(now) || next.Counts.PositiveReactions != 1 {
		t.Fatalf("last validated %v, counts %+v", next.LastValidatedAt, next.Counts)
	}
}

func TestCloneIsDeep(t *testing.T) {
	kn := keepChampion()
	kn.Guidance = Guidance{Summary: "s", Do: []string{"a"}, Dont: []string{"b"}}
	out := kn.clone()
	out.Exceptions[1].Conditions[0].Value.([]any)[0] = "changed"
	out.Exceptions[0].Description = "changed"
	out.Guidance.Do[0] = "changed"
	out.SituationSignature[0].Field = "changed"
	if kn.Exceptions[1].Conditions[0].Value.([]any)[0] != "departed" || kn.Exceptions[0].Description == "changed" ||
		kn.Guidance.Do[0] != "a" || kn.SituationSignature[0].Field != "motion" {
		t.Fatal("clone shares memory with its source")
	}
	pattern := map[string]any{"text": "x", "status": []any{"open"}}
	if got := cloneValue(pattern).(map[string]any); got["status"].([]any)[0] != "open" {
		t.Fatalf("cloneValue = %v", got)
	}
	if empty := (Knowledge{}).clone(); empty.Exceptions != nil || empty.SituationSignature != nil {
		t.Fatal("nil slices stay nil")
	}
}

func TestSilenceFallbackIsCounted(t *testing.T) {
	before := SilenceFallbacks()
	for _, v := range []Value{{}, {Known: true, Scalar: "not a time"}, {Known: true, Scalar: 3.0}} {
		if !silenceOpen(v, now) {
			t.Fatalf("fallback must keep the signal open: %+v", v)
		}
	}
	if !silenceOpen(Value{Known: true, Scalar: now.Add(-time.Hour).Format(time.RFC3339)}, now) {
		t.Fatal("no later interaction: open")
	}
	if got := SilenceFallbacks() - before; got != 3 {
		t.Fatalf("fallbacks counted %d, want 3", got)
	}
}
