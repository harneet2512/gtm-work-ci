package workerclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

const refused = `{"error":{"code":"provider_unavailable_nonretryable","message":"model provider is unavailable"}}`

func TestProviderUnavailableIsNeitherRetryableNorPermanentWhateverTheStatus(t *testing.T) {
	for _, status := range []int{424, 402, 502, 503} { // even a 5xx carrying the code must not be retried
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, refused)
		}))
		c, _ := New(srv.URL)
		_, err := c.Extract(context.Background(), request())
		srv.Close()
		var we *Error
		if !errors.As(err, &we) || we.Retryable || !we.ProviderUnavailable() || !we.ProviderFault() {
			t.Fatalf("status %d: %v (%+v)", status, err, we)
		}
		if claims.IsPermanent(err) {
			t.Fatalf("status %d: a provider outage must not quarantine the activity", status)
		}
		if !claims.IsProviderUnavailable(err) || !claims.IsProviderFault(err) {
			t.Fatalf("status %d: not recognised as a provider refusal", status)
		}
	}
}

func TestRecoverableFailuresStayRetryableAndCountAsProviderFaultsButNotRequestErrors(t *testing.T) {
	for _, tt := range []struct {
		status        int
		body          string
		retryable     bool
		fault, refuse bool
	}{
		{502, `{"error":{"code":"provider_error","message":"x"}}`, true, true, false},
		{503, `{}`, true, true, false},
		{429, `{}`, true, false, false},
		{422, `{"error":{"code":"invalid_request","message":"x"}}`, false, false, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		}))
		c, _ := New(srv.URL)
		_, err := c.Extract(context.Background(), request())
		srv.Close()
		var we *Error
		if !errors.As(err, &we) || we.Retryable != tt.retryable || we.ProviderFault() != tt.fault || we.ProviderUnavailable() != tt.refuse {
			t.Errorf("status %d: %v (%+v)", tt.status, err, we)
		}
	}
}

// HAR-124: the replay worker's "no recording" answer is its own structured code. It is not a provider fault and is never permanent.
func TestCassetteNotFoundIsAStructuredCodeNotAProviderFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":{"code":"cassette_not_found","message":"no recorded answer exists for this request; replay never calls a model"}}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	_, err := c.Extract(context.Background(), request())
	var we *Error
	if !errors.As(err, &we) || we.Code != CodeCassetteNotFound || !we.CassetteNotFound() {
		t.Fatalf("err = %v, want code %s", err, CodeCassetteNotFound)
	}
	if we.ProviderFault() || we.ProviderUnavailable() {
		t.Fatalf("a missing recording must not trip the provider breaker: %+v", we)
	}
	// HAR-124 review: never permanent. A permanent error is quarantined by coalesce and the job succeeds with no claims, which would
	// hide every missing recording outside the setup replay. It must fail the job visibly instead.
	if claims.IsPermanent(err) || claims.IsProviderFault(err) {
		t.Fatal("a missing recording must not be quarantined into empty claims")
	}
}
