package deterministic

import "testing"

func TestRecipientsExist(t *testing.T) {
	t.Run("pass: known, unmerged, reachable", func(t *testing.T) { expectNone(t, RecipientsExist(baseInput())) })
	t.Run("fail: unknown person", func(t *testing.T) {
		in := baseInput()
		in.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}}
		expectFinding(t, RecipientsExist(in), CheckRecipientsExist, true, "not a known person")
	})
	t.Run("fail: merged record", func(t *testing.T) {
		in := baseInput()
		in.People[1].MergedInto = ptr(sam)
		expectFinding(t, RecipientsExist(in), CheckRecipientsExist, true, "merged into p-2")
	})
	t.Run("fail: no email address for an email", func(t *testing.T) {
		in := baseInput()
		in.People[1].HasEmail = false
		expectFinding(t, RecipientsExist(in), CheckRecipientsExist, true, "no email address")
	})
	t.Run("pass: no email address needed for a CRM note", func(t *testing.T) {
		in := baseInput()
		in.People[1].HasEmail = false
		in.Draft.FinishedArtifact.Channel = ChannelCRMNote
		expectNone(t, RecipientsExist(in))
	})
}

func TestRecipientsBelongToAccount(t *testing.T) {
	t.Run("pass: account contact and our employee", func(t *testing.T) {
		in := baseInput()
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: rep, Role: "cc"})
		expectNone(t, RecipientsBelongToAccount(in))
	})
	t.Run("fail: another account's contact", func(t *testing.T) {
		in := baseInput()
		in.People[1].AccountID = ptr(otherAcct)
		expectFinding(t, RecipientsBelongToAccount(in), CheckRecipientBelongs, true, "belongs to account acct-2")
	})
	t.Run("fail: unresolved contact", func(t *testing.T) {
		in := baseInput()
		in.People[1].AccountID = nil
		expectFinding(t, RecipientsBelongToAccount(in), CheckRecipientBelongs, true, "not resolved")
	})
	t.Run("fail: departed buying-group member", func(t *testing.T) {
		in := baseInput()
		in.State.BuyingGroup[0].Status = "departed"
		got := RecipientsBelongToAccount(in)
		expectFinding(t, got, CheckRecipientBelongs, true, "departed")
		if got[0].StateRefs[0] != "buying_group" {
			t.Fatalf("state refs = %v", got[0].StateRefs)
		}
	})
}

func TestRecipientsNotDuplicated(t *testing.T) {
	t.Run("pass: distinct people", func(t *testing.T) {
		in := baseInput()
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: sam, Role: "cc"})
		expectNone(t, RecipientsNotDuplicated(in))
	})
	t.Run("fail: same person on to and cc", func(t *testing.T) {
		in := baseInput()
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: pat, Role: "cc"})
		expectFinding(t, RecipientsNotDuplicated(in), CheckRecipientsNotDuplicate, false, "addressed twice")
	})
	t.Run("fail: merged alias of a recipient", func(t *testing.T) {
		in := baseInput()
		in.People = append(in.People, Person{PersonID: "p-1b", DisplayName: "Pat L", Kind: "contact",
			AccountID: ptr(acct), MergedInto: ptr(pat), HasEmail: true})
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "p-1b", Role: "cc"})
		expectFinding(t, RecipientsNotDuplicated(in), CheckRecipientsNotDuplicate, false, "person p-1")
	})
}

func TestInternalOnlyNotExternalized(t *testing.T) {
	internal := Person{PersonID: "emp-2", DisplayName: "Deal Desk", Kind: KindEmployee, HasEmail: true, InternalOnly: true}
	t.Run("pass: internal-only on bcc", func(t *testing.T) {
		in := baseInput()
		in.People = append(in.People, internal)
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "emp-2", Role: "bcc"})
		expectNone(t, InternalOnlyNotExternalized(in))
	})
	t.Run("pass: internal-only on an internal-only message", func(t *testing.T) {
		in := baseInput()
		in.People = append(in.People, internal)
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}, {PersonID: "emp-2", Role: "cc"}}
		expectNone(t, InternalOnlyNotExternalized(in))
	})
	t.Run("fail: internal-only on cc of a customer email", func(t *testing.T) {
		in := baseInput()
		in.People = append(in.People, internal)
		in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "emp-2", Role: "cc"})
		expectFinding(t, InternalOnlyNotExternalized(in), CheckInternalNotExternal, true, "Deal Desk")
	})
}

func TestRecipientCorrectnessResult(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		j := RecipientCorrectness(baseInput())
		if j.Result.Verdict != "pass" || j.Result.Blocking || len(j.Checks) != 4 {
			t.Fatalf("result = %+v", j.Result)
		}
	})
	t.Run("fail blocks with a correction", func(t *testing.T) {
		in := baseInput()
		in.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}}
		r := RecipientCorrectness(in).Result
		if r.Verdict != "fail" || !r.Blocking || r.SuggestedCorrection == nil || r.EvalVersion != "recipient_correctness:v1" {
			t.Fatalf("result = %+v", r)
		}
	})
}

func TestRecipientCorrectnessIsContractValid(t *testing.T) {
	in := baseInput()
	expectContractValid(t, RecipientCorrectness(in).Result)
	in.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}}
	expectContractValid(t, RecipientCorrectness(in).Result)
}
