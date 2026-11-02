package platform

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

// The configured HTTPS URL is rewritten to an owned local bare repository in
// each Git invocation. Production checkout arguments run against actual Git,
// without network access, global Git configuration changes, or user installs.
func TestSkillNativeGitSelectedRefsAndManualCategorizedUpdate(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("native Git is unavailable")
	}
	ctx := context.Background()
	fixture := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(fixture, "empty-gitconfig"))
	if err := os.WriteFile(filepath.Join(fixture, "empty-gitconfig"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, gitPath, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated Git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	work, remote := filepath.Join(fixture, "working"), filepath.Join(fixture, "remote.git")
	git("init", work)
	git("-C", work, "symbolic-ref", "HEAD", "refs/heads/release/default")
	git("-C", work, "config", "user.name", "Skills isolated test")
	git("-C", work, "config", "user.email", "skills-test@example.invalid")
	directory := writeCompletionSkill(t, work, "skills/example", "example")
	if err := os.MkdirAll(filepath.Join(directory, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(directory, "resources", "asset.txt")
	if err := os.WriteFile(asset, []byte("default branch resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("-C", work, "add", "--all")
	git("-C", work, "commit", "-m", "default fixture")
	defaultCommit := git("-C", work, "rev-parse", "HEAD")
	git("-C", work, "tag", "v1.2.3")
	git("-C", work, "checkout", "-b", "feature/nested")
	if err := os.MkdirAll(filepath.Join(work, "skills", "database"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory, filepath.Join(work, "skills", "database", "example")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "skills", "database", "example", "resources", "asset.txt"), []byte("selected feature resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("-C", work, "add", "--all")
	git("-C", work, "commit", "-m", "categorized fixture")
	featureCommit := git("-C", work, "rev-parse", "HEAD")
	git("init", "--bare", remote)
	git("-C", work, "push", remote, "refs/heads/release/default", "refs/heads/feature/nested", "refs/tags/v1.2.3")
	git("--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/release/default")
	remotePath := filepath.ToSlash(remote)
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = "/" + remotePath
	}
	localURL := (&url.URL{Scheme: "file", Path: remotePath}).String()
	source := completionSkillSource()
	service, installedHome := completionSkillService(t, source)
	read := func(ref, wantRef, wantCommit, wantPath, wantAsset string) SkillSourceSnapshot {
		t.Helper()
		if ref == "" {
			source.Ref = nil
		} else {
			source.Ref = stringPtr(ref)
		}
		if _, err := service.config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
			c.SkillSources = []domain.SkillSource{source}
			return c, nil
		}); err != nil {
			t.Fatal(err)
		}
		checkout, err := service.skillCheckoutPath(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
			t.Fatal(err)
		}
		run := func(args []string) (string, error) {
			options := []string{"-c", "protocol.file.allow=always", "-c", "url." + localURL + ".insteadOf=" + source.RepositoryURL}
			command := exec.CommandContext(ctx, gitPath, append(options, skillGitArguments(args)...)...)
			output, err := command.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("isolated Skill Git %v: %w: %s", args, err, output)
			}
			return strings.TrimSpace(string(output)), err
		}
		origin, err := prepareSkillCheckout(ctx, source, checkout, run)
		if err != nil {
			t.Fatal(err)
		}
		skills, roots, err := discoverSkillsWithContext(ctx, source, checkout)
		if err != nil || len(skills) != 1 || skills[0].SourcePath != wantPath || origin.RepositoryURL != source.RepositoryURL || origin.Ref != wantRef || origin.Commit != wantCommit {
			t.Fatalf("wrong selected repository/ref/content: origin=%+v skills=%+v roots=%v err=%v", origin, skills, roots, err)
		}
		body, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(wantPath), "resources", "asset.txt"))
		if err != nil || string(body) != wantAsset {
			t.Fatal("selected ref returned wrong file contents", string(body), err)
		}
		fingerprint := skillSourceFingerprint(source)
		status := SkillSourceSnapshot{SourceID: source.ID, SourceFingerprint: &fingerprint, ResolvedOrigin: origin, Skills: skills, DiscoveredRoots: roots}
		if _, err := verifySkillCheckoutOrigin(source, status, checkout, run); err != nil {
			t.Fatal(err)
		}
		return status
	}
	first := read("", "release/default", defaultCommit, "skills/example", "default branch resource")
	state := emptyExtensionState()
	state.SkillSources[source.ID] = first
	if _, err := service.installSkillFromSnapshot(ctx, source, "example", false, state, *first.ResolvedOrigin); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(installedHome, ".agents", "skills", "example")
	assertInstalled := func(want string) {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(installed, "resources", "asset.txt"))
		if err != nil || string(body) != want {
			t.Fatal("incorrect flat installed resource", string(body), err)
		}
		if _, err := os.Lstat(filepath.Join(installed, ".git")); !os.IsNotExist(err) {
			t.Fatal("checkout Git metadata was installed", err)
		}
	}
	assertInstalled("default branch resource")
	second := read("feature/nested", "feature/nested", featureCommit, "skills/database/example", "selected feature resource")
	if validateSkillSourceSnapshot(source, first) == nil {
		t.Fatal("previous default-branch snapshot remained valid after ref change")
	}
	assertInstalled("default branch resource")
	state, err = service.loadExtensionState()
	if err != nil {
		t.Fatal(err)
	}
	state.SkillSources[source.ID] = second
	if _, err := service.installSkillFromSnapshot(ctx, source, "example", false, state, *second.ResolvedOrigin); err != nil {
		t.Fatal(err)
	}
	assertInstalled("selected feature resource")
	state, err = service.loadExtensionState()
	if err != nil || len(state.ManagedSkills) != 1 || state.ManagedSkills[0].InstalledOrigin == nil || *state.ManagedSkills[0].InstalledOrigin != *second.ResolvedOrigin {
		t.Fatal("manual categorized update lost identity or actual installed origin", state.ManagedSkills, err)
	}
	read("v1.2.3", "v1.2.3", defaultCommit, "skills/example", "default branch resource")
	read(featureCommit, featureCommit, featureCommit, "skills/database/example", "selected feature resource")
	git("--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/feature/nested")
	read("", "feature/nested", featureCommit, "skills/database/example", "selected feature resource")
	assertInstalled("selected feature resource")
}
