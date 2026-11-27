package production

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/process"
)

func TestProductionReuseReleasePreservesRemoteBranchAndBuildDiff(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const version, branch = "2.3.44", "release/2.3.44"
	head := f.feature(branch, "release-only.txt", "existing release content\n", f.base)
	main := f.feature("master", "main-only.txt", "current main content\n", f.base)
	p, err := f.s.Inspect(ctx, f.selection)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := f.s.MergeProduction(ctx, "", p)
	if err != nil {
		t.Fatal(err)
	}
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	refs := f.run(f.remote, "show-ref")
	plan, err := f.s.PreviewRelease(ctx, p, version, true)
	if err != nil || plan.Version != version || plan.Preview.ReleaseSHA != head || plan.Preview.MainInRelease {
		t.Fatalf("unexpected plan for diverged release: %+v, %v", plan, err)
	}
	if b, err := f.s.Store.Load(initial.ID); err != nil || b.ReleaseBranch != "" || b.InitialSHA != "" {
		t.Fatalf("preview changed the initialization record: %+v, %v", b, err)
	}
	if _, err := f.s.PreviewRelease(ctx, p, version, false); err == nil || !strings.Contains(err.Error(), branch) {
		t.Fatalf("ordinary creation did not preserve the branch reservation: %v", err)
	}
	b, err := f.s.ReuseRelease(ctx, plan.Preview, version, initial.ID)
	if err != nil || b.ID != initial.ID || !b.ReleaseReused || b.ReleaseBranch != branch || b.InitialSHA != head || b.ReleaseSHA != head || b.MainSHA != main || b.ReleaseCreatedAt == "" || b.Intent != nil || b.TagPlan != nil || b.Phase != "READY" {
		t.Fatalf("reuse did not attach the existing head: %+v, %v", b, err)
	}
	if git.pushes != 0 || f.run(f.remote, "show-ref") != refs {
		t.Fatal("preview or adoption attempted to write remote refs")
	}

	f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
	f.s.Genbu = f.g
	b, p, err = f.s.InspectBatch(ctx, b.ID)
	if err != nil || !b.ReleaseReused || b.ReleaseSHA != head || p.ReleaseSHA != head || p.MainInRelease {
		t.Fatalf("reopened batch lost the adopted release: %+v, %+v, %v", b, p, err)
	}
	if _, _, err = f.s.PrepareTag(ctx, b.ID, p); err == nil || !strings.Contains(err.Error(), "合并主分支到 release") {
		t.Fatalf("prepare skipped the main to release merge requirement: %v", err)
	}
	b, err = f.s.MergeMainToRelease(ctx, b.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err = f.s.PrepareTag(ctx, b.ID, p)
	if err != nil || b.TagPlan == nil || b.TagPlan.Tag != version || !strings.Contains(b.TagPlan.Diff, "release-only.txt") {
		t.Fatalf("first build missed the existing release changes: %+v, %v", b.TagPlan, err)
	}
}

func TestProductionReuseReleaseRejectsStaleConfirmation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const version, branch, ref = "2.3.44", "release/2.3.44", "refs/heads/release/2.3.44"
	p, err := f.s.Inspect(ctx, f.selection)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.s.PreviewRelease(ctx, p, version, true)
	if err != nil || plan.Preview.ReleaseSHA != "" {
		t.Fatalf("an absent branch should follow creation: %+v, %v", plan, err)
	}
	if b, err := f.s.ReuseRelease(ctx, plan.Preview, version, ""); err == nil || b.ID != "" {
		t.Fatalf("reuse accepted a plan without a remote head: %+v, %v", b, err)
	}
	head := f.feature(branch, "release-only.txt", "existing release content\n", f.base)
	plan, err = f.s.PreviewRelease(ctx, p, version, true)
	if err != nil || plan.Preview.ReleaseSHA != head {
		t.Fatalf("newly appeared branch was not observed: %+v, %v", plan, err)
	}
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	t.Run("moved", func(t *testing.T) {
		f.run(f.remote, "update-ref", ref, f.base)
		if b, err := f.s.ReuseRelease(ctx, plan.Preview, version, ""); err == nil || b.ID != "" || !strings.Contains(err.Error(), "release 已变化") {
			t.Fatalf("stale branch move accepted: %+v, %v", b, err)
		}
	})
	t.Run("deleted", func(t *testing.T) {
		f.run(f.remote, "update-ref", "-d", ref)
		if b, err := f.s.ReuseRelease(ctx, plan.Preview, version, ""); err == nil || b.ID != "" || !strings.Contains(err.Error(), "release 已变化") {
			t.Fatalf("deleted branch was recreated or adopted: %+v, %v", b, err)
		}
	})
	t.Run("occupied Tag", func(t *testing.T) {
		f.run(f.remote, "update-ref", ref, head)
		f.run(f.remote, "update-ref", "refs/tags/2.3.44.1", head)
		if b, err := f.s.ReuseRelease(ctx, plan.Preview, version, ""); err == nil || b.ID != "" || !strings.Contains(err.Error(), "Tag 2.3.44.1") {
			t.Fatalf("reuse ignored a new Tag reservation: %+v, %v", b, err)
		}
	})
	if batches, err := f.s.Store.List(); err != nil || len(batches) != 0 || git.pushes != 0 {
		t.Fatalf("rejected reuse wrote a batch or tried to push: %v, %v, %d", batches, err, git.pushes)
	}
}
