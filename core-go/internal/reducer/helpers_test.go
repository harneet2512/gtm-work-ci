package reducer

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

func id(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

var (
	t0      = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	acct    = id(1)
	opp     = id(2)
	dana    = id(10)
	priya   = id(11)
	marco   = id(12)
	owen    = id(13)
	elena   = id(14)
	sam     = id(15)
	ravi    = id(16)
	tom     = id(17)
	nextID  = 1000
	validat *schemacheck.Validator
)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

// claim builds an active claim with its own activity and a fresh uuid.
func claim(field claims.FieldPath, value string, st claims.Standing, conf float64, when time.Time) claims.Claim {
	nextID++
	c := claims.Claim{ID: id(nextID), AccountID: acct, OpportunityID: opp, FieldPath: field, Value: json.RawMessage(value), Standing: st,
		Confidence: conf, SourceActivityID: id(nextID + 5000), OccurredAt: when, Extractor: "test@1", Status: claims.StatusActive}
	if st == claims.FirstPartyAI {
		c.EvidenceQuote = "verbatim quote"
		c.SpeakerPersonID = priya
	}
	return c
}

func about(c claims.Claim, person string) claims.Claim { c.SubjectPersonID = person; return c }

func people() map[string]Person {
	return map[string]Person{
		dana:  {ID: dana, DisplayName: "Dana Kim", Kind: "employee"},
		priya: {ID: priya, DisplayName: "Priya Shah", Title: "Director of Operations", Kind: "contact"},
		marco: {ID: marco, DisplayName: "Marco Ruiz", Kind: "contact"},
		owen:  {ID: owen, DisplayName: "Owen Clarke", Kind: "contact"},
		elena: {ID: elena, DisplayName: "Elena Vasquez", Kind: "contact"},
		sam:   {ID: sam, DisplayName: "Sam Okafor", Kind: "contact"},
		ravi:  {ID: ravi, DisplayName: "Ravi Menon", Kind: "contact"},
		tom:   {ID: tom, DisplayName: "Tom Becker", Kind: "contact"},
	}
}

// input adjudicates cs at `now` and wraps them into a reducer input.
func input(now time.Time, cs []claims.Claim, acts []Activity) Input {
	return Input{
		AccountID: acct, AccountName: "Acme Corp", Version: 3, ComputedAt: now,
		Adjudication: claims.Adjudicate(cs, now), Activities: acts, People: people(), OwnDomain: "vendor.example",
	}
}

func inboundEmail(n int, from string, when time.Time) Activity {
	return Activity{ID: id(n), Type: "EmailReceived", OccurredAt: when, Participants: []Participant{
		{PersonID: from, RawIdentity: "x@customer.com", Role: "from"}, {PersonID: dana, RawIdentity: "dana@vendor.example", Role: "to"}}}
}

func mustValidate(t *testing.T, st AccountState) []byte {
	t.Helper()
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if validat == nil {
		if validat, err = schemacheck.New(); err != nil {
			t.Fatal(err)
		}
	}
	if err := validat.Validate("account_state", raw); err != nil {
		t.Fatalf("state violates account_state.v1.json: %v\n%s", err, raw)
	}
	return raw
}

func str(f Field) string { s, _ := f.Value.(string); return s }

func ids(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Text+"="+it.Status)
	}
	return out
}

func items(f Field) []Item { v, _ := f.Value.([]Item); return v }
