package crmarena

import (
	"errors"
	"time"
)

// ReplayableMin is how many activities a current deal needs on each side of the cutoff to be worth
// replaying: state before the cutoff to start from, and changes after it to replay.
const ReplayableMin = 2

// roleDefinitions says how deals are classified at a cutoff.
const roleDefinitions = "previous = ended before the cutoff (last event of any kind: email, task, quote, contract signature) and quiet for at least " +
	"previous_min_quiet_days on the cutoff date; ambiguous = ended before the cutoff but quiet for fewer days (never current); " +
	"current = started before it and active at or after it; replayable = current with >= 2 activities on each side"

// DefaultPreviousMinQuietDays is the default quiet period a deal needs at the cutoff to count as previous.
const DefaultPreviousMinQuietDays = 60

// CutoffRule documents how ChooseCutoff picks the cutoff.
const CutoffRule = "first day of the month that maximizes min(previous deals, replayable current deals), " +
	"ties to the earlier month; " + roleDefinitions

// Candidate is one cutoff considered, with its counts (the justification table).
type Candidate struct {
	Cutoff string `json:"cutoff"`
	Counts Counts `json:"counts"`
}

// Score is what ChooseCutoff maximizes.
func (c Candidate) Score() int { return min(c.Counts.Previous, c.Counts.CurrentReplayable) }

// CountAt classifies every deal at the cutoff.
func CountAt(windows map[string]Window, cutoff time.Time, minQuietDays int) (counts Counts, previous, ambiguous, current []string) {
	prevAccounts, curAccounts := map[string]bool{}, map[string]bool{}
	for _, id := range sortedKeys(windows) {
		w := windows[id]
		switch w.RoleAtQuiet(cutoff, minQuietDays) {
		case RoleAmbiguous:
			counts.Ambiguous++
			ambiguous = append(ambiguous, id)
		case RolePrevious:
			counts.Previous++
			counts.PreviousActivities += w.Activities
			prevAccounts[w.AccountID] = true
			previous = append(previous, id)
		case RoleCurrent:
			before := w.Before(cutoff)
			counts.Current++
			counts.CurrentActivitiesPre += before
			counts.CurrentActivitiesPost += w.Activities - before
			if before >= ReplayableMin && w.Activities-before >= ReplayableMin {
				counts.CurrentReplayable++
			}
			curAccounts[w.AccountID] = true
			current = append(current, id)
		case RoleFuture:
			counts.Future++
		default:
			counts.NoActivity++
		}
	}
	for a := range prevAccounts {
		if curAccounts[a] {
			counts.AccountsWithBoth++
		}
	}
	return counts, previous, ambiguous, current
}

// Candidates lists the first of every month from the first deal start to the last activity.
func Candidates(windows map[string]Window, minQuietDays int) ([]Candidate, error) {
	var first, last time.Time
	for _, w := range windows {
		if !w.HasActivity() {
			continue
		}
		if first.IsZero() || w.Start.Before(first) {
			first = w.Start
		}
		last = later(last, w.Last)
	}
	if first.IsZero() {
		return nil, errors.New("crmarena: no deal has any activity")
	}
	var out []Candidate
	for m := time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0); !m.After(last); m = m.AddDate(0, 1, 0) {
		counts, _, _, _ := CountAt(windows, m, minQuietDays)
		out = append(out, Candidate{Cutoff: m.Format(sfDate), Counts: counts})
	}
	return out, nil
}

// ChooseCutoff applies CutoffRule.
func ChooseCutoff(candidates []Candidate) (Candidate, error) {
	if len(candidates) == 0 {
		return Candidate{}, errors.New("crmarena: no cutoff candidates")
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.Score() > best.Score() {
			best = c
		}
	}
	if best.Score() == 0 {
		return Candidate{}, errors.New("crmarena: no cutoff leaves both previous and replayable current deals")
	}
	return best, nil
}
