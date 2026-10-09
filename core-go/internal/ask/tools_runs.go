package ask

import (
	"context"
	"encoding/json"
	"strings"
)

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// target resolves the run or episode of a call; a non-empty message is returned to the model as a refusal.
func (t *Tools) targetOrMessage(ctx context.Context, args map[string]any) (target, *ToolResult, error) {
	tg, msg, err := t.resolveTarget(ctx, args)
	if err != nil {
		return tg, nil, err
	}
	if msg != "" {
		r := notFound(msg)
		return tg, &r, nil
	}
	return tg, nil, nil
}

func (t *Tools) runLinks(tg target) []Link {
	return []Link{{Label: "Episode", URL: t.L.Episode(tg.EpisodeID)}, {Label: "Run evals", URL: t.L.Evals(tg.RunID)}}
}

func (t *Tools) episode(ctx context.Context, args map[string]any) (ToolResult, error) {
	tg, miss, err := t.targetOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	if tg.EpisodeID == "" {
		return found(map[string]any{"note": "the run has no decision episode yet"}), nil
	}
	v, status, err := t.get(ctx, "/episodes/"+tg.EpisodeID)
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		return notFound("no episode has that id"), nil
	}
	return found(v, t.runLinks(tg)...), nil
}

// gateRow keeps what a person needs from one gate result.
func gateRow(g any) map[string]any {
	m := obj(g)
	return map[string]any{"gate": m["gate"], "sub_gate": m["sub_gate"], "verdict": m["verdict"], "question": m["question"],
		"observed": m["observed"], "why": m["why"], "evidence_refs": m["evidence_refs"], "span_id": m["span_id"]}
}

func (t *Tools) gateResults(ctx context.Context, args map[string]any) (ToolResult, error) {
	tg, miss, err := t.targetOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	if tg.EpisodeID == "" {
		return found([]any{}), nil
	}
	gate := strings.ToUpper(str(args, "gate"))
	v, status, err := t.get(ctx, "/episodes/"+tg.EpisodeID+"/gate-results"+q(map[string]string{"gate": gate}))
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		return notFound("no episode has that id"), nil
	}
	var rows []any
	links := []Link{{Label: "Episode", URL: t.L.Episode(tg.EpisodeID)}}
	for _, g := range arr(obj(v)["items"]) {
		row := gateRow(g)
		rows = append(rows, row)
		if vd := text(row["verdict"]); (vd == "warn" || vd == "fail") && len(links) < 6 {
			links = append(links, Link{Label: "Gate " + text(row["gate"]) + " (" + vd + ") in the trace",
				URL: t.L.Span(tg.EpisodeID, text(row["span_id"]))})
		}
	}
	return found(rows, links...), nil
}

// byRun reads one run-scoped path; 404 is "nothing yet" (strategies not ready, no decision).
func (t *Tools) byRun(ctx context.Context, args map[string]any, suffix, missing string) (ToolResult, error) {
	tg, miss, err := t.targetOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	v, status, err := t.get(ctx, "/runs/"+tg.RunID+suffix)
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		return found(map[string]any{"note": missing}, t.runLinks(tg)...), nil
	}
	return found(v, t.runLinks(tg)...), nil
}

func (t *Tools) strategies(ctx context.Context, args map[string]any) (ToolResult, error) {
	return t.byRun(ctx, args, "/strategies", "the strategies are not ready for this run")
}

func (t *Tools) humanDecision(ctx context.Context, args map[string]any) (ToolResult, error) {
	res, err := t.byRun(ctx, args, "/strategy-decision", "the human has not chosen yet")
	if note := obj(res.Data)["note"]; note != nil {
		res.Empty = true
	}
	return res, err
}

func (t *Tools) judgmentInference(ctx context.Context, args map[string]any) (ToolResult, error) {
	tg, miss, err := t.targetOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	if tg.EpisodeID == "" {
		return found(map[string]any{"note": "the run has no decision episode yet"}), nil
	}
	v, status, err := t.get(ctx, "/episodes/"+tg.EpisodeID+"/judgment-inference")
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		res := found(map[string]any{"note": "the judgment has not been inferred yet"}, t.runLinks(tg)...)
		res.Empty = true
		return res, nil
	}
	return found(v, t.runLinks(tg)...), nil
}

func (t *Tools) knowledge(ctx context.Context, args map[string]any) (ToolResult, error) {
	var acc account
	if str(args, "account") != "" {
		a, miss, err := t.accountOrMessage(ctx, args)
		if err != nil || miss != nil {
			return orMiss(miss), err
		}
		acc = a
	}
	v, _, err := t.get(ctx, "/knowledge"+q(map[string]string{"status": str(args, "status"), "limit": "100"}))
	if err != nil {
		return ToolResult{}, err
	}
	var items []any
	for _, k := range arr(obj(v)["items"]) {
		if acc.ID == "" || strings.Contains(jsonText(k), acc.ID) || strings.Contains(strings.ToLower(jsonText(k)), strings.ToLower(acc.Name)) {
			items = append(items, k)
		}
	}
	return found(items, Link{Label: "Organizational knowledge", URL: t.L.Knowledge()}), nil
}

// attributionNote keeps the words honest: the three lists say what was retrieved, applicable and used, not what influenced.
const attributionNote = "Retrieved, applicable and used are three different things. None of them measures influence."

func (t *Tools) knowledgeAttribution(ctx context.Context, args map[string]any) (ToolResult, error) {
	tg, miss, err := t.targetOrMessage(ctx, args)
	if err != nil || miss != nil {
		return orMiss(miss), err
	}
	if tg.EpisodeID == "" {
		return found(map[string]any{"note": "the run has no decision episode yet"}), nil
	}
	v, status, err := t.get(ctx, "/episodes/"+tg.EpisodeID+"/trace")
	if err != nil {
		return ToolResult{}, err
	}
	if status == 404 {
		return notFound("no episode has that id"), nil
	}
	lists := map[string][]any{"retrieved": {}, "applicable": {}, "used": {}}
	links := []Link{{Label: "Episode trace", URL: t.L.Episode(tg.EpisodeID)}}
	for _, s := range arr(obj(v)["spans"]) {
		m := obj(s)
		name := strings.TrimPrefix(text(m["kind"]), "knowledge_")
		if _, ok := lists[name]; !ok || !strings.HasPrefix(text(m["kind"]), "knowledge_") {
			continue
		}
		lists[name] = append(lists[name], map[string]any{"title": m["title"], "status": m["status"], "summary": m["summary"],
			"attributes": m["attributes"], "span_id": m["id"]})
		links = append(links, Link{Label: "Knowledge " + name + " span", URL: t.L.Span(tg.EpisodeID, text(m["id"]))})
	}
	data := map[string]any{"retrieved": lists["retrieved"], "applicable": lists["applicable"], "used": lists["used"], "note": attributionNote}
	res := found(data, links...)
	res.Empty = len(lists["retrieved"])+len(lists["applicable"])+len(lists["used"]) == 0
	return res, nil
}
