package demorun

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Check reports whether a service is healthy now (nil) or why not.
type Check func(ctx context.Context) error

// ServiceError names the service that failed and what phase it failed in. `demo up` stops on the first one,
// so the user always learns which service to look at.
type ServiceError struct {
	Service string
	Phase   string // "start", "health" or "stop"
	Err     error
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("demo: service %s failed to %s: %v", e.Service, e.Phase, e.Err)
}

func (e *ServiceError) Unwrap() error { return e.Err }

// WaitOptions bound a health wait.
type WaitOptions struct {
	Timeout      time.Duration // overall limit; default 30s
	Interval     time.Duration // pause between attempts; default 500ms
	CheckTimeout time.Duration // limit of one attempt; default 5s
	// Alive, when set, is asked before every attempt: false means the process died, so the wait fails at once
	// instead of burning the whole timeout.
	Alive func() bool
}

func (o WaitOptions) withDefaults() WaitOptions {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Interval <= 0 {
		o.Interval = 500 * time.Millisecond
	}
	if o.CheckTimeout <= 0 {
		o.CheckTimeout = 5 * time.Second
	}
	return o
}

// WaitHealthy polls check until it passes, the timeout elapses, the process dies or ctx ends. Failures are
// *ServiceError values naming the service; a cancelled ctx returns ctx.Err() unwrapped from any ServiceError.
func WaitHealthy(ctx context.Context, service string, check Check, opts WaitOptions) error {
	opts = opts.withDefaults()
	deadline := time.Now().Add(opts.Timeout)
	var last error
	for {
		if opts.Alive != nil && !opts.Alive() {
			return &ServiceError{Service: service, Phase: "health", Err: fmt.Errorf("the process exited before it became healthy (last check: %v); see its log under .demo/logs", last)}
		}
		attempt, cancel := context.WithTimeout(ctx, opts.CheckTimeout)
		last = check(attempt)
		cancel()
		if last == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Add(opts.Interval).Before(deadline) {
			return &ServiceError{Service: service, Phase: "health", Err: fmt.Errorf("not healthy after %s: %v", opts.Timeout, last)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.Interval):
		}
	}
}

// HTTPCheck passes on a 2xx answer from GET url.
func HTTPCheck(url string, headers map[string]string) Check {
	client := &http.Client{}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("GET %s answered %d", url, resp.StatusCode)
		}
		return nil
	}
}

// HTTPAliveCheck passes on any HTTP answer, whatever its status: the server is up. The web app's home page
// calls core's account list, which core does not serve yet (GET /accounts answers 404 until the read WP lands),
// so a 2xx check would call a healthy Next.js server dead. Compile errors still show in `demo logs web`.
func HTTPAliveCheck(url string) Check {
	client := &http.Client{}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
}

// TCPCheck passes when addr accepts a connection.
func TCPCheck(addr string) Check {
	return func(ctx context.Context) error {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		return errors.Join(c.Close())
	}
}
