package production

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestProductionInspectionUsesRemoteNamesWithoutFetchingUnrelatedTags(t *testing.T) {
	f := newFixture(t)
	for _, tag := range []string{"2.3.43.1", "2.3.44.2", "9.0.0"} {
		f.run(f.seed, "tag", "-a", tag, f.base, "-m", tag)
	}
	f.run(f.seed, "push", "origin", "--tags")
	f.g.versions = []string{"2.3.43.1"}
	git := &observedGit{Git: f.git}
	f.s.Git = git
	p, err := f.s.Inspect(context.Background(), f.selection)
	if err != nil || p.ProductionTag != "2.3.43.1" || p.ProductionSHA != f.base {
		t.Fatalf("could not resolve automatic production Tag: %+v / %v", p, err)
	}
	if strings.Join(p.Tags, ",") != "2.3.43,2.3.43.1,2.3.44.2,9.0.0" || p.SuggestedVersion != "2.3.45" {
		t.Fatalf("version policy lost remote Tag names: %v, suggested %s", p.Tags, p.SuggestedVersion)
	}
	fetch := strings.Join(git.fetchArgs, " ")
	if git.fetches != 1 || !strings.Contains(fetch, "refs/tags/2.3.43.1") ||
		strings.Contains(fetch, "2.3.44.2") || strings.Contains(fetch, "9.0.0") {
		t.Fatalf("baseline fetched unrelated Tag objects: %v", git.fetchArgs)
	}
}

func TestProductionRepeatedMergesAndBuildCyclesInvalidateOldPreparation(t *testing.T) {
	f := newFixture(t)
	p, err := f.s.Inspect(context.Background(), f.selection)
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
	_, preview, err := f.s.InspectBatch(ctx, b.ID)
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
