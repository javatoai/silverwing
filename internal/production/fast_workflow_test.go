package production

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/process"
)

// The remote becomes unreachable immediately after acknowledging a push. A
// successful write must still complete; only an explicit recovery may read it.
type observedGit struct {
	Git
	reads, fetches, pushes, readsAfterPush int
	fetchArgs                              []string
	acknowledged                           bool
}

type interruptMergeGit struct{ Git }

func (g interruptMergeGit) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	if len(args) > 0 && args[0] == "merge" {
		return process.Result{}, context.Canceled
	}
	return g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
}

func (g *observedGit) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	if len(args) > 0 {
		switch args[0] {
		case "ls-remote", "fetch":
			if g.acknowledged {
				g.readsAfterPush++
				return process.Result{}, errors.New("synthetic remote unavailable after successful push")
			}
			if args[0] == "ls-remote" {
				g.reads++
			} else {
				g.fetches++
				g.fetchArgs = append(g.fetchArgs, args...)
			}
		case "push":
			g.pushes++
		}
	}
	r, err := g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
	if len(args) > 0 && args[0] == "push" && err == nil && r.ExitCode == 0 {
		g.acknowledged = true
	}
	return r, err
}

func TestProductionSuccessfulBranchPushDoesNotReadRemoteAgain(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/fast", "fast.txt", "fast\n", f.base)
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	done, err := f.s.MergeFeature(context.Background(), b.ID, "feature/fast", sha, b.ReleaseSHA)
	if err != nil || done.Intent != nil || len(done.Branches) != 1 {
		t.Fatalf("acknowledged branch push did not complete: %v %+v", err, done)
	}
	if git.reads != 1 || git.fetches != 1 || git.pushes != 1 || git.readsAfterPush != 0 {
		t.Fatalf("remote commands: reads=%d fetches=%d pushes=%d readsAfterPush=%d", git.reads, git.fetches, git.pushes, git.readsAfterPush)
	}
	if strings.Contains(strings.Join(git.fetchArgs, " "), "*") || strings.Contains(strings.Join(git.fetchArgs, " "), "refs/tags/") {
		t.Fatalf("feature merge fetched unrelated references: %v", git.fetchArgs)
	}
}

func TestProductionPreparedTagUsesOneObservationAndPushReceipt(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	f.g.calls, f.g.statusCalls = 0, 0
	prepared, preview, err := f.s.PrepareTag(context.Background(), b.ID, Preview{Target: b.Target})
	if err != nil || prepared.TagPlan == nil || prepared.TagPlan.Tag != "2.3.44" || preview.ReleaseSHA != b.ReleaseSHA {
		t.Fatalf("fresh preparation failed: %v %+v", err, prepared)
	}
	// The accepted snapshot remains authoritative until this push. A new
	// observation is made on the next build click or an explicit recovery.
	f.g.versionErr = context.DeadlineExceeded
	done, err := f.s.Publish(context.Background(), b.ID, prepared.TagPlan.Token)
	if err != nil || len(done.Tags) != 1 || done.Intent != nil || done.TagPlan != nil {
		t.Fatalf("acknowledged Tag push did not complete: %v %+v", err, done)
	}
	if git.reads != 1 || git.fetches != 0 || git.pushes != 1 || git.readsAfterPush != 0 || f.g.calls != 1 || f.g.statusCalls != 0 {
		t.Fatalf("build commands: remote reads=%d fetches=%d pushes=%d readsAfterPush=%d versions=%d status=%d", git.reads, git.fetches, git.pushes, git.readsAfterPush, f.g.calls, f.g.statusCalls)
	}
	t.Logf("build: remote reference reads=%d, fetches=%d, pushes=%d, version queries=%d, status queries=%d, reads after push=%d", git.reads, git.fetches, git.pushes, f.g.calls, f.g.statusCalls, git.readsAfterPush)
}

