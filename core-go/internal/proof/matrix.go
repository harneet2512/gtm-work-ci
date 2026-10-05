package proof

import "fmt"

// notRunObserved is the honest observation for a row no integrated run has tested.
const notRunObserved = "Not run: no integrated execution has tested this requirement yet."

// BuildMatrix makes one NOT RUN row per requirement in the source of truth. It never invents
// observed behavior: a row can only change when a real integrated run supplies evidence.
func BuildMatrix(reqs *RequirementsFile) []MatrixRow {
	rows := make([]MatrixRow, 0, len(reqs.Requirements))
	for _, r := range reqs.Requirements {
		section, _ := reqs.SectionByID(r.Section)
		rows = append(rows, MatrixRow{
			RequirementID:        r.ID,
			Section:              section.Reference,
			Requirement:          r.Requirement,
			RequiredBehavior:     r.RequiredBehavior,
			ObservedLiveBehavior: notRunObserved,
			Evidence:             []string{},
			Status:               StatusNotRun,
			Gap:                  fmt.Sprintf("No integrated execution has exercised %s yet.", r.Reference),
		})
	}
	return rows
}

// Summarize counts rows per status.
func Summarize(rows []MatrixRow) Summary {
	s := Summary{Total: len(rows)}
	for _, r := range rows {
		switch r.Status {
		case StatusConfirmed:
			s.Confirmed++
		case StatusPartial:
			s.Partial++
		case StatusNotConfirmed:
			s.NotConfirmed++
		case StatusFailed:
			s.Failed++
		default:
			s.NotRun++
		}
	}
	return s
}

// overall is the run status: the strongest blocker wins; all-confirmed is confirmed.
func overall(s Summary) Status {
	switch {
	case s.Total == 0:
		return StatusNotRun
	case s.Confirmed == s.Total:
		return StatusConfirmed
	case s.Failed > 0:
		return StatusFailed
	case s.NotConfirmed > 0:
		return StatusNotConfirmed
	case s.Partial > 0:
		return StatusPartial
	default:
		return StatusNotRun
	}
}
