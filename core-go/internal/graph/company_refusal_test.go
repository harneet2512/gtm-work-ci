package graph_test

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

// A current email mapping that points at a contact (or at a non-person entity) must stop the
// seed: it must not attach a Slack identity or a reports_to edge to it.
func TestSeedCompanyRefusesAnEmailMappedToAContactOrANonPerson(t *testing.T) {
	for name, entityType := range map[string]graph.EntityType{"contact": graph.EntityPerson, "account": graph.EntityAccount} {
		t.Run(name, func(t *testing.T) {
			resetDB(t)
			target := newPerson(t, "contact", "Impostor", "")
			if entityType == graph.EntityAccount {
				target = newAccount(t, "Acme", "acme.com")
			}
			if _, err := insertMapping(t, graph.Mapping{EntityType: entityType, EntityID: target,
				SourceKey: graph.SourceKey{System: "email", Key: "dana@vendor.example"}, Confidence: 1, Method: graph.MethodRule, ValidFrom: t0}); err != nil {
				t.Fatal(err)
			}

			_, err := graph.SeedCompany(ctx, env.DB, smallOrg(), t0)

			if err == nil || !strings.Contains(err.Error(), "not an employee") {
				t.Fatalf("err = %v, want a loud refusal", err)
			}
			if n := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_system = 'slack'`) +
				num(t, `SELECT count(*) FROM relationships`); n != 0 {
				t.Errorf("%d slack mappings / edges written for the refused email", n)
			}
		})
	}
}
