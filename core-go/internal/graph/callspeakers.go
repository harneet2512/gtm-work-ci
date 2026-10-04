package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/speakers"
)

type callPayload struct {
	Kind            string  `json:"kind"`
	CallID          string  `json:"call_id"`
	CalendarEventID *string `json:"calendar_event_id"`
	Speakers        []struct {
		Label string  `json:"label"`
		Email *string `json:"email"`
	} `json:"speakers"`
	Transcript []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"transcript"`
}

func decodeCall(ev normalize.SourceEvent) (callPayload, bool, error) {
	if ev.SourceSystem != "call" {
		return callPayload{}, false, nil
	}
	var c callPayload
	if err := json.Unmarshal(ev.Payload, &c); err != nil {
		return callPayload{}, false, fmt.Errorf("graph: decode call payload of %s: %w", ev.SourceObjectID, err)
	}
	if c.Kind != "call" {
		return callPayload{}, false, fmt.Errorf("graph: call event %s has payload kind %q", ev.SourceObjectID, c.Kind)
	}
	return c, true, nil
}

// resolveCallSpeakers maps every call:<id>:<label> identity of a call event.
//
// A speaker the recorder matched to an email maps to that email's person (exact). An
// unlabelled speaker is matched by the speakers package against the linked calendar event's
// attendees (rule, 0.8, with the matched cue stored as evidence) and left unresolved unless
// exactly one attendee fits. Evidence from an email overrides an earlier rule mapping by
// remap, keeping the history. Self-introductions only ever map unlabelled speakers.
func (f *finalizer) resolveCallSpeakers(ctx context.Context) error {
	c, ok, err := decodeCall(f.in.Event)
	if err != nil || !ok {
		return err
	}
	known := map[string]bool{} // people already identified as speakers of this call
	var unlabelled []string
	for _, s := range c.Speakers {
		mail := strings.ToLower(strings.TrimSpace(derefString(s.Email)))
		if mail == "" {
			unlabelled = append(unlabelled, s.Label)
			continue
		}
		person, err := f.personForEmail(ctx, mail)
		if err != nil {
			return err
		}
		if person == "" {
			continue
		}
		known[person] = true
		ev := evidenceJSON(map[string]string{"rule": "recorder_email", "email": mail})
		if err := f.mapSpeaker(ctx, c.CallID, s.Label, person, MethodExact, recorderConfidence, ev); err != nil {
			return err
		}
	}
	return f.matchUnlabelled(ctx, c, unlabelled, known)
}

// personForEmail returns the person an email maps to now (the participants step has already
// created contacts where the rules allow it).
func (f *finalizer) personForEmail(ctx context.Context, mail string) (string, error) {
	m, ok, err := CurrentMapping(ctx, f.tx, EmailKey(mail))
	if err != nil || !ok || m.EntityType != EntityPerson {
		return "", err
	}
	return m.EntityID, nil
}

func (f *finalizer) matchUnlabelled(ctx context.Context, c callPayload, unlabelled []string, known map[string]bool) error {
	var open []string
	for _, label := range unlabelled {
		m, ok, err := CurrentMapping(ctx, f.tx, SourceKey{System: "call", Key: c.CallID + ":" + label})
		if err != nil {
			return err
		}
		if ok {
			known[m.EntityID] = true
			continue
		}
		open = append(open, label)
	}
	if len(open) == 0 || len(c.Transcript) == 0 || derefString(c.CalendarEventID) == "" {
		return nil
	}
	cands, err := f.calendarCandidates(ctx, derefString(c.CalendarEventID), known)
	if err != nil || len(cands) == 0 {
		return err
	}
	segs := make([]speakers.Segment, len(c.Transcript))
	for i, s := range c.Transcript {
		segs[i] = speakers.Segment{Label: s.Speaker, Text: s.Text}
	}
	for _, m := range speakers.Resolve(speakers.Input{Unlabelled: open, Candidates: cands, Segments: segs}) {
		ev := evidenceJSON(map[string]string{"rule": "calendar_attendee_cue", "cue": m.Cue.Kind, "pattern": m.Cue.Pattern, "span": m.Cue.Span})
		if err := f.mapSpeaker(ctx, c.CallID, m.Label, m.PersonID, MethodRule, speakerConfidence, ev); err != nil {
			return err
		}
	}
	return nil
}

// calendarCandidates are the people who organized or attended the call's calendar event, except
// those already identified on the call. A contact qualifies only if it belongs to the call's
// account: a display name taken from an email header at some other domain must not be able to
// claim a speaker.
func (f *finalizer) calendarCandidates(ctx context.Context, eventID string, known map[string]bool) ([]speakers.Candidate, error) {
	rows, err := f.tx.QueryContext(ctx, `
SELECT DISTINCT p.id::text, p.display_name
  FROM activities a
  JOIN activity_participants ap ON ap.activity_id = a.id AND ap.role IN ('attendee', 'organizer') AND ap.person_id IS NOT NULL
  JOIN people p ON p.id = ap.person_id
 WHERE a.source_system = 'calendar' AND a.source_object_id = $1
   AND (p.kind = 'employee' OR p.account_id = $2::uuid)
 ORDER BY p.id::text`, eventID, optional(f.in.Resolution.AccountID))
	if err != nil {
		return nil, fmt.Errorf("graph: calendar attendees: %w", err)
	}
	defer rows.Close()
	var out []speakers.Candidate
	for rows.Next() {
		var cand speakers.Candidate
		if err := rows.Scan(&cand.PersonID, &cand.Name); err != nil {
			return nil, fmt.Errorf("graph: scan calendar attendee: %w", err)
		}
		if !known[cand.PersonID] {
			out = append(out, cand)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read calendar attendees: %w", err)
	}
	return out, nil
}

// mapSpeaker creates the call-label mapping or, when it points elsewhere and the new evidence
// is stronger than a rule, remaps it.
func (f *finalizer) mapSpeaker(ctx context.Context, callID, label, personID, method string, conf float64, evidence json.RawMessage) error {
	k := SourceKey{System: "call", Key: callID + ":" + label}
	cur, ok, err := CurrentMapping(ctx, f.tx, k)
	if err != nil {
		return err
	}
	switch {
	case !ok:
		return f.newMapping(ctx, Mapping{EntityType: EntityPerson, EntityID: personID, SourceKey: k, Method: method,
			Confidence: conf, Evidence: evidence})
	case cur.EntityID == personID || method == MethodRule:
		return nil // already mapped; a rule guess never replaces an existing mapping
	}
	moved, err := Remap(ctx, f.tx, k, personID, f.at,
		&MappingOverride{Method: method, Confidence: &conf, EvidenceActivityID: f.in.ActivityID, Evidence: evidence})
	if err != nil {
		return err
	}
	return f.repoint(ctx, k, personID, moved.Confidence)
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
