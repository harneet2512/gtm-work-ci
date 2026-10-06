package main

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
)

func newTestBreaker(t *testing.T) *providerbreaker.Breaker {
	t.Helper()
	b, err := providerbreaker.New(0, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
