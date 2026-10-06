package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/ask"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Ask Cliff environment (all optional; without WORKER_URL the routes are not mounted).
const (
	envWebBase    = "GHOST_WEB_BASE_URL" // deep links into the control plane; GHOST_WEB_URL is the fallback
	envWebURL     = "GHOST_WEB_URL"
	envManifestID = "GHOST_DEMO_MANIFEST_ID"
)

// askWiring is Ask Cliff's API option and the loopback to bind to the finished router (both nil when it is off).
type askWiring struct {
	option   api.Option
	loopback *ask.Loopback
}

// newAskWiring builds the service from the environment. getenv is os.Getenv in production.
func newAskWiring(cfg config.Config, getenv func(string) string, logger *slog.Logger) (askWiring, error) {
	if cfg.WorkerURL == "" {
		return askWiring{}, nil
	}
	worker, err := workerclient.New(cfg.WorkerURL, workerclient.WithTimeout(workerclient.AskTimeout))
	if err != nil {
		return askWiring{}, err
	}
	signer, err := ask.NewSigner(runTokenKey(cfg))
	if err != nil {
		return askWiring{}, fmt.Errorf("core: ask token: %w", err)
	}
	web := strings.TrimSpace(getenv(envWebBase))
	if web == "" {
		web = getenv(envWebURL)
	}
	lb := ask.NewLoopback(cfg.APIToken)
	svc := ask.New(worker, lb, signer, ask.Config{WebBase: web, ManifestID: getenv(envManifestID)}, logger)
	return askWiring{option: api.WithAsk(svc), loopback: lb}, nil
}
