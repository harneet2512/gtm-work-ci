package codespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BaselineVersion tags the manifest format.
const BaselineVersion = "ghost-demo-baseline.v1"

// ErrNoBaseline: nothing sealed, or what is sealed does not match its manifest. Start and Reset never rebuild to repair it.
var ErrNoBaseline = errors.New("codespace: the sealed baseline is missing or does not match its manifest")

const setupHint = "run the one-time setup once (scripts\\demo\\setup-local.ps1); Start and Reset never rebuild"

// Component is one sealed piece of state, named by its path under the demo home.
type Component struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // dir | file
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Layout  string `json:"layout_sha256"`  // names and sizes: what the fast check compares
	Content string `json:"content_sha256"` // every byte: what the deep check compares
}

// CacheNote records the model-call cache at sealing time. The cache lives outside the baseline and the live copy, grows with
// use and is never restored, so it is noted, not enforced.
type CacheNote struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// Manifest lists every sealed component and when the baseline was built.
type Manifest struct {
	Version    string        `json:"version"`
	BuiltAt    time.Time     `json:"built_at"`
	Components []Component   `json:"components"`
	History    []CaseHistory `json:"history,omitempty"`
	Cache      *CacheNote    `json:"model_cache,omitempty"`
}

// Component finds a component by path; nil when absent.
func (m Manifest) Component(p string) *Component {
	for i := range m.Components {
		if m.Components[i].Path == p {
			return &m.Components[i]
		}
	}
	return nil
}

// Baseline is the sealed state of the demo at Event N-1: built once by the one-time setup (and the record run), restored by
// Start and Reset. It mirrors the chosen paths of the demo home under <home>\baseline. Nothing here builds, migrates or calls a model.
type Baseline struct {
	Home string
	// Paths are the state to seal, slash-separated and relative to Home (the Postgres cluster, each graph, the case files, ...).
	Paths []string
	// Cases are the case slots whose history facts are recorded and checked (their files are among Paths, under CasesRel).
	Cases []string
	// CasesRel is the slash-separated directory of the per-case files under Home ("live/cases" when empty).
	CasesRel string
	Now      func() time.Time
	// Retries and Backoff govern file locks on Windows (Postgres, Neo4j, the antivirus scanner): defaults 10 and 300 ms.
	Retries int
	Backoff time.Duration

	rename func(from, to string) error // os.Rename unless a test replaces it
}

// Dir is the sealed baseline directory.
func (b Baseline) Dir() string { return filepath.Join(b.Home, "baseline") }

// ManifestPath is the baseline's manifest file.
func (b Baseline) ManifestPath() string { return filepath.Join(b.Dir(), "manifest.json") }

func (b Baseline) casesRel() string {
	if b.CasesRel != "" {
		return filepath.FromSlash(b.CasesRel)
	}
	return filepath.Join("live", "cases")
}

func (b Baseline) abs(rel string) string { return filepath.Join(b.Home, filepath.FromSlash(rel)) }

func (b Baseline) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

func (b Baseline) ren(from, to string) error {
	if b.rename != nil {
		return b.rename(from, to)
	}
	return os.Rename(from, to)
}

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s; %s", ErrNoBaseline, fmt.Sprintf(format, args...), setupHint)
}

// Manifest reads the baseline's manifest.
func (b Baseline) Manifest() (Manifest, error) {
	raw, err := os.ReadFile(b.ManifestPath())
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, bad("no baseline has been sealed under %s", b.Dir())
	}
	if err != nil {
		return Manifest{}, bad("read the manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, bad("the manifest is not valid JSON")
	}
	if m.Version != BaselineVersion {
		return Manifest{}, bad("the manifest has version %q, want %q", m.Version, BaselineVersion)
	}
	return m, nil
}

// Verify is the fast check Start and Reset make before touching anything: every sealed component is present with the file
// count, size and layout the manifest recorded, and each case's history stops at N-1.
func (b Baseline) Verify() error { return b.verify(false) }

// VerifyDeep also reads every byte (it catches a corruption that keeps the size).
func (b Baseline) VerifyDeep() error { return b.verify(true) }

func (b Baseline) verify(deep bool) error {
	m, err := b.Manifest()
	if err != nil {
		return err
	}
	if len(m.Components) == 0 {
		return bad("the manifest lists no component")
	}
	for _, c := range m.Components {
		got, err := fingerprint(filepath.Join(b.Dir(), filepath.FromSlash(c.Path)), deep)
		if err != nil {
			return bad("component %s: %v", c.Path, err)
		}
		if got.Files != c.Files || got.Bytes != c.Bytes || got.Layout != c.Layout {
			return bad("component %s holds %d files / %d bytes, the manifest recorded %d / %d", c.Path, got.Files, got.Bytes, c.Files, c.Bytes)
		}
		if deep && got.Content != c.Content {
			return bad("component %s has the recorded size but different content", c.Path)
		}
	}
	for _, h := range m.History {
		if err := checkSealedCase(filepath.Join(b.Dir(), b.casesRel()), h); err != nil {
			return bad("%v", err)
		}
	}
	return nil
}

