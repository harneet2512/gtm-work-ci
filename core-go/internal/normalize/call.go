package normalize

import (
	"regexp"
	"strings"
	"time"
)

var speakerLabel = regexp.MustCompile(`^speaker_[0-9]{2}$`)

type callSpeaker struct {
	Label string  `json:"label"`
	Email *string `json:"email"`
	Name  *string `json:"name"`
}

type callSegment struct {
	Speaker string  `json:"speaker"`
	OffsetS float64 `json:"offset_s"`
	Text    string  `json:"text"`
}

type callPayload struct {
	Kind            string        `json:"kind"`
	CallID          string        `json:"call_id"`
	CalendarEventID *string       `json:"calendar_event_id"`
	StartedAt       time.Time     `json:"started_at"`
	EndedAt         *time.Time    `json:"ended_at"`
	Speakers        []callSpeaker `json:"speakers"`
	Transcript      []callSegment `json:"transcript"`
}

// normalizeCall implements the two call rows: "ended" (no transcript) and "transcript_ready".
func normalizeCall(ev SourceEvent) (Activity, error) {
	key := ev.SourceEventKey
	if key != "ended" && key != "transcript_ready" {
		return Activity{}, unsupported("call event key %q has no mapping (want ended or transcript_ready)", key)
	}
	p, err := decodePayload[callPayload](ev, "call")
	if err != nil {
		return Activity{}, err
	}
	if err := requireObjectID(ev, p.CallID); err != nil {
		return Activity{}, err
	}
	if p.StartedAt.IsZero() || p.EndedAt == nil || p.EndedAt.IsZero() {
		return Activity{}, invalid("call %s needs started_at and ended_at for event key %q", p.CallID, key)
	}
	if err := validateTranscript(key, p); err != nil {
		return Activity{}, err
	}
	participants, err := callParticipants(p)
	if err != nil {
		return Activity{}, err
	}

	activityType, summary := "CallEnded", "Call "+p.CallID+" ended"
	if key == "transcript_ready" {
		activityType, summary = "TranscriptReady", "Transcript ready for call "+p.CallID
	}
	act := newActivity(ev, activityType, *p.EndedAt)
	act.participants = participants
	act.accountHints = callAccountHints(p)
	act.summary = summarize(summary)
	act.bodyText = transcriptText(p.Transcript)
	return act, nil
}

func validateTranscript(key string, p callPayload) error {
	if key == "ended" && len(p.Transcript) > 0 {
		return invalid("event key \"ended\" must not carry a transcript; use transcript_ready")
	}
	if key == "transcript_ready" && len(p.Transcript) == 0 {
		return invalid("event key \"transcript_ready\" requires a non-empty transcript")
	}
	for _, seg := range p.Transcript {
		if !speakerLabel.MatchString(seg.Speaker) || seg.OffsetS < 0 || strings.TrimSpace(seg.Text) == "" {
			return invalid("transcript segment for %q is malformed", seg.Speaker)
		}
	}
	return nil
}

// callParticipants emits each speaker as call:<call_id>:<label> plus their email when known.
func callParticipants(p callPayload) ([]Participant, error) {
	out := make([]Participant, 0, len(p.Speakers)*2)
	for _, s := range p.Speakers {
		if !speakerLabel.MatchString(s.Label) {
			return nil, invalid("speaker label %q must match speaker_NN", s.Label)
		}
		name := strings.TrimSpace(deref(s.Name))
		out = addParticipant(out, Participant{RawIdentity: "call:" + p.CallID + ":" + s.Label, DisplayName: name, Role: RoleSpeaker})
		if email := strings.TrimSpace(deref(s.Email)); email != "" {
			norm, ok := normalizeEmailAddress(email)
			if !ok {
				return nil, invalid("speaker %s email %q is not valid", s.Label, email)
			}
			out = addParticipant(out, Participant{RawIdentity: norm, DisplayName: name, Role: RoleSpeaker})
		}
	}
	return out, nil
}

// callAccountHints: the calendar link first (resolution follows it, with corroboration), then
// the first external speaker whose email the recorder matched.
func callAccountHints(p callPayload) []Hint {
	emails := make([]string, 0, len(p.Speakers))
	for _, s := range p.Speakers {
		emails = append(emails, deref(s.Email))
	}
	hints := hintOf(HintCalendar, deref(p.CalendarEventID))
	return append(hints, domainHint(firstExternalDomain(emails...))...)
}

func transcriptText(segments []callSegment) string {
	lines := make([]string, len(segments))
	for i, seg := range segments {
		lines[i] = seg.Speaker + ": " + seg.Text
	}
	return strings.Join(lines, "\n")
}
