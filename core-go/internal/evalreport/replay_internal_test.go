package evalreport

import "testing"

// A stored fail whose replay passes on an edited episode is the signature of a refused send: the row
// judged the artifact the human then repaired, which the stored final no longer holds. That row is
// excluded with its reason; every other mismatch still counts.
func TestSpecReplayExcludesSupersededDraftMismatch(t *testing.T) {
	f := newFixture()
	const tag = "evidence_sufficiency:v1"
	k := versionKey{evaluator: "evidence_sufficiency", version: 1, tag: tag}
	v := &versionRow{Evaluator: k.evaluator, Version: 1, Status: "active", PromotedAt: &tProm, Spec: pentestSpec()}

	judged := func(ep *episodeRow, verdict string) {
		for _, r := range f.rows(ep, k.evaluator, tag, verdict) {
			r.Kind = "human_delta"
		}
	}
	// Refused-then-repaired edit: the stored final carries the attachment; the row failed the draft before.
	repaired := f.episode("APPROVE_WITH_EDIT", deltaOfKind("attachment_added"))
	f.drafts(repaired, nil, strs("pentest.pdf"))
	judged(repaired, "fail")
	// A faithful row: unchanged approval, candidate lacks the attachment, stored fail.
	okRow := f.episode("APPROVE_UNCHANGED", nil)
	f.drafts(okRow, nil, nil)
	judged(okRow, "fail")
	// A genuine mismatch: unchanged approval, candidate lacks the attachment, stored PASS.
	bad := f.episode("APPROVE_UNCHANGED", nil)
	f.drafts(bad, nil, nil)
	judged(bad, "pass")
	// A stored pass whose replay fails on an EDITED episode is not the refused-send signature.
	editedBad := f.episode("APPROVE_WITH_EDIT", deltaOfKind("attachment_added"))
	f.drafts(editedBad, nil, strs("other.pdf"))
	judged(editedBad, "pass")

	rp := f.w.specReplay(k, v)
	if rp.Replayable != 3 || rp.Matching != 1 || rp.Excluded != 1 || rp.Rate == nil || !closeTo(*rp.Rate, 1.0/3) {
		t.Fatalf("replay = %+v", rp)
	}
	if len(rp.ExcludedRows) != 1 || rp.ExcludedRows[0].RowID == "" || rp.ExcludedRows[0].Reason == "" {
		t.Fatalf("excluded rows = %+v", rp.ExcludedRows)
	}
}

func TestSpecReplayNeedsAnExecutableSpec(t *testing.T) {
	f := newFixture()
	k := versionKey{evaluator: "grounding", version: 2, tag: "grounding:v2"}
	if rp := f.w.specReplay(k, nil); rp.Replayable != 0 {
		t.Fatalf("nil version replayed: %+v", rp)
	}
	if rp := f.w.specReplay(k, &versionRow{}); rp.Replayable != 0 {
		t.Fatalf("spec-less version replayed: %+v", rp)
	}
}
