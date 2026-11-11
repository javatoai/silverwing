package production

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/process"
)

func TestProductionVersionSequenceOfflineFixture(t *testing.T) {
	f := newFixture(t)
	f.run(f.seed, "tag", "2.4.36", f.base)
	f.run(f.seed, "tag", "2.4.37", f.base)
	f.run(f.seed, "branch", "release/2.4.37", f.base)
	t.Setenv("GIT_COMMITTER_DATE", "2020-07-29T00:00:00Z")
	f.run(f.seed, "tag", "-a", "6.6.6", "-m", "test_jvm", f.base)
	f.run(f.seed, "push", "origin", "refs/tags/2.4.36", "refs/tags/2.4.37", "refs/tags/6.6.6", "refs/heads/release/2.4.37")
	f.g.versions = []string{"2.4.36"}
	ctx := context.Background()
	p, err := f.s.Inspect(ctx, f.selection, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("offline baseline=%s tags=%v branches=%v suggestion=%s", p.ProductionTag, p.Tags, p.Branches, p.SuggestedVersion)
	if p.SuggestedVersion != "2.4.38" {
		t.Errorf("current production series should suggest 2.4.38, got %s", p.SuggestedVersion)
	}
	before := f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags", "refs/heads/release/2.4.37")
	_, err = f.s.Create(ctx, p, "2.4.37", "")
	t.Logf("occupied release/2.4.37: %v", err)
	if err == nil || !strings.Contains(err.Error(), "2.4.37") || !strings.Contains(err.Error(), "release/2.4.37") || !strings.Contains(err.Error(), "release/2.4.38") {
		t.Errorf("occupied version should name the existing Tag, branch and available suggestion: %v", err)
	}
	b, err := f.s.Create(ctx, p, "2.4.38", "")
	t.Logf("create release/2.4.38: %v", err)
	if err != nil {
		t.Fatal(err)
	}
	if b.ReleaseBranch != "release/2.4.38" || b.InitialSHA != p.MainSHA || b.Intent != nil {
		t.Fatalf("new release was not created from confirmed main: %+v", b)
	}
	after := f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags", "refs/heads/release/2.4.37")
	if after != before {
		t.Fatal("creation changed existing remote Tags or release/2.4.37")
	}
}

func TestProductionVersionSuggestionsUseConfirmedSeries(t *testing.T) {
	const maximum = "18446744073709551615"
	for _, tc := range []struct {
		name, production, want, errorPart string
		tags, branches                    []string
		heads                             map[string]string
	}{
		{name: "historical series", production: "2.4.36", tags: []string{"6.6.6", "2.4.37", "2.4.36"}, branches: []string{"release/2.4.37"}, want: "2.4.38"},
		{name: "branch only", production: "2.4.36", branches: []string{"release/2.4.40", "feature/2.4.99", "release/6.6.99"}, want: "2.4.41"},
		{name: "branch heads only", production: "2.4.36", heads: map[string]string{"release/2.4.37": "observed-sha"}, want: "2.4.38"},
		{name: "hotfix family", production: "2.4.36", tags: []string{"2.4.37.9", "2.4.99-beta", "2.4.100.beta-1"}, branches: []string{"release/2.4.100-rc1"}, want: "2.4.38"},
		{name: "only current baseline in series", production: "2.4.36", tags: []string{"6.6.6"}, want: "2.4.37"},
		{name: "current hotfix", production: "2.4.36.7", want: "2.4.37"},
		{name: "zero baseline", production: "0.0.0", want: "0.0.1"},
		{name: "maximum hotfix suffix", production: "2.4.36", tags: []string{"2.4.37." + maximum}, want: "2.4.38"},
		{name: "no baseline", tags: []string{"2.4.37", "6.6.6"}, errorPart: "无法自动建议"},
		{name: "nonnumeric baseline", production: "production-current", tags: []string{"6.6.6"}, errorPart: "手工填写"},
		{name: "malformed baseline", production: "2.4", errorPart: "无法自动建议"},
		{name: "overflowing baseline", production: "2.4.18446744073709551616", errorPart: "无法自动建议"},
		{name: "production patch limit", production: "2.4." + maximum, errorPart: "达到上限"},
		{name: "remote Tag patch limit", production: "2.4.36", tags: []string{"2.4." + maximum + ".1"}, errorPart: "达到上限"},
		{name: "remote branch patch limit", production: "2.4.36", branches: []string{"release/2.4." + maximum}, errorPart: "达到上限"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Preview{ProductionTag: tc.production, Tags: slices.Clone(tc.tags), Branches: slices.Clone(tc.branches), BranchHeads: tc.heads}
			for range 2 {
				got, err := SuggestedVersion(p)
				if got != tc.want || (tc.errorPart == "" && err != nil) || (tc.errorPart != "" && (err == nil || !strings.Contains(err.Error(), tc.errorPart))) {
					t.Fatalf("suggestion=%q error=%v, want %q / %q", got, err, tc.want, tc.errorPart)
				}
				slices.Reverse(p.Tags)
				slices.Reverse(p.Branches)
			}
		})
	}
}

