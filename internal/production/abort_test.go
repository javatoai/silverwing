package production

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type abortGitHook struct {
	Git
	calls  [][]string
	before func() error
	after  func()
}

func (g *abortGitHook) RunContext(ctx context.Context, path string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	g.calls = append(g.calls, slices.Clone(args))
	abort := slices.Equal(args, []string{"merge", "--abort"})
	if abort && g.before != nil {
		if err := g.before(); err != nil {
			return process.Result{}, err
		}
	}
	r, err := g.Git.RunContext(ctx, path, timeout, check, readOnly, args...)
	if abort && err == nil && g.after != nil {
		g.after()
	}
	return r, err
}

func (g *abortGitHook) aborts(t *testing.T) int {
	t.Helper()
	count := 0
	for _, args := range g.calls {
		if slices.Equal(args, []string{"merge", "--abort"}) {
			count++
			continue
		}
		if len(args) > 0 && slices.Contains([]string{"merge", "push", "reset", "clean", "checkout", "switch", "worktree"}, args[0]) {
			t.Fatalf("unexpected Git mutation during abort: %v", args)
		}
	}
	return count
}

func abortConflictFixture(t *testing.T, stage string) (*fixture, Batch) {
	t.Helper()
	f := newFixture(t)
	var b Batch
	var err error
	if stage == "main" {
		f.feature("master", "base.txt", "master change\n", f.base)
		fix := f.feature("hotfix/online", "base.txt", "online change\n", f.base)
		f.run(f.seed, "tag", "2.3.43.1", fix)
		f.run(f.seed, "push", "origin", "2.3.43.1")
		f.g.versions = []string{"2.3.43.1"}
		p, e := f.s.Inspect(context.Background(), f.selection, "")
		if e != nil {
			t.Fatal(e)
		}
		b, err = f.s.MergeProduction(context.Background(), "", p)
	} else {
		b = f.create()
		one := f.feature("feature/one", "base.txt", "feature one\n", f.base)
		b = f.publish(f.sync(f.merge(b, "feature/one", one)))
		if stage == "feature" {
			two := f.feature("feature/two", "base.txt", "feature two\n", f.base)
			b, err = f.s.MergeFeature(context.Background(), b.ID, "feature/two", two, b.ReleaseSHA)
		} else {
			f.feature("master", "base.txt", "new master change\n", f.base)
			_, p, e := f.s.InspectBatch(context.Background(), b.ID, "")
			if e != nil {
				t.Fatal(e)
			}
			b, err = f.s.MergeMainToRelease(context.Background(), b.ID, p)
		}
	}
	if err == nil || b.Phase != "CONFLICT" || b.Intent == nil || b.Intent.Merge == nil {
		t.Fatalf("missing %s conflict: %+v / %v", stage, b, err)
	}
	return f, b
}

