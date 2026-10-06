package demorun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// Spec describes one demo service. Env is an overlay on the real process environment; no method here prints it.
type Spec struct {
	Name   string
	Dir    string // working directory
	Path   string // executable
	Args   []string
	Env    Env
	Health Check
	Wait   WaitOptions
	URL    string // shown by `demo status`
	Port   int    // TCP port checked for a conflict before the service starts (0 = none)
	// Graceful, when > 0, stops the service by creating its stop file and waiting this long before the tree is
	// killed. Only the ghostctl-hosted stores (Postgres, Neo4j) watch the stop file.
	Graceful time.Duration
	// Cleanup runs after Stop whether or not the service was running (for example `pg_ctl stop` after a host
	// that was killed hard).
	Cleanup func() error
}

// Supervisor starts, stops and inspects detached services. Everything it writes lives under Layout.
type Supervisor struct {
	Layout Layout
	PIDs   PIDStore
	Out    io.Writer
}

func (s Supervisor) say(format string, args ...any) {
	if s.Out != nil {
		fmt.Fprintf(s.Out, format+"\n", args...)
	}
}

// StopFile is the path a graceful stop is requested through.
func (s Supervisor) StopFile(service string) string {
	return filepath.Join(s.Layout.PIDDir(), service+".stop")
}

// Start launches the service detached, records its PID and waits until its health check passes. A service that
// is already running is left alone (its health is still checked). On any failure the process is killed and
// forgotten and a *ServiceError names the service and its log.
func (s Supervisor) Start(ctx context.Context, spec Spec) error {
	state, rec := s.PIDs.State(spec.Name)
	if state == StateRunning {
		s.say("%s: already running (pid %d)", spec.Name, rec.PID)
		return s.wait(ctx, spec, rec.PID)
	}
	if state != StateStopped {
		_ = s.PIDs.Remove(spec.Name)
	}
	_ = os.Remove(s.StopFile(spec.Name))
	if err := os.MkdirAll(s.Layout.LogDir(), 0o755); err != nil {
		return &ServiceError{Service: spec.Name, Phase: "start", Err: err}
	}
	logPath := s.Layout.LogFile(spec.Name)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &ServiceError{Service: spec.Name, Phase: "start", Err: err}
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "--- demo start %s ---\n", time.Now().UTC().Format(time.RFC3339))

	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = Merge(ProcessEnv(), spec.Env).Environ()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return &ServiceError{Service: spec.Name, Phase: "start", Err: fmt.Errorf("cannot launch %s: %w", filepath.Base(spec.Path), err)}
	}
	pid := cmd.Process.Pid
	// Reap the child if it exits while this process is still alive (a zombie would look alive to kill(pid, 0)); when
	// this command exits first, the child is reparented and the goroutine dies with us.
	go func() { _ = cmd.Wait() }()
	if err := s.PIDs.Write(Record{Service: spec.Name, PID: pid, StartedAt: time.Now().UTC(), Log: logPath}); err != nil {
		_ = KillTree(pid)
		return &ServiceError{Service: spec.Name, Phase: "start", Err: err}
	}
	s.say("%s: started (pid %d), waiting for it to become healthy", spec.Name, pid)
	if err := s.wait(ctx, spec, pid); err != nil {
		_ = KillTree(pid)
		_ = s.PIDs.Remove(spec.Name)
		return err
	}
	s.say("%s: healthy", spec.Name)
	return nil
}

func (s Supervisor) wait(ctx context.Context, spec Spec, pid int) error {
	if spec.Health == nil {
		return nil
	}
	opts := spec.Wait
	opts.Alive = func() bool { return ProcessAlive(pid) }
	err := WaitHealthy(ctx, spec.Name, spec.Health, opts)
	var se *ServiceError
	if errors.As(err, &se) {
		return &ServiceError{Service: se.Service, Phase: se.Phase, Err: fmt.Errorf("%v (log: %s)", se.Err, s.Layout.LogFile(spec.Name))}
	}
	return err
}