func TestProductionNewReleaseVersionContext(t *testing.T) {
	const maximum = "18446744073709551615"
	for _, tc := range []struct {
		name, version string
		preview       Preview
		wantError     []string
	}{
		{name: "current series", version: "2.4.38", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"6.6.6", "2.4.37"}}},
		{name: "major upgrade", version: "3.0.0", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"6.6.6", "2.4.37"}, Branches: []string{"release/2.4.37"}}},
		{name: "minor upgrade", version: "2.5.0", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"6.6.6", "2.4.37"}}},
		{name: "Tag and branch occupied", version: "2.4.37", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"6.6.6", "2.4.37"}, Branches: []string{"release/2.4.37"}}, wantError: []string{"Tag 2.4.37", "分支 release/2.4.37", "release/2.4.38"}},
		{name: "hotfix occupied", version: "2.4.37", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"2.4.37.2"}}, wantError: []string{"占用", "Tag 2.4.37.2", "release/2.4.38"}},
		{name: "branch occupied", version: "2.4.37", preview: Preview{ProductionTag: "2.4.36", Branches: []string{"release/2.4.37"}}, wantError: []string{"占用", "分支 release/2.4.37", "release/2.4.38"}},
		{name: "branch head occupied", version: "2.4.37", preview: Preview{ProductionTag: "2.4.36", BranchHeads: map[string]string{"release/2.4.37": "observed-sha"}}, wantError: []string{"占用", "release/2.4.37", "release/2.4.38"}},
		{name: "major series has later Tag", version: "3.0.0", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"3.0.2", "6.6.6"}}, wantError: []string{"同系列", "Tag 3.0.2", "release/3.0.3"}},
		{name: "minor series has later branch", version: "2.5.0", preview: Preview{ProductionTag: "2.4.36", Branches: []string{"release/2.5.2"}}, wantError: []string{"同系列", "release/2.5.2", "release/2.5.3"}},
		{name: "hotfix in upgrade series", version: "3.0.0", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"3.0.0.1"}}, wantError: []string{"占用", "3.0.0.1", "release/3.0.1"}},
		{name: "older series", version: "2.3.99", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"2.4.37"}}, wantError: []string{"线上生产 Tag 2.4.36", "release/2.4.38"}},
		{name: "equal production", version: "2.4.36", preview: Preview{ProductionTag: "2.4.36"}, wantError: []string{"必须大于", "2.4.36", "release/2.4.37"}},
		{name: "behind current hotfix", version: "2.4.36", preview: Preview{ProductionTag: "2.4.36.1"}, wantError: []string{"线上生产 Tag 2.4.36.1", "release/2.4.37"}},
		{name: "pre-release does not occupy", version: "2.4.38", preview: Preview{ProductionTag: "2.4.36", Tags: []string{"2.4.38.beta-1"}, Branches: []string{"release/2.4.38-rc1"}}},
		{name: "manual nonnumeric production", version: "2.4.38", preview: Preview{ProductionTag: "production-current", Manual: true, Tags: []string{"6.6.6"}}},
		{name: "manual still respects target series", version: "3.0.0", preview: Preview{ProductionTag: "production-current", Manual: true, Tags: []string{"3.0.2"}}, wantError: []string{"3.0.2", "release/3.0.3"}},
		{name: "policy leaves baseline confirmation to workflow", version: "0.0.0", preview: Preview{}},
		{name: "upgrade beyond patch limit", version: "3.0.0", preview: Preview{ProductionTag: "2.4." + maximum, Tags: []string{"6.6.6"}}},
		{name: "valid maximum patch", version: "2.4." + maximum, preview: Preview{ProductionTag: "2.4.36"}},
		{name: "overflowing input", version: "2.4.18446744073709551616", wantError: []string{"三段正式数字"}},
		{name: "occupied patch limit", version: "2.4." + maximum, preview: Preview{ProductionTag: "2.4.36", Tags: []string{"2.4." + maximum}}, wantError: []string{"占用", "达到上限", "手工填写"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, err := ReleaseVersion("release/"+tc.version, tc.preview)
			if len(tc.wantError) == 0 {
				if err != nil || version != tc.version {
					t.Fatalf("new release rejected: %q / %v", version, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid or occupied release accepted")
			}
			for _, part := range tc.wantError {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("error %q does not include %q", err, part)
				}
			}
		})
	}
}

