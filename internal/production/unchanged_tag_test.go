package production

import (
	"context"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestProductionTagsAllowUnmergedAndUnchangedRelease(t *testing.T) {
	f := newFixture(t)
	b := f.create()
	head := b.ReleaseSHA
	for n, name := range []string{"2.3.44", "2.3.44.1", "2.3.44.2"} {
		calls := f.g.calls
		b = f.sync(b)
		if b.TagPlan == nil || b.TagPlan.Tag != name || b.TagPlan.Commit != head || b.TagPlan.Diff != "" || b.TagPlan.Commits != "" {
			t.Fatalf("unchanged release did not prepare %s: %+v", name, b.TagPlan)
		}
		if f.g.calls <= calls || len(b.Tags) != n || len(b.Branches) != 0 {
			t.Fatal("preparation skipped the fresh baseline check or wrote history")
		}
		refs, err := f.s.remoteRefs(context.Background(), b.Target)
		if err != nil || refs["refs/tags/"+name] != "" {
			t.Fatalf("preparation pushed an unconfirmed Tag: %v", err)
		}
		token := b.TagPlan.Token
		calls = f.g.calls
		b = f.publish(b)
		if f.g.calls <= calls || b.ReleaseSHA != head || b.TagPlan != nil || b.Intent != nil || b.Ended || len(b.Tags) != n+1 {
			t.Fatalf("publishing changed the batch or skipped final validation: %+v", b)
		}
		if got := f.run(f.remote, "rev-parse", "refs/tags/"+name+"^{commit}"); got != head {
			t.Fatalf("Tag points to %s, want %s", got, head)
		}
		if got := f.run(f.remote, "cat-file", "-t", "refs/tags/"+name); got != "tag" {
			t.Fatal("expected annotated Tag")
		}
		if _, err := f.s.Publish(context.Background(), b.ID, token); err == nil {
			t.Fatal("consumed confirmation published another Tag")
		}
		observed, _, err := f.s.InspectBatch(context.Background(), b.ID, "")
		if err != nil || observed.TagPlan != nil || observed.Intent != nil || len(observed.Tags) != n+1 {
			t.Fatalf("refresh prepared or published the next Tag: %v", err)
		}
	}
	g := &perTagGenbu{}
	f.s.Genbu = g
	b, err := f.s.RefreshBuild(context.Background(), b.ID)
	if err != nil || len(b.Tags) != 3 || strings.Join(g.seen, ",") != "2.3.44,2.3.44.1,2.3.44.2" ||
		b.Tags[0].Status.Build != domain.GenbuFailed || b.Tags[1].Status.Build != domain.GenbuSuccess || b.Tags[2].Status.Build != domain.GenbuFailed || b.TagPlan != nil {
		t.Fatalf("same-commit build statuses leaked between Tags: %+v / %v", b.Tags, err)
	}
}

func TestProductionUnchangedTagRejectsNumberRaceAndRechecks(t *testing.T) {
	f := newFixture(t)
	b := f.publish(f.sync(f.create()))
	b = f.sync(b)
	if b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.1" {
		t.Fatal("missing repeated Tag plan")
	}
	token := b.TagPlan.Token
	f.run(f.seed, "tag", "-a", "2.3.44.1", f.base, "-m", "another publisher")
	f.run(f.seed, "push", "origin", "refs/tags/2.3.44.1")
	object := f.run(f.remote, "rev-parse", "refs/tags/2.3.44.1")
	if _, err := f.s.Publish(context.Background(), b.ID, token); err == nil {
		t.Fatal("number race accepted the old confirmation")
	}
	if got := f.run(f.remote, "rev-parse", "refs/tags/2.3.44.1"); got != object {
		t.Fatal("occupied Tag was overwritten")
	}
	b = f.sync(b)
	if b.TagPlan == nil || b.TagPlan.Tag != "2.3.44.2" || b.TagPlan.Token == token {
		t.Fatal("fresh check did not require a new numbered confirmation")
	}
	// Even a same-commit Tag must reject a failed live production query.
	f.g.versionErr = context.DeadlineExceeded
	if _, err := f.s.Publish(context.Background(), b.ID, b.TagPlan.Token); err == nil {
		t.Fatal("same-commit Tag bypassed the fresh production check")
	}
	refs, err := f.s.remoteRefs(context.Background(), b.Target)
	if err != nil || refs["refs/tags/2.3.44.2"] != "" {
		t.Fatalf("failed production check wrote a Tag: %v", err)
	}
	f.g.versionErr = nil
	b = f.publish(f.sync(b))
	if len(b.Tags) != 2 || b.Tags[1].Name != "2.3.44.2" || b.Tags[0].Commit != b.Tags[1].Commit {
		t.Fatalf("fresh confirmation lost unchanged-release numbering: %+v", b.Tags)
	}
}
