package demorun

import (
	"fmt"
	"strings"
)

// Slack runtime token variables. The tokens come only from the process environment and the git-ignored .env: no credential
// file is read (product-owner rule). The config token (xoxe.xoxp-...) is for app management only and is never given to a
// runtime process.
var slackTokenNames = []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"}

// ResolveSlackTokens returns SLACK_BOT_TOKEN and SLACK_APP_TOKEN. processEnv wins over dotEnv. The error names the missing
// variables and nothing else.
func ResolveSlackTokens(processEnv, dotEnv Env) (Env, error) {
	out := Env{}
	var missing []string
	for _, name := range slackTokenNames {
		switch {
		case processEnv[name] != "":
			out[name] = processEnv[name]
		case dotEnv[name] != "":
			out[name] = dotEnv[name]
		default:
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("demorun: missing %s (set it in the environment or .env); run with --no-slack to skip the Slack surface",
			strings.Join(missing, ", "))
	}
	return out, nil
}