func TestProductionAbortAllMergeStagesPreservesHistoryAndAllowsRetry(t *testing.T) {
	for _, stage := range []string{"feature", "main", "release-sync"} {
		t.Run(stage, func(t *testing.T) {
			f, b := abortConflictFixture(t, stage)
			m := *b.Intent.Merge
			f.write(m.Worktree, "base.txt", "partly resolved by user\n")
			f.run(m.Worktree, "add", "base.txt")
			f.write(m.Worktree, "untracked-user-note.txt", "keep this\n")
			beforeRefs := f.run(f.remote, "show-ref")
			g := &abortGitHook{Git: f.s.Git}
			f.s.Git = g
			calls := f.g.calls
			completed, err := f.s.AbortMerge(context.Background(), b.ID, b.Target, m)
			if err != nil || completed.Intent != nil || completed.Phase != "READY" || completed.Ended || completed.TagPlan != nil {
				t.Fatalf("abort failed: %+v / %v", completed, err)
			}
			if completed.MainSHA != b.MainSHA || completed.ReleaseSHA != b.ReleaseSHA || completed.InitialSHA != b.InitialSHA ||
				!reflect.DeepEqual(completed.Branches, b.Branches) || !reflect.DeepEqual(completed.Tags, b.Tags) {
				t.Fatal("abort changed an earlier successful result")
			}
			if !slices.Contains(completed.RetainedWorktrees, m.Worktree) || f.run(m.Worktree, "rev-parse", "HEAD") != m.BeforeSHA {
				t.Fatal("abort lost workspace or original HEAD")
			}
			content, err := os.ReadFile(filepath.Join(m.Worktree, "base.txt"))
			if err != nil || strings.TrimSpace(string(content)) != f.run(m.Worktree, "show", "HEAD:base.txt") {
				t.Fatal("manual conflict resolution was not discarded")
			}
			if text, err := os.ReadFile(filepath.Join(m.Worktree, "untracked-user-note.txt")); err != nil || string(text) != "keep this\n" {
				t.Fatal("untracked file lost")
			}
			if f.run(f.remote, "show-ref") != beforeRefs || f.g.calls != calls || g.aborts(t) != 1 {
				t.Fatal("abort changed remote refs, rechecked Genbu, or ran twice")
			}
			if _, err := f.s.AbortMerge(context.Background(), b.ID, b.Target, m); err == nil || g.aborts(t) != 1 {
				t.Fatal("duplicate confirmation performed another mutation")
			}
			f.s.Git = g.Git
			var retry Batch
			if stage == "feature" {
				retry, err = f.s.MergeFeature(context.Background(), b.ID, b.Intent.SourceBranch, m.SourceSHA, m.BeforeSHA)
			} else {
				_, p, e := f.s.InspectBatch(context.Background(), b.ID, "")
				if e != nil {
					t.Fatal(e)
				}
				if stage == "main" {
					retry, err = f.s.MergeProduction(context.Background(), b.ID, p)
				} else {
					retry, err = f.s.MergeMainToRelease(context.Background(), b.ID, p)
				}
			}
			if err == nil || retry.Phase != "CONFLICT" || retry.Intent.Merge.ID == m.ID || retry.Intent.Merge.Worktree == m.Worktree {
				t.Fatalf("new attempt did not get its own conflict workspace: %+v / %v", retry, err)
			}
			if f.run(f.remote, "show-ref") != beforeRefs {
				t.Fatal("conflicting retry unexpectedly pushed")
			}
		})
	}
}

func TestProductionAbortRecoversCommandCancellationAndCompletionSaveFailure(t *testing.T) {
	f, b := abortConflictFixture(t, "feature")
	for index, mode := range []string{"command-failure", "cancel-before", "cancel-after", "save-intent", "save-completion"} {
		t.Run(mode, func(t *testing.T) {
			if index > 0 {
				// Each previous case finished its abort. Start an independent
				// merge/worktree without rebuilding the same remote and Tag history.
				f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
				f.s.Genbu = f.g
				var err error
				b, err = f.s.MergeFeature(context.Background(), b.ID, b.Intent.SourceBranch, b.Intent.Merge.SourceSHA, b.ReleaseSHA)
				if err == nil || b.Phase != "CONFLICT" {
					t.Fatalf("missing new conflict: %v", err)
				}
			}
			m := *b.Intent.Merge
			refs := f.run(f.remote, "show-ref")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			g := &abortGitHook{Git: f.s.Git}
			f.s.Git = g
			switch mode {
			case "command-failure":
				g.before = func() error { return errors.New("synthetic abort failed") }
			case "cancel-before":
				g.before = func() error { cancel(); return context.Canceled }
			case "cancel-after":
				g.after = cancel
			case "save-intent", "save-completion":
				f.s.Store.writeAtomic = func(path string, data []byte, permissions os.FileMode) error {
					var record Batch
					if err := json.Unmarshal(data, &record); err != nil {
						return err
					}
					if mode == "save-intent" || record.Intent == nil {
						return errors.New("synthetic disk failure")
					}
					return storage.WriteFileAtomic(path, data, permissions)
				}
			}
			if _, err := f.s.AbortMerge(ctx, b.ID, b.Target, m); err == nil {
				t.Fatal("injected failure was hidden")
			}
			stored, err := f.s.Store.Load(b.ID)
			if err != nil || stored.Intent == nil || stored.Intent.Merge.ID != m.ID {
				t.Fatalf("recovery intent lost: %+v / %v", stored, err)
			}
			if mode == "save-intent" {
				if g.aborts(t) != 0 || stored.Phase != "CONFLICT" {
					t.Fatal("Git changed before durable abort intent")
				}
			} else if stored.Phase != abortingMerge {
				t.Fatal("incomplete abort lost its phase")
			}
			f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
			f.s.Genbu = f.g
			if mode != "save-intent" {
				for _, action := range []func() error{
					func() error { _, e := f.s.Resume(context.Background(), b.ID); return e },
					func() error { _, e := f.s.MergeProduction(context.Background(), b.ID, b.Intent.Preview); return e },
					func() error { _, e := f.s.RebasePending(context.Background(), b.ID, m.BeforeSHA); return e },
				} {
					if err := action(); err == nil || !strings.Contains(err.Error(), "正在撤回") {
						t.Fatalf("another entry restarted an aborted merge: %v", err)
					}
				}
			}
			retry := &abortGitHook{Git: f.s.Git}
			f.s.Git = retry
			completed, err := f.s.AbortMerge(context.Background(), b.ID, b.Target, m)
			if err != nil || completed.Intent != nil || completed.Phase != "READY" {
				t.Fatalf("restart recovery failed: %+v / %v", completed, err)
			}
			want := 1
			if mode == "cancel-after" || mode == "save-completion" {
				want = 0
			}
			if retry.aborts(t) != want || refs != f.run(f.remote, "show-ref") {
				t.Fatal("recovery repeated mutation or changed remote refs")
			}
		})
	}
}

