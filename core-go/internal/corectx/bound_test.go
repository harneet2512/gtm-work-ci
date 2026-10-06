package corectx

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	uuidA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	uuidB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestBoundOfNothingIsAnEmptyListNotNull(t *testing.T) {
	b := bound(nil, 10, nil)
	raw, _ := json.Marshal(b.items)
	if string(raw) != "[]" || b.bytes != 2 || b.truncated || len(b.ids) != 0 {
		t.Fatalf("empty bound = %s %+v", raw, b)
	}
}

func TestBoundKeepsAtMostLimitItemsAndSaysSo(t *testing.T) {
	items := []any{map[string]any{"n": 1}, map[string]any{"n": 2}, map[string]any{"n": 3}}
	b := bound(items, 2, nil)
	if len(b.items) != 2 || !b.truncated {
		t.Fatalf("kept %d, truncated=%v", len(b.items), b.truncated)
	}
	if b := bound(items, 3, nil); len(b.items) != 3 || b.truncated {
		t.Fatalf("a full packet within the limit was marked truncated: %+v", b)
	}
}

func TestBoundClipsLongStringsAndListsAndFlagsTruncation(t *testing.T) {
	refs := make([]any, 9)
	for i := range refs {
		refs[i] = map[string]any{"activity_id": uuidA}
	}
	long := strings.Repeat("é", maxStringRunes+50)
	b := bound([]any{map[string]any{"text": long, "evidence_refs": refs}}, 5, nil)
	if !b.truncated || len(b.items) != 1 {
		t.Fatalf("clipped item not reported: %+v", b)
	}
	var got struct {
		Text string `json:"text"`
		Refs []any  `json:"evidence_refs"`
	}
	if err := json.Unmarshal(b.items[0], &got); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(got.Text)); n != maxStringRunes+3 || !strings.HasSuffix(got.Text, "...") {
		t.Fatalf("text clipped to %d runes", n)
	}
	if len(got.Refs) != maxEvidenceRefs {
		t.Fatalf("evidence refs = %d, want %d", len(got.Refs), maxEvidenceRefs)
	}

	many := make([]any, maxArrayItems+5)
	for i := range many {
		many[i] = i
	}
	b = bound([]any{map[string]any{"list": many}}, 5, nil)
	var gotList struct {
		List []int `json:"list"`
	}
	_ = json.Unmarshal(b.items[0], &gotList)
	if len(gotList.List) != maxArrayItems || !b.truncated {
		t.Fatalf("list clipped to %d (truncated=%v)", len(gotList.List), b.truncated)
	}
}

func TestBoundCutsDeepNesting(t *testing.T) {
	var deep any = "leaf"
	for i := 0; i < maxDepth+4; i++ {
		deep = map[string]any{"x": deep}
	}
	b := bound([]any{deep}, 5, nil)
	if !b.truncated || len(b.items) != 1 {
		t.Fatalf("deep item: %+v", b)
	}
	if strings.Contains(string(b.items[0]), "leaf") {
		t.Fatal("nesting beyond maxDepth survived")
	}
}

func TestBoundDropsAnItemThatCannotFitAloneButKeepsTheRest(t *testing.T) {
	// 40 keys of 500 runes each cannot be clipped below the packet size.
	wide := map[string]any{}
	for i := 0; i < 40; i++ {
		wide["k"+string(rune('a'+i%26))+string(rune('a'+i/26))] = strings.Repeat("x", maxStringRunes)
	}
	b := bound([]any{wide, map[string]any{"ok": true}}, 5, nil)
	if len(b.items) != 1 || string(b.items[0]) != `{"ok":true}` || !b.truncated {
		t.Fatalf("items = %q truncated=%v", b.items, b.truncated)
	}
}

func TestBoundStopsAtTheByteBudgetAndNeverExceedsIt(t *testing.T) {
	var items []any
	for i := 0; i < 20; i++ {
		items = append(items, map[string]any{"n": i, "text": strings.Repeat("y", maxStringRunes), "more": strings.Repeat("z", maxStringRunes)})
	}
	b := bound(items, 20, nil)
	if !b.truncated || len(b.items) == 0 || len(b.items) == 20 {
		t.Fatalf("kept %d of 20 (truncated=%v)", len(b.items), b.truncated)
	}
	if b.bytes > MaxPacketBytes {
		t.Fatalf("bytes = %d exceeds %d", b.bytes, MaxPacketBytes)
	}
	for i, raw := range b.items { // the kept items are a prefix: order is intact
		if !strings.Contains(string(raw), `"n":`+itoa(i)+`,`) {
			t.Fatalf("item %d is %.20s", i, raw)
		}
	}
	whole, _ := json.Marshal(b.items)
	if b.bytes != len(whole) {
		t.Fatalf("bytes = %d, serialized = %d", b.bytes, len(whole))
	}
}

