package codespace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// Phases of the readiness status the operator section shows.
const (
	PhaseReady        = "ready"
	PhaseStarting     = "starting"
	PhaseBusy         = "busy"
	PhaseSetup        = "setup"
	PhaseNeedsSecrets = "needs-secrets"
	PhaseAttention    = "attention"
)

// ReadyMessage is the line the demo operator waits for.
const ReadyMessage = "All systems ready"

// invisibilityTTL bounds how often a status poll asks core for the (database and graph reading) assertion.
const invisibilityTTL = 20 * time.Second

// ServiceStatus is one supervised service. It carries no URL, PID or environment: only what the operator section needs.
type ServiceStatus struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Healthy bool   `json:"healthy"`
}

// CaseStatus is one demo case. The ids are what the web needs to link to the replay and account pages.
type CaseStatus struct {
	Slot          string `json:"slot"`
	Label         string `json:"label"`
	Seeded        bool   `json:"seeded"`
	Active        bool   `json:"active"`
	ManifestID    string `json:"manifest_id,omitempty"`
	AccountID     string `json:"account_id,omitempty"`
	OpportunityID string `json:"opportunity_id,omitempty"`
	// Invisibility is core's event-N-invisible status, read for the active case only: withheld (before Play),
	// released (after Play), leaked (a defect), or unknown when core did not answer.
	Invisibility string `json:"invisibility,omitempty"`
}

// JobStatus is the control service's current or last operation (Reset demo, Switch case).
type JobStatus struct {
	Op        string    `json:"op"`
	Step      string    `json:"step"`
	Running   bool      `json:"running"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// Status is the readiness document: GET /status of the control service and, through the web server, /api/status.
type Status struct {
	Ready          bool            `json:"ready"`
	Phase          string          `json:"phase"`
	Message        string          `json:"message"`
	Services       []ServiceStatus `json:"services"`
	Cases          []CaseStatus    `json:"cases"`
	ActiveCase     string          `json:"active_case,omitempty"`
	Job            *JobStatus      `json:"job,omitempty"`
	MissingSecrets []string        `json:"missing_secrets,omitempty"`
	// LLM says whether the model calls are being replayed from the recorded run or a new one is being recorded.
	LLM       *LLMStatus `json:"llm,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
}

// StatusSource computes Status from reality (process table, health checks, database state), never from a remembered
// "boot finished" flag, so a crashed service turns the readiness line from ready to starting.
type StatusSource struct {
	Ops Ops
	// Rows reports the supervised services in start order (Supervisor.Status over the plan's specs).
	Rows func(ctx context.Context) []demorun.StatusRow
	// Required are the services that must be healthy; the rest of Rows is informational.
	Required []string
	// Secrets are the Codespaces secrets that must be set; Present says whether one is.
	Secrets []string
	Present func(name string) bool
	// Invisibility asks core for the assertion of a manifest ("" and an error when core does not answer).
	Invisibility func(ctx context.Context, manifestID string) (string, error)
	Job          func() *JobStatus
	// BootError is why the last start-up failed ("" when it did not): without it a failed service would read as
	// "Starting ..." forever.
	BootError func() string
	// LLM reads the worker's cache activity (nil when the cache is not in use).
	LLM func() *LLMStatus
	Now func() time.Time

	mu      sync.Mutex
	invMemo map[string]invMemo
}

type invMemo struct {
	status string
	at     time.Time
}

