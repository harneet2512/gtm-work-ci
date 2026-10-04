package crmarena

import (
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func TestAsKnownRemovesOnlyFieldsDatedAfterTheEventItself(t *testing.T) {
	at := time.Date(2023, 5, 10, 9, 0, 0, 0, time.UTC)
	e := Event{DealID: "D", Source: normalize.SourceEvent{OccurredAt: &at, Payload: []byte(
		`{"object_type":"Quote","Name":"q","CreatedDate":"2023-05-10T09:00:00.000+0000","ExpirationDate":"2023-06-10","Earlier":"2023-05-01"}`)}}
	got, n, err := e.AsKnown()
	if err != nil {
		t.Fatal(err)
	}
	p := string(got.Source.Payload)
	if n != 1 || strings.Contains(p, "ExpirationDate") {
		t.Fatalf("removed %d, payload %s: the future expiry must go", n, p)
	}
	if !strings.Contains(p, "CreatedDate") || !strings.Contains(p, "Earlier") {
		t.Fatalf("a field dated at or before the event must stay: %s", p)
	}
	if !strings.Contains(string(e.Source.Payload), "ExpirationDate") {
		t.Fatal("the input event was modified")
	}
}

func TestReplayDealIsTheDealOrTheDealThatIntroducedTheContact(t *testing.T) {
	if got := (Event{DealID: "A"}).ReplayDeal(); got != "A" {
		t.Fatalf("got %q", got)
	}
	if got := (Event{introducedBy: "B"}).ReplayDeal(); got != "B" {
		t.Fatalf("got %q", got)
	}
	if got := (Event{}).ReplayDeal(); got != "" {
		t.Fatalf("got %q", got)
	}
}
