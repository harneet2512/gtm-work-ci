package crmarena

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// WP32 (HAR-131), PR #19 review HIGH 1: a synthetic reply (rendered by the WP32 synthetic-layer renderer, pinned in
// fixtures/synthetic/reply_event.json) must be indistinguishable in FORMAT from a WP31 base email. Both go through
// the same normalisation; only the envelope's origin/provenance may differ.
var (
	emailIDShape  = regexp.MustCompile(`^02sWt[0-9A-Za-z]{13}$`)
	oppRefShape   = regexp.MustCompile(`^opp:006Wt[0-9A-Za-z]{13}$`)
	whole2ndShape = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)
)

func syntheticReply(t *testing.T) normalize.SourceEvent {
	t.Helper()
	raw, err := os.ReadFile(repoPath(t, "fixtures/synthetic/reply_event.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ev normalize.SourceEvent
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("strict decode of the synthetic reply: %v", err)
	}
	return ev
}

// baseReply is a WP31 inbound email that answers an earlier one, like every synthetic reply.
func baseReply(t *testing.T) normalize.SourceEvent {
	t.Helper()
	for _, e := range build(t, sample(t)).Events {
		if e.Source.SourceSystem != "email" {
			continue
		}
		p := payloadOf(t, e)
		if p["direction"] == "inbound" && p["in_reply_to"] != nil {
			return e.Source
		}
	}
	t.Fatal("sample has no inbound reply")
	return normalize.SourceEvent{}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func decodePayload(t *testing.T, ev normalize.SourceEvent) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSyntheticReplyHasExactlyTheBaseEmailFormat(t *testing.T) {
	syn, base := syntheticReply(t), baseReply(t)
	if syn.Origin != "synthetic" || syn.Provenance != "synthetic:v1" || base.Origin != "" {
		t.Fatalf("only the envelope marks the synthetic event: syn %q/%q base %q", syn.Origin, syn.Provenance, base.Origin)
	}
	for name, ev := range map[string]normalize.SourceEvent{"synthetic": syn, "base": base} {
		if !emailIDShape.MatchString(ev.SourceObjectID) || ev.SourceEventKey != "received" {
			t.Errorf("%s id/key shape: %q %q", name, ev.SourceObjectID, ev.SourceEventKey)
		}
		if ev.OccurredAt == nil || ev.OccurredAt.Nanosecond() != 0 || ev.OccurredAt.Location().String() != "UTC" {
			t.Errorf("%s occurred_at precision: %v", name, ev.OccurredAt)
		}
	}
	if syn.Connector != base.Connector || syn.ConnectorVersion != base.ConnectorVersion {
		t.Errorf("connector %s/%s vs base %s/%s", syn.Connector, syn.ConnectorVersion, base.Connector, base.ConnectorVersion)
	}
	sp, bp := decodePayload(t, syn), decodePayload(t, base)
	if a, b := keys(sp), keys(bp); !equalStrings(a, b) {
		t.Errorf("payload keys differ: synthetic %v base %v", a, b)
	}
	for _, p := range []map[string]any{sp, bp} {
		if !oppRefShape.MatchString(p["thread_id"].(string)) || p["thread_id"] != p["crm_opportunity_ref"] ||
			!emailIDShape.MatchString(p["in_reply_to"].(string)) || !whole2ndShape.MatchString(p["date"].(string)) {
			t.Errorf("payload format: thread %v ref %v reply-to %v date %v", p["thread_id"], p["crm_opportunity_ref"], p["in_reply_to"], p["date"])
		}
	}
	if a, b := keys(sp["from"].(map[string]any)), keys(bp["from"].(map[string]any)); !equalStrings(a, b) {
		t.Errorf("from keys differ: %v vs %v", a, b)
	}
	if a, b := keys(sp["to"].([]any)[0].(map[string]any)), keys(bp["to"].([]any)[0].(map[string]any)); !equalStrings(a, b) {
		t.Errorf("to keys differ: %v vs %v", a, b)
	}
	sa, errS := normalize.Normalize(syn)
	ba, errB := normalize.Normalize(base)
	if errS != nil || errB != nil {
		t.Fatalf("normalize: %v / %v", errS, errB)
	}
	if sa.Type() != ba.Type() || sa.Provenance().Connector != ba.Provenance().Connector || len(sa.Participants()) != len(ba.Participants()) {
		t.Errorf("activity differs: %s/%d vs %s/%d", sa.Type(), len(sa.Participants()), ba.Type(), len(ba.Participants()))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
