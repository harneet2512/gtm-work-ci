package crmarena

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Window is a deal's span: from creation (or its first event, if earlier) to its last event of any
// kind. Activities are the deal's emails and tasks (what a replay is made of); Last also covers the
// deal's quotes and contract signature, because the snapshot-only values (final stage, quote status)
// are dated there and must not precede any event of the deal. Orders name no deal in the source, so
// they are account-level and not part of a deal's span. The source marks no deal closed (IsClosed is
// false everywhere): a deal has ENDED at its last event; see the WP31 deviation note.
type Window struct {
	DealID     string
	AccountID  string
	Stage      string // the snapshot stage; the source has no stage history
	Start      time.Time
	Last       time.Time // last event of any kind; zero when the deal has no event
	Activities int       // emails + tasks
	times      []time.Time
}

// Before counts the deal's activities strictly before t; the rest are at or after it.
func (w Window) Before(t time.Time) int {
	n := 0
	for _, at := range w.times {
		if at.Before(t) {
			n++
		}
	}
	return n
}

// HasActivity reports whether the deal has any email or task.
func (w Window) HasActivity() bool { return w.Activities > 0 }

// Windows computes every deal's window.
func Windows(s Snapshot) (map[string]Window, error) {
	out := map[string]Window{}
	for _, o := range s.Opportunities {
		created, err := parseDateTime("Opportunity.CreatedDate", o.CreatedDate)
		if err != nil {
			return nil, err
		}
		out[o.ID] = Window{DealID: o.ID, AccountID: o.AccountID, Stage: o.StageName, Start: created}
	}
	touch := func(deal string, at time.Time, activity bool) error {
		w, ok := out[deal]
		if !ok {
			return fmt.Errorf("crmarena: record dated %s names unknown opportunity %q", at.Format(sfDate), deal)
		}
		if at.Before(w.Start) {
			w.Start = at
		}
		w.Last = later(w.Last, at)
		if activity {
			w.Activities++
			w.times = append(w.times, at)
		}
		out[deal] = w
		return nil
	}
	if err := touchActivities(s, touch); err != nil {
		return nil, err
	}
	if err := touchDealRecords(s, touch); err != nil {
		return nil, err
	}
	return out, nil
}

type toucher func(deal string, at time.Time, activity bool) error

func touchActivities(s Snapshot, touch toucher) error {
	for _, e := range s.Emails {
		at, err := parseDateTime("EmailMessage.MessageDate", e.MessageDate)
		if err != nil {
			return err
		}
		if err := touch(e.RelatedToID, at, true); err != nil {
			return err
		}
	}
	for _, t := range s.Tasks {
		at, err := parseDate("Task.ActivityDate", t.ActivityDate)
		if err != nil {
			return err
		}
		if err := touch(t.WhatID, at, true); err != nil {
			return err
		}
	}
	return nil
}

