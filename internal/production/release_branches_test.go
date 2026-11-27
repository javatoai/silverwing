package production

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProductionInspectReleaseBranchesSkipsBaselineAndRetainsDrift(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	const featureBranch = "feature/branch-choices"
	feature := f.feature(featureBranch, "feature.txt", "not yet in release\n", f.base)
	main := f.feature(b.Target.MainBranch, "main.txt", "main update\n", f.base)
	path := filepath.Join(f.s.Store.Directory, b.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	f.g.onVersions = func() { t.Fatal("loading feature branch choices queried the production version") }
	f.g.onStatus = func() { t.Fatal("loading feature branch choices queried Tag status") }
	ctx := WithProgress(context.Background(), func(p Progress) {
		if p.Step == 1 {
			t.Fatalf("loading feature branch choices reported production baseline progress: %+v", p)
		}
	})
	inspected, p, err := f.s.InspectReleaseBranches(ctx, b.ID)
	if err != nil || inspected.ID != b.ID || p.Target != b.Target || p.MainSHA != main || p.ReleaseSHA != b.ReleaseSHA || p.BranchHeads[featureBranch] != feature ||
		!slices.Equal(p.Branches, []string{featureBranch, b.Target.MainBranch, b.ReleaseBranch}) {
		t.Fatalf("branch-only inspection did not return current branch heads: %+v / %v", p, err)
	}
	if p.RuntimeAvailable || p.ProductionTag != "" || len(p.Tags) != 0 || p.Diff != "" || p.Commits != "" || git.fetches != 0 {
		t.Fatalf("unchanged release inspection did extra work: %+v / %+v", p, git)
	}

	drift := f.feature(b.ReleaseBranch, "drift.txt", "external release change\n", b.ReleaseSHA)
	f.run(f.remote, "update-ref", "-d", "refs/heads/"+b.Target.MainBranch)
	remoteBefore := f.run(f.remote, "show-ref")
	inspected, p, err = f.s.InspectReleaseBranches(ctx, b.ID)
	if err != nil || inspected.ReleaseSHA != b.ReleaseSHA || p.ReleaseSHA != drift || p.MainSHA != "" || p.BranchHeads[featureBranch] != feature ||
		!strings.Contains(p.Diff, "drift.txt") || !strings.Contains(p.Commits, b.ReleaseBranch) {
		t.Fatalf("branch-only inspection lost release drift without adoption: %+v / %+v / %v", inspected, p, err)
	}
	readArgs := []string{"ls-remote", "--heads", b.Target.Remote, "ls-remote", "--heads", b.Target.Remote}
	fetchArgs := []string{"fetch", "--no-tags", b.Target.Remote, "+refs/heads/" + b.ReleaseBranch + ":" + namespace(b.Target) + "/heads/" + b.ReleaseBranch}
	if git.reads != 2 || git.fetches != 1 || git.pushes != 0 || !slices.Equal(git.readArgs, readArgs) || !slices.Equal(git.fetchArgs, fetchArgs) {
		t.Fatalf("unexpected branch inspection Git operations: %+v", git)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) || f.run(f.remote, "show-ref") != remoteBefore {
		t.Fatalf("branch inspection changed the batch or remote refs: %v", err)
	}
}
