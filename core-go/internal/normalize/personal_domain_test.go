package normalize

import "testing"

func TestPersonalMailDomainsNeverIdentifyAnAccount(t *testing.T) {
	for _, d := range []string{"gmail.com", "Outlook.COM", "yahoo.com", "hotmail.com", "icloud.com", "proton.me", "protonmail.com", "live.com", "aol.com", "gmx.com"} {
		if !IsPersonalDomain(d) {
			t.Errorf("IsPersonalDomain(%q) = false", d)
		}
		if got := ExternalDomain("someone@" + d); got != "" {
			t.Errorf("ExternalDomain(%s) = %q, want empty", d, got)
		}
		if got := firstExternalDomain("x@"+d, "y@acme.com"); got != "acme.com" {
			t.Errorf("firstExternalDomain did not skip %s: %q", d, got)
		}
		if got := externalDomainHints("x@" + d); len(got) != 0 {
			t.Errorf("externalDomainHints(%s) = %v, want none", d, got)
		}
		if got := DomainFromWebsite("https://www." + d); got != "" {
			t.Errorf("DomainFromWebsite(%s) = %q, want empty", d, got)
		}
	}
	if IsPersonalDomain("acme.com") || IsPersonalDomain("") {
		t.Error("a company domain or an empty string is not a personal mail domain")
	}
}

func TestIsOurEmail(t *testing.T) {
	for in, want := range map[string]bool{
		"dana@vendor.example": true, "Dana@vendor.example": true, "x@eu.vendor.example": true,
		"x@acme.com": false, "x@notvendor.example": false, "vendor.example": false, "": false,
	} {
		if got := IsOurEmail(in); got != want {
			t.Errorf("IsOurEmail(%q) = %v, want %v", in, got, want)
		}
	}
}
