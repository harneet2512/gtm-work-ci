package strategystore

import (
	"slices"
	"strings"
)

// Edit is one literal change in the HumanDelta vocabulary (human_delta.literal_changes[]).
type Edit struct {
	Kind   string `json:"kind"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

// Draft is what a human may change in a candidate: who it goes to, and what it says.
type Draft struct {
	To, CC   []Recipient
	Artifact Artifact
}

// ComputeEdits is the structured diff of a final draft against the candidate's: recipients added, removed or
// moved between To and CC, subject, channel, attachments and body paragraphs. It is deterministic and returns
// an empty non-nil slice when nothing changed. Semantic labels are not decided here (HAR-97 does that).
func ComputeEdits(candidate, final Draft) []Edit {
	edits := []Edit{}
	edits = append(edits, recipientEdits(candidate, final)...)
	if subjectText(candidate.Artifact) != subjectText(final.Artifact) {
		edits = append(edits, Edit{Kind: "subject_changed", Before: subjectText(candidate.Artifact), After: subjectText(final.Artifact)})
	}
	if candidate.Artifact.Channel != final.Artifact.Channel {
		edits = append(edits, Edit{Kind: "channel_changed", Before: candidate.Artifact.Channel, After: final.Artifact.Channel})
	}
	edits = append(edits, paragraphEdits(candidate.Artifact.Body, final.Artifact.Body)...)
	for _, a := range diffStrings(final.Artifact.Attachments, candidate.Artifact.Attachments) {
		edits = append(edits, Edit{Kind: "attachment_added", After: a})
	}
	for _, a := range diffStrings(candidate.Artifact.Attachments, final.Artifact.Attachments) {
		edits = append(edits, Edit{Kind: "attachment_removed", Before: a})
	}
	return edits
}

func subjectText(a Artifact) string {
	if a.Subject == nil {
		return ""
	}
	return *a.Subject
}

func recipientEdits(candidate, final Draft) []Edit {
	before, after := roleByPerson(candidate), roleByPerson(final)
	var edits []Edit
	for _, id := range orderedPeople(final) {
		switch role, was := before[id]; {
		case !was:
			edits = append(edits, Edit{Kind: "recipient_added", After: map[string]string{"person_id": id, "role": after[id]}})
		case role != after[id]:
			edits = append(edits, Edit{Kind: "recipient_role_changed", Before: map[string]string{"person_id": id, "role": role}, After: map[string]string{"person_id": id, "role": after[id]}})
		}
	}
	for _, id := range orderedPeople(candidate) {
		if _, kept := after[id]; !kept {
			edits = append(edits, Edit{Kind: "recipient_removed", Before: map[string]string{"person_id": id, "role": before[id]}})
		}
	}
	return edits
}

func roleByPerson(d Draft) map[string]string {
	m := map[string]string{}
	for _, r := range d.To {
		m[r.PersonID] = "to"
	}
	for _, r := range d.CC {
		if _, already := m[r.PersonID]; !already {
			m[r.PersonID] = "cc"
		}
	}
	return m
}

func orderedPeople(d Draft) []string {
	var ids []string
	for _, r := range slices.Concat(d.To, d.CC) {
		if !slices.Contains(ids, r.PersonID) {
			ids = append(ids, r.PersonID)
		}
	}
	return ids
}

// paragraphEdits compares the bodies paragraph by paragraph (blank-line separated), by position.
func paragraphEdits(before, after string) []Edit {
	if before == after {
		return nil
	}
	b, a := paragraphs(before), paragraphs(after)
	var edits []Edit
	for i := 0; i < max(len(a), len(b)); i++ {
		switch {
		case i >= len(b):
			edits = append(edits, Edit{Kind: "paragraph_added", After: a[i]})
		case i >= len(a):
			edits = append(edits, Edit{Kind: "paragraph_removed", Before: b[i]})
		case a[i] != b[i]:
			edits = append(edits, Edit{Kind: "paragraph_edited", Before: b[i], After: a[i]})
		}
	}
	if len(edits) == 0 { // only whitespace between paragraphs differs
		edits = append(edits, Edit{Kind: "paragraph_edited"})
	}
	return edits
}

func paragraphs(body string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// diffStrings returns the elements of a that are not in b, in order.
func diffStrings(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}
