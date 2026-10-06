package demomine

import (
	"crypto/sha1" //nolint:gosec // RFC 4122 version 5 names are SHA-1 by definition; not a security use.
	"fmt"
)

// eventNamespace is the fixed namespace of replay event ids (a random v4 uuid, generated once).
var eventNamespace = [16]byte{0x7c, 0x1e, 0x4d, 0x20, 0x91, 0x5b, 0x4f, 0x6a, 0x8e, 0x33, 0x2b, 0x0d, 0xa6, 0x41, 0x5f, 0x9c}

// EventUUID is the replay dataset's stable id of an event: a version 5 uuid of its ingest identity (source
// system, source object id, source event key). The same event always has the same id, in any database and
// whether or not it has been ingested, which is what lets a manifest name the held-out event before Play.
func EventUUID(system, object, key string) string {
	return uuidV5(eventNamespace, system+"\x00"+object+"\x00"+key)
}

// uuidV5 is RFC 4122 section 4.3: SHA-1 of the namespace and the name, with the version and variant set.
func uuidV5(ns [16]byte, name string) string {
	h := sha1.New() //nolint:gosec // see import
	h.Write(ns[:])
	h.Write([]byte(name))
	b := h.Sum(nil)[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