func TestProductionCreateRechecksVersionReservations(t *testing.T) {
	for _, ref := range []string{"refs/heads/release/2.3.44", "refs/tags/2.3.44", "refs/tags/2.3.44.2"} {
		t.Run(ref, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p, err := f.s.Inspect(ctx, f.selection, "")
			if err != nil {
				t.Fatal(err)
			}
			f.run(f.remote, "update-ref", ref, f.base)
			before := f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)")
			_, err = f.s.Create(ctx, p, "2.3.44", "")
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/tags/")) || !strings.Contains(err.Error(), "release/2.3.45") {
				t.Fatalf("write did not recheck the new reservation: %v", err)
			}
			batches, err := f.s.Store.List()
			if err != nil || len(batches) != 0 || f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)") != before {
				t.Fatal("rejected creation changed remote refs or created a batch")
			}
		})
	}
}

func TestProductionManualNonnumericBaselineAndExistingCreateRecovery(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.run(f.seed, "tag", "production-current", f.base)
	f.run(f.seed, "tag", "6.6.6", f.base)
	f.run(f.seed, "push", "origin", "refs/tags/production-current", "refs/tags/6.6.6")
	f.g.versionErr = errors.New("offline runtime unavailable")
	p, err := f.s.Inspect(ctx, f.selection, "")
	if err != nil || p.SuggestedVersion != "" || p.ProductionTag != "" {
		t.Fatalf("unavailable baseline produced a suggestion: %+v / %v", p, err)
	}
	if _, err = f.s.Create(ctx, p, "2.3.44", ""); err == nil || !strings.Contains(err.Error(), "生产基线") {
		t.Fatalf("missing baseline bypassed existing workflow guard: %v", err)
	}
	p, err = f.s.Inspect(ctx, f.selection, "production-current")
	if err != nil || !p.Manual || p.SuggestedVersion != "" || !strings.Contains(p.Warning, "手工填写") {
		t.Fatalf("manual nonnumeric baseline: %+v / %v", p, err)
	}
	b, err := f.s.Create(ctx, p, "2.3.44", "")
	if err != nil {
		t.Fatalf("supported manual production Tag flow rejected: %v", err)
	}
	// Model an older persisted creation whose push succeeded before the result
	// was saved. New-release policy must not invalidate or recreate this batch.
	f.run(f.remote, "update-ref", "refs/tags/2.3.50", f.base)
	b.InitialSHA, b.ReleaseSHA = "", ""
	b.Intent = &Intent{Kind: "create", Step: "release-create", Preview: p}
	if err = f.s.Store.Save(&b); err != nil {
		t.Fatal(err)
	}
	f.s = New(f.s.Paths, f.s.Config, f.git, process.NewRunner())
	f.s.Genbu = f.g
	stored, err := f.s.Store.Load(b.ID)
	if err != nil || stored.Version != "2.3.44" || stored.Intent == nil {
		t.Fatalf("existing batch could not load: %+v / %v", stored, err)
	}
	before := f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)")
	done, err := f.s.Resume(ctx, b.ID)
	if err != nil || done.ID != b.ID || done.Intent != nil || done.InitialSHA != p.MainSHA || len(done.Tags) != 0 {
		t.Fatalf("existing creation recovery changed behavior: %+v / %v", done, err)
	}
	if f.run(f.remote, "for-each-ref", "--format=%(refname) %(objectname)") != before {
		t.Fatal("recovery changed existing remote refs")
	}
}
