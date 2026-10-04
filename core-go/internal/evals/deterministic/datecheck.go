package deterministic

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// HAR-97 §4 "Date / commitment consistency" sub-checks.
const (
	CheckDatesMatchState     Check = "date.matches_current_state"
	CheckMeetingNotStale     Check = "date.meeting_not_stale"
	CheckCommitmentHonored   Check = "date.commitment_not_contradicted"
	CheckPromisedAssetExists Check = "date.promised_asset_exists"
)

var dateChecks = []Check{CheckDatesMatchState, CheckMeetingNotStale, CheckCommitmentHonored, CheckPromisedAssetExists}

var (
	futureRE  = regexp.MustCompile(`(?i)\b(will|shall|going to|by|until|before|due|see you|look(?:ing)? forward|scheduled for|booked for|confirmed for|invite for|works for|available|propose|how about|could we|can we|let's|moved to|moved up to|rescheduled (?:to|for)|pushed (?:out )?to|next (?:sync|meeting|call|review|step|check-in) (?:is|will be)|is (?:on|set for)|thinking|planning|aiming)\b|'ll\b`)
	meetingRE = regexp.MustCompile(`(?i)\b(meet|meeting|call|review|session|sync|demo|workshop|walkthrough|invite|slot|calendar|book)\b`)
	bookedRE  = regexp.MustCompile(`(?i)(?:\b(?:our|the)\s+(?:[\w-]+\s+){0,3}(?:meeting|call|review|session|sync|demo|workshop|walkthrough)\s+(?:on|for|at)|\bsee you(?:\s+on)?|\b(?:confirmed|scheduled|booked|set|locked in)\s+for)\s*$`)
)

const day = "2006-01-02"

// DateCommitmentConsistency is the HAR-97 §4 "Date / commitment consistency" eval.
func DateCommitmentConsistency(in Input) Judgment {
	var f []Finding
	f = append(f, DatesMatchState(in)...)
	f = append(f, MeetingTimeNotStale(in)...)
	f = append(f, CommitmentNotContradicted(in)...)
	f = append(f, PromisedAssetExists(in)...)
	return judge(in, TypeDate, dateChecks, []string{"next_meeting", "current_commitments"}, f)
}

// clauseDate is a date mention with the clause it appears in.
type clauseDate struct {
	dateMention
	Clause string
	Prefix string
}

func draftDates(in Input) []clauseDate {
	var out []clauseDate
	for _, c := range clauses(draftText(in.Draft)) {
		for _, d := range findDates(c, in.EvaluatedAt) {
			out = append(out, clauseDate{dateMention: d, Clause: c, Prefix: c[:d.Start]})
		}
	}
	return out
}

func isPast(d time.Time, now time.Time) bool { return d.Before(civilDay(now)) }

// DatesMatchState: a meeting the draft presents as booked exists in state or on the calendar, and
// no date the draft presents as upcoming (or writes as due/wait) has already passed.
func DatesMatchState(in Input) []Finding {
	var out []Finding
	meeting := in.Draft.ProposedActionType == ActionScheduleMeeting
	booked, merr := meetingDays(in)
	if merr != nil {
		out = append(out, failure(CheckDatesMatchState, fmt.Sprintf("the next_meeting state is unreadable: %v", merr),
			"Repair the account state before the draft relies on it.").blocking().withState("next_meeting"))
	}
	for _, d := range draftDates(in) {
		switch {
		case !meeting && !isPast(d.Day, in.EvaluatedAt) && bookedRE.MatchString(d.Prefix) && !booked[d.Day]:
			out = append(out, failure(CheckDatesMatchState,
				fmt.Sprintf("presents a meeting on %s as booked, but state and calendar have no meeting that day", d.Day.Format(day)),
				"Do not present the meeting as booked: propose it, or wait until it is on the calendar.").blocking().withState("next_meeting"))
		case isPast(d.Day, in.EvaluatedAt) && d.upcoming() && !meetingRE.MatchString(d.Clause):
			out = append(out, failure(CheckDatesMatchState,
				fmt.Sprintf("presents %s (%q) as upcoming, but it has passed", d.Day.Format(day), d.Text),
				"Replace the past date with a current one.").blocking())
		}
	}
	c := in.Draft.CRMNextStepIntent
	if !meeting && c.DueAt != nil && c.DueAt.Before(in.EvaluatedAt) {
		out = append(out, failure(CheckDatesMatchState, fmt.Sprintf("CRM due_at %s has already passed", c.DueAt.Format(day)),
			"Set the next-step due date in the future.").blocking())
	}
	if w := in.Draft.WaitUntil; w != nil && w.Before(in.EvaluatedAt) {
		out = append(out, failure(CheckDatesMatchState, fmt.Sprintf("wait_until %s has already passed", w.Format(day)),
			"Wait until a future time.").blocking())
	}
	return out
}

