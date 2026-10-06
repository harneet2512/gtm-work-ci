package claims

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type scriptedExtractor struct {
	resp  ExtractResponse
	err   error
	calls []ExtractRequest
}

func (s *scriptedExtractor) Extract(_ context.Context, req ExtractRequest) (ExtractResponse, error) {
	s.calls = append(s.calls, req)
	return s.resp, s.err
}

type memCache struct {
	m      map[string]ExtractResponse
	getErr error
	putErr error
}

func (c *memCache) Get(_ context.Context, id, v string) (ExtractResponse, bool, error) {
	r, ok := c.m[id+"|"+v]
	return r, ok, c.getErr
}

func (c *memCache) Put(_ context.Context, id, v string, r ExtractResponse) error {
	if c.putErr != nil {
		return c.putErr
	}
	c.m[id+"|"+v] = r
	return nil
}

func goodResponse() ExtractResponse {
	return ExtractResponse{Model: "fake-model", ExtractorVersion: "extract-v1", Claims: []Candidate{
		{FieldPath: FieldChampionStatus, Value: json.RawMessage(`"delegated"`), Confidence: 0.9, EvidenceQuote: "I'm stepping back from the day-to-day"},
		{FieldPath: FieldHealth, Value: json.RawMessage(`"at_risk"`), Confidence: 0.9, EvidenceQuote: "invented"},
	}}
}

func pipeline(ex Extractor, cache Cache) Pipeline {
	return Pipeline{Rules: RuleExtractor{Dir: testDir}, LLM: ex, Cache: cache}
}

func resolver(act ActivityInput) Resolver { return NewIdentityIndex(act.Participants, nil).Resolve }

func TestPipelineExtractsEmailThroughWorkerAndCachesTheAnswer(t *testing.T) {
	act := emailActivity()
	ex := &scriptedExtractor{resp: goodResponse()}
	cache := &memCache{m: map[string]ExtractResponse{}}
	p := pipeline(ex, cache)

	out, err := p.Run(context.Background(), act, []KnownPerson{{RawIdentity: "x@y.com", DisplayName: "X"}}, resolver(act))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Claims) != 1 || out.Claims[0].FieldPath != FieldChampionStatus || len(out.Dropped) != 1 || out.LLMCalls != 1 {
		t.Fatalf("%+v", out)
	}
	req := ex.calls[0]
	if req.Text != emailBody || req.ExtractorVersion != DefaultExtractorVersion || len(req.KnownPeople) != 1 || req.Activity.ID != act.ID {
		t.Fatalf("request = %+v", req)
	}
	if len(cache.m) != 1 {
		t.Fatalf("answer not cached: %v", cache.m)
	}

	again, err := p.Run(context.Background(), act, nil, resolver(act))
	if err != nil || len(ex.calls) != 1 || again.LLMCalls != 0 || len(again.Claims) != 1 {
		t.Fatalf("a cache hit must not call the worker again: calls=%d out=%+v err=%v", len(ex.calls), again, err)
	}
}

func TestPipelineSkipsWorkerForStructuredAndEmptyActivitiesAndWhenDisabled(t *testing.T) {
	ex := &scriptedExtractor{resp: goodResponse()}
	crm := crmActivity("OpportunityStageChanged", `{"object_type":"Opportunity","fields":{"StageName":{"new":"Discovery"}}}`)
	crm.Body = "some body"
	out, err := pipeline(ex, nil).Run(context.Background(), crm, nil, resolver(crm))
	if err != nil || len(ex.calls) != 0 || len(out.Claims) != 1 {
		t.Fatalf("structured CRM activity must use rules only: calls=%d out=%+v err=%v", len(ex.calls), out, err)
	}
	empty := emailActivity()
	empty.Body = "  \n"
	if _, err := pipeline(ex, nil).Run(context.Background(), empty, nil, resolver(empty)); err != nil || len(ex.calls) != 0 {
		t.Fatalf("empty body reached the worker: %v", err)
	}
	act := emailActivity()
	if out, err := pipeline(nil, nil).Run(context.Background(), act, nil, resolver(act)); err != nil || len(out.Claims) != 0 {
		t.Fatalf("nil LLM: %+v %v", out, err)
	}
}

func TestPipelinePropagatesWorkerAndCacheFailures(t *testing.T) {
	act := emailActivity()
	boom := errors.New("worker 502")
	if _, err := pipeline(&scriptedExtractor{err: boom}, nil).Run(context.Background(), act, nil, resolver(act)); !errors.Is(err, boom) {
		t.Fatalf("worker error not propagated: %v", err)
	}
	cases := map[string]*memCache{
		"read":  {m: map[string]ExtractResponse{}, getErr: errors.New("db down")},
		"write": {m: map[string]ExtractResponse{}, putErr: errors.New("db full")},
	}
	for name, cache := range cases {
		if _, err := pipeline(&scriptedExtractor{resp: goodResponse()}, cache).Run(context.Background(), act, nil, resolver(act)); err == nil {
			t.Errorf("%s cache failure swallowed", name)
		}
	}
	if _, err := pipeline(&scriptedExtractor{resp: ExtractResponse{}}, nil).Run(context.Background(), act, nil, resolver(act)); err == nil {
		t.Fatal("a response without a model must be rejected")
	}
}

func TestPipelinePropagatesRuleErrors(t *testing.T) {
	bad := ActivityInput{ID: actIDOne, SourceSystem: "crm", Type: "CRMFieldChanged", Payload: json.RawMessage(`{`)}
	if _, err := pipeline(nil, nil).Run(context.Background(), bad, nil, resolver(bad)); err == nil {
		t.Fatal("rule error swallowed")
	}
}

func TestPipelineTruncatesOversizedTextToTheWorkerLimit(t *testing.T) {
	act := emailActivity()
	act.Body = strings.Repeat("é", MaxExtractTextRunes+10)
	ex := &scriptedExtractor{resp: ExtractResponse{Model: "m"}}
	if _, err := pipeline(ex, nil).Run(context.Background(), act, nil, resolver(act)); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(ex.calls[0].Text)); got != MaxExtractTextRunes {
		t.Fatalf("text runes = %d", got)
	}
}

func TestExtractableTypes(t *testing.T) {
	for typ, want := range map[string]bool{"EmailReceived": true, "TranscriptReady": true, "SlackDecision": true, "CRMNoteAdded": true,
		"MeetingScheduled": false, "CRMFieldChanged": false, "EnrichmentUpdated": false, "CallEnded": false} {
		if got := Extractable(ActivityInput{Type: typ, Body: "text"}); got != want {
			t.Errorf("Extractable(%s) = %v, want %v", typ, got, want)
		}
	}
}
