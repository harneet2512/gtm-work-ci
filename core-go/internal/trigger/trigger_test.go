package trigger_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

var now = time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC)

func material() statediff.Diff {
	return statediff.Diff{IsMaterial: true, Changes: []statediff.Change{{Field: "blockers", Material: true}}}
}

func sig(types ...string) []signals.Signal {
	var out []signals.Signal
	for _, t := range types {
		out = append(out, signals.Signal{Type: t})
	}
	return out
}

func base() trigger.Input {
	return trigger.Input{AccountID: "acct", Diff: material(), ChampionKnown: true, Now: now}
}

func TestEveryOutcomeAndItsReason(t *testing.T) {
	recent := now.Add(-time.Minute)
	cases := []struct {
		name string
		mut  func(*trigger.Input)
		want []string
	}{
		{"unresolved account", func(i *trigger.Input) { i.AccountID = "" }, []string{trigger.AccountUnresolved}},
		{"no material change", func(i *trigger.Input) { i.Diff = statediff.Diff{} }, []string{trigger.NoMaterialChange}},
		{"permission denied", func(i *trigger.Input) { i.PermissionDenied = true; i.Signals = sig("customer_replied") }, []string{trigger.PermissionDenied}},
		{"open run", func(i *trigger.Input) { i.OpenRun = true; i.Signals = sig("customer_replied") }, []string{trigger.OpenRunExists}},
		{"no relevant signal", func(i *trigger.Input) { i.Signals = sig("stage_advanced", "meeting_accepted") }, []string{trigger.NoRelevantSignal}},
		{"rep already replied", func(i *trigger.Input) {
			i.Signals = sig("customer_replied")
			i.Activities = []signals.ActivityFact{{ID: "1", Type: "EmailReceived", OccurredAt: now.Add(-time.Hour), FromCustomer: true},
				{ID: "2", Type: "EmailSent", OccurredAt: now.Add(-time.Minute), FromRep: true}}
		}, []string{trigger.RepAlreadyReplied}},
		{"cooldown", func(i *trigger.Input) { i.Signals = sig("customer_replied"); i.LastEligibleAt = &recent }, []string{trigger.Cooldown}},
		{"champion unknown", func(i *trigger.Input) { i.ChampionKnown = false; i.Signals = sig("new_stakeholder_entered") }, []string{trigger.ChampionUnknown}},
		{"customer replied", func(i *trigger.Input) { i.Signals = sig("customer_replied") }, []string{trigger.EligibleCustomerReplied}},
		{"customer replied without a champion", func(i *trigger.Input) { i.ChampionKnown = false; i.Signals = sig("customer_replied") }, []string{trigger.EligibleCustomerReplied}},
		{"meeting completed", func(i *trigger.Input) { i.Activities = []signals.ActivityFact{{ID: "m", Type: "TranscriptReady"}} }, []string{trigger.EligibleMeetingCompleted}},
		{"stakeholder and reply", func(i *trigger.Input) { i.Signals = sig("customer_replied", "new_stakeholder_entered") },
			[]string{trigger.EligibleCustomerReplied, trigger.EligibleStakeholderChange}},
		{"blocker change", func(i *trigger.Input) { i.Signals = sig("security_blocker_appeared") }, []string{trigger.EligibleBlockerChange}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mut(&in)
			got := trigger.Evaluate(in)
			if !reflect.DeepEqual(got.ReasonCodes, c.want) || got.Eligible != strings.HasPrefix(c.want[0], "eligible_") || got.Explanation == "" || got.Workflow != trigger.Workflow {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestBuyingGroupChangeIsAStakeholderChangeWithoutASignal(t *testing.T) {
	in := base()
	in.Diff = statediff.Diff{IsMaterial: true, Changes: []statediff.Change{{Field: "buying_group", Op: statediff.OpChanged, Material: true}}}
	if got := trigger.Evaluate(in); !got.Eligible || got.ReasonCodes[0] != trigger.EligibleStakeholderChange {
		t.Fatalf("got %+v", got)
	}
	in.Diff.Changes[0].Op = statediff.OpSet // the first state's group is a load, not a change
	if got := trigger.Evaluate(in); got.Eligible {
		t.Fatalf("an initial buying group must not trigger: %+v", got)
	}
}

func TestCooldownEndsAndCustomCooldownApplies(t *testing.T) {
	old := now.Add(-time.Hour)
	in := base()
	in.Signals = sig("customer_replied")
	in.LastEligibleAt = &old
	if !trigger.Evaluate(in).Eligible {
		t.Fatal("an hour is past the default cooldown")
	}
	in.Cooldown = 2 * time.Hour
	if got := trigger.Evaluate(in); got.Eligible || got.ReasonCodes[0] != trigger.Cooldown {
		t.Fatalf("got %+v", got)
	}
}

func TestPrecedenceNoMaterialChangeBeatsEverything(t *testing.T) {
	in := base()
	in.Diff = statediff.Diff{}
	in.OpenRun, in.PermissionDenied = true, true
	in.Signals = sig("customer_replied")
	if got := trigger.Evaluate(in); got.ReasonCodes[0] != trigger.NoMaterialChange {
		t.Fatalf("got %+v", got)
	}
}

// The reason vocabulary is the contract's (parity with trigger_evaluation.v1.json).
func TestReasonCodesMatchTheContract(t *testing.T) {
	dir, _ := os.Getwd()
	var raw []byte
	for {
		b, err := os.ReadFile(filepath.Join(dir, "contracts", "schemas", "trigger_evaluation.v1.json"))
		if err == nil {
			raw = b
			break
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("contract not found")
		}
		dir = filepath.Dir(dir)
	}
	var c struct {
		Properties struct {
			Reasons struct {
				Items struct{ Enum []string } `json:"items"`
			} `json:"reason_codes"`
			Workflow struct{ Enum []string } `json:"workflow"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	got := []string{trigger.EligibleCustomerReplied, trigger.EligibleMeetingCompleted, trigger.EligibleStakeholderChange,
		trigger.EligibleBlockerChange, trigger.NoMaterialChange, trigger.NoRelevantSignal, trigger.OpenRunExists,
		trigger.RepAlreadyReplied, trigger.Cooldown, trigger.ChampionUnknown, trigger.PermissionDenied, trigger.AccountUnresolved}
	want := append([]string(nil), c.Properties.Reasons.Items.Enum...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) || c.Properties.Workflow.Enum[0] != trigger.Workflow {
		t.Fatalf("code drifted from the contract:\n got %v\nwant %v", got, want)
	}
}
