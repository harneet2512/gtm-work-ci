package slacksurface

import (
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// sellerSuffix is how a seller-side person reads on a surface: the audience never sees a seller mail
// address (the seller domain is a data key, not demo copy). Customer-side emails are unchanged.
const sellerSuffix = " · seller, Vendor Co."

// sellerMarker is sellerSuffix without its comma, so an edited recipient line can still be split on commas.
const sellerMarker = " · seller"

// isSellerEmail reports whether email is on the vendor's own domain or one of its subdomains.
func isSellerEmail(email string) bool {
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok {
		return false
	}
	own := strings.ToLower(normalize.OurDomain)
	return domain == own || strings.HasSuffix(domain, "."+own)
}

// sellerLabel is "<name> · seller, Vendor Co." ("seller, Vendor Co." when the name is unknown).
func sellerLabel(name string) string {
	if name == "" {
		return strings.TrimPrefix(sellerSuffix, " · ")
	}
	return name + sellerSuffix
}
