package contracts

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileDef compiles one $defs entry of a schema so an artifact payload can be validated on its own.
func compileDef(t *testing.T, c *jsonschema.Compiler, schema, def string) *jsonschema.Schema {
	t.Helper()
	s, err := c.Compile("https://ghost.local/contracts/" + schema + ".v1.json#/$defs/" + def)
	if err != nil {
		t.Fatalf("compile %s#/$defs/%s: %v", schema, def, err)
	}
	return s
}

// TestEpisodeArtifactPayloadsValidate checks the three payloads the proof harness reads (HAR-129 §H:
// episode_timeline, state_snapshots, graph_diffs) against the episode_replay contract, so a real run's
// emitted artifacts are schema-checked before they can evidence a §B/§C row.
func TestEpisodeArtifactPayloadsValidate(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	payloads := []struct {
		def  string
		json string
	}{
		{"episodeTimeline", `{
			"manifest_id":"11111111-1111-4111-8111-111111111111",
			"account_id":"22222222-2222-4222-8222-222222222222",
			"total":2,"released":1,
			"episodes":[{"position":1,"event_id":"44444444-4444-4444-8444-444444444444",
				"occurred_at":"2026-09-29T11:00:00Z","source_system":"email","provenance_origin":"dataset",
				"provenance":"crmarena-pro:b2b","released":true,"held_out":false,"material":false,
				"account_change_id":null,"decision_episode_id":null,"state_version":1,"graph_diff_id":null,
				"no_action_reason":"no_material_change","coalesced":false}],
			"generated_at":"2026-10-04T05:00:00Z"}`},
		{"stateSnapshots", `{
			"manifest_id":"11111111-1111-4111-8111-111111111111",
			"account_id":"22222222-2222-4222-8222-222222222222",
			"snapshots":[{"position":1,"event_id":"44444444-4444-4444-8444-444444444444","version":1,
				"as_of":"2026-09-29T11:00:00Z",
				"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
			"generated_at":"2026-10-04T05:00:00Z"}`},
		{"graphDiffs", `{
			"manifest_id":"11111111-1111-4111-8111-111111111111",
			"account_id":"22222222-2222-4222-8222-222222222222",
			"diffs":[{"position":1,"event_id":"44444444-4444-4444-8444-444444444444",
				"graph_diff_id":42,"job_id":7,
				"summary":{"added":2,"changed":0,"removed":0,"repaired":0},
				"changes":[{"kind":"node","op":"added","type":"Person","id":"p-1"}]}],
			"generated_at":"2026-10-04T05:00:00Z"}`},
	}
	for _, p := range payloads {
		t.Run(p.def, func(t *testing.T) {
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(p.json))
			if err != nil {
				t.Fatal(err)
			}
			if err := compileDef(t, c, "episode_replay", p.def).Validate(doc); err != nil {
				t.Fatalf("%s payload invalid: %v", p.def, err)
			}
		})
	}
}

// TestEpisodeReplayKnownBadAreRejected pins the leakage-critical constraints: an episode cannot be negative,
// an event cannot carry an unknown source system, and a released event must state its held-out flag.
func TestEpisodeReplayKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	c := compiler(t, root)
	cases := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"episode k cannot be negative", func(m map[string]any) { m["episode"] = -1 }},
		{"window is none, historical or live", func(m map[string]any) { m["window"] = "future" }},
		{"an event names a known source system", func(m map[string]any) {
			m["prior_episodes"].([]any)[0].(map[string]any)["source_system"] = "telepathy"
		}},
		{"a state snapshot carries its digest", func(m map[string]any) {
			delete(m["state"].(map[string]any), "digest")
		}},
		{"provenance origin is dataset, synthetic or live", func(m map[string]any) {
			m["prior_episodes"].([]any)[0].(map[string]any)["provenance_origin"] = "future"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schemaFor(t, c, "episode_replay").Validate(mutate(t, root, "episode_replay", tc.fn)); err == nil {
				t.Fatal("expected the episode replay view to be rejected")
			}
		})
	}
}
