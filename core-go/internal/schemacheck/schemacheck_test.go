package schemacheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewFailsOutsideTheRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := New(); err == nil {
		t.Fatal("New succeeded without a contracts/ directory above the working directory")
	}
}

func TestValidatesTheAccountStateExampleAndRejectsABrokenOne(t *testing.T) {
	v, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findContracts()
	if err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(filepath.Join(root, "examples", "account_state.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("account_state", good); err != nil {
		t.Fatalf("example rejected: %v", err)
	}
	if err := v.Validate("account_state", []byte(`{"account_id":"nope"}`)); err == nil {
		t.Fatal("an invalid document was accepted")
	}
	if err := v.Validate("account_state", []byte(`{`)); err == nil {
		t.Fatal("unparseable JSON was accepted")
	}
	if err := v.Validate("no_such_schema", good); err == nil {
		t.Fatal("an unknown schema name was accepted")
	}
}