func itoa(i int) string { raw, _ := json.Marshal(i); return string(raw) }

func TestBoundWithholdsEveryShapeWhoseEvidenceIsHidden(t *testing.T) {
	hidden := map[string]bool{uuidA: true}
	secret := map[string]any{"activity_id": uuidA, "quote": "CFO approved a 30% floor"}
	visible := map[string]any{"activity_id": uuidB}
	field := map[string]any{"field_path": "current_commitments", "known": true, "value": []any{map[string]any{"text": "30% floor", "evidence_refs": []any{secret}}},
		"evidence_refs": []any{visible}}
	items := []any{
		map[string]any{"field_path": "stage", "known": true, "value": "Negotiation: 30% floor", "evidence_refs": []any{visible, secret}},
		map[string]any{"person_id": uuidA, "display_name": "Hidden person", "evidence_refs": []any{secret}},
		map[string]any{"text": "30% floor", "claim_id": uuidB, "evidence_refs": []any{secret}},
		map[string]any{"id": uuidB, "activity_ids": []any{uuidA, uuidB}, "changes": []any{
			map[string]any{"field": "objections", "op": "added", "before": nil, "after": "30% floor", "material": true, "evidence_refs": []any{secret}},
			map[string]any{"field": "stage", "op": "changed", "before": "a", "after": "b", "material": true, "evidence_refs": []any{visible}}}},
		field,
	}
	b := bound(items, 20, hidden)
	for i, raw := range b.items {
		text := string(raw)
		for _, leak := range []string{"30%", "CFO", "Hidden person", uuidA} {
			if strings.Contains(text, leak) {
				t.Errorf("item %d leaks %q: %s", i, leak, text)
			}
		}
	}
	if !b.truncated || len(b.items) != len(items) {
		t.Fatalf("truncated=%v items=%d", b.truncated, len(b.items))
	}
	var stage map[string]any
	_ = json.Unmarshal(b.items[0], &stage)
	if stage["known"] != false || stage["value"] != nil || stage["withheld"] != WithheldVisibility || stage["field_path"] != "stage" {
		t.Fatalf("withheld field = %v", stage)
	}
	if !strings.Contains(string(b.items[3]), `"withheld":"visibility"`) || !strings.Contains(string(b.items[3]), `"before":"a"`) {
		t.Fatalf("only the hidden change loses its values: %s", b.items[3])
	}
	if got := strings.Join(b.ids, ","); strings.Contains(got, uuidA) {
		t.Fatalf("returned ids include a hidden activity: %v", b.ids)
	}
}

func TestBoundWithholdsBeforeClippingEvidence(t *testing.T) {
	// The hidden reference is the sixth: clipping to five refs first would let it through.
	refs := []any{}
	for i := 0; i < maxEvidenceRefs; i++ {
		refs = append(refs, map[string]any{"activity_id": uuidB})
	}
	refs = append(refs, map[string]any{"activity_id": uuidA})
	b := bound([]any{map[string]any{"field_path": "stage", "known": true, "value": "secret", "evidence_refs": refs}}, 5, map[string]bool{uuidA: true})
	if strings.Contains(string(b.items[0]), "secret") {
		t.Fatalf("a hidden reference beyond the clip limit leaked the value: %s", b.items[0])
	}
}

func TestBoundCollectsReturnedIDsOnceInOrderFromIDKeysOnly(t *testing.T) {
	b := bound([]any{
		map[string]any{"claim_id": uuidB, "activity_id": uuidA, "evidence_refs": []any{map[string]any{"activity_id": uuidA}}},
		map[string]any{"person_id": uuidA, "note": uuidB, "id": "not-a-uuid"},
	}, 5, nil)
	if got := strings.Join(b.ids, ","); got != uuidA+","+uuidB {
		t.Fatalf("ids = %v", b.ids)
	}
}