func TestProductionSuccessfulReleasePushDoesNotReadRemoteAgain(t *testing.T) {
	f := newFixture(t)
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	b, err := f.s.Create(context.Background(), p, "2.3.44", "")
	if err != nil || b.Intent != nil || b.ReleaseSHA != p.MainSHA || git.readsAfterPush != 0 || git.reads != 1 || git.pushes != 1 {
		t.Fatalf("release creation did not finish from its push receipt: %v %+v / %+v", err, b, git)
	}
}

func TestProductionPreparationUsesLatestRemoteTagNumber(t *testing.T) {
	f := newFixture(t)
	b := f.publish(f.sync(f.create()))
	f.run(f.seed, "tag", "-a", "2.3.44.7", f.base, "-m", "another build")
	f.run(f.seed, "push", "origin", "refs/tags/2.3.44.7")
	git := &observedGit{Git: f.s.Git}
	f.s.Git = git
	b, _, err := f.s.PrepareTag(context.Background(), b.ID, Preview{Target: b.Target})
	if err != nil || b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.8" || git.reads != 1 || git.fetches != 0 || git.pushes != 0 {
		t.Fatalf("numbering ignored latest remote Tags or fetched unrelated objects: %v %+v / %+v", err, b.TagPlan, git)
	}
}

func TestProductionSaveFailureAfterPushRetainsRecoverableReceipt(t *testing.T) {
	for _, kind := range []string{"branch", "tag"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			b := f.create()
			sha := ""
			if kind == "tag" {
				var err error
				b, _, err = f.s.PrepareTag(context.Background(), b.ID, Preview{Target: b.Target})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				sha = f.feature("feature/save", "save.txt", "save\n", f.base)
			}
			f.s.Store.writeAtomic = func(path string, data []byte, mode os.FileMode) error {
				var next Batch
				if err := json.Unmarshal(data, &next); err != nil {
					return err
				}
				if next.Intent == nil {
					return errors.New("synthetic result save failure")
				}
				return os.WriteFile(path, data, mode)
			}
			git := &observedGit{Git: f.s.Git}
			f.s.Git = git
			var err error
			if kind == "tag" {
				_, err = f.s.Publish(context.Background(), b.ID, b.TagPlan.Token)
			} else {
				_, err = f.s.MergeFeature(context.Background(), b.ID, "feature/save", sha, b.ReleaseSHA)
			}
			if err == nil || !strings.Contains(err.Error(), "已推送，本地记录保存失败") || git.pushes != 1 || git.readsAfterPush != 0 {
				t.Fatalf("lost the successful push receipt: %v / %+v", err, git)
			}
			pending, err := f.s.Store.Load(b.ID)
			if err != nil || pending.Intent == nil {
				t.Fatalf("push intent did not survive failed result save: %v %+v", err, pending)
			}
			f.s.Store.writeAtomic = nil
			git.acknowledged = false
			done, err := f.s.Resume(context.Background(), b.ID)
			if err != nil || done.Intent != nil || git.pushes != 1 || (kind == "tag" && len(done.Tags) != 1) || (kind == "branch" && len(done.Branches) != 1) {
				t.Fatalf("receipt recovery repeated or lost the write: %v %+v / %+v", err, done, git)
			}
		})
	}
}

func TestProductionResumeAfterWorktreeCreatedBeforeMerge(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/interrupted", "interrupted.txt", "interrupted\n", f.base)
	f.s.Git = interruptMergeGit{Git: f.s.Git}
	paused, err := f.s.MergeFeature(context.Background(), b.ID, "feature/interrupted", sha, b.ReleaseSHA)
	if err == nil || paused.Intent == nil || paused.Intent.Merge == nil || paused.Intent.Merge.AfterSHA != "" {
		t.Fatalf("expected interruption before merge: %v %+v", err, paused)
	}
	f.s.Git = f.git
	done, err := f.s.Resume(context.Background(), b.ID)
	if err != nil || done.Intent != nil || len(done.Branches) != 1 {
		t.Fatalf("clean retained worktree could not resume its merge: %v %+v", err, done)
	}
}
