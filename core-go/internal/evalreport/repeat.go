package evalreport

import (
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// seededVersion reports whether the evaluator_versions row came out of the learning loop's seeding:
// delta-seeded (created_from human_delta) or verdict-seeded (SeedVerdictCriterion writes evaluator
// 'human_delta', created_from 'manual', a linked knowledge row and no executable spec).
func seededVersion(v *versionRow) bool {
	if v.CreatedFrom == "human_delta" && v.SourceDeltaID != "" {
		return true
	}
	return v.Evaluator == "human_delta" && v.CreatedFrom == "manual" && v.KnowledgeID != "" &&
		len(v.Spec.LiteralChanges) == 0
}

const repeatBasis = "a repeat is an episode where the version's executable shadow_spec FAILS the selected " +
	"candidate AND PASSES the human's final draft (the human made the very correction the version " +
	"encodes); a version whose spec cannot execute falls back to an exact semantic-label match with its " +
	"source delta. Windows are scoped to the version's account (global versions: every account), the " +
	"version's in-force period (promoted_at until retirement/supersession) and, after activation, to " +
	"episodes the exact version ran on; the split is evaluator_versions.promoted_at vs the episode's " +
	"human_decisions.created_at"

// repeatCorrection compares how often the same correction repeats before vs after activation.
func (w *world) repeatCorrection(k versionKey, v *versionRow, all []*evalRow) Metric[RepeatCorrection] {
	if v != nil && v.SpecErr != "" {
		return na[RepeatCorrection](k.tag + " has a malformed shadow_spec (" + v.SpecErr +
			") — its repeat correction cannot be measured")
	}
	if v == nil || v.PromotedAt == nil {
		return na[RepeatCorrection](k.tag + " has no promotion timestamp (status " + versionStatus(v) +
			") — promoted_at marks activation")
	}
	method, labels := "spec_replay", map[string]bool(nil)
	if !v.Spec.Executable() {
		method, labels = "label_match", w.sourceLabels(v)
		if len(labels) == 0 {
			return na[RepeatCorrection](k.tag + " has no executable shadow_spec and its source delta carries " +
				"no semantic label — there is nothing to match a repeated correction on")
		}
	}
	rc := RepeatCorrection{PromotedAt: *v.PromotedAt, Method: method, Scope: scopeOf(v),
		InForceUntil: w.inForceUntil(v), Basis: repeatBasis}
	ran := runsOf(k.tag, all)
	for _, ep := range w.sortedEpisodes() {
		if !ep.HasDecision || !inScope(v, ep) {
			continue
		}
		if ep.HumanDeltaID != "" && ep.HumanDeltaID == v.SourceDeltaID {
			rc.SeedExcluded = true // the seeding correction created the criterion: not a repeat of itself
			continue
		}
		win := &rc.Before
		if !ep.DecidedAt.Before(*v.PromotedAt) {
			if rc.InForceUntil != nil && !ep.DecidedAt.Before(*rc.InForceUntil) {
				continue // after retirement/supersession the version was not in force
			}
			if !ran[ep.RunID] {
				continue // the exact version never produced a result on this episode
			}
			win = &rc.After
		}
		win.Episodes++
		d := w.deltaOf(ep)
		if d != nil {
			win.Deltas++
		}
		repeat, scorable := w.isRepeat(v, labels, ep, d)
		switch {
		case !scorable:
			win.Unscorable++
		case repeat:
			win.Matching++
			win.MatchingEpisode++
		}
	}
	for _, win := range []*Window{&rc.Before, &rc.After} {
		if win.Deltas > 0 {
			r := float64(win.Matching) / float64(win.Deltas)
			win.Rate = &r
		}
		if win.Episodes > 0 {
			r := float64(win.MatchingEpisode) / float64(win.Episodes)
			win.EpisodeRate = &r
		}
	}
	rc.AxisFail = w.axisFail(v, rc.InForceUntil, k.tag, all)
	return avail(rc)
}

// isRepeat decides whether an episode repeats the version's correction. spec_replay: the executable spec
// is violated by the selected candidate and satisfied by the human's final draft; scorable=false when
// the drafts cannot be reconstructed from the stored rows. label_match: the episode's delta carries an
// exact semantic label of the source delta.
func (w *world) isRepeat(v *versionRow, labels map[string]bool, ep *episodeRow, d *deltaRow) (repeat, scorable bool) {
	if labels != nil {
		return criterionMatch(d, labels), true
	}
	cand, final, ok := w.draftsOf(ep)
	if !ok {
		return false, false
	}
	failedCandidate, _ := v.Spec.RunSpec(cand)
	failedFinal, _ := v.Spec.RunSpec(final)
	return failedCandidate && !failedFinal, true
}

// criterionMatch reports whether a delta repeats a label-matched criterion: it carries one of the
// criterion's semantic labels EXACTLY. The broad change-kind axis is deliberately not consulted — an
// axis like next_action_quality is a hub that nearly every edit touches.
func criterionMatch(d *deltaRow, labels map[string]bool) bool {
	if d == nil {
		return false
	}
	for _, l := range d.Labels {
		if labels[l] {
			return true
		}
	}
	return false
}

// sourceLabels is the semantic-label set of the version's source delta.
func (w *world) sourceLabels(v *versionRow) map[string]bool {
	out := map[string]bool{}
	if src := w.deltas[v.SourceDeltaID]; src != nil {
		for _, l := range src.Labels {
			out[l] = true
		}
	}
	return out
}

// draftsOf reconstructs the episode's selected candidate and the human's final draft from stored rows.
// Without a stored final the human sent the candidate as-is. A final that stored only the artifact
// inherits the candidate's recipients (an artifact-only edit leaves final_to/final_cc NULL).
func (w *world) draftsOf(ep *episodeRow) (cand, final deterministic.Output, ok bool) {
	h := w.decisions[ep.ID]
	idx := ep.FinalDraftIndex
	if h != nil && h.SelectedDraft >= 0 {
		idx = h.SelectedDraft
	}
	c, found := w.candidates[ep.RunID][idx]
	if !found {
		return cand, final, false
	}
	cand, ok = decodeDraft(c.ActionType, c.To, c.CC, c.Artifact)
	if !ok {
		return cand, final, false
	}
	final = cand
	if h == nil || len(h.Final.Artifact) == 0 {
		return cand, final, true
	}
	to, cc, at := orBytes(h.Final.To, c.To), orBytes(h.Final.CC, c.CC), h.Final.ActionType
	if at == "" {
		at = c.ActionType
	}
	final, ok = decodeDraft(at, to, cc, h.Final.Artifact)
	return cand, final, ok
}

func orBytes(b, fallback []byte) []byte {
	if len(b) == 0 || string(b) == "null" {
		return fallback
	}
	return b
}

func scopeOf(v *versionRow) string {
	if v.AccountID == "" {
		return "global"
	}
	return "account " + v.AccountID
}

// inScope reports whether the version applies to the episode: global versions apply everywhere, an
// account-scoped version only on its own account.
func inScope(v *versionRow, ep *episodeRow) bool {
	return v.AccountID == "" || ep.AccountID == v.AccountID
}

// inForceUntil is the instant the version stopped being in force: the earliest of its recorded
// retirement and the promotion of a later version of the same evaluator whose scope overlaps (a
// promotion retires the prior active version atomically). nil = still in force (or unknowable).
func (w *world) inForceUntil(v *versionRow) *time.Time {
	var end *time.Time
	take := func(t time.Time) {
		if end == nil || t.Before(*end) {
			c := t
			end = &c
		}
	}
	if t, ok := w.retired[keyOf(v.Evaluator, v.Version).tag]; ok {
		take(t)
	}
	if v.PromotedAt == nil {
		return end
	}
	for _, u := range w.versions {
		if u.Evaluator != v.Evaluator || u.Version == v.Version || u.PromotedAt == nil ||
			!u.PromotedAt.After(*v.PromotedAt) {
			continue
		}
		if u.AccountID == "" || v.AccountID == "" || u.AccountID == v.AccountID {
			take(*u.PromotedAt)
		}
	}
	return end
}

// runsOf is the set of runs the exact version tag produced at least one result on.
func runsOf(tag string, rows []*evalRow) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		if r.Tag == tag {
			out[r.RunID] = true
		}
	}
	return out
}

