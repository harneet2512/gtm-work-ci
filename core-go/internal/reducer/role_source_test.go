package reducer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func roleOf(t *testing.T, ms []Member, person string) Member {
	t.Helper()
	for _, m := range ms {
		if m.PersonID == person {
			return m
		}
	}
	t.Fatalf("no member %s in %+v", person, ms)
	return Member{}
}

func TestRolesSayWhetherTheyAreRecordedOrInferred(t *testing.T) {
	crm := on(about(claim(claims.FieldStakeholderRole, `"security"`, claims.CRMExplicit, 1, day(2)), priya), dealA)
	ai := on(about(claim(claims.FieldBuyingGroupMember, `{"role":"champion"}`, claims.FirstPartyAI, 0.9, day(3)), marco), dealA)
	title := on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(1)), owen), dealA)
	acts := []Activity{dealActs(80, dealA, 4), {ID: id(81), Type: "EmailReceived", OccurredAt: day(4), OpportunityID: dealA,
		Participants: []Participant{{PersonID: owen, Role: "from", RawIdentity: "o@c.com"}}}}
	res := ReduceAll(input(day(20), []claims.Claim{crm, ai, title}, acts))

	for name, group := range map[string][]Member{"deal": deal(t, res, dealA).BuyingGroup, "account": res.Account.BuyingGroup} {
		p, m, o := roleOf(t, group, priya), roleOf(t, group, marco), roleOf(t, group, owen)
		if p.RoleSource == nil || *p.RoleSource != RoleRecorded || p.RoleBasis == nil || *p.RoleBasis != "crm_explicit stakeholder_role" {
			t.Errorf("%s: a CRM role is recorded: %v / %v", name, deref(p.RoleSource), deref(p.RoleBasis))
		}
		if m.RoleSource == nil || *m.RoleSource != RoleInferred || m.RoleBasis == nil || !strings.HasPrefix(*m.RoleBasis, "first_party_ai buying_group.member: \"verbatim quote\"") {
			t.Errorf("%s: a model's role is inferred, with its quote: %v / %v", name, deref(m.RoleSource), deref(m.RoleBasis))
		}
		if o.RoleSource != nil || o.RoleBasis != nil || strings.Join(o.Roles, ",") != "unknown" {
			t.Errorf("%s: a title alone gives no role and no role provenance: %+v", name, o)
		}
	}
	mustValidate(t, res.Account)
}

func TestTheStrongestClaimDecidesRoleSourceAndBasisIsClipped(t *testing.T) {
	long := claim(claims.FieldStakeholderRole, `"legal"`, claims.FirstPartyAI, 0.9, day(5))
	long.EvidenceQuote = strings.Repeat("quote ", 60)
	long = on(about(long, priya), dealA)
	human := on(about(claim(claims.FieldStakeholderRole, `"security"`, claims.HumanApproved, 1, day(1)), priya), dealA)

	m := roleOf(t, deal(t, ReduceAll(input(day(20), []claims.Claim{long, human}, nil)), dealA).BuyingGroup, priya)
	if *m.RoleSource != RoleRecorded || *m.RoleBasis != "human_approved stakeholder_role" {
		t.Fatalf("the human's claim is stronger than the newer model claim: %v / %v", deref(m.RoleSource), deref(m.RoleBasis))
	}
	m = roleOf(t, deal(t, ReduceAll(input(day(20), []claims.Claim{long}, nil)), dealA).BuyingGroup, priya)
	if *m.RoleSource != RoleInferred || len(*m.RoleBasis) > 200 || !strings.Contains(*m.RoleBasis, "…") {
		t.Fatalf("basis = %q, want a clipped quote within 200 characters", deref(m.RoleBasis))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestRoleSourcesMatchTheContract: the Go values are the schema's enum (null aside).
func TestRoleSourcesMatchTheContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "schemas", "account_state.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			BuyingGroup struct {
				Items struct {
					Properties struct {
						RoleSource struct {
							Enum []any `json:"enum"`
						} `json:"role_source"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"buying_group"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range doc.Properties.BuyingGroup.Items.Properties.RoleSource.Enum {
		if s, ok := v.(string); ok {
			got = append(got, s)
		}
	}
	want := RoleSources()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("schema role_source = %v, reducer = %v", got, want)
	}
}
