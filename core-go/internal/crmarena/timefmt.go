package crmarena

import (
	"fmt"
	"time"
)

// Salesforce REST formats: datetimes like 2025-04-20T20:07:50.000+0000, dates like 2024-02-15.
const (
	sfDateTime = "2006-01-02T15:04:05.000-0700"
	sfDate     = "2006-01-02"
)

// parseDateTime parses a Salesforce datetime into UTC.
func parseDateTime(field, v string) (time.Time, error) {
	t, err := time.Parse(sfDateTime, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("crmarena: %s %q is not a Salesforce datetime", field, v)
	}
	return t.UTC(), nil
}

// parseDate parses a Salesforce date as midnight UTC: day-precision records sort before the timed
// records of the same day.
func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(sfDate, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("crmarena: %s %q is not a Salesforce date", field, v)
	}
	return t.UTC(), nil
}

// later returns the later of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
