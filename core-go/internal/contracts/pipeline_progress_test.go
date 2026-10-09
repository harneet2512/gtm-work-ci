package contracts

import "testing"

// stageOf returns the stage object of the decoded progress example.
func stageOf(m map[string]any, id string) map[string]any {
	for _, s := range m["stages"].([]any) {
		if st := s.(map[string]any); st["stage"] == id {
			return st
		}
	}
	panic("no stage " + id)
}

// TestPipelineProgressKnownBadAreRejected pins the honesty rules of the live Play pipeline (HAR-145): a
// waiting stage carries nothing, a running stage has not ended, a transport error is never a verdict, and
// the judging stage fails only as a judgment.
func TestPipelineProgressKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "pipeline_progress")
	const evalID = "0e1a0000-0000-4000-8000-000000000911"
	cases := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"every stage is reported (seven)", func(m map[string]any) { m["stages"] = m["stages"].([]any)[:6] }},
		{"an unknown stage", func(m map[string]any) { stageOf(m, "ingest")["stage"] = "deploy" }},
		{"an unknown status", func(m map[string]any) { stageOf(m, "ingest")["status"] = "done" }},
		{"a waiting stage has no start time", func(m map[string]any) { stageOf(m, "evals")["started_at"] = "2026-10-04T15:00:00Z" }},
		{"a waiting stage has attempt 0", func(m map[string]any) { stageOf(m, "evals")["attempt"] = 1 }},
		{"a waiting stage has no failure kind", func(m map[string]any) { stageOf(m, "evals")["failure_kind"] = "internal" }},
		{"a running stage has not ended", func(m map[string]any) { stageOf(m, "decide")["ended_at"] = "2026-10-04T15:00:04Z" }},
		{"a running stage has started", func(m map[string]any) { stageOf(m, "decide")["started_at"] = nil }},
		{"a completed stage has ended", func(m map[string]any) { stageOf(m, "ingest")["ended_at"] = nil }},
		{"a failed stage says why", func(m map[string]any) {
			s := stageOf(m, "graph")
			s["status"], s["failure_kind"] = "failed", nil
		}},
		{"a transport error is never passed", func(m map[string]any) { stageOf(m, "graph")["failure_kind"] = "transport" }},
		{"a transport error while judging is unknown, never failed", func(m map[string]any) {
			s := stageOf(m, "evals")
			s["status"], s["attempt"], s["started_at"], s["ended_at"] = "failed", 1, "2026-10-04T15:00:05Z", "2026-10-04T15:00:06Z"
			s["failure_kind"], s["eval_result_ids"] = "transport", []any{evalID}
		}},
		{"evals fails only as a judgment: it needs the failing results", func(m map[string]any) {
			s := stageOf(m, "evals")
			s["status"], s["attempt"], s["started_at"], s["ended_at"] = "failed", 1, "2026-10-04T15:00:05Z", "2026-10-04T15:00:06Z"
		}},
		{"an error while judging cannot be passed", func(m map[string]any) {
			s := stageOf(m, "evals")
			s["status"], s["attempt"], s["started_at"], s["ended_at"] = "passed", 1, "2026-10-04T15:00:05Z", "2026-10-04T15:00:06Z"
			s["failure_kind"] = "internal"
		}},
		{"passed is an eval verdict: a non-eval stage is completed", func(m map[string]any) { stageOf(m, "ingest")["status"] = "passed" }},
		{"warning is an eval verdict: a non-eval stage is completed", func(m map[string]any) { stageOf(m, "ingest")["status"] = "warning" }},
		{"the judging stage is never merely completed", func(m map[string]any) {
			s := stageOf(m, "evals")
			s["status"], s["attempt"], s["started_at"], s["ended_at"] = "completed", 1, "2026-10-04T15:00:05Z", "2026-10-04T15:00:06Z"
		}},
		{"a ref id is a uuid", func(m map[string]any) { stageOf(m, "ingest")["refs"].(map[string]any)["activity_id"] = "not-a-uuid" }},
		{"no unknown refs", func(m map[string]any) { stageOf(m, "ingest")["refs"].(map[string]any)["invented"] = "x" }},
		{"overall is a known state", func(m map[string]any) { m["overall"] = "done" }},
		{"a stage carries no extra fields", func(m map[string]any) { stageOf(m, "ingest")["eta_ms"] = 10 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "pipeline_progress", tc.fn)); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			}
		})
	}
}

