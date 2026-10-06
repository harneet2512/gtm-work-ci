package bucket1

import (
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Operations is HAR-97's knowledge-mutation vocabulary (contracts/schemas/knowledge_mutation.v1.json).
var Operations = []string{"CREATE", "STRENGTHEN", "WEAKEN", "REFINE", "NARROW", "EXPAND", "ADD_EXCEPTION", "DISPUTE", "MARK_STALE", "NO_CHANGE"}

// RevisionTrace is the complete episode trace a knowledge revision must be justified by: the human's edit
// as D5 read it, the customer or world evidence kept apart from it, and how the episode sits against the
// knowledge that existed.
type RevisionTrace struct {
	HumanEdit               bool     `json:"human_edit"`
	SignalStrength          string   `json:"signal_strength"` // weak | moderate | strong (D5)
	HumanNarrows            bool     `json:"human_narrows"`
	HumanRewords            bool     `json:"human_rewords"`
	Customer                []string `json:"customer"`             // polarity of each customer reply: positive | neutral | negative
	IndependentPositive     int      `json:"independent_positive"` // positive replies from distinct, independent sources
	OutcomeAdvanced         bool     `json:"outcome_advanced"`
	KnowledgeInScope        bool     `json:"knowledge_in_scope"`       // existing knowledge applied to this state
	KnowledgeOutOfScope     bool     `json:"knowledge_out_of_scope"`   // existing knowledge, but the state is outside its scope
	DistinguishingCondition bool     `json:"distinguishing_condition"` // the episode differs from the scope by a nameable condition
	Disputed                bool     `json:"disputed"`
	Stale                   bool     `json:"stale"`
}

// RevisionEvidence is one piece of evidence a revision rests on. Human and customer evidence are separate
// kinds with their own ids.
type RevisionEvidence struct {
	Kind       string `json:"kind"` // decision_episode | human_decision | customer_reaction | business_outcome | counterexample
	RefID      string `json:"ref_id"`
	Polarity   string `json:"polarity"`
	ActivityID string `json:"activity_id"`
}

// Revision is the KnowledgeMutation under judgment.
type Revision struct {
	ID                      string                `json:"id"`
	Operation               string                `json:"operation"`
	KnowledgeID             string                `json:"knowledge_id"`
	Status                  string                `json:"status"`
	Preconditions           []knowledge.Condition `json:"preconditions"`
	ApplicabilityConditions []knowledge.Condition `json:"applicability_conditions"`
	Exceptions              []knowledge.Exception `json:"exceptions"`
	Evidence                []RevisionEvidence    `json:"evidence"`
	Version                 int                   `json:"version"`
	VersionBefore           int                   `json:"version_before"`
	PriorKept               bool                  `json:"prior_kept"` // the earlier version is still readable
	SourceEpisode           string                `json:"source_episode"`
	OccurredAt              time.Time             `json:"occurred_at"`
}

// empty reports a trace in which nothing happened: no edit, no customer or world evidence, no knowledge in play.
func (t RevisionTrace) empty() bool {
	return !t.HumanEdit && len(t.Customer) == 0 && !t.OutcomeAdvanced && !t.KnowledgeInScope && !t.KnowledgeOutOfScope &&
		!t.Disputed && !t.Stale
}

// MinIndependentForExpand is how many independent positive replies widen a knowledge object's scope.
const MinIndependentForExpand = 2

func humanKind(k string) bool { return k == "decision_episode" || k == "human_decision" }

func hasPolarity(ps []string, p string) bool { return contains(ps, p) }

// ClassifyRevision is the operation the episode's evidence justifies. Human choice is evidence that the
// human chose, not that it worked: only customer or world evidence strengthens, weakens or expands.
func ClassifyRevision(t RevisionTrace) string {
	strongEdit := t.HumanEdit && t.SignalStrength != "weak" && t.SignalStrength != ""
	positive := hasPolarity(t.Customer, "positive") || t.OutcomeAdvanced
	negative := hasPolarity(t.Customer, "negative")
	switch {
	case t.Stale:
		return "MARK_STALE"
	case t.Disputed:
		return "DISPUTE"
	case t.KnowledgeOutOfScope:
		// One positive reply outside the scope is one observation, not a reason to widen it (HAR-97 B9):
		// EXPAND needs repeated independent evidence, or a strong signal with an advanced outcome.
		independent := t.IndependentPositive >= MinIndependentForExpand || (strongEdit && t.OutcomeAdvanced)
		switch {
		case positive && !negative && independent:
			return "EXPAND"
		case strongEdit:
			return "CREATE" // a candidate scoped to the new state, not a wider old rule
		}
		return "NO_CHANGE"
	case t.KnowledgeInScope:
		switch {
		case negative && t.DistinguishingCondition:
			return "ADD_EXCEPTION"
		case negative:
			return "WEAKEN"
		case strongEdit && t.HumanNarrows:
			return "NARROW"
		case strongEdit && t.HumanRewords:
			return "REFINE"
		case positive:
			return "STRENGTHEN"
		}
		return "NO_CHANGE"
	case strongEdit:
		return "CREATE"
	}
	return "NO_CHANGE"
}

// GradeB9 judges whether the knowledge revision an episode caused is the one its trace justifies, with
// explicit scope, human and customer evidence kept apart, history kept and the replay time. Deterministic.
func GradeB9(ep Episode) Result {
	if ep.Trace == nil {
		return Unmeasured(ep, "B9", "the episode carries no revision trace (human edit interpretation and customer evidence)")
	}
	if ep.Trace.empty() && len(ep.Revisions) == 0 {
		return Unmeasured(ep, "B9", "nothing to judge: the episode has no human edit, customer evidence or existing knowledge in play, so no knowledge revision was due")
	}
	want := ClassifyRevision(*ep.Trace)
	if len(ep.Revisions) == 0 {
		obj := JudgedObject{Type: "KnowledgeMutation", ID: ep.ID}
		ref := Ref{ActivityID: firstActivity(ep), Note: "episode trace"}
		if want == "NO_CHANGE" {
			return rollup(ep, "B9", obj, []Assertion{pass("justified_by_trace", "no knowledge revision recorded; the trace justifies NO_CHANGE", ref)})
		}
		return rollup(ep, "B9", obj, []Assertion{fail("justified_by_trace", "no knowledge revision recorded",
			"the trace justifies "+want+" but nothing was recorded", ref)})
	}
	var merged []Assertion
	for _, rev := range ep.Revisions {
		merged = mergeAssertions(merged, gradeRevision(ep, *ep.Trace, want, rev))
	}
	return rollup(ep, "B9", JudgedObject{Type: "KnowledgeMutation", ID: ep.Revisions[0].ID}, merged)
}

// firstActivity is the id of the episode's first real activity, or "" when it has none (never an invented id).
func firstActivity(ep Episode) string {
	if len(ep.Activities) > 0 {
		return ep.Activities[0].ID
	}
	return ""
}

func gradeRevision(ep Episode, tr RevisionTrace, want string, rev Revision) []Assertion {
	refs := revisionRefs(ep, rev)
	return []Assertion{
		b9Operation(rev, refs), b9Justified(rev, want, refs), b9HumanNotTruth(rev, refs), b9CustomerSeparate(tr, rev, refs),
		b9Scope(rev, refs), b9History(rev, refs), b9ReplayTime(ep, rev, refs),
	}
}

func revisionRefs(ep Episode, rev Revision) []Ref {
	var refs []Ref
	for _, e := range rev.Evidence {
		a := e.ActivityID
		if a == "" {
			a = firstActivity(ep)
		}
		refs = append(refs, Ref{ActivityID: a, Note: e.Kind + " " + e.RefID})
	}
	if len(refs) == 0 {
		refs = []Ref{{ActivityID: firstActivity(ep), Note: "mutation " + rev.ID}}
	}
	return refs
}

func b9Operation(rev Revision, refs []Ref) Assertion {
	if contains(Operations, rev.Operation) {
		return pass("operation_valid", rev.Operation+" is in the vocabulary", refs...)
	}
	return fail("operation_valid", rev.Operation, "not one of "+strings.Join(Operations, " / "), refs...)
}

func b9Justified(rev Revision, want string, refs []Ref) Assertion {
	if rev.Operation == want {
		return pass("justified_by_trace", rev.Operation+" is what the complete episode trace justifies", refs...)
	}
	return fail("justified_by_trace", rev.Operation+" recorded", "the trace justifies "+want, refs...)
}

func statusRank(s string) int {
	switch s {
	case "provisional":
		return 1
	case "supported":
		return 2
	case "confirmed":
		return 3
	}
	return 0
}

func b9HumanNotTruth(rev Revision, refs []Ref) Assertion {
	const name = "human_choice_not_truth"
	if statusRank(rev.Status) == 0 {
		return pass(name, "status "+rev.Status+": a human choice alone earns no applicable status", refs...)
	}
	for _, e := range rev.Evidence {
		if (e.Kind == "customer_reaction" && e.Polarity == "positive") || e.Kind == "business_outcome" {
			return pass(name, "status "+rev.Status+" rests on customer or world evidence", refs...)
		}
	}
	return fail(name, "status "+rev.Status+" with human evidence only", "a human choice was treated as truth", refs...)
}

func b9CustomerSeparate(tr RevisionTrace, rev Revision, refs []Ref) Assertion {
	const name = "customer_evidence_separate"
	if len(tr.Customer) == 0 && !tr.OutcomeAdvanced {
		return na(name, "the episode has no customer or world evidence to keep apart")
	}
	var customer, human []string
	for _, e := range rev.Evidence {
		if e.Kind == "customer_reaction" || e.Kind == "business_outcome" {
			customer = append(customer, e.RefID)
		}
		if humanKind(e.Kind) {
			human = append(human, e.RefID)
		}
	}
	if len(customer) == 0 {
		return fail(name, "customer or world evidence exists in the episode but the revision does not record it", "customer evidence was dropped or folded into the human edit", refs...)
	}
	for _, c := range customer {
		if contains(human, c) {
			return fail(name, "customer and human evidence share id "+c, "customer evidence was recorded as the human decision", refs...)
		}
	}
	return pass(name, fmt.Sprintf("%d customer/world records, distinct from %d human records", len(customer), len(human)), refs...)
}

func b9Scope(rev Revision, refs []Ref) Assertion {
	const name = "scope_explicit"
	if rev.Operation == "NO_CHANGE" {
		return pass(name, "no scope change", refs...)
	}
	k := knowledge.Knowledge{SituationSignature: rev.Preconditions, ApplicabilityConditions: rev.ApplicabilityConditions}
	fields := knowledge.ScopeFields(k)
	if knowledge.ScopeTooBroad(k) {
		return fail(name, fmt.Sprintf("scope constrains %d field(s): %v", len(fields), fields),
			fmt.Sprintf("needs at least %d distinct preconditions; one edit and one reply must not apply everywhere", knowledge.MinScopeFields), refs...)
	}
	if rev.Operation == "ADD_EXCEPTION" && len(rev.Exceptions) == 0 {
		return fail(name, "ADD_EXCEPTION recorded with no exception", "the exception and its conditions must be explicit", refs...)
	}
	return pass(name, fmt.Sprintf("scope constrains %v with %d exception(s)", fields, len(rev.Exceptions)), refs...)
}

func b9History(rev Revision, refs []Ref) Assertion {
	const name = "history_preserved"
	switch {
	case rev.SourceEpisode == "" || len(rev.Evidence) == 0:
		return fail(name, "no provenance", "a revision needs its source episode and evidence", refs...)
	case rev.Operation == "CREATE" && (rev.Version != 1 || rev.VersionBefore != 0):
		return fail(name, fmt.Sprintf("CREATE at version %d from %d", rev.Version, rev.VersionBefore), "a created knowledge object starts at version 1", refs...)
	case rev.Operation != "CREATE" && rev.Operation != "NO_CHANGE" && (rev.Version != rev.VersionBefore+1 || !rev.PriorKept):
		return fail(name, fmt.Sprintf("version %d from %d, prior kept: %v", rev.Version, rev.VersionBefore, rev.PriorKept),
			"the earlier valid version must be kept and the version advanced by one", refs...)
	}
	return pass(name, fmt.Sprintf("version %d from %d, provenance %s", rev.Version, rev.VersionBefore, rev.SourceEpisode), refs...)
}

func b9ReplayTime(ep Episode, rev Revision, refs []Ref) Assertion {
	const name = "replay_time"
	if rev.OccurredAt.Equal(ep.At) {
		return pass(name, "stamped with the episode's replay time "+ep.At.Format(time.RFC3339), refs...)
	}
	return fail(name, "stamped "+rev.OccurredAt.Format(time.RFC3339), "must equal the replay time "+ep.At.Format(time.RFC3339)+", never the wall clock", refs...)
}

// mergeAssertions keeps one assertion per name across several revisions: the worst verdict wins, refs union.
func mergeAssertions(dst, src []Assertion) []Assertion {
	rank := map[string]int{Pass: 0, Unknown: 1, Warn: 2, Fail: 3}
	for _, s := range src {
		merged := false
		for i := range dst {
			if dst[i].Name != s.Name {
				continue
			}
			if rank[s.Verdict] > rank[dst[i].Verdict] {
				dst[i].Verdict, dst[i].Observed, dst[i].Why = s.Verdict, s.Observed, s.Why
			}
			dst[i].Refs = appendRefs(dst[i].Refs, s.Refs)
			merged = true
		}
		if !merged {
			dst = append(dst, s)
		}
	}
	return dst
}
