package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/slack-go/slack"
)

// Exactly-once channel messages (HAR-136). Core's outbox delivers at least once; the guard that makes a post
// exactly-once lives in core, as create-only message refs (contracts/schemas/surface_message.v1.json), because the
// slackbot process can die between any two of its steps and must not need to remember anything. The flow, per
// (subject, kind):
//
//  1. Read the ref. A ts means the message exists: chat.update it (unless a human has acted on it), done.
//  2. No ref: reserve the slot (atomic in core), then post with Slack message metadata {subject_id, kind}, then
//     store the ts (written once), and only then does the relay acknowledge the event.
//  3. A reservation without a ts means an earlier attempt may or may not have posted. Look in conversations.history
//     since the reservation for a message with the same metadata and adopt it; post only if there is none.
//  4. Storing a ts that disagrees with the recorded one means another process posted first: our message is the
//     duplicate and is deleted.

// Kinds of channel message, the `kind` of surface_message.v1.json.
const (
	KindBI       = "bi"       // Message 1, keyed by the BusinessIntelligenceUpdate
	KindChooser  = "chooser"  // Message 2, keyed by the DecisionEpisode
	KindJudgment = "judgment" // Message 3, keyed by the DecisionEpisode
)

// RefSurface is this surface's name in surface_message.v1.json.
const RefSurface = "slack"

// MetaEventType is the Slack message metadata event_type of every message this bot posts.
const MetaEventType = "ghost_message"

// refLookback widens the history window before the reservation: core's clock and Slack's can differ, and the
// metadata match makes an older window harmless (a message with this subject and kind cannot predate its reservation).
const refLookback = time.Minute

// ErrSuperseded means the business-intelligence update an event names is no longer the account's latest: its
// message is not posted (the surface only renders the latest update) and nothing waits for it.
var ErrSuperseded = errors.New("slacksurface: the business-intelligence update was superseded by a later one")

// MessageMeta identifies what a channel message is about. It rides in the message's Slack metadata.
type MessageMeta struct{ SubjectID, Kind string }

func (m MessageMeta) slack() slack.SlackMetadata {
	return slack.SlackMetadata{EventType: MetaEventType, EventPayload: map[string]any{"subject_id": m.SubjectID, "kind": m.Kind}}
}

// matches reports whether a message's metadata is this one's.
func (m MessageMeta) matches(md slack.SlackMetadata) bool {
	if md.EventType != MetaEventType {
		return false
	}
	subject, _ := md.EventPayload["subject_id"].(string)
	kind, _ := md.EventPayload["kind"].(string)
	return subject == m.SubjectID && kind == m.Kind
}

// RefRecord is surface_message.v1.json. TS is empty until the post is recorded.
type RefRecord struct {
	SubjectID  string
	Kind       string
	Channel    string
	TS         string
	ReservedAt time.Time
}

// RefTSConflictError is a RecordRefTS of a ts different from the recorded one: another process posted first.
type RefTSConflictError struct{ Existing RefRecord }

func (e *RefTSConflictError) Error() string {
	return "slacksurface: a different ts is already recorded"
}

// RefStore is core's message refs (GET /surface-messages/..., its reservation and its ts); *CoreHTTP implements it.
// A Core that is also a RefStore makes the Publisher exactly-once; one that is not (the fixture core of --dry-run)
// keeps the in-process guard.
type RefStore interface {
	// GetRef returns ErrNotFound when the slot was never reserved.
	GetRef(ctx context.Context, subjectID, kind string) (RefRecord, error)
	// ReserveRef creates the slot (created true: the caller owns the post) or returns the existing one unchanged.
	ReserveRef(ctx context.Context, subjectID, kind, channel string) (ref RefRecord, created bool, err error)
	// RecordRefTS stores the ts once; a different ts is a *RefTSConflictError carrying the recorded one.
	RecordRefTS(ctx context.Context, subjectID, kind, ts string) (RefRecord, error)
}

// slackTS formats a time as the `oldest` of conversations.history.
func slackTS(t time.Time) string {
	return fmt.Sprintf("%s.%06d", strconv.FormatInt(t.Unix(), 10), t.Nanosecond()/1000)
}
