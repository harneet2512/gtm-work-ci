package graph

import "testing"

func TestUUIDv5MatchesTheRFCVector(t *testing.T) {
	// RFC 4122 appendix B style vector: uuid5(NAMESPACE_DNS, "python.org").
	dns := namespaceDNS()
	if got, want := uuidV5(dns, "python.org"), "886313e1-3b8a-5372-9b90-0c9aee199e5d"; got != want {
		t.Errorf("uuidV5 = %s, want %s", got, want)
	}
}

func TestDocumentIDIsDeterministicAndDistinct(t *testing.T) {
	a, b := DocumentID("gdrive:acme-eu-proposal-v1"), DocumentID("gdrive:acme-mnda-2026")
	if a != DocumentID("gdrive:acme-eu-proposal-v1") {
		t.Error("DocumentID is not deterministic")
	}
	if a == b {
		t.Error("different documents share an id")
	}
	if len(a) != 36 || a[14] != '5' {
		t.Errorf("%q is not a version-5 UUID", a)
	}
}
