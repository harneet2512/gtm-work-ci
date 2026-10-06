package main

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestAskIsOffWithoutAWorker(t *testing.T) {
	w, err := newAskWiring(config.Config{APIToken: strings.Repeat("a1", 20)}, envOf(nil), nil)
	if err != nil || w.option != nil || w.loopback != nil {
		t.Errorf("wiring = %+v, %v", w, err)
	}
}

func TestAskIsWiredFromTheEnvironment(t *testing.T) {
	cfg := config.Config{WorkerURL: "http://127.0.0.1:8090", APIToken: strings.Repeat("a1", 20)}
	w, err := newAskWiring(cfg, envOf(map[string]string{envWebURL: "http://web.test",
		envManifestID: "m"}), nil)
	if err != nil || w.option == nil || w.loopback == nil {
		t.Fatalf("wiring = %+v, %v", w, err)
	}
}

func TestAskRefusesAWeakSigningSecret(t *testing.T) {
	cfg := config.Config{WorkerURL: "http://127.0.0.1:8090", RunTokenSecret: "short", APIToken: "x"}
	if _, err := newAskWiring(cfg, envOf(nil), nil); err == nil {
		t.Error("a short secret must be refused")
	}
}
