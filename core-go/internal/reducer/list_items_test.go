package reducer

import (
	"encoding/json"
	"testing"
)

func TestListItemsReadsTypedAndJSONDecodedValues(t *testing.T) {
	typed := Field{Known: true, Value: []Item{{Text: "SOC2", ClaimID: "c1", Status: "open"}}}
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Field
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]Field{"typed": typed, "decoded": decoded} {
		items := f.ListItems()
		if len(items) != 1 || items[0].Text != "SOC2" || items[0].Status != "open" || items[0].ClaimID != "c1" {
			t.Errorf("%s: %+v", name, items)
		}
	}
}

func TestListItemsIsNilForNonListsAndUnknowns(t *testing.T) {
	for name, f := range map[string]Field{
		"unknown":       {Value: "unknown"},
		"known string":  {Known: true, Value: "Discovery"},
		"known nil":     {Known: true, Value: nil},
		"not a list":    {Known: true, Value: map[string]any{"a": 1}},
		"unmarshalable": {Known: true, Value: make(chan int)},
	} {
		if got := f.ListItems(); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
