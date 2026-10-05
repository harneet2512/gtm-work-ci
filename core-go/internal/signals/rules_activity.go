package signals

import (
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// SilenceAfter is how long without customer activity before the clock says the customer went quiet
// (the seed world's ghost.clock customer_silence tick fires after 7 silent days).
const SilenceAfter = 7 * 24 * time.Hour

var inboundTypes = map[string]bool{"EmailReceived": true, "EmailReply": true, "CustomerReplied": true}

func activityRef(a ActivityFact) []reducer.EvidenceRef {
	return []reducer.EvidenceRef{{ActivityID: a.ID, OccurredAt: a.OccurredAt.UTC()}}
}

// customerReplied: the customer wrote to us (one signal per inbound activity).
func customerReplied(in Input) []Signal {
	var out []Signal
	for _, a := range in.Activities {
		if inboundTypes[a.Type] && a.FromCustomer {
			out = append(out, Signal{Type: "customer_replied", Key: a.ID, PerActivity: true, OccurredAt: a.OccurredAt, EvidenceRefs: activityRef(a)})
		}
	}
	return out
}

// meetingAccepted: an attendee accepted a meeting invitation.
func meetingAccepted(in Input) []Signal {
	var out []Signal
	for _, a := range in.Activities {
		if a.Type == "MeetingAccepted" {
			out = append(out, Signal{Type: "meeting_accepted", Key: a.ID, PerActivity: true, OccurredAt: a.OccurredAt, EvidenceRefs: activityRef(a)})
		}
	}
	return out
}

// customerSilence: a ghost.clock customer_silence tick was ingested (CustomerWentSilent). The key is the
// silence episode, so it converges with Tick's signal for the same last customer interaction.
func customerSilence(in Input) []Signal {
	var out []Signal
	for _, a := range in.Activities {
		if a.Type != "CustomerWentSilent" {
			continue
		}
		s := Signal{Type: "customer_went_silent", Key: a.ID, OccurredAt: a.OccurredAt, EvidenceRefs: activityRef(a)}
		if last, ok := scalar(in.Next.Fields.LastCustomerInteraction); ok {
			s.DedupeKey = silenceKey(in.Next.AccountID, last)
		} else { // no known last interaction to name the episode: key on the tick itself
			s.PerActivity = true
		}
		out = append(out, s)
	}
	return out
}

func silenceKey(accountID, lastCustomerInteraction string) string {
	return fmt.Sprintf("clock:%s:customer_silence:%s", accountID, lastCustomerInteraction)
}

// fieldContradicted: a newer lower-standing claim contradicts a field's winner (ADR-0008). The recompute
// reports every standing conflict each time, so the key is the contradicting claim, not the diff: the
// signal persists once and the store ignores the repeats.
func fieldContradicted(in Input) []Signal {
	var out []Signal
	for _, c := range in.Conflicts {
		ref := reducer.EvidenceRef{ActivityID: c.Contradicting.SourceActivityID, ClaimID: c.Contradicting.ID,
			Quote: c.Contradicting.EvidenceQuote, SpeakerPersonID: c.Contradicting.SpeakerPersonID, OccurredAt: c.Contradicting.OccurredAt.UTC()}
		out = append(out, Signal{
			Type: "field_contradicted", SubjectClaimID: c.Winner.ID, OccurredAt: c.Contradicting.OccurredAt,
			EvidenceRefs: []reducer.EvidenceRef{ref}, Key: c.Contradicting.ID,
			DedupeKey: fmt.Sprintf("contradiction:%s:%s:%s", in.Next.AccountID, c.Field, c.Contradicting.ID),
			Details: map[string]any{
				"field": string(c.Field), "winning_claim_id": c.Winner.ID, "winning_standing": string(c.Winner.Standing),
				"contradicting_claim_id": c.Contradicting.ID, "contradicting_standing": string(c.Contradicting.Standing),
				"reason": c.Reason,
			},
		})
	}
	return out
}

// Tick returns the time-driven signals for a state at now (the ghost.clock rules that need no activity).
// Today: customer_went_silent once last_customer_interaction is SilenceAfter old. The signal is timed at
// the moment the silence threshold was crossed, so re-running Tick later yields the same signal and
// the same idempotency key. A state with no known last customer interaction emits nothing.
func Tick(st reducer.AccountState, now time.Time) []Signal {
	text, ok := scalar(st.Fields.LastCustomerInteraction)
	if !ok {
		return nil
	}
	last, err := time.Parse(time.RFC3339, strings.TrimSpace(text))
	if err != nil {
		return nil
	}
	crossed := last.Add(SilenceAfter)
	if now.Before(crossed) {
		return nil
	}
	s := Signal{Type: "customer_went_silent", Rule: "sig.customer_silence@1", Key: text, OccurredAt: crossed,
		EvidenceRefs: st.Fields.LastCustomerInteraction.EvidenceRefs, DedupeKey: silenceKey(st.AccountID, text),
		Details: map[string]any{"last_customer_activity_at": text, "silent_days": int(now.Sub(last).Hours() / 24)}}
	return []Signal{finalize(Input{Next: st}, s)}
}
