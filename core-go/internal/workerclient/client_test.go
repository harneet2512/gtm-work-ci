package workerclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

const (
	actID   = "0ac70000-0000-4000-8000-000000000101"
	evID    = "0e000000-0000-4000-8000-000000000101"
	personA = "0b0e0000-0000-4000-8000-000000000018"
)

func request() claims.ExtractRequest {
	return claims.ExtractRequest{
		Text:             "Hi Dana - SOC2 please.\nMarco",
		ExtractorVersion: "extract-v1",
		KnownPeople:      []claims.KnownPerson{{PersonID: personA, RawIdentity: "marco.ruiz@acme.com", DisplayName: "Marco Ruiz", Title: "Head of Security"}},
		Activity: claims.ActivityInput{
			ID: actID, AccountID: "0a0c0000-0000-4000-8000-000000000001", Type: "EmailReceived", SourceSystem: "email",
			SourceEventID: evID, SourceObjectID: "<msg-1@acme.com>", IdempotencyKey: strings.Repeat("ab", 32),
			OccurredAt: time.Date(2026, 9, 29, 15, 42, 0, 0, time.UTC), IngestedAt: time.Date(2026, 9, 29, 15, 43, 0, 0, time.UTC),
			Participants: []claims.Participant{{RawIdentity: "marco.ruiz@acme.com", Role: "from", DisplayName: "Marco Ruiz", PersonID: personA}, {RawIdentity: "dana@vendor.example", Role: "to"}},
		},
	}
}

func okBody() string {
	return `{"claims":[{"field_path":"blockers","value":"SOC2 report","confidence":0.9,"evidence_quote":"SOC2 please","speaker_identity":"marco.ruiz@acme.com","subject_identity":null,"role":null,"due_at":null}],
		"model":"deepseek-v4-flash","extractor_version":"extract-v1","dropped":2}`
}

func TestExtractSendsAContractConformantRequestAndParsesTheResponse(t *testing.T) {
	var gotBody []byte
	var gotPath, gotCT, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotPath, gotCT, gotMethod = r.URL.Path, r.Header.Get("Content-Type"), r.Method
		_, _ = io.WriteString(w, okBody())
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Extract(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/extract" || gotMethod != http.MethodPost || gotCT != "application/json" {
		t.Fatalf("%s %s %s", gotMethod, gotPath, gotCT)
	}
	var body struct {
		Activity         json.RawMessage `json:"activity"`
		Text             string          `json:"text"`
		ExtractorVersion string          `json:"extractor_version"`
		KnownPeople      []struct {
			RawIdentity string `json:"raw_identity"`
			DisplayName string `json:"display_name"`
			Title       string `json:"title"`
		} `json:"known_people"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if body.Text != request().Text || body.ExtractorVersion != "extract-v1" || len(body.KnownPeople) != 1 || body.KnownPeople[0].RawIdentity != "marco.ruiz@acme.com" {
		t.Fatalf("body = %s", gotBody)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("activity", body.Activity); err != nil {
		t.Fatalf("the activity sent to the worker violates activity.v1.json: %v\n%s", err, body.Activity)
	}
	if resp.Model != "deepseek-v4-flash" || resp.Dropped != 2 || len(resp.Claims) != 1 || resp.Claims[0].FieldPath != claims.FieldBlockers || resp.Claims[0].EvidenceQuote != "SOC2 please" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestExtractMapsErrorEnvelopesAndStatuses(t *testing.T) {
	for _, tt := range []struct {
		status    int
		body      string
		retryable bool
		code      string
	}{
		{422, `{"error":{"code":"invalid_request","message":"bad"}}`, false, "invalid_request"},
		{502, `{"error":{"code":"provider_error","message":"model down"}}`, true, "provider_error"},
		{500, `not json`, true, ""},
		{429, `{}`, true, ""},
		{400, `{"error":{"code":"x","message":"y"}}`, false, "x"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		}))
		c, _ := New(srv.URL)
		_, err := c.Extract(context.Background(), request())
		srv.Close()
		var we *Error
		if !errors.As(err, &we) || we.Status != tt.status || we.Retryable != tt.retryable || we.Code != tt.code || we.Permanent() == tt.retryable || claims.IsPermanent(err) == tt.retryable {
			t.Errorf("status %d: err = %v (%+v)", tt.status, err, we)
		}
	}
}

func TestExtractRejectsMalformedAndOversizedSuccessBodies(t *testing.T) {
	for name, body := range map[string]string{
		"not json":    `<html>`,
		"no model":    `{"claims":[],"model":"","extractor_version":"v","dropped":0}`,
		"null claims": `{"model":"m","extractor_version":"v","dropped":0}`,
		"oversize":    `{"claims":[],"model":"m","extractor_version":"v","dropped":0,"pad":"` + strings.Repeat("x", MaxResponseBytes) + `"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		c, _ := New(srv.URL)
		_, err := c.Extract(context.Background(), request())
		srv.Close()
		if name == "null claims" {
			if err != nil {
				t.Errorf("%s: a response without candidates is a valid empty answer: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestExtractTimesOutAndHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c, _ := New(srv.URL, WithTimeout(50*time.Millisecond))
	start := time.Now()
	_, err := c.Extract(context.Background(), request())
	var we *Error
	if err == nil || !errors.As(err, &we) || !we.Retryable || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: err = %v after %v", err, time.Since(start))
	}

	c, _ = New(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Extract(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
}

func TestExtractReportsUnreachableWorkerAsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := New(url, WithTimeout(time.Second))
	_, err := c.Extract(context.Background(), request())
	var we *Error
	if !errors.As(err, &we) || !we.Retryable {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if _, err := c.Extract(context.Background(), request()); err == nil {
		t.Fatal("a redirect must not be followed with the activity text")
	}
}

func TestNewValidatesTheBaseURL(t *testing.T) {
	for _, bad := range []string{"", "not a url", "ftp://host", "http://", "127.0.0.1:8090"} {
		if _, err := New(bad); err == nil {
			t.Errorf("New(%q) succeeded", bad)
		}
	}
	if c, err := New("http://127.0.0.1:8090/"); err != nil || c.baseURL != "http://127.0.0.1:8090" {
		t.Fatalf("%v %v", c, err)
	}
}

func TestExtractRejectsAnActivityWithoutText(t *testing.T) {
	c, _ := New("http://127.0.0.1:1")
	req := request()
	req.Text = "  "
	if _, err := c.Extract(context.Background(), req); err == nil {
		t.Fatal("empty text accepted")
	}
}

func TestClientSatisfiesTheExtractorInterface(t *testing.T) {
	var _ claims.Extractor = (*Client)(nil)
}
