package neo4jtest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

func TestParseJavaMajor(t *testing.T) {
	cases := map[string]int{
		`openjdk version "21.0.4" 2024-07-16`:  21,
		`openjdk version "17" 2021-09-14`:      17,
		`java version "1.8.0_402"`:             8,
		`openjdk version "22.0.1" 2024-04-16`:  22,
		`openjdk version "17.0.12" 2024-07-16`: 17,
	}
	for in, want := range cases {
		got, err := parseJavaMajor(in)
		if err != nil || got != want {
			t.Errorf("parseJavaMajor(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := parseJavaMajor("not java"); err == nil {
		t.Error("garbage output must be an error")
	}
}

func TestMissingJavaIsASkipWithAClearMessage(t *testing.T) {
	t.Setenv("JAVA_HOME", filepath.Join(t.TempDir(), "nowhere"))
	t.Setenv("PATH", t.TempDir())
	_, _, err := findJava(context.Background())
	if !errors.Is(err, ErrSkip) {
		t.Fatalf("err = %v, want a skip", err)
	}
	if !strings.Contains(err.Error(), "Java") {
		t.Errorf("message does not name Java: %v", err)
	}
}

func TestUnsafeExternalURIsAreRefused(t *testing.T) {
	if err := checkSafeURI("bolt://db.example.com:7687", ""); err == nil {
		t.Error("a remote host must be refused")
	}
	if err := checkSafeURI("bolt://127.0.0.1:7687", "bolt://127.0.0.1:7687"); err == nil {
		t.Error("the dev URI must be refused")
	}
	if err := checkSafeURI("bolt://localhost:7687", ""); err != nil {
		t.Errorf("loopback refused: %v", err)
	}
	t.Setenv("NEO4J_TEST_ALLOW_REMOTE", "1")
	if err := checkSafeURI("bolt://db.example.com:7687", ""); err != nil {
		t.Errorf("explicit remote opt-in refused: %v", err)
	}
}

// tarball builds a one-file archive laid out like the distribution.
func tarball(t *testing.T, name string) []byte {
	t.Helper()
	var sb strings.Builder
	gz := gzip.NewWriter(&sb)
	tw := tar.NewWriter(gz)
	body := []byte("x")
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(sb.String())
}

func serve(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv
}

func TestChecksumMismatchIsAHardErrorAndDeletesTheArchive(t *testing.T) {
	srv := serve(t, tarball(t, distDirName()+"/lib/a.jar"))
	cache := t.TempDir()
	_, err := Fetcher{CacheDir: cache, URL: srv.URL, SHA256: strings.Repeat("0", 64)}.Ensure(context.Background())
	if err == nil || errors.Is(err, ErrSkip) || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a hard checksum error", err)
	}
	if _, statErr := os.Stat(filepath.Join(cache, archiveName())); !os.IsNotExist(statErr) {
		t.Error("the corrupt archive was kept")
	}
}

func TestEnsureDownloadsVerifiesAndExtracts(t *testing.T) {
	body := tarball(t, distDirName()+"/lib/a.jar")
	sum := sha256.Sum256(body)
	srv := serve(t, body)
	cache := t.TempDir()
	f := Fetcher{CacheDir: cache, URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}
	home, err := f.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "lib", "a.jar")); err != nil {
		t.Fatalf("not extracted: %v", err)
	}
	srv.Close() // a second call must come from the cache
	if _, err := f.Ensure(context.Background()); err != nil {
		t.Fatalf("cached call failed: %v", err)
	}
}

func TestArchiveEntriesCannotEscapeTheCache(t *testing.T) {
	body := tarball(t, "../evil.txt")
	cache := t.TempDir()
	archive := filepath.Join(cache, "a.tgz")
	if err := os.WriteFile(archive, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(archive, filepath.Join(cache, "out")); err == nil {
		t.Fatal("path traversal was not refused")
	}
}

func TestPinnedChecksumLooksLikeSHA256(t *testing.T) {
	if len(ArchiveSHA) != 64 {
		t.Fatalf("ArchiveSHA has %d chars", len(ArchiveSHA))
	}
}

func TestStartedNeo4jRequiresTheGeneratedPassword(t *testing.T) {
	env := Require(t)
	ctx := context.Background()
	good, err := neo4j.NewDriverWithContext(env.URI, neo4j.BasicAuth(env.User, env.Password, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close(ctx)
	res, err := neo4j.ExecuteQuery(ctx, good, "RETURN 41 + 1 AS n", nil, neo4j.EagerResultTransformer)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.Records[0].Get("n"); n != int64(42) {
		t.Fatalf("n = %v", n)
	}
	if env.External {
		return
	}
	bad, err := neo4j.NewDriverWithContext(env.URI, neo4j.BasicAuth(env.User, "wrong-password", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close(ctx)
	if err := bad.VerifyConnectivity(ctx); err == nil {
		t.Fatal("a wrong password was accepted: auth is not enabled")
	}
}

func TestInstallRetriesARenameThatWindowsRefusesWhileAScannerHoldsTheFiles(t *testing.T) {
	fails := 2
	var calls int
	err := renameWithRetry("a", "b", 5, 0, func(string, string) error {
		calls++
		if fails > 0 {
			fails--
			return errors.New("rename a b: Access is denied.")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d: two refusals then success", err, calls)
	}
	calls = 0
	err = renameWithRetry("a", "b", 3, 0, func(string, string) error { calls++; return errors.New("Access is denied.") })
	if err == nil || calls != 3 || !strings.Contains(err.Error(), "Access is denied") {
		t.Fatalf("a persistent refusal is reported after the last attempt: err=%v calls=%d", err, calls)
	}
}
