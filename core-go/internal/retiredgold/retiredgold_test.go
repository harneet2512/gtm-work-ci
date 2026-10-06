package retiredgold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/retiredgold"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestADirWithoutAMarkerIsNotRetired(t *testing.T) {
	n, err := retiredgold.Read(t.TempDir())
	if err != nil || n != nil {
		t.Fatalf("Read = %+v, %v; want nil, nil", n, err)
	}
}

func TestAMissingDirIsNotRetiredEither(t *testing.T) {
	if n, err := retiredgold.Read(filepath.Join(t.TempDir(), "absent")); err != nil || n != nil {
		t.Fatalf("Read = %+v, %v", n, err)
	}
}

func TestAnInventedMarkerRetiresTheDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "RETIRED.json", `{"retired":"invented","reason":"invented companies","cases":87,"accounts":["Acme Corp"]}`)
	n, err := retiredgold.Read(dir)
	if err != nil || n == nil {
		t.Fatalf("Read = %+v, %v", n, err)
	}
	if n.Retired != "invented" || n.Dir != dir || n.Cases != 87 || !strings.Contains(n.Reason, "invented") {
		t.Fatalf("notice = %+v", n)
	}
	if msg := n.NoGold(); !strings.HasPrefix(msg, "no gold: ") || !strings.Contains(msg, dir) {
		t.Fatalf("NoGold = %q", msg)
	}
}

func TestAMarkerThatDoesNotSayWhyIsAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "RETIRED.json", `{"retired":""}`)
	if _, err := retiredgold.Read(dir); err == nil {
		t.Fatal("an unreadable marker must not quietly un-retire the gold")
	}
	write(t, dir, "RETIRED.json", `not json`)
	if _, err := retiredgold.Read(dir); err == nil {
		t.Fatal("a broken marker must be an error")
	}
}

func TestSplitSeparatesLiveFromRetiredDirs(t *testing.T) {
	live, gone := t.TempDir(), t.TempDir()
	write(t, gone, "RETIRED.json", `{"retired":"invented","reason":"r","cases":1}`)
	keep, notices, err := retiredgold.Split([]string{live, gone, filepath.Join(live, "absent")})
	if err != nil {
		t.Fatal(err)
	}
	if len(keep) != 2 || keep[0] != live || len(notices) != 1 || notices[0].Dir != gone {
		t.Fatalf("keep %v notices %+v", keep, notices)
	}
}