// touchDealRecords moves a deal's last event to its quotes' creation and its contract's signature.
func touchDealRecords(s Snapshot, touch toucher) error {
	for _, q := range s.Quotes {
		if q.OpportunityID == "" {
			continue
		}
		at, err := parseDateTime("Quote.CreatedDate", q.CreatedDate)
		if err != nil {
			return err
		}
		if err := touch(q.OpportunityID, at, false); err != nil {
			return err
		}
	}
	owners := contractOwners(s)
	for _, c := range s.Contracts {
		at, err := signedAt(c)
		if err != nil {
			return err
		}
		for _, deal := range owners[c.ID] {
			if err := touch(deal, at, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// Role of a deal at a cutoff.
type Role string

// Deal roles.
const (
	RolePrevious   Role = "previous"    // ended before the cutoff and quiet for at least the minimum quiet days: knowledge source
	RoleAmbiguous  Role = "ambiguous"   // no event after the cutoff but quiet for fewer days than the minimum: neither previous nor current
	RoleCurrent    Role = "current"     // started before the cutoff, active at or after it: replayed
	RoleFuture     Role = "future"      // starts at or after the cutoff: not used
	RoleNoActivity Role = "no_activity" // no email or task at all
)

// RoleAt classifies a deal at the cutoff with no quiet-period filter (hindsight: a deal with no event
// after the cutoff has "ended").
func (w Window) RoleAt(cutoff time.Time) Role { return w.RoleAtQuiet(cutoff, 0) }

// QuietDaysAt is the whole days between the deal's last event and the cutoff: how long the deal had
// been silent on the cutoff date. It is what anyone could know that day, unlike "has no later event".
// Zero or negative for a deal that has an event at or after the cutoff.
func (w Window) QuietDaysAt(cutoff time.Time) int {
	if w.Last.IsZero() {
		return 0
	}
	return int(cutoff.Sub(w.Last).Hours() / 24)
}

// RoleAtQuiet classifies a deal at the cutoff. A deal whose last event precedes the cutoff is previous
// only when it had been quiet for at least minQuietDays on the cutoff date; otherwise it is ambiguous
// (it may simply not have closed yet), and never current.
func (w Window) RoleAtQuiet(cutoff time.Time, minQuietDays int) Role {
	switch {
	case !w.HasActivity():
		return RoleNoActivity
	case w.Last.Before(cutoff) && w.QuietDaysAt(cutoff) < minQuietDays:
		return RoleAmbiguous
	case w.Last.Before(cutoff):
		return RolePrevious
	case w.Start.Before(cutoff):
		return RoleCurrent
	default:
		return RoleFuture
	}
}

// Split is the frozen previous/current partition (bench/data/deal_split.json).
type Split struct {
	Cutoff string `json:"cutoff"`
	Rule   string `json:"rule"`
	Seed   int64  `json:"seed"`
	// PreviousMinQuietDays is the quiet-period filter the split was frozen with, recorded so the split
	// reproduces from its own manifest.
	PreviousMinQuietDays int         `json:"previous_min_quiet_days"`
	Counts               Counts      `json:"counts"`
	Candidates           []Candidate `json:"candidates"`
	// PreviousByStage counts the previous deals per snapshot stage (the deviation note in WP31 relies on it).
	PreviousByStage map[string]int `json:"previous_deals_by_stage"`
	// AmbiguousByStage counts the ambiguous deals per snapshot stage.
	AmbiguousByStage map[string]int `json:"ambiguous_deals_by_stage"`
	Previous         []string       `json:"previous_deal_ids"`
	// PreviousDeals and AmbiguousDeals carry each deal's quiet days at the cutoff, in the order of the
	// id lists.
	PreviousDeals  []DealQuiet     `json:"previous_deals"`
	Ambiguous      []string        `json:"ambiguous_deal_ids"`
	AmbiguousDeals []DealQuiet     `json:"ambiguous_deals"`
	Current        []string        `json:"current_deal_ids"`
	Source         SplitProvenance `json:"source"`
}

// SplitProvenance records what the split was computed from.
type SplitProvenance struct {
	Dataset  string `json:"dataset"`
	Licence  string `json:"licence"`
	Manifest string `json:"manifest_sha256,omitempty"`
}

// Counts are the deal counts per role, plus the accounts with deals of both kinds.
type Counts struct {
	Previous              int `json:"previous"`
	Ambiguous             int `json:"ambiguous"`
	Current               int `json:"current"`
	CurrentReplayable     int `json:"current_replayable"`
	Future                int `json:"future"`
	NoActivity            int `json:"no_activity"`
	AccountsWithBoth      int `json:"accounts_with_previous_and_current"`
	PreviousActivities    int `json:"previous_deal_activities"`
	CurrentActivitiesPre  int `json:"current_deal_activities_before_cutoff"`
	CurrentActivitiesPost int `json:"current_deal_activities_from_cutoff"`
}

// DealQuiet is a deal that ended before the cutoff and how long it had been quiet on that date.
type DealQuiet struct {
	DealID    string `json:"deal_id"`
	Stage     string `json:"stage"`
	DaysQuiet int    `json:"days_quiet_at_cutoff"`
}

// CutoffTime parses the split's cutoff (a date, midnight UTC).
func (s Split) CutoffTime() (time.Time, error) {
	if s.Cutoff == "" {
		return time.Time{}, errors.New("crmarena: split has no cutoff")
	}
	return parseDate("split.cutoff", s.Cutoff)
}

// PreviousSet and CurrentSet index the deal lists.
func (s Split) PreviousSet() map[string]bool { return toSet(s.Previous) }

// CurrentSet indexes the current deals.
func (s Split) CurrentSet() map[string]bool { return toSet(s.Current) }

func toSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// sortedKeys lists map keys in order.
func sortedKeys(m map[string]Window) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
