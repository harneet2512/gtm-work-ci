package ask

import (
	"net/url"
	"strings"
)

// Links builds deep links into the web control plane from GHOST_WEB_BASE_URL. Every builder returns "" unless the
// base is an absolute http(s) URL, so a deployment without it simply has no links.
type Links struct{ base string }

// NewLinks returns a Links for base (trailing slash ignored).
func NewLinks(base string) Links {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Links{}
	}
	return Links{base: base}
}

// Base is the validated web base, "" when none.
func (l Links) Base() string { return l.base }

// Owns reports whether raw points into the control plane (the only URLs an answer may carry).
func (l Links) Owns(raw string) bool {
	return l.base != "" && (raw == l.base || strings.HasPrefix(raw, l.base+"/"))
}

// Account is the account page: the graph, state and timeline.
func (l Links) Account(accountID string) string {
	if l.base == "" || accountID == "" {
		return ""
	}
	return l.base + "/accounts/" + url.PathEscape(accountID)
}

// Episode is the episode page.
func (l Links) Episode(episodeID string) string {
	if l.base == "" || episodeID == "" {
		return ""
	}
	return l.base + "/episodes/" + url.PathEscape(episodeID)
}

// Span deep-links one trace span of an episode (a gate result attaches to a span).
func (l Links) Span(episodeID, spanID string) string {
	page := l.Episode(episodeID)
	if page == "" || spanID == "" {
		return ""
	}
	return page + "?mode=trace&span=" + url.QueryEscape(spanID)
}

// Evals is a run's eval page.
func (l Links) Evals(runID string) string {
	if l.base == "" || runID == "" {
		return ""
	}
	return l.base + "/runs/" + url.PathEscape(runID) + "/evals"
}

// Knowledge is the organizational knowledge page.
func (l Links) Knowledge() string {
	if l.base == "" {
		return ""
	}
	return l.base + "/knowledge"
}

// Replay is the replay page of a manifest.
func (l Links) Replay(manifestID string) string {
	if l.base == "" || manifestID == "" {
		return ""
	}
	return l.base + "/replay/" + url.PathEscape(manifestID)
}

// Of returns the links of a list that exist (non-empty URL), keeping the label.
func Of(pairs ...Link) []Link {
	out := make([]Link, 0, len(pairs))
	for _, p := range pairs {
		if p.URL != "" {
			out = append(out, p)
		}
	}
	return out
}