// TestPipelineProgressLegalShapesAreAccepted: the honest failure shapes are valid.
func TestPipelineProgressLegalShapesAreAccepted(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "pipeline_progress")
	const evalID = "0e1a0000-0000-4000-8000-000000000911"
	finish := func(s map[string]any, status string) {
		s["status"], s["attempt"], s["started_at"], s["ended_at"], s["duration_ms"] = status, 1, "2026-10-04T15:00:05Z", "2026-10-04T15:00:06Z", 1000
		s["seq"], s["updated_at"] = 9, "2026-10-04T15:00:06Z"
	}
	for name, fn := range map[string]func(m map[string]any){
		"the graph projection timed out": func(m map[string]any) {
			s := stageOf(m, "graph")
			s["failure_kind"], s["status"] = "transport", "failed"
		},
		"a transport error while judging is unknown": func(m map[string]any) {
			s := stageOf(m, "evals")
			finish(s, "unknown")
			s["failure_kind"] = "transport"
		},
		"judged and failed: results name the failure": func(m map[string]any) {
			s := stageOf(m, "evals")
			finish(s, "failed")
			s["eval_result_ids"] = []any{evalID}
		},
		"evals passed is a verdict": func(m map[string]any) {
			s := stageOf(m, "evals")
			finish(s, "passed")
			s["eval_result_ids"] = []any{evalID}
		},
		"evals warned": func(m map[string]any) {
			s := stageOf(m, "evals")
			finish(s, "warning")
			s["eval_result_ids"] = []any{evalID}
		},
		"a stage that never heard back is unknown with a transport kind": func(m map[string]any) {
			s := stageOf(m, "decide")
			s["status"], s["failure_kind"], s["detail"] = "unknown", "transport", "no heartbeat: the decide stage has been running too long"
		},
		"a non-material event skips the decision": func(m map[string]any) {
			s := stageOf(m, "decide")
			finish(s, "skipped")
			s["detail"] = "no_material_change"
		},
		"a run-scope document with no manifest": func(m map[string]any) {
			m["scope"], m["manifest_id"], m["run_id"] = "run", nil, "0f0a0000-0000-4000-8000-000000000601"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "pipeline_progress", fn)); err != nil {
				t.Fatalf("rejected %s: %v", name, err)
			}
		})
	}
}

func invEntry(m map[string]any) map[string]any { return m["entries"].([]any)[0].(map[string]any) }

// TestDependencyInvalidationKnownBadAreRejected pins E11: no edit means an empty invalidation set, an
// edit is a declared field and class, and references carry a reason.
func TestDependencyInvalidationKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "dependency_invalidation")
	cases := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"unedited has no entries", func(m map[string]any) { m["status"], m["edited"] = "unedited", false }},
		{"unedited is not edited", func(m map[string]any) { m["status"], m["entries"] = "unedited", []any{} }},
		{"not decided has no entries", func(m map[string]any) { m["status"], m["edited"] = "not_decided", false }},
		{"a recomputed run has an entry", func(m map[string]any) { m["entries"] = []any{} }},
		{"a recomputed run is edited", func(m map[string]any) { m["edited"] = false }},
		{"before the send nothing is recomputed: no version after", func(m map[string]any) { m["status"] = "edits_pending" }},
		{"an unknown status", func(m map[string]any) { m["status"] = "done" }},
		{"an edit names a declared field", func(m map[string]any) { invEntry(m)["edit"].(map[string]any)["field"] = "tone" }},
		{"an edit has a class", func(m map[string]any) { delete(invEntry(m)["edit"].(map[string]any), "semantic_class") }},
		{"an unknown edit kind", func(m map[string]any) { invEntry(m)["edit"].(map[string]any)["kind"] = "rewritten" }},
		{"a reference carries a reason", func(m map[string]any) {
			invEntry(m)["invalidated"].([]any)[0].(map[string]any)["reason"] = ""
		}},
		{"an unknown reference kind", func(m map[string]any) {
			invEntry(m)["invalidated"].([]any)[0].(map[string]any)["kind"] = "claim"
		}},
		{"an entry lists what was not recomputed", func(m map[string]any) { delete(invEntry(m), "not_recomputed") }},
		{"the account state is reported", func(m map[string]any) { delete(m, "account_state") }},
		{"no extra fields", func(m map[string]any) { m["score"] = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "dependency_invalidation", tc.fn)); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			}
		})
	}
}

