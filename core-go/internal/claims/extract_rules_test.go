package claims

import (
	"context"
	"errors"
	"testing"
)

// A model failure must cost only the AI claims: the deterministic claims of the same activity survive.
func TestPipelineKeepsRuleClaimsWhenOnlyTheModelCallFails(t *testing.T) {
	act := crmActivity("CRMNoteAdded", `{"object_type":"Opportunity","fields":{"StageName":{"new":"Technical evaluation"}}}`)
	act.Body = "Stage moved after the security call."
	boom := errors.New("worker 503")

	out, err := pipeline(&scriptedExtractor{err: boom}, nil).Run(context.Background(), act, nil, resolver(act))
	if !errors.Is(err, boom) {
		t.Fatalf("the model failure must still be reported: %v", err)
	}
	if len(out.Claims) != 1 || out.Claims[0].FieldPath != FieldStage || out.Claims[0].Standing != CRMExplicit {
		t.Fatalf("rule claims lost on a model failure: %+v", out.Claims)
	}

	cache := &memCache{m: map[string]ExtractResponse{}, getErr: errors.New("cache read failed")}
	out, err = pipeline(&scriptedExtractor{resp: goodResponse()}, cache).Run(context.Background(), act, nil, resolver(act))
	if err == nil || len(out.Claims) != 1 {
		t.Fatalf("a cache failure also keeps the rule claims: %+v %v", out.Claims, err)
	}
}
