package graph

import (
	"crypto/sha1" //nolint:gosec // UUIDv5 is defined over SHA-1; this is an identifier, not a security control.
	"fmt"
)

// namespaceDNS is the RFC 4122 DNS namespace.
func namespaceDNS() [16]byte {
	return [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
}

// documentNamespace is uuid5(NAMESPACE_DNS, "documents.ghost.local").
func documentNamespace() [16]byte { return uuidV5Bytes(namespaceDNS(), "documents.ghost.local") }

// DocumentID returns the node id of a source document (for example "gdrive:acme-mnda-2026").
// Documents have no table (relationships allows the 'document' node type but there is no
// migration for one), so the id is the deterministic UUIDv5 of the source document id.
func DocumentID(sourceDocumentID string) string {
	return formatUUID(uuidV5Bytes(documentNamespace(), sourceDocumentID))
}

// DocumentRef is the graph node reference of a source document.
func DocumentRef(sourceDocumentID string) EntityRef {
	return EntityRef{Type: EntityDocument, ID: DocumentID(sourceDocumentID)}
}

// uuidV5 implements RFC 4122 name-based UUIDs with SHA-1.
func uuidV5(namespace [16]byte, name string) string { return formatUUID(uuidV5Bytes(namespace, name)) }

func uuidV5Bytes(namespace [16]byte, name string) [16]byte {
	h := sha1.New() //nolint:gosec
	h.Write(namespace[:])
	h.Write([]byte(name))
	var u [16]byte
	copy(u[:], h.Sum(nil)[:16])
	u[6] = (u[6] & 0x0f) | 0x50
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

func formatUUID(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
