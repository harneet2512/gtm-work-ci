package normalize

import (
	"fmt"
	"testing"
)

func callJSON(endedAt, calendarID, transcript string) string {
	return fmt.Sprintf(`{"kind":"call","call_id":"C19","calendar_event_id":%s,
		"started_at":"2026-10-01T14:00:00Z","ended_at":%s,
		"speakers":[
			{"label":"speaker_01","email":"Dana@ghostvendor.com","name":"Dana Kim"},
			{"label":"speaker_02","email":null,"name":null},
			{"label":"speaker_03","email":"marco@acme.com"}
		]%s}`, calendarID, endedAt, transcript)
}

const transcriptJSON = `,"transcript":[
	{"speaker":"speaker_01","offset_s":0,"text":"Thanks for joining."},
	{"speaker":"speaker_02","offset_s":4.5,"text":"We need SOC2 before signing."}
]`

func TestNormalizeCall(t *testing.T) {
	const ended = "2026-10-01T14:44:30Z"
	speakers := []Participant{
		part("call:C19:speaker_01", "Dana Kim", "speaker"),
		part("dana@ghostvendor.com", "Dana Kim", "speaker"),
		part("call:C19:speaker_02", "", "speaker"),
		part("call:C19:speaker_03", "", "speaker"),
		part("marco@acme.com", "", "speaker"),
	}
	runCases(t, []normCase{
		{
			name: "ended without transcript -> CallEnded",
			ev:   event(t, "call", "C19", "ended", "", callJSON(`"`+ended+`"`, `"ev1"`, "")),
			want: want{
				typ: "CallEnded", occurred: ended, participants: speakers,
				accountHint: "ev1", accountKind: HintCalendar, moreHints: []Hint{dom("acme.com")},
				summary: "Call C19 ended",
			},
		},
		{
			name: "transcript -> TranscriptReady with one line per segment",
			ev:   event(t, "call", "C19", "transcript_ready", "2026-10-01T15:10:00Z", callJSON(`"`+ended+`"`, `"ev1"`, transcriptJSON)),
			want: want{
				typ: "TranscriptReady", occurred: ended, participants: speakers,
				accountHint: "ev1", accountKind: HintCalendar, moreHints: []Hint{dom("acme.com")},
				summary: "Transcript ready for call C19",
				body:    "speaker_01: Thanks for joining.\nspeaker_02: We need SOC2 before signing.",
			},
		},
		{
			name: "no calendar link falls back to the first external speaker email domain",
			ev:   event(t, "call", "C19", "ended", "", callJSON(`"`+ended+`"`, `null`, "")),
			want: want{typ: "CallEnded", occurred: ended, participants: speakers, accountHint: "acme.com", summary: "Call C19 ended"},
		},
	})
}

func TestNormalizeCallRejectsInvalidInput(t *testing.T) {
	const ended = `"2026-10-01T14:44:30Z"`
	runCases(t, []normCase{
		{name: "ended key but transcript present", ev: event(t, "call", "C19", "ended", "", callJSON(ended, `"ev1"`, transcriptJSON)), errCode: CodeInvalidEvent},
		{name: "ended key without ended_at", ev: event(t, "call", "C19", "ended", "", callJSON(`null`, `"ev1"`, "")), errCode: CodeInvalidEvent},
		{name: "transcript_ready without transcript", ev: event(t, "call", "C19", "transcript_ready", "", callJSON(ended, `"ev1"`, "")), errCode: CodeInvalidEvent},
		{name: "transcript_ready without ended_at", ev: event(t, "call", "C19", "transcript_ready", "", callJSON(`null`, `"ev1"`, transcriptJSON)), errCode: CodeInvalidEvent},
		{name: "started key is not mapped", ev: event(t, "call", "C19", "started", "", callJSON(ended, `"ev1"`, "")), errCode: CodeUnsupportedEvent},
		{name: "object id differs from call_id", ev: event(t, "call", "C20", "ended", "", callJSON(ended, `"ev1"`, "")), errCode: CodeInvalidEvent},
		{name: "speaker label violates the contract", ev: event(t, "call", "C19", "ended", "", `{"kind":"call","call_id":"C19","started_at":"2026-10-01T14:00:00Z","ended_at":"2026-10-01T14:44:30Z","speakers":[{"label":"Dana"}]}`), errCode: CodeInvalidEvent},
		{name: "bad speaker email", ev: event(t, "call", "C19", "ended", "", `{"kind":"call","call_id":"C19","started_at":"2026-10-01T14:00:00Z","ended_at":"2026-10-01T14:44:30Z","speakers":[{"label":"speaker_01","email":"nope"}]}`), errCode: CodeInvalidEvent},
		{name: "empty transcript text", ev: event(t, "call", "C19", "transcript_ready", "", `{"kind":"call","call_id":"C19","started_at":"2026-10-01T14:00:00Z","ended_at":"2026-10-01T14:44:30Z","speakers":[],"transcript":[{"speaker":"speaker_01","offset_s":0,"text":""}]}`), errCode: CodeInvalidEvent},
		{name: "wrong payload kind", ev: event(t, "call", "C19", "ended", "", `{"kind":"email"}`), errCode: CodeInvalidEvent},
	})
}
