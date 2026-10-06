package demorun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Status is the outcome of one verify step. SKIP is never a pass: it means the step could not be judged.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

// Step is one line of `demo verify`.
type Step struct {
	ID     string
	Name   string
	Status Status
	Detail string
}

// DecisionFact is the human_strategy_decisions row (with its human_decisions and decision_episodes rows).
type DecisionFact struct {
	ID                  string
	SelectedCandidateID string
	OriginalPreference  string
	SendDecision        string // pending | send | discard
	HumanDecision       string // approve | edit | reject | ignore (empty until sent)
	HumanAction         string
	Edits               int
}

// DeltaFact is the human_deltas row.
type DeltaFact struct {
	ID             string
	Labels         []string
	Unexplained    bool
	LiteralChanges int
}

// InferenceFact is the judgment_inferences row.
type InferenceFact struct {
	ID            string
	Verdict       string // pending | confirmed | corrected
	HasCorrection bool
}

// Facts are the authoritative rows `demo verify` reads back (Postgres), plus the two things only core or the
// Slack audit file can say.
type Facts struct {
	ManifestID         string
	InvisibilityAtSeed string
	InvisibilityNow    string
	Played             bool
	SlackOn            bool

	BIUpdateID    string
	M1TS          string
	RunID         string
	StrategySetID string
	EpisodeID     string
	M2TS          string
	M3TS          string

	Decision    *DecisionFact
	Delta       *DeltaFact
	Inference   *InferenceFact
	VerdictRows int
	NoteRows    int

	SlackRowsPosted int
	AuditPosts      int
	AuditUpdates    int
	AuditKnown      bool
}

// Evaluate judges every step of the walkthrough from the facts. It is pure: no step passes on the strength of
// anything but a fact.
func Evaluate(f Facts) []Step {
	var out []Step
	add := func(id, name string, st Status, format string, args ...any) {
		out = append(out, Step{ID: id, Name: name, Status: st, Detail: fmt.Sprintf(format, args...)})
	}
	slackStep := func(id, name, ts, hint string, gate bool) {
		switch {
		case !f.SlackOn:
			add(id, name, StatusSkip, "Slack was not running, so this was not observed")
		case !gate:
			add(id, name, StatusSkip, "an earlier step has not happened yet")
		case ts != "":
			add(id, name, StatusPass, "posted (ts %s)", ts)
		default:
			add(id, name, StatusFail, "%s", hint)
		}
	}
	published := f.StrategySetID != ""
	sent := f.Decision != nil && f.Decision.SendDecision == "send"

	if f.ManifestID != "" && f.InvisibilityAtSeed == "withheld" {
		add("V01", "case frozen, Event N invisible before Play", StatusPass, "manifest %s; invisibility was withheld at seed time", f.ManifestID)
	} else {
		add("V01", "case frozen, Event N invisible before Play", StatusFail, "manifest %q, invisibility at seed %q (want withheld)", f.ManifestID, f.InvisibilityAtSeed)
	}
	switch {
	case !f.Played:
		add("V02", "Event N released by Play", StatusSkip, "Play has not run yet")
	case f.InvisibilityNow == "released":
		add("V02", "Event N released by Play", StatusPass, "core reports the manifest released")
	default:
		add("V02", "Event N released by Play", StatusFail, "core reports %q after Play", f.InvisibilityNow)
	}
	switch {
	case !f.Played:
		add("V03", "BI update written", StatusSkip, "Play has not run yet")
	case f.BIUpdateID != "":
		add("V03", "BI update written", StatusPass, "business_intelligence_updates %s", f.BIUpdateID)
	default:
		add("V03", "BI update written", StatusFail, "no business_intelligence_updates row for the account")
	}
	slackStep("V04", "M1 posted", f.M1TS, "no Slack ts recorded for the BI update (is the slackbot running and in #ghost-demo?)", f.BIUpdateID != "")
	if published {
		add("V05", "strategy run published", StatusPass, "run %s, strategy set %s, episode %s", orDash(f.RunID), f.StrategySetID, f.EpisodeID)
	} else if !f.Played {
		add("V05", "strategy run published", StatusSkip, "Play has not run yet")
	} else {
		add("V05", "strategy run published", StatusFail, "no strategy_sets row for the account yet (`demo logs core` / `demo logs worker`)")
	}
	slackStep("V06", "M2 posted", f.M2TS, "no Slack ts recorded for the chooser (Message 2)", published)

	out = append(out, decisionSteps(f, published, sent)...)
	slackStep("V09", "M3 posted", f.M3TS, "no Slack ts recorded for the judgment (Message 3), which follows Send", sent)
	out = append(out, judgmentSteps(f, sent)...)
	out = append(out, slackCountStep(f))
	return out
}