// Stop ends the service and forgets its PID file. A service that is not running is not an error. With
// spec.Graceful the service is first asked to stop through its stop file.
func (s Supervisor) Stop(ctx context.Context, spec Spec) error {
	var errs []error
	state, rec := s.PIDs.State(spec.Name)
	switch state {
	case StateRunning:
		if err := s.stopProcess(ctx, spec, rec.PID); err != nil {
			errs = append(errs, &ServiceError{Service: spec.Name, Phase: "stop", Err: err})
		} else {
			s.say("%s: stopped", spec.Name)
		}
		_ = s.PIDs.Remove(spec.Name)
	case StateStopped:
	default:
		_ = s.PIDs.Remove(spec.Name)
	}
	_ = os.Remove(s.StopFile(spec.Name))
	if spec.Cleanup != nil {
		if err := spec.Cleanup(); err != nil {
			errs = append(errs, &ServiceError{Service: spec.Name, Phase: "stop", Err: err})
		}
	}
	return errors.Join(errs...)
}

func (s Supervisor) stopProcess(ctx context.Context, spec Spec, pid int) error {
	if spec.Graceful > 0 {
		if err := os.MkdirAll(s.Layout.PIDDir(), 0o755); err == nil {
			_ = os.WriteFile(s.StopFile(spec.Name), []byte("stop\n"), 0o600)
			if waitGone(ctx, pid, spec.Graceful) {
				return nil
			}
			s.say("%s: did not stop within %s, killing it", spec.Name, spec.Graceful)
		}
	}
	if err := KillTree(pid); err != nil {
		return err
	}
	if !waitGone(ctx, pid, 15*time.Second) {
		return fmt.Errorf("pid %d is still alive after the kill", pid)
	}
	return nil
}

func waitGone(ctx context.Context, pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return true
		}
		select {
		case <-ctx.Done():
			return !ProcessAlive(pid)
		case <-time.After(50 * time.Millisecond):
		}
	}
	return !ProcessAlive(pid)
}

// StatusRow is one line of `demo status`.
type StatusRow struct {
	Name    string
	State   State
	PID     int
	Uptime  time.Duration
	Healthy bool
	URL     string
}

// Status reports every service in the given order. Health is checked (briefly) only for running services.
func (s Supervisor) Status(ctx context.Context, specs []Spec) []StatusRow {
	rows := make([]StatusRow, 0, len(specs))
	for _, spec := range specs {
		state, rec := s.PIDs.State(spec.Name)
		row := StatusRow{Name: spec.Name, State: state, PID: rec.PID, URL: spec.URL}
		if state == StateRunning {
			row.Uptime = time.Since(rec.StartedAt).Round(time.Second)
			if spec.Health != nil {
				c, cancel := context.WithTimeout(ctx, 2*time.Second)
				row.Healthy = spec.Health(c) == nil
				cancel()
			} else {
				row.Healthy = true
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// WriteStatus prints the table. It never prints environment values.
func WriteStatus(w io.Writer, rows []StatusRow) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATE\tPID\tUPTIME\tHEALTH\tURL")
	for _, r := range rows {
		pid, up, health := "-", "-", "-"
		if r.State == StateRunning {
			pid, up = fmt.Sprint(r.PID), r.Uptime.String()
			health = map[bool]string{true: "ok", false: "UNHEALTHY"}[r.Healthy]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.State, pid, up, health, r.URL)
	}
	_ = tw.Flush()
}

const tailWindow = 256 * 1024

// Logs prints the last n lines of a service's log.
func (s Supervisor) Logs(service string, n int, w io.Writer) error {
	if !serviceName.MatchString(service) {
		return fmt.Errorf("demorun: invalid service name %q", service)
	}
	f, err := os.Open(s.Layout.LogFile(service))
	if os.IsNotExist(err) {
		return fmt.Errorf("demorun: no log for service %s yet (%s)", service, s.Layout.LogFile(service))
	}
	if err != nil {
		return fmt.Errorf("demorun: open log of %s: %w", service, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	start := int64(0)
	if info.Size() > tailWindow {
		start = info.Size() - tailWindow
	}
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return fmt.Errorf("demorun: read log of %s: %w", service, err)
	}
	lines := strings.Split(strings.TrimRight(string(bytes.ReplaceAll(buf, []byte("\r\n"), []byte("\n"))), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	_, err = fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
