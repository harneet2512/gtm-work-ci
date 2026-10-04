package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
)

// Routing is contracts/transitions/routing.v1.json (transition_routing.v1.json): which eval suite applies to an
// account, keyed on its StateTransition's (status, to_state) and, with no transition, on its confirmed
// relationship state (ADR-0012, HAR-128). Suites add to the always-on deterministic evals.
type Routing struct {
	Version      string
	routes       []route
	actionRoutes []actionRoute
	stateSuites  map[string]*string
	suites       map[string]map[string]bool // suite -> its eval types
	governed     []string                   // every eval type some suite includes
}

// route is one (status, to_state) -> suite rule; the most specific (exact to_state before "*") wins.
type route struct {
	Status        string `json:"status"`
	ToState       string `json:"to_state"`
	Suite         string `json:"suite"`
	SuiteOfState  string `json:"suite_of_state"`
	FallbackSuite string `json:"fallback_suite"`
}

// actionRoute refines a route for one candidate: under a transition in Statuses, a candidate of ActionClass is
// judged under Suite (routing.v1.json action_routes).
type actionRoute struct {
	Statuses    []string `json:"statuses"`
	ActionClass string   `json:"action_class"`
	Suite       string   `json:"suite"`
}

// LoadRouting reads and checks a routing file.
func LoadRouting(path string) (*Routing, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: read routing: %w", err)
	}
	return ParseRouting(raw)
}

// ParseRouting decodes the routing document and verifies every route names a known suite.
func ParseRouting(raw []byte) (*Routing, error) {
	var doc struct {
		Version      string             `json:"routing_version"`
		Routes       []route            `json:"routes"`
		ActionRoutes []actionRoute      `json:"action_routes"`
		StateSuites  map[string]*string `json:"state_suites"`
		Suites       map[string]struct {
			Evals []struct {
				EvalTypes []string `json:"eval_types"`
			} `json:"evals"`
		} `json:"suites"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("orchestrator: decode routing: %w", err)
	}
	if len(doc.Routes) == 0 || len(doc.Suites) == 0 {
		return nil, errors.New("orchestrator: routing has no routes or no suites")
	}
	r := &Routing{Version: doc.Version, routes: doc.Routes, actionRoutes: doc.ActionRoutes, stateSuites: doc.StateSuites, suites: map[string]map[string]bool{}}
	governed := map[string]bool{}
	for name, s := range doc.Suites {
		r.suites[name] = map[string]bool{}
		for _, e := range s.Evals {
			for _, t := range e.EvalTypes {
				r.suites[name][t], governed[t] = true, true
			}
		}
	}
	for _, rt := range doc.Routes {
		for _, name := range []string{rt.Suite, rt.FallbackSuite} {
			if _, ok := r.suites[name]; name != "" && !ok {
				return nil, fmt.Errorf("orchestrator: route %s/%s names the unknown suite %q", rt.Status, rt.ToState, name)
			}
		}
	}
	for _, ar := range doc.ActionRoutes {
		if _, ok := r.suites[ar.Suite]; !ok {
			return nil, fmt.Errorf("orchestrator: action route %s names the unknown suite %q", ar.ActionClass, ar.Suite)
		}
	}
	for t := range governed {
		r.governed = append(r.governed, t)
	}
	sort.Strings(r.governed)
	return r, nil
}

// Select picks the suite from the transition (status, to_state, from_state) or, when the account has none
// (status ""), from its relationship state. "" means no suite applies (no state-specific suite yet).
func (r *Routing) Select(status, toState, fromState, relationship string) string {
	if status == "" {
		return r.stateSuite(relationship)
	}
	var exact, wildcard *route
	for i := range r.routes {
		rt := &r.routes[i]
		switch {
		case rt.Status != status:
		case rt.ToState == toState && toState != "":
			exact = rt
		case rt.ToState == "*":
			wildcard = rt
		}
	}
	rt := exact
	if rt == nil {
		rt = wildcard
	}
	switch {
	case rt == nil:
		return r.stateSuite(toState) // e.g. CONFIRMED NEW_LOGO: the state's own suite, if it has one
	case rt.Suite != "":
		return rt.Suite
	}
	if s := r.stateSuite(fromState); s != "" {
		return s
	}
	return rt.FallbackSuite
}

// SelectFor is Select refined by the candidate's own action (HAR-128: routing by transition status, state and
// action): under a transition whose status an action route lists, a candidate of that action class is judged under
// the route's suite instead. With no transition, or no matching rule, the transition's suite stands.
func (r *Routing) SelectFor(status, toState, fromState, relationship, actionClass string) string {
	for _, ar := range r.actionRoutes {
		if ar.ActionClass == actionClass && slices.Contains(ar.Statuses, status) {
			return ar.Suite
		}
	}
	return r.Select(status, toState, fromState, relationship)
}

func (r *Routing) stateSuite(state string) string {
	if s := r.stateSuites[state]; s != nil {
		return *s
	}
	return ""
}

// Excluded is the evals some suite governs that `suite` does not include: the worker judges none of them for a
// candidate under `suite`. Evals no suite governs stay routed by the proposed action alone. "" excludes nothing.
func (r *Routing) Excluded(suite string) []string {
	out := []string{}
	if suite == "" {
		return out
	}
	for _, t := range r.governed {
		if !r.suites[suite][t] {
			out = append(out, t)
		}
	}
	return out
}