// TestDependencyInvalidationLegalShapesAreAccepted: no edit is a valid, empty set; so is a discarded edit.
func TestDependencyInvalidationLegalShapesAreAccepted(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "dependency_invalidation")
	for name, fn := range map[string]func(m map[string]any){
		"unedited": func(m map[string]any) {
			m["status"], m["edited"], m["entries"], m["human_delta_id"], m["semantic_labels"] = "unedited", false, []any{}, nil, []any{}
		},
		"discarded with saved edits": func(m map[string]any) { m["status"], m["entries"] = "discarded", []any{} },
		"edits pending": func(m map[string]any) {
			m["status"], m["human_delta_id"] = "edits_pending", nil
			m["account_state"].(map[string]any)["version_after"], m["account_state"].(map[string]any)["preserved"] = nil, nil
			invEntry(m)["recomputed"] = []any{}
		},
		"recomputation unavailable": func(m map[string]any) { m["status"] = "recomputation_unavailable" },
		"a subject edit with a null before": func(m map[string]any) {
			e := invEntry(m)["edit"].(map[string]any)
			e["field"], e["kind"], e["before"], e["after"], e["semantic_class"] = "subject", "subject_changed", nil, "New subject", "content_change"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "dependency_invalidation", fn)); err != nil {
				t.Fatalf("rejected %s: %v", name, err)
			}
		})
	}
}

func TestDependencyInvalidationStatePreservedIsNullableAndNeverGuessedBeforeASend(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "dependency_invalidation")
	state := func(m map[string]any) map[string]any { return m["account_state"].(map[string]any) }
	for name, fn := range map[string]func(m map[string]any){
		"proven preserved":   func(m map[string]any) { state(m)["preserved"] = true },
		"proven moved":       func(m map[string]any) { state(m)["version_after"], state(m)["preserved"] = 9, false },
		"unknown after send": func(m map[string]any) { state(m)["preserved"] = nil },
		"unknown before the send": func(m map[string]any) {
			m["status"], m["human_delta_id"] = "edits_pending", nil
			state(m)["version_after"], state(m)["preserved"] = nil, nil
			invEntry(m)["recomputed"] = []any{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "dependency_invalidation", fn)); err != nil {
				t.Fatalf("rejected %s: %v", name, err)
			}
		})
	}
	t.Run("a state no send read cannot be called preserved", func(t *testing.T) {
		bad := mutate(t, root, "dependency_invalidation", func(m map[string]any) {
			m["status"], m["human_delta_id"] = "edits_pending", nil
			state(m)["version_after"], state(m)["preserved"] = nil, true
			invEntry(m)["recomputed"] = []any{}
		})
		if err := schema.Validate(bad); err == nil {
			t.Fatal("accepted preserved=true with no version read by a send")
		}
	})
	t.Run("the old status name is gone", func(t *testing.T) {
		if err := schema.Validate(mutate(t, root, "dependency_invalidation", func(m map[string]any) { m["status"] = "recomputed" })); err == nil {
			t.Fatal("accepted the retired status recomputed: it overstated what was re-derived")
		}
	})
}

// The example must say what the code says: an eval re-run at send time names the old result it replaces.
func TestDependencyInvalidationExampleNamesWhatEachReRunReplaces(t *testing.T) {
	root := contractsDir(t)
	m := mutate(t, root, "dependency_invalidation", func(map[string]any) {}).(map[string]any)
	e := invEntry(m)
	old := map[string]string{}
	for _, r := range e["invalidated"].([]any) {
		if r := r.(map[string]any); r["kind"] == "eval_result" {
			old[r["label"].(string)] = r["ref_id"].(string)
		}
	}
	for _, r := range e["recomputed"].([]any) {
		r := r.(map[string]any)
		if want, ok := old[r["label"].(string)]; r["kind"] == "eval_result" && ok && r["replaces"] != want {
			t.Errorf("%v replaces %v, want the invalidated result %s", r["label"], r["replaces"], want)
		}
	}
}
