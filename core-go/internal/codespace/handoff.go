package codespace

import (
	"context"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Handoff errors, told apart so the control service can answer each with its own status.
var (
	// ErrNoNextCase: the case is the last one of the chronology.
	ErrNoNextCase = errors.New("codespace: there is no later episode to continue into")
	// ErrNotPlayed: Event N of the case has not been played, so the chronology cannot move on yet.
	ErrNotPlayed = errors.New("codespace: Event N has not been played in this case yet")
	// ErrUnknownManifest: no case was frozen under that manifest.
	ErrUnknownManifest = errors.New("codespace: no demo case was frozen under that manifest")
)

// Handoff is the case the demo moved on to.
type Handoff struct {
	Slot       string `json:"slot"`
	Label      string `json:"label"`
	ManifestID string `json:"manifest_id"`
	AccountID  string `json:"account_id"`
}

// Handoff is the hidden step behind the second Play. The audience presses Play on a case whose Event N is already
// released: the chronology continues with the next case's Event N (HAR-129 section F, "through the real replay
// chronology"). Activate does the whole switch: the knowledge the earlier case learned is carried into the next
// case's database, the graph is rebuilt from it and core is restarted on it. fromManifest names the case being left; the
// call is idempotent, so a Play retried after a lost response finds the next case already active.
func (o Ops) Handoff(ctx context.Context, fromManifest string) (Handoff, error) {
	from, next, err := o.successor(fromManifest)
	if err != nil {
		return Handoff{}, err
	}
	st, _, err := o.SeedState(next.Slot)
	if err != nil {
		return Handoff{}, err
	}
	result := Handoff{Slot: next.Slot, Label: caseLabel(next, st), ManifestID: st.ManifestID, AccountID: st.AccountID}
	active, err := ReadMarker(o.Paths.ActiveFile())
	if err != nil {
		return Handoff{}, err
	}
	if active == next.Slot {
		return result, nil
	}
	if err := o.requirePlayed(ctx, from, fromManifest); err != nil {
		return Handoff{}, err
	}
	o.say("continuing into %s", next.Label)
	if _, err := o.Activate(ctx, next.Slot); err != nil {
		return Handoff{}, err
	}
	return result, nil
}

// successor finds the case frozen under the manifest and the case that follows it.
func (o Ops) successor(manifestID string) (from, next Case, err error) {
	for i, c := range o.Cases {
		st, ok, serr := o.SeedState(c.Slot)
		if serr != nil {
			return Case{}, Case{}, serr
		}
		if !ok || st.ManifestID != manifestID {
			continue
		}
		if i+1 >= len(o.Cases) {
			return Case{}, Case{}, ErrNoNextCase
		}
		return c, o.Cases[i+1], nil
	}
	return Case{}, Case{}, ErrUnknownManifest
}

func (o Ops) requirePlayed(ctx context.Context, from Case, manifestID string) error {
	inv, err := o.P.Invisibility(ctx, manifestID)
	if err != nil {
		return fmt.Errorf("codespace: check that Event N of %s was played: %w", from.Label, err)
	}
	if inv.Status != "released" {
		return fmt.Errorf("%w (%s)", ErrNotPlayed, from.Label)
	}
	return nil
}

func caseLabel(c Case, st demorun.DemoState) string {
	if st.CaseName != "" {
		return st.CaseName
	}
	return c.Label
}