func (w *world) sortedEpisodes() []*episodeRow {
	out := make([]*episodeRow, 0, len(w.episodes))
	for _, ep := range w.episodes {
		out = append(out, ep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// windowOf resolves which side of activation an eval row sits on: the decided episode's decision
// time when the run reached one (eval_runs.created_at is the run's replay clock, not the write
// time — the episode's human_decisions.created_at is the real decision wall-clock); the row's own
// replay-clock time otherwise.
func (w *world) windowOf(r *evalRow, promotedAt time.Time) bool {
	if ep := w.byRun[r.RunID]; ep != nil {
		return ep.DecidedAt.Before(promotedAt)
	}
	return r.CreatedAt.Before(promotedAt)
}

func versionStatus(v *versionRow) string {
	if v == nil {
		return "unregistered"
	}
	return v.Status
}

// axisFail is the share of the version's own stored results still failing before vs after activation
// — the same correction still needed, per evaluated draft. Rows are limited to the version's scope
// (its account's runs; every run for a global version) and to its in-force period; rows on decided
// runs split by the episode's decision wall-clock, rows on undecided runs by their replay-clock time.
func (w *world) axisFail(v *versionRow, end *time.Time, tag string, all []*evalRow) AxisFail {
	af := AxisFail{}
	for _, r := range all {
		if r.Tag != tag || !w.rowInScope(v, r) {
			continue
		}
		if end != nil && w.rowTime(r).After(*end) {
			continue
		}
		if w.windowOf(r, *v.PromotedAt) {
			af.BeforeN++
			if r.Verdict == "fail" {
				af.BeforeFail++
			}
		} else {
			af.AfterN++
			if r.Verdict == "fail" {
				af.AfterFail++
			}
		}
	}
	if af.BeforeN > 0 {
		r := float64(af.BeforeFail) / float64(af.BeforeN)
		af.BeforeRate = &r
	}
	if af.AfterN > 0 {
		r := float64(af.AfterFail) / float64(af.AfterN)
		af.AfterRate = &r
	}
	return af
}

// rowInScope applies the version's account scope to an eval row through its run's episode.
func (w *world) rowInScope(v *versionRow, r *evalRow) bool {
	if v.AccountID == "" {
		return true
	}
	ep := w.anyByRun[r.RunID]
	return ep != nil && ep.AccountID == v.AccountID
}

func (w *world) rowTime(r *evalRow) time.Time {
	if ep := w.byRun[r.RunID]; ep != nil {
		return ep.DecidedAt
	}
	return r.CreatedAt
}
