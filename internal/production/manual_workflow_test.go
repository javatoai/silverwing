package production

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

func (f *fixture) advanceProduction() string {
	f.t.Helper()
	sha := f.feature("production/new", "production.txt", "new production\n", f.base)
	f.run(f.seed, "tag", "-a", "2.3.43.1", "-m", "new production", sha)
	f.run(f.seed, "push", "origin", "refs/tags/2.3.43.1")
	f.g.versions = []string{"2.3.43.1"}
	return sha
}

func TestProductionManualInitializationStopsAndRestoresBeforeRelease(t *testing.T) {
	f := newFixture(t)
	production := f.advanceProduction()
	main := f.feature("master", "main-only.txt", "retain main history\n", f.base)
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.s.MergeProduction(context.Background(), "", p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Intent != nil || b.Version != "" || b.ReleaseBranch != "" || b.InitialSHA != "" || b.MainSHA == "" {
		t.Fatalf("main merge continued initialization: %+v", b)
	}
	for _, source := range []string{production, main} {
		if ok, e := f.s.ancestor(context.Background(), b.Target, source, b.MainSHA); e != nil || !ok {
			t.Fatalf("lost source %s: %v", source, e)
		}
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil || refs["refs/heads/release/2.3.44"] != "" {
		t.Fatal("main merge created release")
	}
	// Restart with the same persisted initialization record and no UI state.
	f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
	f.s.Genbu = f.g
	stored, err := f.s.Store.Load(b.ID)
	if err != nil || stored.MainSHA != b.MainSHA {
		t.Fatalf("unversioned initialization did not restore: %v", err)
	}
	_, p, err = f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil || !p.ProductionInMain {
		t.Fatalf("main inclusion not reflected: %v", err)
	}
	if _, wrongErr := f.s.Create(context.Background(), p, "2.3.44", strings.Repeat("a", 32)); wrongErr == nil {
		t.Fatal("creation adopted another initialization batch")
	}
	b, err = f.s.Create(context.Background(), p, "2.3.44", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != stored.ID || b.InitialSHA != stored.MainSHA || b.ReleaseBranch != "release/2.3.44" || b.Intent != nil || b.TagPlan != nil || len(b.Tags) != 0 {
		t.Fatalf("release creation crossed operation boundary: %+v", b)
	}
	if got := f.run(f.remote, "rev-parse", "refs/heads/release/2.3.44"); got != b.InitialSHA {
		t.Fatal("release not created from confirmed main")
	}
}

func TestProductionMainAndReleaseMergesStopBeforeTagPreparation(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	one := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", one)
	f.advanceProduction()
	_, p, err := f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	before := b.ReleaseSHA
	b, err = f.s.MergeProduction(context.Background(), b.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.ReleaseSHA != before || b.Intent != nil || b.TagPlan != nil {
		t.Fatal("main merge continued into release")
	}
	_, p, err = f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.CheckForTag(context.Background(), b.ID, p); err == nil {
		t.Fatal("check did not require explicit release merge")
	}
	b, err = f.s.MergeMainToRelease(context.Background(), b.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Intent != nil || b.TagPlan != nil || len(b.Tags) != 0 || b.ReleaseSHA == before {
		t.Fatal("release merge prepared or pushed a Tag")
	}
	_, p, err = f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.CheckForTag(context.Background(), b.ID, p)
	if err != nil || b.TagPlan == nil {
		t.Fatalf("read-only check could not prepare final confirmation: %v", err)
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil || refs["refs/tags/2.3.44"] != "" {
		t.Fatal("check pushed Tag")
	}
	b = f.publish(b)
	if len(b.Tags) != 1 || b.Ended {
		t.Fatal("publish did not preserve active batch")
	}
}

func TestProductionDifferenceUsesImmutableIncomingTreesAndDoesNotTouchCheckout(t *testing.T) {
	f := newFixture(t)
	f.feature("master", "main-only.txt", "retain this\n", f.base)
	f.advanceProduction()
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	before := f.run(f.repo, "status", "--porcelain") + f.run(f.repo, "rev-parse", "HEAD") + f.run(f.repo, "show-ref")
	diff, err := f.s.Difference(context.Background(), p.Target, p.ProductionSHA, p.MainSHA, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Files.Files) != 1 || diff.Files.Files[0].Path != "production.txt" || diff.Files.BaseCommit != f.base {
		t.Fatalf("main-only changes appeared as incoming deletions: %+v", diff.Files)
	}
	file := diff.Files.Files[0]
	preview, err := f.s.Difference(context.Background(), p.Target, p.ProductionSHA, p.MainSHA, true, &file)
	if err != nil || preview.Preview == nil || preview.Preview.Content == nil || preview.Preview.Content.Text != "new production\n" {
		t.Fatalf("no real file contents: %v / %+v", err, preview.Preview)
	}
	if !strings.Contains(diff.Commits, ">") || !strings.Contains(diff.Commits, "<") {
		t.Fatal("missing both sides' commit history")
	}
	for _, sha := range []string{"HEAD", "--all", p.ProductionSHA[:10]} {
		if _, err = f.s.Difference(context.Background(), p.Target, sha, p.MainSHA, true, nil); err == nil {
			t.Fatalf("accepted mutable/invalid revision %s", sha)
		}
	}
	file = workspace.CompareChange{Path: "main-only.txt", Kind: "ADDED"}
	if _, err = f.s.Difference(context.Background(), p.Target, p.ProductionSHA, p.MainSHA, true, &file); err == nil {
		t.Fatal("accepted file outside snapshot")
	}
	after := f.run(f.repo, "status", "--porcelain") + f.run(f.repo, "rev-parse", "HEAD") + f.run(f.repo, "show-ref")
	if after != before {
		t.Fatal("read-only difference changed user repository")
	}
}

func TestProductionReleaseBranchParsing(t *testing.T) {
	for _, name := range []string{"release/2.3.44", " release/2.3.45 "} {
		if _, err := ReleaseVersion(name, []string{"2.3.43.2"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"2.3.44", "feature/2.3.44", "release/release/2.3.44", "release/2.3.43", "release/2.3.44.1", "release/../2.3.44"} {
		if _, err := ReleaseVersion(name, []string{"2.3.43.2"}); err == nil {
			t.Fatalf("invalid release branch accepted: %s", name)
		}
	}
}

func TestProductionLegacyPendingStagesResumeOnlyTheirCurrentOperation(t *testing.T) {
	for _, step := range []string{"main", "release-sync"} {
		t.Run(step, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var b Batch
			var p Preview
			var err error
			if step == "main" {
				f.advanceProduction()
				p, err = f.s.Inspect(ctx, f.selection, "")
				if err != nil {
					t.Fatal(err)
				}
				b = Batch{ID: newID(), Target: p.Target, Version: "2.3.44", ReleaseBranch: "release/2.3.44", CreatedAt: timestamp(), Phase: "INITIALIZING", Intent: &Intent{Kind: "create", Step: "main", Preview: p}}
			} else {
				b = f.create()
				one := f.feature("feature/one", "one.txt", "one\n", f.base)
				b = f.merge(b, "feature/one", one)
				f.feature("master", "extra.txt", "extra main\n", f.base)
				_, p, err = f.s.InspectBatch(ctx, b.ID, "")
				if err != nil {
					t.Fatal(err)
				}
				b.MainSHA = p.MainSHA
				b.Intent = &Intent{Kind: "sync", Step: "release-sync", Preview: p}
			}
			if err = f.s.Store.Save(&b); err != nil {
				t.Fatal(err)
			}
			b, err = f.s.Resume(ctx, b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if b.Intent != nil || b.TagPlan != nil || len(b.Tags) != 0 {
				t.Fatal("legacy recovery continued to another operation")
			}
			refs, err := f.s.remoteRefs(ctx, b.Target)
			if err != nil {
				t.Fatal(err)
			}
			if step == "main" && (b.InitialSHA != "" || refs["refs/heads/"+b.ReleaseBranch] != "") {
				t.Fatal("legacy main recovery created release")
			}
			if step == "release-sync" && b.ReleaseSHA == p.ReleaseSHA {
				t.Fatal("legacy current release merge was not completed")
			}
		})
	}
}

func TestProductionBuildCheckRejectsConfigChangedDuringRead(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	one := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", one)
	_, p, err := f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.g.onStatus = func() {
		c, e := f.s.Config.Load()
		if e != nil {
			t.Fatal(e)
		}
		c.Groups[0].Services[0].GenbuServiceName = "changed-during-check"
		if e = f.s.Config.Save(c); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = f.s.CheckForTag(context.Background(), b.ID, p); err == nil {
		t.Fatal("late build check persisted a stale confirmation")
	}
	stored, err := f.s.Store.Load(b.ID)
	if err != nil || stored.TagPlan != nil {
		t.Fatalf("stale preparation entered saved batch: %v", err)
	}
}

func TestProductionCreateRequiresExplicitBaselineMerge(t *testing.T) {
	f := newFixture(t)
	f.advanceProduction()
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Create(context.Background(), p, "2.3.44", "")
	if err == nil {
		t.Fatal("create release silently merged production into main")
	}
	refs, err := f.s.remoteRefs(context.Background(), p.Target)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/heads/master"] != f.base || refs["refs/heads/release/2.3.44"] != "" {
		t.Fatal("create release changed remote prerequisites")
	}
}

func TestProductionBuildCheckDoesNotMerge(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	feature := f.feature("feature/one", "one.txt", "one\n", f.base)
	b = f.merge(b, "feature/one", feature)
	f.advanceProduction()
	_, p, err := f.s.InspectBatch(context.Background(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.CheckForTag(context.Background(), b.ID, p)
	if err == nil {
		t.Fatal("build check silently synchronized production")
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/heads/master"] != f.base || refs["refs/heads/"+b.ReleaseBranch] != b.ReleaseSHA {
		t.Fatal("read-only check pushed a branch")
	}
}
