package demorun

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	// ErrNotRecorded: no PID file exists for the service.
	ErrNotRecorded = errors.New("demorun: no pid file for the service")
	// ErrAlreadyRunning: a live process is already recorded for the service.
	ErrAlreadyRunning = errors.New("demorun: the service is already running")
	// ErrCorrupt: the PID file exists but cannot be read.
	ErrCorrupt = errors.New("demorun: the pid file is unreadable")
)

var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

const pidSuffix = ".pid.json"

// Record is what the runner knows about one started service. It never holds environment values.
type Record struct {
	Service   string    `json:"service"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Log       string    `json:"log,omitempty"`
}

// State is the liveness of a recorded service.
type State int

const (
	// StateStopped: no PID file.
	StateStopped State = iota
	// StateRunning: the recorded process is alive.
	StateRunning
	// StateStale: a PID file exists but its process is gone (a crash or a reboot).
	StateStale
	// StateCorrupt: the PID file cannot be parsed.
	StateCorrupt
)

func (s State) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateStale:
		return "stale"
	case StateCorrupt:
		return "corrupt"
	default:
		return "stopped"
	}
}

// PIDStore keeps one JSON file per service under Dir. Alive decides liveness (ProcessAlive when nil), so
// the logic is testable without real processes.
type PIDStore struct {
	Dir   string
	Alive func(pid int) bool
}

func (s PIDStore) alive(pid int) bool {
	if s.Alive != nil {
		return s.Alive(pid)
	}
	return ProcessAlive(pid)
}

func (s PIDStore) path(service string) (string, error) {
	if !serviceName.MatchString(service) {
		return "", fmt.Errorf("demorun: invalid service name %q", service)
	}
	return filepath.Join(s.Dir, service+pidSuffix), nil
}

// Write records a started service atomically. It refuses to replace a record whose process is alive
// (ErrAlreadyRunning) and replaces a stale one.
func (s PIDStore) Write(r Record) error {
	p, err := s.path(r.Service)
	if err != nil {
		return err
	}
	if r.PID <= 0 {
		return fmt.Errorf("demorun: invalid pid %d for %s", r.PID, r.Service)
	}
	if st, _ := s.State(r.Service); st == StateRunning {
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, r.Service)
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("demorun: create pid dir: %w", err)
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("demorun: encode pid record: %w", err)
	}
	tmp, err := os.CreateTemp(s.Dir, r.Service+".*.tmp")
	if err != nil {
		return fmt.Errorf("demorun: create pid temp file: %w", err)
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("demorun: write pid file: %w", errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("demorun: install pid file: %w", err)
	}
	return nil
}

// Read returns the recorded process: ErrNotRecorded when there is no file, ErrCorrupt when it is garbage.
func (s PIDStore) Read(service string) (Record, error) {
	p, err := s.path(service)
	if err != nil {
		return Record{}, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Record{}, fmt.Errorf("%w: %s", ErrNotRecorded, service)
	}
	if err != nil {
		return Record{}, fmt.Errorf("demorun: read pid file of %s: %w", service, err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil || r.PID <= 0 || r.Service != service {
		return Record{}, fmt.Errorf("%w: %s", ErrCorrupt, service)
	}
	return r, nil
}

// State classifies the service. The record is returned when one could be read.
func (s PIDStore) State(service string) (State, Record) {
	rec, err := s.Read(service)
	switch {
	case errors.Is(err, ErrNotRecorded):
		return StateStopped, Record{}
	case err != nil:
		return StateCorrupt, Record{}
	case s.alive(rec.PID):
		return StateRunning, rec
	default:
		return StateStale, rec
	}
}

// Remove deletes the service's PID file. A missing file is not an error.
func (s PIDStore) Remove(service string) error {
	p, err := s.path(service)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("demorun: remove pid file of %s: %w", service, err)
	}
	return nil
}

// List returns the services that have a PID file, sorted.
func (s PIDStore) List() []string {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), pidSuffix); ok && serviceName.MatchString(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
