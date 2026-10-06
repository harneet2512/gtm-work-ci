// Package bucket2run runs the Bucket 2 gates (HAR-97 D1-D10) over one real episode. It reads the stored
// strategy set, eval bundles, human decision, judgment inference, send-time evals, recompute derivation and
// recorded effects, calls the worker's model judges (one call per judgment, no live model in tests), builds
// every gate's results and stores them. A judge that fails leaves its gate unmeasured and the error is
// returned: a missing result shows "not measured", never a made-up pass.
//
// Model calls per episode (all through /v1/decision-judge, cached or replayed in tests):
//
//	1  rank_rationale (D3a)          1  intent_fit (D1)
//	1  set_quality (D2 set)          3  candidate_quality (D2, one per candidate)
//	1  ranking (D3)                  1  final_artifact (D8, only after a send)
//
// plus the judgment inference (D5) the send already requests: 8 new judge calls and 1 inference call.
package bucket2run
