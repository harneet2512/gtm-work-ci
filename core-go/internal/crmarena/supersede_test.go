package crmarena

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func crmEvent(deal, objType, key string, phase int) Event {
	return Event{DealID: deal, phase: phase, Source: normalize.SourceEvent{SourceSystem: "crm", SourceEventKey: key,
		Payload: []byte(`{"kind":"crm_change","object_type":"` + objType + `"}`)}}
}

func TestRealTerminalDealsReadsContractsAndFinalQuoteStatus(t *testing.T) {
	events := []Event{
		crmEvent("d-contract", "Contract", "created", phaseActivity),
		crmEvent("d-accepted", "Quote", "field:Status:Accepted", phaseSnapshot),
		crmEvent("d-rejected", "Quote", "field:Status:Rejected", phaseSnapshot),
		crmEvent("d-denied", "Quote", "field:Status:Denied", phaseSnapshot),
		crmEvent("d-both", "Quote", "field:Status:Denied", phaseSnapshot),
		crmEvent("d-both", "Contract", "created", phaseActivity),
		crmEvent("d-open", "Quote", "field:Status:Presented", phaseSnapshot),
	}
	got := RealTerminalDeals(events)
	want := map[string]string{"d-contract": RealWon, "d-accepted": RealWon, "d-rejected": RealLost, "d-denied": RealLost, "d-both": RealWon}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for d, o := range want {
		if got[d] != o {
			t.Errorf("%s = %q, want %q", d, got[d], o)
		}
	}
}

func TestSyntheticPathSupersedesOnlyTheSnapshotStageOfCoveredDeals(t *testing.T) {
	res := build(t, sample(t))
	real := RealTerminalDeals(res.Events)
	covered := map[string]bool{}
	for _, e := range res.Events {
		if isSnapshotStage(e) && real[e.DealID] == "" {
			covered[e.DealID] = true
		}
	}
	if len(covered) == 0 {
		t.Fatal("sample has no deal to cover")
	}
	out, err := SupersedeSnapshotStage(res.Events, covered)
	if err != nil {
		t.Fatal(err)
	}
	dropped := 0
	j := 0
	for _, e := range res.Events {
		if covered[e.DealID] && isSnapshotStage(e) {
			dropped++
			continue
		}
		if out[j].Source.SourceObjectID != e.Source.SourceObjectID || out[j].Source.SourceEventKey != e.Source.SourceEventKey {
			t.Fatalf("event %d out of order or changed", j)
		}
		j++
	}
	if dropped != len(covered) || len(out) != len(res.Events)-dropped {
		t.Fatalf("dropped %d of %d covered deals; %d events left of %d", dropped, len(covered), len(out), len(res.Events))
	}
	for _, e := range out {
		if covered[e.DealID] && isSnapshotStage(e) {
			t.Fatalf("snapshot stage of covered deal %s survived", e.DealID)
		}
	}
}

func TestADealWithAVisibleRealOutcomeIsNeverCovered(t *testing.T) {
	events := []Event{crmEvent("d-won", "Quote", "field:Status:Accepted", phaseSnapshot),
		crmEvent("d-won", "Opportunity", "field:StageName:Closed", phaseSnapshot)}
	_, err := SupersedeSnapshotStage(events, map[string]bool{"d-won": true})
	if err == nil || !strings.Contains(err.Error(), "visible real outcome") {
		t.Fatalf("covering a real-terminal deal must fail, got %v", err)
	}
	kept, err := SupersedeSnapshotStage(events, map[string]bool{})
	if err != nil || len(kept) != 2 {
		t.Fatalf("uncovered deals keep their snapshot: %v %d", err, len(kept))
	}
}
