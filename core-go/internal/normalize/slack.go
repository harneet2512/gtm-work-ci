package normalize

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dealChannelPrefix marks the per-deal channels whose name identifies the account.
const dealChannelPrefix = "#deal-"

var slackTSPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)$`)

type slackPayload struct {
	Kind       string  `json:"kind"`
	Channel    string  `json:"channel"`
	TS         string  `json:"ts"`
	ThreadTS   *string `json:"thread_ts"`
	User       string  `json:"user"`
	UserEmail  *string `json:"user_email"`
	Text       string  `json:"text"`
	IsDecision bool    `json:"is_decision"`
}

// normalizeSlack implements the SlackMessage / SlackDecision rows.
func normalizeSlack(ev SourceEvent) (Activity, error) {
	if ev.SourceEventKey != "posted" {
		return Activity{}, unsupported("slack event key %q has no mapping (want posted)", ev.SourceEventKey)
	}
	p, err := decodePayload[slackPayload](ev, "slack_message")
	if err != nil {
		return Activity{}, err
	}
	if p.Channel == "" || strings.TrimSpace(p.User) == "" || strings.TrimSpace(p.Text) == "" {
		return Activity{}, invalid("slack message requires channel, user and non-empty text")
	}
	if err := requireObjectID(ev, p.Channel+":"+p.TS); err != nil {
		return Activity{}, err
	}
	at, err := parseSlackTS(p.TS)
	if err != nil {
		return Activity{}, err
	}
	if email := strings.TrimSpace(deref(p.UserEmail)); email != "" {
		if _, ok := normalizeEmailAddress(email); !ok {
			return Activity{}, invalid("user_email %q is not valid", email)
		}
	}

	activityType, summary := "SlackMessage", p.Channel
	if p.IsDecision {
		activityType, summary = "SlackDecision", "Decision in "+p.Channel
	}
	act := newActivity(ev, activityType, at)
	act.participants = []Participant{{RawIdentity: "slack:" + strings.TrimSpace(p.User), Role: RoleActor}}
	if strings.HasPrefix(p.Channel, dealChannelPrefix) && len(p.Channel) > len(dealChannelPrefix) {
		act.accountHints = hintOf(HintSlack, p.Channel)
	}
	act.bodyText = p.Text
	act.summary = summarize(withDetail(summary, p.Text))
	return act, nil
}

// parseSlackTS converts a Slack "seconds.fraction" timestamp to UTC time. Fraction digits
// beyond nanoseconds are dropped.
func parseSlackTS(ts string) (time.Time, error) {
	m := slackTSPattern.FindStringSubmatch(ts)
	if m == nil {
		return time.Time{}, invalid("slack ts %q must look like 1700000000.000100", ts)
	}
	sec, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return time.Time{}, invalid("slack ts %q seconds out of range", ts)
	}
	frac := (m[2] + "000000000")[:9]
	nanos, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return time.Time{}, invalid("slack ts %q fraction invalid", ts)
	}
	return time.Unix(sec, nanos).UTC(), nil
}
