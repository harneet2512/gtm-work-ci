package bucket2run

import (
	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
)

func spanRefs(in []recompute.SpanRef) []bucket2.SpanRef {
	out := make([]bucket2.SpanRef, 0, len(in))
	for _, r := range in {
		x := bucket2.SpanRef{Kind: string(r.Kind), Label: r.Label}
		if r.RefID != nil {
			x.RefID = *r.RefID
		}
		if r.Field != nil {
			x.Field = string(*r.Field)
		}
		if r.Verdict != nil {
			x.Verdict = *r.Verdict
		}
		out = append(out, x)
	}
	return out
}

// toInvalidation converts the recompute engine's derivation into the plain values D7 reads.
func toInvalidation(inv recompute.Invalidation) bucket2.Invalidation {
	out := bucket2.Invalidation{RunID: inv.RunID, Status: bucket2.Status(inv.Status), Edited: inv.Edited, AccountID: inv.AccountState.AccountID,
		VersionBefore: inv.AccountState.VersionBefore, VersionAfter: inv.AccountState.VersionAfter, Preserved: inv.AccountState.Preserved}
	for _, e := range inv.Entries {
		out.Entries = append(out.Entries, bucket2.Entry{Index: e.Index, Invalidated: spanRefs(e.Invalidated),
			Recomputed: spanRefs(e.Recomputed), NotRecomputed: spanRefs(e.NotRecomputed)})
	}
	return out
}
