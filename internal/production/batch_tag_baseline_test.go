package production

import (
	"context"
	"strings"
	"testing"
)

func TestProductionTagInCurrentReleaseBatchDoesNotRequireMainMerge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	b := f.create()
	initialTag, initialSHA := b.ProductionTag, b.ProductionSHA
	b = f.merge(b, "feature/first", f.feature("feature/first", "first.txt", "first\n", f.base))
	b, _, err := f.s.PrepareTag(ctx, b.ID, Preview{Target: b.Target})
	if err != nil {
		t.Fatal(err)
	}
	b = f.publish(b)
	live := b.Tags[0]
	f.g.versions = []string{live.Name}
	b = f.merge(b, "feature/second", f.feature("feature/second", "second.txt", "second\n", f.base))
	if b.ReleaseSHA == live.Commit {
		t.Fatal("second feature must advance the release branch beyond the online Tag")
	}
	refsBefore := f.run(f.remote, "show-ref")
	b, preview, err := f.s.PrepareTag(ctx, b.ID, Preview{Target: b.Target})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ProductionInMain || !preview.ProductionInRelease || !preview.MainInRelease || b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.1" || b.TagPlan.Commit != b.ReleaseSHA {
		t.Fatalf("the existing release should cover its own live Tag: preview=%+v plan=%+v", preview, b.TagPlan)
	}
	if b.ProductionTag != initialTag || b.ProductionSHA != initialSHA || !sameProductionBaseline(preview, b.TagPlan.Preview) {
		t.Fatalf("confirmation did not preserve both baselines: batch=%+v", b)
	}
	if refsAfter := f.run(f.remote, "show-ref"); refsAfter != refsBefore {
		t.Fatalf("Tag preparation modified remote refs:\nbefore:\n%s\nafter:\n%s", refsBefore, refsAfter)
	}

	mainSHA := f.feature("master", "main.txt", "main\n", f.base)
	b, preview, err = f.s.PrepareTag(ctx, b.ID, Preview{Target: b.Target})
	if err == nil || !strings.Contains(err.Error(), "合并主分支到 release") || strings.Contains(err.Error(), "合并生产 Tag") || b.TagPlan != nil {
		t.Fatalf("only the new main commits should need synchronization: batch=%+v err=%v", b, err)
	}
	b, err = f.s.MergeMainToRelease(ctx, b.ID, preview)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.run(f.remote, "rev-parse", "refs/heads/master"); got != mainSHA {
		t.Fatalf("main ref changed during release sync: %s, want %s", got, mainSHA)
	}
	if b.ProductionTag != initialTag || b.ProductionSHA != initialSHA {
		t.Fatalf("release sync replaced the recorded main baseline: batch=%+v", b)
	}
	_, preview, err = f.s.InspectBatch(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.CheckForTag(ctx, b.ID, preview)
	if err != nil || b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.1" {
		t.Fatalf("release sync should allow the next Tag: batch=%+v err=%v", b, err)
	}
	b = f.publish(b)
	if len(b.Tags) != 2 || b.Tags[1].Commit != b.ReleaseSHA {
		t.Fatalf("next Tag should use the updated release commit: %+v", b.Tags)
	}
}

func TestProductionBaselineReadyRequiresTrackedLiveTag(t *testing.T) {
	target := Target{Selection: Selection{ServiceID: "test"}}
	b := Batch{
		Target:     target,
		ReleaseSHA: "release-commit",
		Tags:       []Tag{{Name: "2.3.44", Commit: "live-commit"}, {Name: "2.3.44.1", Commit: "release-commit"}},
	}
	live := Preview{
		Target: target, ReleaseSHA: b.ReleaseSHA, MainSHA: "main-commit",
		RuntimeAvailable: true, ProductionCandidates: []string{"2.3.44"},
		ProductionTag: "2.3.44", ProductionSHA: "live-commit", ProductionInRelease: true,
	}
	if !b.ProductionBaselineReady(live) {
		t.Fatal("an earlier Tag from this batch can still be the live version")
	}
	for _, tc := range []struct {
		name   string
		change func(*Preview)
	}{
		{"different name at the same commit", func(p *Preview) {
			p.ProductionTag, p.ProductionCandidates = "2.3.45", []string{"2.3.45"}
		}},
		{"same name at a different commit", func(p *Preview) { p.ProductionSHA = "changed-commit" }},
		{"different target", func(p *Preview) { p.Target.ServiceID = "other" }},
		{"runtime unavailable", func(p *Preview) { p.RuntimeAvailable = false }},
		{"release head changed", func(p *Preview) { p.ReleaseSHA = "changed-head" }},
		{"release does not contain live Tag", func(p *Preview) { p.ProductionInRelease = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := live
			tc.change(&p)
			if b.ProductionBaselineReady(p) {
				t.Fatalf("baseline readiness accepted unmatched evidence: %+v", p)
			}
		})
	}
}
