package contracts

import "testing"

// TestEvalDisputeKnownBadAreRejected pins the EvalDispute invariants (HAR-97 E19): a dispute names the
// result and a reason, snapshots what it disputes, only a fail can have blocked, and an expected verdict
// differs from the disputed one (to dispute only the reasoning the expected verdict is null).
func TestEvalDisputeKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "eval_dispute")
	cases := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"a dispute names the result", func(m map[string]any) { delete(m, "eval_result_id") }},
		{"a dispute carries the human's reason", func(m map[string]any) { m["reason"] = "" }},
		{"the reason is bounded", func(m map[string]any) { m["reason"] = string(make([]byte, 2001)) }},
		{"the disputed verdict is snapshotted", func(m map[string]any) { delete(m, "disputed_verdict") }},
		{"not_relevant is never a disputed verdict (no result exists)", func(m map[string]any) { m["disputed_verdict"] = "not_relevant" }},
		{"only a fail can have blocked", func(m map[string]any) {
			m["disputed_verdict"] = "warn"
			m["expected_verdict"] = "pass"
		}},
		{"expected_verdict is always present (null when only the reasoning is disputed)", func(m map[string]any) { delete(m, "expected_verdict") }},
		{"an expected verdict differs from the disputed one", func(m map[string]any) { m["expected_verdict"] = "fail" }},
		{"an unknown expected verdict", func(m map[string]any) { m["expected_verdict"] = "maybe" }},
		{"an unknown surface", func(m map[string]any) { m["surface"] = "email" }},
		{"an actor label is required", func(m map[string]any) { m["actor_label"] = "" }},
		{"no extra fields", func(m map[string]any) { m["score"] = 0.4 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "eval_dispute", tc.fn)); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			}
		})
	}
}

// TestEvalDisputeLegalShapesAreAccepted: a reasoning-only dispute (expected null) and a false-pass
// dispute of a non-blocking pass are both valid.
func TestEvalDisputeLegalShapesAreAccepted(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "eval_dispute")
	for name, fn := range map[string]func(m map[string]any){
		"reasoning only": func(m map[string]any) { m["expected_verdict"] = nil },
		"false pass": func(m map[string]any) {
			m["disputed_verdict"], m["disputed_blocking"], m["expected_verdict"] = "pass", false, "fail"
		},
		"should not apply": func(m map[string]any) { m["expected_verdict"] = "not_relevant" },
		"no person id":     func(m map[string]any) { m["actor_person_id"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "eval_dispute", fn)); err != nil {
				t.Fatalf("rejected %s: %v", name, err)
			}
		})
	}
}
