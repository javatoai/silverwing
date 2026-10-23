package production

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func TestProductionTagsUseRemoteCreatorDatesWithoutChangingVersionPolicy(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GIT_COMMITTER_DATE", "2020-01-01T00:00:00Z")
	old := f.feature("feature/dated", "old.txt", "old\n", f.base)
	f.run(f.seed, "tag", "9.0.0", old)
	t.Setenv("GIT_COMMITTER_DATE", "2020-03-01T00:00:00Z")
	f.run(f.seed, "tag", "-a", "1.0.0", old, "-m", "new tag, old commit")
	f.run(f.seed, "tag", "-a", "1.0.1", old, "-m", "same date")
	t.Setenv("GIT_COMMITTER_DATE", "2020-02-01T00:00:00Z")
	newer := f.feature("feature/dated-new", "new.txt", "new\n", f.base)
	f.run(f.seed, "tag", "0.1.0-beta", newer)
	f.run(f.seed, "push", "origin", "--tags")
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	var selected []string
	for _, name := range p.Tags {
		if name != "2.3.43" {
			selected = append(selected, name)
		}
	}
	if strings.Join(selected, ",") != "1.0.0,1.0.1,0.1.0-beta,9.0.0" {
		t.Fatalf("creator-date order: %v", selected)
	}
	if p.SuggestedVersion != "9.0.1" {
		t.Fatalf("presentation order changed numeric version selection: %s", p.SuggestedVersion)
	}
}

type productionOrderGit struct {
	output string
	err    error
}

func (g productionOrderGit) RunContext(context.Context, string, time.Duration, bool, bool, ...string) (process.Result, error) {
	return process.Result{Stdout: g.output}, g.err
}

func TestProductionTagDateMissingAndUnrelatedObjectsSortLast(t *testing.T) {
	target := Target{CommonDirectory: "synthetic", Remote: "origin"}
	prefix := namespace(target) + "/tags/"
	s := &Service{Git: productionOrderGit{output: prefix + "known\x00a\x00100\n" + prefix + "stale\x00other\x00999\n" + prefix + "missing\x00b\x00"}}
	refs := references{"refs/tags/known": "a", "refs/tags/stale": "c", "refs/tags/missing": "b", "refs/tags/absent": "d"}
	names, err := s.tagsByCreation(context.Background(), target, refs)
	if err != nil || strings.Join(names, ",") != "known,absent,missing,stale" {
		t.Fatalf("bad missing/stale fallback: %v %v", names, err)
	}
	s.Git = productionOrderGit{err: errors.New("synthetic read failure")}
	names, err = s.tagsByCreation(context.Background(), target, refs)
	if err == nil || strings.Join(names, ",") != "absent,known,missing,stale" {
		t.Fatalf("bad failed-read fallback: %v %v", names, err)
	}
}

func TestProductionRepeatedMergesAndBuildCyclesInvalidateOldPreparation(t *testing.T) {
	f := newFixture(t)
	p, err := f.s.Inspect(context.Background(), f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	var progress []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { progress = append(progress, p) })
	b, err := f.s.Create(ctx, p, "2.3.44", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 || progress[len(progress)-1].Step != 3 || progress[len(progress)-1].State != "complete" {
		t.Fatalf("missing saved initialization progress: %v", progress)
	}
	one := f.feature("feature/one", "one.txt", "one\n", f.base)
	two := f.feature("feature/two", "two.txt", "two\n", f.base)
	b = f.sync(f.merge(f.merge(b, "feature/one", one), "feature/two", two))
	oldToken := b.TagPlan.Token
	// Canceling a confirmation is no backend mutation. Another merge must
	// invalidate that prepared Token even when it is the same branch updated.
	one = f.feature("feature/one", "one.txt", "one updated\n", one)
	b = f.merge(b, "feature/one", one)
	if b.TagPlan != nil {
		t.Fatal("new merge retained old Tag plan")
	}
	if _, err := f.s.Publish(context.Background(), b.ID, oldToken); err == nil {
		t.Fatal("stale confirmation pushed Tag")
	}
	b = f.publish(f.sync(b))
	if b.Ended || len(b.Branches) != 3 || len(b.Tags) != 1 {
		t.Fatalf("cycle ended or lost records: %+v", b)
	}
	unchanged := f.sync(b)
	if unchanged.TagPlan == nil || unchanged.TagPlan.Tag != "2.3.44.1" || len(unchanged.Tags) != 1 {
		t.Fatal("repeated sync must prepare the next Tag without publishing it")
	}
	one = f.feature("feature/one", "one.txt", "one again\n", one)
	b = f.merge(b, "feature/one", one)
	_, preview, err := f.s.InspectBatch(ctx, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.CheckForTag(ctx, b.ID, preview)
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.s.Publish(ctx, b.ID, b.TagPlan.Token)
	if err != nil {
		t.Fatal(err)
	}
	if b.Ended || len(b.Tags) != 2 || b.Tags[1].Name != "2.3.44.1" {
		t.Fatalf("second cycle: %+v", b.Tags)
	}
	if progress[len(progress)-1].Step != 6 || progress[len(progress)-1].State != "complete" {
		t.Fatal("missing verified Tag progress")
	}
}

func TestProductionContainedFeaturePreservesPreparedTag(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	sha := f.feature("feature/already", "already.txt", "already\n", f.base)
	b = f.sync(f.merge(b, "feature/already", sha))
	token, head := b.TagPlan.Token, b.ReleaseSHA
	b = f.merge(b, "feature/already", sha)
	if b.TagPlan == nil || b.TagPlan.Token != token || b.ReleaseSHA != head || len(b.Branches) != 1 {
		t.Fatal("contained feature discarded valid preparation or appended duplicate history")
	}
}

type perTagGenbu struct {
	fakeGenbu
	seen []string
}

func (g *perTagGenbu) Status(_ context.Context, _ domain.AppConfig, _ string, tag string) (domain.GenbuTagProbeStatus, error) {
	g.seen = append(g.seen, tag)
	state := domain.GenbuFailed
	if tag == "2.3.44.1" {
		state = domain.GenbuSuccess
	}
	return domain.GenbuTagProbeStatus{Build: state}, nil
}

func TestProductionBuildRefreshRetainsIndependentOlderPendingResults(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	b.Tags = []Tag{{Name: "2.3.44", Status: unknownStatus()}, {Name: "2.3.44.1", Status: unknownStatus()}}
	if err := f.s.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	g := &perTagGenbu{}
	f.s.Genbu = g
	b, err := f.s.RefreshBuild(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(g.seen, ",") != "2.3.44,2.3.44.1" || b.Tags[0].Status.Build != domain.GenbuFailed || b.Tags[1].Status.Build != domain.GenbuSuccess {
		t.Fatalf("mixed Tag results: %+v %v", b.Tags, g.seen)
	}
}
