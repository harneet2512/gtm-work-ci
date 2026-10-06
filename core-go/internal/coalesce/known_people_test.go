package coalesce_test

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

// The known-people block is part of the extraction prompt, which keys the recorded model answers. Two people with the same
// name must be listed in the same order whatever random ids the database gave them, or a rebuilt history looks up other
// recordings (the "history event differs" flake).
func TestKnownPeopleOrderIgnoresTheRandomIDs(t *testing.T) {
	w := seedBasic(t)
	for i := 0; i < 8; i++ { // each insert gets a new random id, in an order that is not the email order
		for _, e := range []string{"sam.b", "sam.a", "sam.c"} {
			scalar(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Sam Same', $1, $2::uuid) RETURNING id::text`,
				fmt.Sprintf("%s%d@acme.com", e, i), w.account)
		}
	}
	known, err := coalesce.LoadKnownPeople(context.Background(), env.DB, w.account)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range known {
		if k.DisplayName == "Sam Same" {
			got = append(got, k.RawIdentity)
		}
	}
	if len(got) != 24 || !sort.StringsAreSorted(got) {
		t.Fatalf("same-name people must be ordered by email, not by random id: %v", got)
	}
}

// The worker tells our side from the buyer's with known_people[].internal (worker.yaml); without it
// the extractor reads a buyer's "on our side" as the seller and files the champion as owner.
func TestKnownPeopleMarkTheBuyerSideAndIncludeParticipatingEmployees(t *testing.T) {
	w := seedBasic(t)
	scalar(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Leo Park', 'leo@ghostvendor.com') RETURNING id::text`)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0, "Thanks Dana."))

	known, err := coalesce.LoadKnownPeople(context.Background(), env.DB, w.account)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]claims.KnownPerson{}
	for _, k := range known {
		byID[k.PersonID] = k
	}
	if p, ok := byID[w.priya]; !ok || p.Internal == nil || *p.Internal {
		t.Errorf("Priya (contact) = %+v, want internal=false", p)
	}
	if d, ok := byID[w.dana]; !ok || d.Internal == nil || !*d.Internal {
		t.Errorf("Dana (employee on the thread) = %+v, want internal=true", d)
	}
	if len(known) != 2 {
		t.Errorf("known people = %d, want 2 (an employee who never took part in this account stays out)", len(known))
	}
}
