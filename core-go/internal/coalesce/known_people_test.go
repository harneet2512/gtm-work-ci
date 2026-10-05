package coalesce_test

import (
	"context"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

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
