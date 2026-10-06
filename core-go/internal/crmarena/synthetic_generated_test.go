package crmarena

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// WP32 (HAR-131) step 2: one generated event of every rendered kind (pinned by worker-py
// tests/test_synthetic_generate.py in fixtures/synthetic/generated_kinds.json) decodes strictly, normalises, and
// has the payload keys and actor of the WP31 base event of the same kind. Only the envelope marks it.
func generatedKinds(t *testing.T) []normalize.SourceEvent {
	t.Helper()
	raw, err := os.ReadFile(repoPath(t, "fixtures/synthetic/generated_kinds.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out []normalize.SourceEvent
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("strict decode of the generated kinds: %v", err)
	}
	return out
}

// kindOf is "<system>|<object type or direction>|<key head>", e.g. crm|Contact|created, email|inbound|received.
func kindOf(t *testing.T, ev normalize.SourceEvent) string {
	p := decodePayload(t, ev)
	side, _ := p["object_type"].(string)
	if side == "" {
		side, _ = p["direction"].(string)
	}
	return ev.SourceSystem + "|" + side + "|" + strings.SplitN(ev.SourceEventKey, ":", 3)[0] + fieldOf(ev.SourceEventKey)
}

func fieldOf(key string) string {
	if parts := strings.SplitN(key, ":", 3); len(parts) == 3 {
		return ":" + parts[1]
	}
	return ""
}

func TestGeneratedEventsHaveTheBaseFormatOfTheirKind(t *testing.T) {
	base := map[string]normalize.SourceEvent{}
	for _, e := range build(t, sample(t)).Events {
		if k := kindOf(t, e.Source); base[k].SourceSystem == "" {
			base[k] = e.Source
		}
	}
	gen := generatedKinds(t)
	if len(gen) < 5 {
		t.Fatalf("expected every rendered kind, got %d", len(gen))
	}
	for _, syn := range gen {
		k := kindOf(t, syn)
		if syn.Origin != "synthetic" || syn.Provenance != "synthetic:v1" {
			t.Errorf("%s: envelope marker %q/%q", k, syn.Origin, syn.Provenance)
		}
		if syn.OccurredAt == nil || syn.OccurredAt.Nanosecond() != 0 {
			t.Errorf("%s: occurred_at precision %v", k, syn.OccurredAt)
		}
		if _, err := normalize.Normalize(syn); err != nil {
			t.Errorf("%s: normalize: %v", k, err)
		}
		ref, ok := base[k]
		if !ok {
			t.Logf("%s: no WP31 base event of this kind in the sample (synthetic-only kind)", k)
			continue
		}
		sp, bp := decodePayload(t, syn), decodePayload(t, ref)
		delete(sp, "cc") // a colleague in cc is the only optional email key
		delete(bp, "cc")
		if syn.SourceSystem == "crm" {
			delete(sp, "description")
			delete(bp, "description")
			if sp["changed_by"] != bp["changed_by"] {
				t.Errorf("%s: actor %v, base %v", k, sp["changed_by"], bp["changed_by"])
			}
		}
		if a, b := keys(sp), keys(bp); !equalStrings(a, b) {
			t.Errorf("%s: payload keys %v, base %v", k, a, b)
		}
		if syn.Connector != ref.Connector || syn.ConnectorVersion != ref.ConnectorVersion {
			t.Errorf("%s: connector %s/%s", k, syn.Connector, syn.ConnectorVersion)
		}
	}
}
