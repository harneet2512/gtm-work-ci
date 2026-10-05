package proof

import (
	"fmt"
	"strings"
)

// RenderReport renders demo-report.md: the human view of the run with the HAR-129 section I
// confirmation matrix, the counts and the remaining-work list.
func RenderReport(m *Manifest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# HAR-129 proof report — %s\n\n", m.RunID)
	fmt.Fprintf(&b, "- Contract: %s\n", m.Contract)
	fmt.Fprintf(&b, "- Command: `%s`\n", m.Command)
	fmt.Fprintf(&b, "- Generated: %s\n", m.GeneratedAt)
	fmt.Fprintf(&b, "- Run status: **%s**\n", m.Status.Display())
	fmt.Fprintf(&b, "- Requirement source: `%s` (%s, %d rows, sha256 `%s`)\n\n",
		m.RequirementSource.Path, m.RequirementSource.Version, m.RequirementSource.RequirementsTotal, m.RequirementSource.SHA256)
	b.WriteString("A local unit test, a schema existing, a mock/fake-core run, a screenshot, hard-coded UI state or\n")
	b.WriteString("code inspection alone can never produce CONFIRMED (HAR-129 §I.4). A row is CONFIRMED only when a\n")
	b.WriteString("real integrated run observed the required behavior and this report links the machine evidence.\n\n")

	b.WriteString("## Summary\n\n")
	b.WriteString("| Status | Count |\n|---|---|\n")
	fmt.Fprintf(&b, "| CONFIRMED | %d |\n", m.Summary.Confirmed)
	fmt.Fprintf(&b, "| PARTIAL | %d |\n", m.Summary.Partial)
	fmt.Fprintf(&b, "| NOT CONFIRMED | %d |\n", m.Summary.NotConfirmed)
	fmt.Fprintf(&b, "| FAILED | %d |\n", m.Summary.Failed)
	fmt.Fprintf(&b, "| NOT RUN | %d |\n", m.Summary.NotRun)
	fmt.Fprintf(&b, "| **Total** | **%d** |\n\n", m.Summary.Total)

	b.WriteString("## Confirmation matrix\n\n")
	b.WriteString("| HAR-129 requirement | Required behavior | Observed live behavior | Evidence | Status | Gap |\n")
	b.WriteString("| -- | -- | -- | -- | -- | -- |\n")
	for _, r := range m.Matrix {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			cell(r.Requirement), cell(r.RequiredBehavior), cell(r.ObservedLiveBehavior),
			cell(evidence(r.Evidence)), r.Status.Display(), cell(r.Gap))
	}
	b.WriteString("\n")

	b.WriteString("## Remaining work\n\n")
	remaining := 0
	for _, r := range m.Matrix {
		if r.Status == StatusConfirmed {
			continue
		}
		remaining++
		fmt.Fprintf(&b, "- [%s] %s (%s) — %s\n", r.Status.Display(), r.RequirementID, r.Section, cell(r.Gap))
	}
	if remaining == 0 {
		b.WriteString("_None: every requirement is CONFIRMED._\n")
	}
	b.WriteString("\n")

	b.WriteString("## Artifacts\n\n")
	for _, a := range m.Artifacts {
		fmt.Fprintf(&b, "- `%s` — %s", a.Path, a.Status.Display())
		if !a.Present {
			b.WriteString(" (missing)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("## How to run\n\n")
	b.WriteString("```\nghostctl proof-har129\n```\n\n")
	b.WriteString("Run directory: `artifacts/har129/<run_id>/`.\n")
	return []byte(b.String())
}

// cell makes a value safe inside one Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}

func evidence(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	return strings.Join(refs, ", ")
}
