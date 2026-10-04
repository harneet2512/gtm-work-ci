// Command fakecore is a deterministic in-memory stand-in for the core API endpoints the Slack surface calls
// (HAR-137 section 12), serving the contract fixture episode. It is for the Slack smoke test only: it keeps
// state in memory, records every request to a JSON log, validates request bodies against
// contracts/openapi/core.yaml and listens on loopback only.
//
//	GHOST_API_TOKEN=... fakecore [--addr 127.0.0.1:0] [--log fakecore-log.json] [--repo <repo root>]
//
// The bearer token is read from GHOST_API_TOKEN (the same variable the slackbot sends). The first stdout
// line is "fakecore listening on http://<host:port>".
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fakecore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "127.0.0.1:0", "listen address; the host must be loopback")
	logPath := fs.String("log", "", "write the request log (JSON) here after every request")
	repo := fs.String("repo", "", "repository root (to find contracts/); default: search upwards from the working directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := loopbackOnly(*addr); err != nil {
		fmt.Fprintln(stderr, "fakecore:", err)
		return 2
	}
	if *repo != "" {
		if err := os.Chdir(*repo); err != nil {
			fmt.Fprintln(stderr, "fakecore: --repo:", err)
			return 2
		}
	}
	srv, err := fakecore.New(fakecore.Options{Token: getenv("GHOST_API_TOKEN"), LogPath: *logPath})
	if err != nil {
		fmt.Fprintln(stderr, "fakecore:", err) // never includes the token
		return 2
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(stderr, "fakecore: listen:", err)
		return 1
	}
	fmt.Fprintf(stdout, "fakecore listening on http://%s\n", ln.Addr())
	if err := api.Serve(ctx, api.NewServer(*addr, srv.Handler()), ln, 5*time.Second); err != nil {
		fmt.Fprintln(stderr, "fakecore:", err)
		return 1
	}
	return 0
}

// loopbackOnly refuses any listen address that is not on this machine.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("--addr must be a loopback address (127.0.0.1, ::1 or localhost): the fake core has no real access control")
}
