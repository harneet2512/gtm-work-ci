package normalize

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// OurDomain is the vendor's own email domain (contracts/normalization.md, "Internal
// identities"). Every other domain is a customer or prospect.
const OurDomain = "vendor.example"

const maxEmailLen = 254

// address is the {email, name} shape used by the email and calendar payloads.
type address struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// normalizeEmailAddress trims and lower-cases an address and reports whether it is plausibly
// an email (one '@', non-empty local part, dotted domain without empty labels or spaces).
func normalizeEmailAddress(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > maxEmailLen || strings.ContainsFunc(s, unicode.IsSpace) {
		return "", false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" {
		return "", false
	}
	ascii, ok := asciiDomain(domain)
	if !ok {
		return "", false
	}
	return local + "@" + ascii, true
}

// asciiDomain canonicalizes a hostname: one trailing root dot is dropped and
// internationalized names become lower-case punycode, so that "Bücher.example." and
// "xn--bcher-kva.example" compare equal. Names that are not dotted hostnames are rejected.
func asciiDomain(d string) (string, bool) {
	d = strings.TrimSuffix(strings.TrimSpace(d), ".")
	if d == "" || strings.ContainsFunc(d, func(r rune) bool { return r == '@' || unicode.IsSpace(r) }) {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil || !validDomain(ascii) {
		return "", false
	}
	return ascii, true
}

// validDomain accepts dotted hostnames such as "acme.com"; labels must be non-empty.
func validDomain(d string) bool {
	if d == "" || strings.ContainsAny(d, "@ \t\r\n") {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" {
			return false
		}
	}
	return true
}

// domainOf returns the domain part of an address, or "" when s is not an email.
func domainOf(email string) string {
	norm, ok := normalizeEmailAddress(email)
	if !ok {
		return ""
	}
	_, domain, _ := strings.Cut(norm, "@")
	return domain
}

// ExternalDomain returns the canonical (lower-case punycode) domain of an email address, or ""
// when it is not a valid address or belongs to our own domain.
func ExternalDomain(email string) string {
	d := domainOf(email)
	if d == "" || isInternalDomain(d) || IsPersonalDomain(d) {
		return ""
	}
	return d
}

// personalDomains lists mailbox providers: an address there says nothing about the sender's
// employer, so such a domain never identifies an account.
func personalDomains() []string {
	return []string{
		"gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com", "yahoo.com", "ymail.com",
		"icloud.com", "me.com", "mac.com", "proton.me", "protonmail.com", "pm.me", "aol.com", "gmx.com", "gmx.net",
		"mail.com", "zoho.com", "fastmail.com", "yandex.com", "qq.com", "163.com",
	}
}

// IsPersonalDomain reports whether the domain is a personal mailbox provider (gmail.com, ...).
func IsPersonalDomain(domain string) bool {
	return slices.Contains(personalDomains(), strings.ToLower(strings.TrimSpace(domain)))
}

// IsOurEmail reports whether an address is at our domain or one of its subdomains.
func IsOurEmail(email string) bool { return isInternalDomain(domainOf(email)) }

// isInternalDomain is true for OurDomain and its subdomains.
func isInternalDomain(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	return d == OurDomain || strings.HasSuffix(d, "."+OurDomain)
}

// firstExternalDomain returns the domain of the first address that is a valid, non-internal
// email, in argument order; "" when there is none.
func firstExternalDomain(emails ...string) string {
	for _, e := range emails {
		if d := domainOf(e); d != "" && !isInternalDomain(d) && !IsPersonalDomain(d) {
			return d
		}
	}
	return ""
}

// maxDomainHints bounds how many fallback domains one activity carries.
const maxDomainHints = 5

// domainHint wraps a domain as a one-element hint list; "" yields none.
func domainHint(d string) []Hint {
	if d == "" {
		return nil
	}
	return []Hint{{Kind: HintDomain, Value: d}}
}

// externalDomainHints returns the distinct external domains of the addresses, in order.
func externalDomainHints(emails ...string) []Hint {
	var out []Hint
	seen := map[string]bool{}
	for _, e := range emails {
		d := domainOf(e)
		if d == "" || isInternalDomain(d) || IsPersonalDomain(d) || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, Hint{Kind: HintDomain, Value: d})
		if len(out) == maxDomainHints {
			break
		}
	}
	return out
}

// hintOf builds a one-element hint list from a trimmed value; blank yields none.
func hintOf(kind HintKind, value string) []Hint {
	if v := strings.TrimSpace(value); v != "" {
		return []Hint{{Kind: kind, Value: v}}
	}
	return nil
}

// participantFromAddress validates an address and turns it into a Participant.
func participantFromAddress(a address, role string) (Participant, error) {
	email, ok := normalizeEmailAddress(a.Email)
	if !ok {
		return Participant{}, invalid("%s address %q is not a valid email", role, a.Email)
	}
	return Participant{RawIdentity: email, DisplayName: strings.TrimSpace(a.Name), Role: role}, nil
}

// addParticipant appends p unless the same (identity, role) is already present, which the
// database primary key would reject. A later display name fills an empty one.
func addParticipant(list []Participant, p Participant) []Participant {
	for i, existing := range list {
		if existing.RawIdentity == p.RawIdentity && existing.Role == p.Role {
			if existing.DisplayName == "" && p.DisplayName != "" {
				list[i].DisplayName = p.DisplayName
			}
			return list
		}
	}
	return append(list, p)
}

// collapseSpace trims and folds every whitespace run into a single space.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes shortens s to at most max runes, marking the cut with an ellipsis.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// containsEscapedNUL reports whether raw JSON holds a \u0000 escape that is not itself part
// of an escaped backslash. PostgreSQL jsonb and text cannot store NUL.
func containsEscapedNUL(raw []byte) bool {
	const needle = `\u0000`
	s := string(raw)
	for from := 0; ; {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return false
		}
		pos := from + i
		backslashes := 0
		for j := pos - 1; j >= 0 && s[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return true
		}
		from = pos + len(needle)
	}
}
