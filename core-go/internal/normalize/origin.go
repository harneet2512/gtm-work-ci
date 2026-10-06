package normalize

import (
	"regexp"
	"strings"
)

// Origin values (common.v1.json#recordOrigin, WP32 / HAR-131).
const (
	OriginLive      = "live"
	OriginDataset   = "dataset"
	OriginSynthetic = "synthetic"
)

var (
	// provenancePattern is common.v1.json#recordProvenance.
	provenancePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*:[A-Za-z0-9._-]{1,64}$`)
	// syntheticPattern is source_event.v1.json#/$defs/syntheticProvenance.
	syntheticPattern = regexp.MustCompile(`^synthetic:v[1-9][0-9]*$`)
)

// validateOrigin mirrors the source_event.v1.json rules on the origin/provenance pair:
// dataset and synthetic events name their source, live and undeclared events have none, and a
// synthetic provenance is carried only by a synthetic event.
func validateOrigin(origin, provenance string) error {
	switch origin {
	case "", OriginLive:
		if provenance != "" {
			return invalid("provenance %q requires origin dataset or synthetic", provenance)
		}
		return nil
	case OriginDataset, OriginSynthetic:
	default:
		return invalid("origin %q is not live, dataset or synthetic", origin)
	}
	if !provenancePattern.MatchString(provenance) {
		return invalid("origin %s requires a provenance like '<name>:<version>', got %q", origin, provenance)
	}
	isSynthetic := strings.HasPrefix(provenance, "synthetic:")
	if origin == OriginSynthetic && !syntheticPattern.MatchString(provenance) {
		return invalid("a synthetic event needs provenance synthetic:v<n>, got %q", provenance)
	}
	if origin == OriginDataset && isSynthetic {
		return invalid("provenance %q belongs to a synthetic event, not a dataset", provenance)
	}
	return nil
}
