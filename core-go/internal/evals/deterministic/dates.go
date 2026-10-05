package deterministic

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dateMention is an explicit calendar date in text, resolved against the evaluation time.
type dateMention struct {
	Day   time.Time // UTC midnight
	Start int
	End   int
	Text  string
}

const (
	monthAlt   = `jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?`
	weekdayAlt = `monday|tuesday|wednesday|thursday|friday|saturday|sunday`
	ordinal    = `(?:st|nd|rd|th)?`
)

var (
	isoDateRE    = regexp.MustCompile(`\b(20\d\d)-(\d\d)-(\d\d)\b`)
	monthDayRE   = regexp.MustCompile(`(?i)\b(?:(?:` + weekdayAlt + `),?\s+)?(` + monthAlt + `)\.?\s+(\d{1,2})` + ordinal + `\b(?:,?\s+(20\d\d)\b)?`)
	dayMonthRE   = regexp.MustCompile(`(?i)\b(\d{1,2})` + ordinal + `\s+(?:of\s+)?(` + monthAlt + `)\b(?:,?\s+(20\d\d)\b)?`)
	weekdayNthRE = regexp.MustCompile(`(?i)\b(` + weekdayAlt + `)\s+the\s+(\d{1,2})` + ordinal + `\b`)
	numericRE    = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/(\d{4}|\d{2}))?\b`)
)

var months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7,
	"aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}

var weekdays = map[string]time.Weekday{"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4,
	"friday": 5, "saturday": 6}

// findDates returns the explicit dates in text, earliest position first, without overlaps.
// A date without a year takes the year that puts it closest to now.
func findDates(text string, now time.Time) []dateMention {
	var all []dateMention
	all = append(all, scan(text, isoDateRE, func(m []string) (time.Time, bool) {
		return makeDate(atoi(m[1]), atoi(m[2]), atoi(m[3]))
	})...)
	all = append(all, scan(text, monthDayRE, func(m []string) (time.Time, bool) {
		return withYear(m[3], monthOf(m[1]), atoi(m[2]), now)
	})...)
	all = append(all, scan(text, dayMonthRE, func(m []string) (time.Time, bool) {
		return withYear(m[3], monthOf(m[2]), atoi(m[1]), now)
	})...)
	all = append(all, scan(text, weekdayNthRE, func(m []string) (time.Time, bool) {
		return weekdayNth(weekdays[strings.ToLower(m[1])], atoi(m[2]), now)
	})...)
	all = append(all, scan(text, numericRE, func(m []string) (time.Time, bool) {
		return withYear(fullYear(m[3]), time.Month(atoi(m[1])), atoi(m[2]), now)
	})...)
	return dropOverlaps(all)
}

func scan(text string, re *regexp.Regexp, resolve func([]string) (time.Time, bool)) []dateMention {
	var out []dateMention
	for _, idx := range re.FindAllStringSubmatchIndex(text, -1) {
		m := make([]string, len(idx)/2)
		for i := range m {
			if idx[2*i] >= 0 {
				m[i] = text[idx[2*i]:idx[2*i+1]]
			}
		}
		if d, ok := resolve(m); ok {
			out = append(out, dateMention{Day: d, Start: idx[0], End: idx[1], Text: m[0]})
		}
	}
	return out
}

func dropOverlaps(all []dateMention) []dateMention {
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start < all[j].Start
		}
		return all[i].End > all[j].End
	})
	var out []dateMention
	end := -1
	for _, d := range all {
		if d.Start >= end {
			out = append(out, d)
			end = d.End
		}
	}
	return out
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func monthOf(s string) time.Month {
	if len(s) < 3 {
		return 0
	}
	return months[strings.ToLower(s[:3])]
}

func fullYear(s string) string {
	if len(s) == 2 {
		return "20" + s
	}
	return s
}

func makeDate(y, m, d int) (time.Time, bool) {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d // rejects 31 February
}

func withYear(year string, m time.Month, d int, now time.Time) (time.Time, bool) {
	if year != "" {
		return makeDate(atoi(year), int(m), d)
	}
	var best time.Time
	found := false
	for y := now.Year() - 1; y <= now.Year()+1; y++ {
		if t, ok := makeDate(y, int(m), d); ok && (!found || closer(t, best, now)) {
			best, found = t, true
		}
	}
	return best, found
}

// weekdayNth resolves "Monday the 14th" to the closest month in which the 14th is a Monday.
func weekdayNth(wd time.Weekday, d int, now time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for off := -6; off <= 6; off++ {
		base := time.Date(now.Year(), now.Month()+time.Month(off), 1, 0, 0, 0, 0, time.UTC)
		t, ok := makeDate(base.Year(), int(base.Month()), d)
		if ok && t.Weekday() == wd && (!found || closer(t, best, now)) {
			best, found = t, true
		}
	}
	return best, found
}

func closer(a, b, now time.Time) bool {
	return absDur(a.Sub(civilDay(now))) < absDur(b.Sub(civilDay(now)))
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
