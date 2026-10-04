package demorun

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Slack runtime token variables and the prefixes the credential file is searched by. This ports
// scripts/slack/bootstrap.py token(): process environment, then .env, then the credential file by prefix.
// The config token (xoxe.xoxp-...) is for app management only and is never given to a runtime process.
var slackTokenSpecs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"SLACK_BOT_TOKEN", regexp.MustCompile(`xoxb-[A-Za-z0-9\-]+`)},
	{"SLACK_APP_TOKEN", regexp.MustCompile(`xapp-[A-Za-z0-9\-]+`)},
}

// DefaultSlackCredFile is where bootstrap.py looks when GHOST_SLACK_CRED_FILE is not set.
func DefaultSlackCredFile() string {
	if p := os.Getenv("GHOST_SLACK_CRED_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Desktop", "cloud_access.md")
}

// ResolveSlackTokens returns SLACK_BOT_TOKEN and SLACK_APP_TOKEN. processEnv wins over dotEnv, which wins
// over the credential file (searched by token prefix; its content is never logged or returned in an error).
// The error names the missing variables and nothing else.
func ResolveSlackTokens(processEnv, dotEnv Env, credFile string) (Env, error) {
	out := Env{}
	var missing []string
	var credText string
	var credLoaded bool
	for _, spec := range slackTokenSpecs {
		switch {
		case processEnv[spec.name] != "":
			out[spec.name] = processEnv[spec.name]
			continue
		case dotEnv[spec.name] != "":
			out[spec.name] = dotEnv[spec.name]
			continue
		}
		if !credLoaded {
			credLoaded = true
			if credFile != "" {
				if b, err := os.ReadFile(credFile); err == nil {
					credText = string(b)
				}
			}
		}
		if m := spec.re.FindString(credText); m != "" {
			out[spec.name] = m
			continue
		}
		missing = append(missing, spec.name)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("demorun: missing %s (not in the environment, .env or the credential file); "+
			"run with --no-slack to skip the Slack surface", strings.Join(missing, ", "))
	}
	return out, nil
}
