package signalstore

import "testing"

func TestUUIDArrayQuotesEveryElement(t *testing.T) {
	got := UUIDArray([]string{`a,b`, `c"d`, `NULL`, `e\f`})
	want := `{"a,b","c\"d","NULL","e\\f"}`
	if got != want || UUIDArray(nil) != "{}" {
		t.Fatalf("got %s", got)
	}
}
