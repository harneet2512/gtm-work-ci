package claims

import (
	"errors"
	"fmt"
	"testing"
)

type classifiedErr struct{ permanent bool }

func (e classifiedErr) Error() string   { return "classified" }
func (e classifiedErr) Permanent() bool { return e.permanent }

func TestIsPermanentClassifiesErrors(t *testing.T) {
	root := errors.New("bad payload")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error is retryable", root, false},
		{"permanent wrapper", Permanent(root), true},
		{"wrapped permanent", fmt.Errorf("activity x: %w", Permanent(root)), true},
		{"classified permanent", classifiedErr{true}, true},
		{"classified retryable", classifiedErr{false}, false},
		{"wrapped classified", fmt.Errorf("w: %w", classifiedErr{true}), true},
	}
	for _, tt := range tests {
		if got := IsPermanent(tt.err); got != tt.want {
			t.Errorf("%s: IsPermanent = %v, want %v", tt.name, got, tt.want)
		}
	}
	if Permanent(nil) != nil {
		t.Error("Permanent(nil) must stay nil")
	}
	if !errors.Is(Permanent(root), root) || Permanent(root).Error() != "bad payload" {
		t.Error("the wrapper must expose its cause")
	}
}

func TestMalformedStructuredPayloadsAreClassifiedPermanent(t *testing.T) {
	for _, src := range []string{"crm", "calendar", "enrichment"} {
		act := ActivityInput{ID: actIDOne, SourceSystem: src, Payload: []byte(`{not json`)}
		_, err := (RuleExtractor{}).Extract(nil, act) //nolint:staticcheck // context unused by the rules
		if !IsPermanent(err) {
			t.Errorf("%s: err = %v, want a permanent error", src, err)
		}
	}
}
