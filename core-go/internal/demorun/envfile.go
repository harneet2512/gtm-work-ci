// Package demorun is the one-command live demo runner behind `ghostctl demo` (HAR-137 live smoke, HAR-129).
// It starts the real integrated system on a laptop without Docker (Neo4j tarball, embedded Postgres, the
// Python worker, core, the Slack bot and the web app), seeds the frozen demo case, plays Event N and reads
// the human-supervision rows back.
//
// Secrets are handled by one rule: values live only in Env maps and child-process environments, and Env
// never formats a value (String/GoString/Format print names only), so a stray %v cannot leak one.
package demorun

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Env is a set of environment variables. Its formatting methods print variable NAMES only, never values:
// any fmt verb (%v, %+v, %#v, %s, %q) renders "Env{NAME1 NAME2 ...}".
type Env map[string]string

// Names returns the variable names, sorted.
func (e Env) Names() []string {
	names := make([]string, 0, len(e))
	for k := range e {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (e Env) describe() string { return "Env{" + strings.Join(e.Names(), " ") + "}" }

// String implements fmt.Stringer without values.
func (e Env) String() string { return e.describe() }

// GoString implements fmt.GoStringer without values.
func (e Env) GoString() string { return e.describe() }

// Format implements fmt.Formatter so no verb can reach the map's values.
func (e Env) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, e.describe()) }

// Environ renders KEY=VALUE pairs for exec.Cmd.Env, sorted, skipping empty names.
func (e Env) Environ() []string {
	out := make([]string, 0, len(e))
	for _, k := range e.Names() {
		if k != "" {
			out = append(out, k+"="+e[k])
		}
	}
	return out
}

// Merge returns a new Env: every later argument overrides the earlier ones. Inputs are not modified.
func Merge(layers ...Env) Env {
	out := Env{}
	for _, l := range layers {
		for k, v := range l {
			out[k] = v
		}
	}
	return out
}

// ProcessEnv is the current process environment without empty values (an empty variable must not shadow
// a value from .env, matching core's own config loader).
func ProcessEnv() Env {
	out := Env{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// ParseEnv reads dotenv text: KEY=VALUE per line, `#` comments, an optional `export ` prefix, and one level
// of matching surrounding quotes. Lines without "=" are skipped. Errors never quote file content.
func ParseEnv(r io.Reader) (Env, error) {
	out := Env{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
			first = false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = unquote(strings.TrimSpace(v))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("demorun: read env text: %w", err)
	}
	return out, nil
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// LoadEnvFile reads a dotenv file. A missing file is not an error (found=false): the caller decides whether
// the variables it needed are all present. The returned error names the path only.
func LoadEnvFile(path string) (env Env, found bool, err error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Env{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("demorun: open %s: %w", path, err)
	}
	defer f.Close()
	env, err = ParseEnv(f)
	if err != nil {
		return nil, true, fmt.Errorf("demorun: parse %s: %w", path, err)
	}
	return env, true, nil
}
