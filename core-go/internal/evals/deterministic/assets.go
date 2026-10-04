package deterministic

import "fmt"

// PromisedAssetExists: a draft that says it attaches or shares something attaches it, every
// attachment is an available library asset, and any library asset the text promises is available.
// An empty library means attachments cannot be verified beyond being present.
func PromisedAssetExists(in Input) []Finding {
	var out []Finding
	art := in.Draft.FinishedArtifact
	if len(art.Attachments) == 0 && (attachClaimRE.MatchString(art.Body) || in.Draft.ProposedActionType == ActionShareDocument) {
		out = append(out, failure(CheckPromisedAssetExists, "the draft says it attaches or shares material, but nothing is attached",
			"Attach the referenced asset or remove the reference.").blocking())
	}
	if len(in.Assets) == 0 {
		return out
	}
	for _, name := range art.Attachments {
		a, ok := findAsset(in.Assets, name)
		switch {
		case !ok:
			out = append(out, failure(CheckPromisedAssetExists, fmt.Sprintf("attachment %s is not in the asset library", name),
				"Attach a library asset; do not invent documents.").blocking())
		case !a.Available:
			out = append(out, failure(CheckPromisedAssetExists, fmt.Sprintf("attachment %s is not available", name),
				fmt.Sprintf("Remove %s until it is available.", a.Name)).blocking())
		}
	}
	for _, a := range mentionedAssets(in.Assets, art.Body) {
		if !a.Available {
			out = append(out, failure(CheckPromisedAssetExists, fmt.Sprintf("the text promises %s, which is not available", a.Name),
				fmt.Sprintf("Do not promise %s until it exists.", a.Name)).blocking())
		}
	}
	return out
}

func findAsset(assets []Asset, name string) (Asset, bool) {
	n := normalize(name)
	for _, a := range assets {
		if normalize(a.Name) == n {
			return a, true
		}
		for _, alias := range a.Aliases {
			if normalize(alias) == n {
				return a, true
			}
		}
	}
	return Asset{}, false
}

// mentionedAssets are library assets whose name or an alias occurs in text.
func mentionedAssets(assets []Asset, text string) []Asset {
	var out []Asset
	for _, a := range assets {
		names := append([]string{a.Name}, a.Aliases...)
		for _, n := range names {
			if containsFold(text, n) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}
