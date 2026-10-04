package biwriter

import (
	"errors"
	"fmt"
)

// Validate is the citation rule of HAR-124: every claim of the update names the diff entry it restates and
// cites at least one activity that exists among the facts it was built from; every graph item it names is in
// the event's graph diff; a material change carries evidence and its update points at it. Build calls it on its
// own output, and the writer calls it again before persisting, so an uncited claim cannot reach the database.
func Validate(f Facts, r Result) error {
	known := knownActivities(f)
	entries := map[string]DiffEntry{}
	for _, e := range f.Diff.Entries {
		entries[e.Field] = e
	}
	graph := map[GraphDiffItem]bool{}
	for _, it := range f.Graph.Items {
		graph[GraphDiffItem{Kind: it.Kind, Type: it.Type, ID: it.ID}] = true
	}
	c := r.Change
	if c.MaterialChange && len(c.EvidenceRefs) == 0 {
		return errors.New("biwriter: a material change must carry evidence")
	}
	if err := refsKnown("change", c.EvidenceRefs, known); err != nil {
		return err
	}
	if r.BI == nil {
		if c.MaterialChange {
			return errors.New("biwriter: a material change needs its business-intelligence update")
		}
		return nil
	}
	if r.BI.AccountChangeID != c.ID {
		return fmt.Errorf("biwriter: the update restates change %s, not %s", r.BI.AccountChangeID, c.ID)
	}
	if len(r.BI.Claims) == 0 {
		return errors.New("biwriter: an update reports at least one change")
	}
	for i, cl := range r.BI.Claims {
		where := fmt.Sprintf("claim %d", i+1)
		if cl.StateDiffField == nil || *cl.StateDiffField == "" {
			return fmt.Errorf("biwriter: %s does not name the diff entry it restates", where)
		}
		if e, ok := entries[*cl.StateDiffField]; !ok || !e.Material {
			return fmt.Errorf("biwriter: %s restates %q, which is not a material entry of the state diff", where, *cl.StateDiffField)
		}
		if len(cl.EvidenceRefs) == 0 {
			return fmt.Errorf("biwriter: %s is uncited: it carries no evidence refs", where)
		}
		if err := refsKnown(where, cl.EvidenceRefs, known); err != nil {
			return err
		}
		for _, it := range cl.GraphDiffItems {
			if !graph[it] {
				return fmt.Errorf("biwriter: %s cites graph item %s %s %s that is not in the event's graph diff", where, it.Kind, it.Type, it.ID)
			}
		}
	}
	return nil
}

func refsKnown(where string, refs []EvidenceRef, known map[string]bool) error {
	for _, ref := range refs {
		if ref.ActivityID == "" {
			return fmt.Errorf("biwriter: %s cites an evidence ref without an activity", where)
		}
		if !known[ref.ActivityID] {
			return fmt.Errorf("biwriter: %s cites activity %s, which is not among the facts of this change", where, ref.ActivityID)
		}
	}
	return nil
}

// knownActivities are the activities the facts rest on: the trigger activity, the diff's activities and
// the evidence the diff entries and the transition carry.
func knownActivities(f Facts) map[string]bool {
	out := map[string]bool{f.Activity.ID: true}
	for _, id := range f.Diff.ActivityIDs {
		out[id] = true
	}
	for _, e := range f.Diff.Entries {
		for _, r := range e.EvidenceRefs {
			out[r.ActivityID] = true
		}
	}
	if f.Transition != nil {
		for _, list := range [][]Fact{f.Transition.Supporting, f.Transition.Missing} {
			for _, fact := range list {
				for _, r := range fact.Evidence {
					out[r.ActivityID] = true
				}
			}
		}
	}
	return out
}