func (s *StatusSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Compute builds the status. It is safe for concurrent use.
func (s *StatusSource) Compute(ctx context.Context) Status {
	st := Status{CheckedAt: s.now().UTC()}
	if s.Job != nil {
		st.Job = s.Job()
	}
	for _, name := range s.Secrets {
		if s.Present == nil || !s.Present(name) {
			st.MissingSecrets = append(st.MissingSecrets, name)
		}
	}
	active, _ := ReadMarker(s.Ops.Paths.ActiveFile())
	st.ActiveCase = active
	if s.LLM != nil {
		st.LLM = s.LLM()
	}
	st.Cases = s.cases(ctx, active)
	healthy := map[string]bool{}
	for _, r := range s.Rows(ctx) {
		st.Services = append(st.Services, ServiceStatus{Name: r.Name, State: r.State.String(), Healthy: r.Healthy})
		healthy[r.Name] = r.Healthy
	}
	st.Phase, st.Message = s.verdict(st, healthy)
	st.Ready = st.Phase == PhaseReady
	return st
}

func (s *StatusSource) cases(ctx context.Context, active string) []CaseStatus {
	out := make([]CaseStatus, 0, len(s.Ops.Cases))
	for _, c := range s.Ops.Cases {
		cs := CaseStatus{Slot: c.Slot, Label: c.Label, Active: c.Slot == active, OpportunityID: c.OpportunityID}
		if ds, ok, _ := s.Ops.SeedState(c.Slot); ok {
			cs.Seeded, cs.ManifestID, cs.AccountID = true, ds.ManifestID, ds.AccountID
			if ds.CaseName != "" {
				cs.Label = ds.CaseName
			}
			if cs.Active {
				cs.Invisibility = s.invisibility(ctx, ds.ManifestID)
			}
		}
		out = append(out, cs)
	}
	return out
}

func (s *StatusSource) invisibility(ctx context.Context, manifestID string) string {
	if s.Invisibility == nil {
		return "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.invMemo[manifestID]; ok && s.now().Sub(m.at) < invisibilityTTL {
		return m.status
	}
	status, err := s.Invisibility(ctx, manifestID)
	if err != nil || status == "" {
		return "unknown" // not cached: core may only be starting
	}
	if s.invMemo == nil {
		s.invMemo = map[string]invMemo{}
	}
	s.invMemo[manifestID] = invMemo{status: status, at: s.now()}
	return status
}

// Forget drops the cached assertions; a Reset or a case switch calls it so the next poll reads the new world.
func (s *StatusSource) Forget() {
	s.mu.Lock()
	s.invMemo = nil
	s.mu.Unlock()
}

// verdict is the one line the operator section shows and whether the demo can start, most blocking cause first.
func (s *StatusSource) verdict(st Status, healthy map[string]bool) (phase, message string) {
	if st.Job != nil && st.Job.Running {
		return PhaseBusy, fmt.Sprintf("%s: %s", st.Job.Op, st.Job.Step)
	}
	if len(st.MissingSecrets) > 0 {
		return PhaseNeedsSecrets, "Missing Codespaces secrets: " + strings.Join(st.MissingSecrets, ", ") + " (see docs/demo/codespace.md)"
	}
	var unseeded []string
	for _, c := range st.Cases {
		if !c.Seeded {
			unseeded = append(unseeded, c.Label)
		}
	}
	if len(unseeded) > 0 {
		return PhaseSetup, "First-time setup has not finished: " + strings.Join(unseeded, ", ") + " not frozen yet"
	}
	for _, name := range s.Required {
		if !healthy[name] {
			if s.BootError != nil {
				if why := s.BootError(); why != "" {
					return PhaseAttention, "Start-up failed: " + why + ". Stop and start the codespace to retry; logs are in .demo/logs."
				}
			}
			return PhaseStarting, "Starting " + name + "..."
		}
	}
	active := activeCase(st)
	if active == nil {
		return PhaseStarting, "Choosing the active case..."
	}
	switch active.Invisibility {
	case "withheld", "released":
		return PhaseReady, ReadyMessage
	case "leaked":
		return PhaseAttention, "Event N is visible before Play in " + active.Label + ". Use Reset demo."
	}
	return PhaseStarting, "Checking that Event N is withheld..."
}

func activeCase(st Status) *CaseStatus {
	for i := range st.Cases {
		if st.Cases[i].Active {
			return &st.Cases[i]
		}
	}
	return nil
}