// Seal copies the current state into a new baseline and swaps it in. It is the only place a baseline is made, and it must
// run with the stores stopped (a running Postgres or Neo4j would be copied half-written).
func (b Baseline) Seal(ctx context.Context) (Manifest, error) {
	if err := b.checkPaths(b.Paths); err != nil {
		return Manifest{}, err
	}
	tmp := b.Dir() + ".tmp"
	if err := b.removeAll(ctx, tmp); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: BaselineVersion, BuiltAt: b.now()}
	for _, p := range b.Paths {
		c, err := b.copyComponent(ctx, tmp, p)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return Manifest{}, err
		}
		m.Components = append(m.Components, c)
	}
	for _, slot := range b.Cases {
		h, err := ReadCaseHistory(filepath.Join(tmp, b.casesRel(), slot, "manifest.json"), slot)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return Manifest{}, err
		}
		if err := checkSealedCase(filepath.Join(tmp, b.casesRel()), h); err != nil {
			_ = os.RemoveAll(tmp)
			return Manifest{}, err
		}
		m.History = append(m.History, h)
	}
	if err := writeManifest(filepath.Join(tmp, "manifest.json"), m); err != nil {
		return Manifest{}, err
	}
	old := b.Dir() + ".old"
	if err := b.removeAll(ctx, old); err != nil {
		return Manifest{}, err
	}
	if _, err := os.Stat(b.Dir()); err == nil {
		if err := b.retry(ctx, func() error { return b.ren(b.Dir(), old) }); err != nil {
			return Manifest{}, fmt.Errorf("codespace: set the earlier baseline aside: %w", err)
		}
	}
	if err := b.retry(ctx, func() error { return b.ren(tmp, b.Dir()) }); err != nil {
		return Manifest{}, fmt.Errorf("codespace: put the new baseline in place: %w", err)
	}
	_ = b.removeAll(ctx, old)
	return m, nil
}

// checkPaths validates what is to be sealed: confined to Home, present, and not a running Postgres.
func (b Baseline) checkPaths(paths []string) error {
	if len(paths) == 0 {
		return errors.New("codespace: nothing to seal")
	}
	for _, p := range paths {
		if p == "" || path.IsAbs(p) || filepath.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "..") {
			return fmt.Errorf("codespace: %q is not a path inside the demo home", p)
		}
		if _, err := os.Stat(b.abs(p)); err != nil {
			return fmt.Errorf("codespace: the state to seal is missing: %s (%v)", p, err)
		}
		if _, err := os.Stat(filepath.Join(b.abs(p), "postmaster.pid")); err == nil {
			return fmt.Errorf("codespace: Postgres is still running (%s/postmaster.pid); stop the demo before sealing", p)
		}
	}
	return nil
}

// copyComponent copies Home/rel to root/rel and describes it, hashing the bytes as it goes.
func (b Baseline) copyComponent(ctx context.Context, root, rel string) (Component, error) {
	src, dst := b.abs(rel), filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(src)
	if err != nil {
		return Component{}, fmt.Errorf("codespace: the state to seal is missing: %s (%v)", rel, err)
	}
	p, err := copyTree(ctx, src, dst, true)
	if err != nil {
		return Component{}, fmt.Errorf("codespace: seal %s: %w", rel, err)
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	}
	return Component{Path: rel, Kind: kind, Files: p.Files, Bytes: p.Bytes, Layout: p.Layout, Content: p.Content}, nil
}

func writeManifest(file string, m Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// Include adds (or refreshes) one more sealed path, such as the run sheet the record run writes after the baseline was built.
func (b Baseline) Include(ctx context.Context, rel string) error {
	if err := b.checkPaths([]string{rel}); err != nil {
		return err
	}
	m, err := b.Manifest()
	if err != nil {
		return err
	}
	staging := b.Dir() + ".include"
	if err := b.removeAll(ctx, staging); err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	c, err := b.copyComponent(ctx, staging, rel)
	if err != nil {
		return err
	}
	final := filepath.Join(b.Dir(), filepath.FromSlash(rel))
	if err := b.removeAll(ctx, final); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	if err := b.retry(ctx, func() error { return b.ren(filepath.Join(staging, filepath.FromSlash(rel)), final) }); err != nil {
		return err
	}
	replaced := false
	for i := range m.Components {
		if m.Components[i].Path == rel {
			m.Components[i], replaced = c, true
		}
	}
	if !replaced {
		m.Components = append(m.Components, c)
	}
	return writeManifest(b.ManifestPath(), m)
}

// NoteCache records the model-call cache (a directory under Home) in the manifest. It is never copied or checked.
func (b Baseline) NoteCache(rel string) error {
	m, err := b.Manifest()
	if err != nil {
		return err
	}
	p, err := fingerprint(b.abs(rel), false)
	if err != nil {
		return fmt.Errorf("codespace: read the model-call cache: %w", err)
	}
	m.Cache = &CacheNote{Path: rel, Files: p.Files, Bytes: p.Bytes}
	return writeManifest(b.ManifestPath(), m)
}
