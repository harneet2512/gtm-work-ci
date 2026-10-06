package claims

import "strings"

// KnownPerson is an identity already resolved for an account; it is sent to the worker to help
// attribute speakers and used here to resolve the identities the worker returns.
type KnownPerson struct {
	PersonID    string `json:"-"`
	RawIdentity string `json:"raw_identity"`
	DisplayName string `json:"display_name"`
	Title       string `json:"title,omitempty"`
	// Internal is true for our own people, false for the account's, nil when unknown
	// (worker.yaml known_people[].internal); the extractor uses it to read "our side" correctly.
	Internal *bool `json:"internal,omitempty"`
}

// Resolver maps a raw identity (email, call speaker label, display name) to a person id.
type Resolver func(identity string) (personID string, ok bool)

// IdentityIndex resolves raw identities against an activity's participants and the account's
// known people. An identity that maps to two different people (two attendees with the same
// display name) is ambiguous and never resolves; guessing would attribute claims to the wrong
// person.
type IdentityIndex struct {
	byKey     map[string]string
	ambiguous map[string]bool
}

// NewIdentityIndex indexes participants that are linked to a person, plus known people. Call
// speaker identities (call:<call>:<label>) are also reachable by their bare label.
func NewIdentityIndex(parts []Participant, known []KnownPerson) *IdentityIndex {
	ix := &IdentityIndex{byKey: map[string]string{}, ambiguous: map[string]bool{}}
	for _, p := range parts {
		if p.PersonID == "" {
			continue
		}
		ix.add(p.RawIdentity, p.PersonID)
		ix.add(p.DisplayName, p.PersonID)
		if strings.HasPrefix(strings.ToLower(p.RawIdentity), "call:") {
			if i := strings.LastIndex(p.RawIdentity, ":"); i >= 0 {
				ix.add(p.RawIdentity[i+1:], p.PersonID)
			}
		}
	}
	for _, k := range known {
		if k.PersonID == "" {
			continue
		}
		ix.add(k.RawIdentity, k.PersonID)
		ix.add(k.DisplayName, k.PersonID)
	}
	return ix
}

func (ix *IdentityIndex) add(identity, personID string) {
	key := normalizeIdentity(identity)
	if key == "" {
		return
	}
	if prev, ok := ix.byKey[key]; ok && prev != personID {
		ix.ambiguous[key] = true
		return
	}
	ix.byKey[key] = personID
}

// Resolve returns the person an identity refers to.
func (ix *IdentityIndex) Resolve(identity string) (string, bool) {
	key := normalizeIdentity(identity)
	if key == "" || ix.ambiguous[key] {
		return "", false
	}
	id, ok := ix.byKey[key]
	return id, ok
}

func normalizeIdentity(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
