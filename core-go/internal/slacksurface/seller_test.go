package slacksurface

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

func sellerDir() Directory {
	return Directory{
		"p-seller":   {ID: "p-seller", Name: "Luis Rodriguez", Email: "luis.rodriguez@" + normalize.OurDomain},
		"p-sub":      {ID: "p-sub", Name: "Dana Kim", Email: "dana@mail." + normalize.OurDomain},
		"p-customer": {ID: "p-customer", Name: "Fatoumata Toure", Email: "fatoumata@medtechadvances.com"},
		"p-noemail":  {ID: "p-noemail", Name: "Priya"},
		"p-bare":     {ID: "p-bare", Email: "owen@" + normalize.OurDomain},
	}
}

func TestDisplayShowsSellerWithoutAddress(t *testing.T) {
	d := sellerDir()
	cases := map[string]string{
		"p-seller": "Luis Rodriguez · seller, Vendor Co.",
		"p-sub":    "Dana Kim · seller, Vendor Co.",
		"p-bare":   "seller, Vendor Co.",
	}
	for id, want := range cases {
		got := d.display(Recipient{PersonID: id})
		if got != want {
			t.Errorf("display(%s) = %q, want %q", id, got, want)
		}
		if strings.Contains(strings.ToLower(got), "ghostvendor") || strings.Contains(got, "@") {
			t.Errorf("display(%s) leaks the seller address: %q", id, got)
		}
	}
}

func TestDisplayKeepsCustomerEmail(t *testing.T) {
	d := sellerDir()
	if got := d.display(Recipient{PersonID: "p-customer"}); got != "Fatoumata Toure <fatoumata@medtechadvances.com>" {
		t.Errorf("customer display changed: %q", got)
	}
	if got := d.display(Recipient{PersonID: "p-noemail"}); got != "Priya" {
		t.Errorf("name-only display changed: %q", got)
	}
}

func TestSellerDisplayRoundTripsThroughTheEditModal(t *testing.T) {
	d := sellerDir()
	text := d.display(Recipient{PersonID: "p-seller"}) + ", " + d.display(Recipient{PersonID: "p-customer"})
	got, err := parseRecipients(text, "cc", "cc", d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].PersonID != "p-seller" || got[1].PersonID != "p-customer" {
		t.Errorf("round trip lost people: %+v", got)
	}
}

func TestIsSellerEmailRejectsLookalikes(t *testing.T) {
	cases := map[string]bool{
		"a@" + normalize.OurDomain:                       true,
		"A@MAIL." + strings.ToUpper(normalize.OurDomain): true,
		"a@not" + normalize.OurDomain:                    false,
		"a@" + normalize.OurDomain + ".evil.io":          false,
		"":                                               false,
		"a@acme.com":                                     false,
	}
	for email, want := range cases {
		if got := isSellerEmail(email); got != want {
			t.Errorf("isSellerEmail(%q) = %v, want %v", email, got, want)
		}
	}
}
