package controlplane

import (
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
)

// AreaCounts is one area of eval_run.v1.json areas[]: its tally, and the change against the previous run.
type AreaCounts struct {
	Area     string  `json:"area"`
	Label    string  `json:"label"`
	Order    int     `json:"order"`
	Measured bool    `json:"measured"`
	Counts   Counts  `json:"counts"`
	Delta    *Counts `json:"delta"`
}

// EvalTypeCounts is one eval type of a family (eval_family_summary.v1.json evalTypeCounts).
type EvalTypeCounts struct {
	EvalType  string   `json:"eval_type"`
	Counts    Counts   `json:"counts"`
	Delta     *Counts  `json:"delta"`
	ResultIDs []string `json:"result_ids"`
}

// Family is one registry family inside an area.
type Family struct {
	FamilyID  string           `json:"family_id"`
	Name      string           `json:"name"`
	Counts    Counts           `json:"counts"`
	Delta     *Counts          `json:"delta"`
	EvalTypes []EvalTypeCounts `json:"eval_types"`
}

// FamilyArea is one area of eval_family_summary.v1.json: the area tally plus its families.
type FamilyArea struct {
	AreaCounts
	Families []Family `json:"families"`
}

// inArea keeps the results whose eval type rolls up into the area; an eval type outside the mapping belongs to
// no area (it still counts in the run total, and a test pins that every catalog type is mapped).
func inArea(rs []result, area evalarea.Area) []result {
	var out []result
	for _, r := range rs {
		if a, ok := evalarea.AreaOf(r.EvalType); ok && a == area {
			out = append(out, r)
		}
	}
	return out
}

func inFamily(rs []result, family string) []result {
	var out []result
	for _, r := range rs {
		if f, ok := evalarea.FamilyOf(r.EvalType); ok && f == family {
			out = append(out, r)
		}
	}
	return out
}

func ofType(rs []result, evalType string) []result {
	var out []result
	for _, r := range rs {
		if r.EvalType == evalType {
			out = append(out, r)
		}
	}
	return out
}

// deltaOf is cur - prev, or nil when there is no previous run.
func deltaOf(cur, prev Counts, hasPrev bool) *Counts {
	if !hasPrev {
		return nil
	}
	d := cur.Minus(prev)
	return &d
}

func areaOf(info evalarea.Info, cur, prev []result, hasPrev bool) AreaCounts {
	c := tally(inArea(cur, info.ID))
	return AreaCounts{
		Area: string(info.ID), Label: info.Label, Order: info.Order, Measured: c.Total > 0, Counts: c,
		Delta: deltaOf(c, tally(inArea(prev, info.ID)), hasPrev),
	}
}

// areaCounts rolls the results up into the four areas, always all four in display order.
func areaCounts(cur, prev []result, hasPrev bool) []AreaCounts {
	infos := evalarea.Areas()
	out := make([]AreaCounts, 0, len(infos))
	for _, info := range infos {
		out = append(out, areaOf(info, cur, prev, hasPrev))
	}
	return out
}

// familySummary groups the results into areas, families and eval types. A family or eval type appears when this
// run or the previous one has results for it, so one that vanished shows as a negative delta.
func familySummary(cur, prev []result, hasPrev bool) []FamilyArea {
	infos := evalarea.Areas()
	out := make([]FamilyArea, 0, len(infos))
	for _, info := range infos {
		fa := FamilyArea{AreaCounts: areaOf(info, cur, prev, hasPrev), Families: []Family{}}
		for _, id := range info.Families {
			fc, fp := inFamily(cur, id), inFamily(prev, id)
			if len(fc) == 0 && len(fp) == 0 {
				continue
			}
			name, _ := evalarea.FamilyName(id)
			cc := tally(fc)
			fa.Families = append(fa.Families, Family{
				FamilyID: id, Name: name, Counts: cc, Delta: deltaOf(cc, tally(fp), hasPrev), EvalTypes: evalTypes(fc, fp, hasPrev),
			})
		}
		out = append(out, fa)
	}
	return out
}

func evalTypes(cur, prev []result, hasPrev bool) []EvalTypeCounts {
	var types []string
	for _, r := range append(slices.Clone(cur), prev...) {
		if !slices.Contains(types, r.EvalType) {
			types = append(types, r.EvalType)
		}
	}
	slices.Sort(types)
	out := make([]EvalTypeCounts, 0, len(types))
	for _, t := range types {
		rc := ofType(cur, t)
		ids := make([]string, 0, len(rc))
		for _, r := range rc {
			ids = append(ids, r.ID)
		}
		c := tally(rc)
		out = append(out, EvalTypeCounts{EvalType: t, Counts: c, Delta: deltaOf(c, tally(ofType(prev, t)), hasPrev), ResultIDs: ids})
	}
	return out
}
