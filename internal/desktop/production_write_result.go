package desktop

import (
	"maps"
	"slices"
	"strings"

	"silverwing-go/internal/production"
)

func productionCompletedAction(kind string, before *production.Batch) string {
	if (kind == "production.resume" || kind == "production.rebase-pending") && before != nil && before.Intent != nil {
		switch before.Intent.Step {
		case "main":
			return "production.merge-main"
		case "release-create":
			return "production.create"
		case "release-sync":
			return "production.merge-release"
		case "feature":
			return "production.merge"
		case "tag-push":
			return "production.publish"
		}
	}
	return kind
}

// A successful push already tells us what this operation changed. Keep that
// result visible without a second, blocking observation of Git and Genbu.
func applyProductionWritePreview(v *productionView, b production.Batch, kind string) {
	if v.Preview == nil || v.Preview.Target != b.Target {
		return
	}
	p := *v.Preview
	p.Branches, p.Tags = slices.Clone(p.Branches), slices.Clone(p.Tags)
	p.BranchHeads = maps.Clone(p.BranchHeads)
	if p.BranchHeads == nil {
		p.BranchHeads = map[string]string{}
	}
	switch kind {
	case "production.create", "production.merge", "production.merge-main", "production.merge-release":
		if kind != "production.merge" {
			if p.MainSHA != b.MainSHA {
				p.MainInRelease = false
			}
			p.MainSHA = b.MainSHA
			p.BranchHeads[b.Target.MainBranch] = b.MainSHA
		}
		p.ReleaseSHA = b.ReleaseSHA
		if b.ReleaseSHA != "" {
			p.BranchHeads[b.ReleaseBranch] = b.ReleaseSHA
			if !slices.Contains(p.Branches, b.ReleaseBranch) {
				p.Branches = append(p.Branches, b.ReleaseBranch)
				slices.Sort(p.Branches)
			}
		}
		if kind != "production.merge" && p.ProductionSHA != "" {
			p.ProductionTag, p.ProductionSHA = b.ProductionTag, b.ProductionSHA
			p.ProductionInMain = true
		}
		if kind == "production.create" || kind == "production.merge-release" || p.MainSHA == p.ReleaseSHA {
			p.MainInRelease = true
		}
		if kind == "production.merge-main" {
			// The production Tag is now contained; the old incoming diff is stale.
			p.Commits, p.Diff = "", ""
		}
	case "production.publish":
		if len(b.Tags) > 0 {
			tag := b.Tags[len(b.Tags)-1]
			if !slices.Contains(p.Tags, tag.Name) {
				p.Tags = append(p.Tags, tag.Name)
			}
		}
	default:
		return
	}
	if p.ProductionSHA != "" {
		suggested, suggestionErr := production.SuggestedVersion(p)
		p.SuggestedVersion = suggested
		if suggestionErr != nil && !strings.Contains(p.Warning, suggestionErr.Error()) {
			p.Warning = strings.TrimSpace(p.Warning + "\n" + suggestionErr.Error())
		}
	}
	v.Preview = &p
}