func TestProductionAbortRejectsCommittedOrChangedTargets(t *testing.T) {
	f, b := abortConflictFixture(t, "feature")
	baseGit := f.s.Git
	for _, mode := range []string{"confirmation", "workspace", "branch", "merge-head", "orig-head", "config", "ended", "committed", "pushed"} {
		t.Run(mode, func(t *testing.T) {
			f.s.Git = baseGit
			if err := f.s.Store.Save(&b); err != nil {
				t.Fatal(err)
			}
			m := *b.Intent.Merge
			switch mode {
			case "committed", "pushed":
				if f.run(m.Worktree, "rev-parse", "HEAD") == m.BeforeSHA {
					f.write(m.Worktree, "base.txt", "resolved\n")
					f.run(m.Worktree, "add", "base.txt")
					f.run(m.Worktree, "commit", "-m", "manual resolution")
				}
				if mode == "pushed" {
					f.run(m.Worktree, "push", "origin", "HEAD:refs/heads/"+b.ReleaseBranch)
				}
			case "confirmation":
				m.ID = strings.Repeat("f", 32)
			case "workspace":
				m.Worktree = f.repo
			case "branch":
				f.run(m.Worktree, "symbolic-ref", "HEAD", "refs/heads/master")
				defer f.run(m.Worktree, "symbolic-ref", "HEAD", "refs/heads/"+m.Branch)
			case "merge-head":
				path := f.run(m.Worktree, "rev-parse", "--path-format=absolute", "--git-path", "MERGE_HEAD")
				if err := os.WriteFile(path, []byte(f.base+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.WriteFile(path, []byte(m.SourceSHA+"\n"), 0o600); err != nil {
						t.Error(err)
					}
				}()
				if f.run(m.Worktree, "rev-parse", "MERGE_HEAD") != f.base {
					t.Fatal("fixture did not replace merge source")
				}
			case "orig-head":
				f.run(m.Worktree, "update-ref", "ORIG_HEAD", f.base)
				defer f.run(m.Worktree, "update-ref", "ORIG_HEAD", m.BeforeSHA)
			case "config":
				config, err := f.s.Config.Load()
				if err != nil {
					t.Fatal(err)
				}
				original := config
				original.Groups = slices.Clone(config.Groups)
				original.Groups[0].Services = slices.Clone(config.Groups[0].Services)
				defer func() {
					if err := f.s.Config.Save(original); err != nil {
						t.Error(err)
					}
				}()
				config.Groups[0].Services[0].MasterBranch = "origin/another-main"
				if err := f.s.Config.Save(config); err != nil {
					t.Fatal(err)
				}
			case "ended":
				if _, err := f.s.End(context.Background(), b.ID); err != nil {
					t.Fatal(err)
				}
			}
			head := f.run(b.Intent.Merge.Worktree, "rev-parse", "HEAD")
			refs := f.run(f.remote, "show-ref")
			g := &abortGitHook{Git: f.s.Git}
			f.s.Git = g
			if _, err := f.s.AbortMerge(context.Background(), b.ID, b.Target, m); err == nil {
				t.Fatal("unsafe abort accepted")
			}
			if g.aborts(t) != 0 || f.run(b.Intent.Merge.Worktree, "rev-parse", "HEAD") != head || f.run(f.remote, "show-ref") != refs {
				t.Fatal("rejected abort changed Git state")
			}
		})
	}
}