// meetingDays are the days with a booked meeting: the state's next meeting and scheduled calendar
// events. A next_meeting value of an unknown shape is an error, not an empty calendar.
func meetingDays(in Input) (map[time.Time]bool, error) {
	days := map[time.Time]bool{}
	var err error
	f := latestState(in).Fields.NextMeeting
	if f.Known && f.Value != nil {
		days, err = stateMeetingDays(f.Value, in.EvaluatedAt, days)
	}
	for _, p := range in.PriorActions {
		if p.Action == ActionScheduleMeeting && p.Status == "scheduled" && p.MeetingStart != nil {
			days[civilDay(*p.MeetingStart)] = true
		}
	}
	return days, err
}

func stateMeetingDays(v any, now time.Time, days map[time.Time]bool) (map[time.Time]bool, error) {
	switch m := v.(type) {
	case map[string]any:
		s, ok := m["start"].(string)
		if !ok {
			return days, fmt.Errorf("next_meeting has no start time")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return days, fmt.Errorf("next_meeting start %q: %w", s, err)
		}
		days[civilDay(t)] = true
	case string:
		for _, d := range findDates(m, now) {
			days[d.Day] = true
		}
	default:
		return days, fmt.Errorf("next_meeting is a %T, not an object or text", v)
	}
	return days, nil
}

// MeetingTimeNotStale: a proposed or scheduled meeting time is not in the past.
func MeetingTimeNotStale(in Input) []Finding {
	var out []Finding
	c := in.Draft.CRMNextStepIntent
	if in.Draft.ProposedActionType == ActionScheduleMeeting && c.DueAt != nil && c.DueAt.Before(in.EvaluatedAt) {
		out = append(out, failure(CheckMeetingNotStale, fmt.Sprintf("meeting time %s has already passed", c.DueAt.Format(time.RFC3339)),
			"Propose a slot after the evaluation time, or ask the customer for new times.").blocking())
	}
	for _, d := range draftDates(in) {
		if isPast(d.Day, in.EvaluatedAt) && d.upcoming() && meetingRE.MatchString(d.Clause) {
			out = append(out, failure(CheckMeetingNotStale,
				fmt.Sprintf("proposes a meeting on %s (%q), which has passed", d.Day.Format(day), d.Text),
				"Propose a slot after the evaluation time, or ask the customer for new times.").blocking())
		}
	}
	return out
}

// decodeItems decodes a list field's items whether they were built in Go or decoded from JSON.
func decodeItems(f reducer.Field) ([]reducer.Item, error) {
	if !f.Known || f.Value == nil {
		return nil, nil
	}
	if items, ok := f.Value.([]reducer.Item); ok {
		return items, nil
	}
	raw, err := json.Marshal(f.Value)
	if err != nil {
		return nil, fmt.Errorf("encode list field: %w", err)
	}
	var items []reducer.Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode list field: %w", err)
	}
	return items, nil
}

// listItems is decodeItems for support text, where an unreadable field supports nothing.
func listItems(f reducer.Field) []reducer.Item {
	items, _ := decodeItems(f)
	return items
}

// ourOpenCommitments are open or overdue commitments with a due date that one of our employees owns.
func ourOpenCommitments(in Input) ([]reducer.Item, error) {
	people := personIndex(in)
	items, err := decodeItems(latestState(in).Fields.CurrentCommitments)
	var out []reducer.Item
	for _, it := range items {
		p, ok := people[it.OwnerPersonID]
		if ok && p.Kind == KindEmployee && it.DueAt != nil && (it.Status == "open" || it.Status == "overdue") {
			out = append(out, it)
		}
	}
	return out, err
}