func decisionSteps(f Facts, published, sent bool) []Step {
	var out []Step
	add := func(id, name string, st Status, format string, args ...any) {
		out = append(out, Step{ID: id, Name: name, Status: st, Detail: fmt.Sprintf(format, args...)})
	}
	switch {
	case !published:
		add("V07", "human decision recorded and sent", StatusSkip, "no published strategy set to decide on")
	case f.Decision == nil:
		add("V07", "human decision recorded and sent", StatusFail, "no choice recorded: click View full, then Choose, in Message 2")
	case !sent:
		add("V07", "human decision recorded and sent", StatusFail, "candidate %s is chosen but send_decision is %q: click Send", f.Decision.SelectedCandidateID, f.Decision.SendDecision)
	default:
		d := f.Decision
		agree := "overrode gtm_ai's preference " + d.OriginalPreference
		if d.SelectedCandidateID == d.OriginalPreference {
			agree = "agreed with gtm_ai's preference"
		}
		add("V07", "human decision recorded and sent", StatusPass, "decision %s: candidate %s (%s); human_decision=%s, human_action=%s, %d saved edit(s)",
			d.ID, d.SelectedCandidateID, agree, orDash(d.HumanDecision), orDash(d.HumanAction), d.Edits)
	}
	switch {
	case !sent:
		add("V08", "edit captured as a HumanDelta", StatusSkip, "nothing has been sent yet")
	case f.Decision.HumanAction == "APPROVE_UNCHANGED":
		add("V08", "edit captured as a HumanDelta", StatusFail, "the draft was sent unchanged, so no HumanDelta exists: click Edit, change the draft, then Send")
	case f.Delta == nil:
		add("V08", "edit captured as a HumanDelta", StatusFail, "human_action %s but no HumanDelta row exists for the episode", f.Decision.HumanAction)
	default:
		d := f.Delta
		add("V08", "edit captured as a HumanDelta", StatusPass, "human_deltas %s: %d literal change(s), labels [%s], unexplained=%v",
			d.ID, d.LiteralChanges, strings.Join(d.Labels, ","), d.Unexplained)
	}
	return out
}

func judgmentSteps(f Facts, sent bool) []Step {
	var out []Step
	add := func(id, name string, st Status, format string, args ...any) {
		out = append(out, Step{ID: id, Name: name, Status: st, Detail: fmt.Sprintf(format, args...)})
	}
	inf := f.Inference
	switch {
	case !sent:
		add("V10", "judgment verdict recorded", StatusSkip, "nothing has been sent yet")
	case inf == nil:
		add("V10", "judgment verdict recorded", StatusFail, "the judgment inference is not generated yet (it follows Send; Message 3 appears when it exists)")
	case inf.Verdict == "pending":
		add("V10", "judgment verdict recorded", StatusFail, "inference %s is still pending: click Needs correction (or Looks right) in Message 3", inf.ID)
	default:
		add("V10", "judgment verdict recorded", StatusPass, "inference %s: verdict %s (correction text: %v), %d verdict history row(s)", inf.ID, inf.Verdict, inf.HasCorrection, f.VerdictRows)
	}
	switch {
	case !sent || inf == nil:
		add("V11", "judgment note recorded", StatusSkip, "no judgment inference yet")
	case f.NoteRows >= 1:
		add("V11", "judgment note recorded", StatusPass, "%d judgment_notes row(s)", f.NoteRows)
	default:
		add("V11", "judgment note recorded", StatusFail, "no note: click Add note in Message 3")
	}
	switch {
	case inf == nil || inf.Verdict == "pending":
		add("V12", "verdict and note history is consistent", StatusSkip, "no answered inference yet")
	case f.VerdictRows >= 1:
		add("V12", "verdict and note history is consistent", StatusPass, "the answer %q has %d append-only judgment_verdicts row(s)", inf.Verdict, f.VerdictRows)
	default:
		add("V12", "verdict and note history is consistent", StatusFail, "inference %s is answered (%s) but judgment_verdicts has no row", inf.ID, inf.Verdict)
	}
	return out
}

func slackCountStep(f Facts) Step {
	s := Step{ID: "V13", Name: "Slack outbox sent at most 3 messages"}
	if !f.SlackOn {
		s.Status, s.Detail = StatusSkip, "Slack was not running, so this was not observed"
		return s
	}
	if !f.Played {
		s.Status, s.Detail = StatusSkip, "Play has not run yet"
		return s
	}
	detail := fmt.Sprintf("%d surface message(s) posted", f.SlackRowsPosted)
	if f.AuditKnown {
		detail += fmt.Sprintf("; the Slack audit shows %d post(s) and %d in-place update(s)", f.AuditPosts, f.AuditUpdates)
	} else {
		detail += "; no Slack audit file to cross-check"
	}
	s.Detail = detail
	if f.SlackRowsPosted > 3 || (f.AuditKnown && f.AuditPosts > 3) {
		s.Status = StatusFail
		return s
	}
	s.Status = StatusPass
	return s
}

// WriteVerify prints one PASS/FAIL/SKIP line per step and a summary. It returns the number of FAIL steps.
func WriteVerify(w io.Writer, steps []Step) int {
	var pass, fail, skip int
	for _, s := range steps {
		fmt.Fprintf(w, "%-4s  %s  %-42s  %s\n", s.Status, s.ID, s.Name, s.Detail)
		switch s.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		default:
			skip++
		}
	}
	fmt.Fprintf(w, "\n%d PASS, %d FAIL, %d SKIP\n", pass, fail, skip)
	return fail
}

// CountAudit counts the successful chat.postMessage and chat.update records of the Slack audit file. known is
// false when the file does not exist (Slack never wrote anything, or the bot is not configured to audit).
func CountAudit(path string) (posts, updates int, known bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			Method string `json:"method"`
			OK     bool   `json:"ok"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || !rec.OK {
			continue
		}
		switch rec.Method {
		case "chat.postMessage":
			posts++
		case "chat.update":
			updates++
		}
	}
	return posts, updates, true
}
