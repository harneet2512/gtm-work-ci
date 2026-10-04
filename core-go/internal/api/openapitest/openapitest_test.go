package openapitest

import (
	"strings"
	"testing"
)

func TestResponseValidatesBodiesAgainstTheSpec(t *testing.T) {
	spec, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	good := `{"activity_id":"11111111-1111-4111-8111-111111111111","source_event_id":"22222222-2222-4222-8222-222222222222","duplicate":false}`
	if err := spec.Response("POST", "/ingest", 201, []byte(good)); err != nil {
		t.Fatalf("valid IngestResult rejected: %v", err)
	}
	bad := `{"activity_id":"not-a-uuid","source_event_id":"22222222-2222-4222-8222-222222222222","duplicate":false}`
	if err := spec.Response("POST", "/ingest", 201, []byte(bad)); err == nil {
		t.Fatal("an IngestResult with a bad uuid was accepted")
	}
	// A $ref'd error response resolves through components.responses.
	if err := spec.Response("POST", "/ingest", 400, []byte(`{"error":{"code":"bad_request","message":"x"}}`)); err != nil {
		t.Fatalf("valid error envelope rejected: %v", err)
	}
	if err := spec.Response("POST", "/ingest", 400, []byte(`{"error":{"code":"bad_request"}}`)); err == nil {
		t.Fatal("an error envelope without a message was accepted")
	}
}

func TestResponseRefusesUndocumentedStatusesAndOperations(t *testing.T) {
	spec, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Response("POST", "/ingest", 418, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "does not document") {
		t.Fatalf("undocumented status accepted: %v", err)
	}
	if err := spec.Response("GET", "/nope", 200, []byte(`{}`)); err == nil {
		t.Fatal("unknown operation accepted")
	}
}

func TestOperationsListsTheContextAndReadEndpoints(t *testing.T) {
	spec, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, op := range spec.Operations() {
		have[op.Method+" "+op.Path] = true
	}
	for _, want := range []string{"GET /internal/ctx/{tool}", "GET /accounts/{account_id}/state", "GET /runs/{run_id}/trace", "POST /ingest"} {
		if !have[want] {
			t.Errorf("operation %q missing from the spec listing", want)
		}
	}
	if codes := spec.Statuses("GET", "/internal/ctx/{tool}"); len(codes) < 5 {
		t.Errorf("pullContext statuses = %v", codes)
	}
}
