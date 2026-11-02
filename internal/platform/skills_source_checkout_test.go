package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func completionGit(t *testing.T, source domain.SkillSource, checkout, remote, branch, commit string) (skillGitRunner, *[]string) {
	t.Helper()
	commands := []string{}
	return func(args []string) (string, error) {
		command := strings.Join(args, " ")
		commands = append(commands, command)
		switch {
		case len(args) > 0 && args[0] == "clone":
			if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o700); err != nil {
				return "", err
			}
			remote = source.RepositoryURL
		case strings.Contains(command, " config --get remote.origin.url"):
			return remote, nil
		case strings.Contains(command, " ls-remote --symref origin HEAD"):
			return "ref: refs/heads/" + branch + "\tHEAD\n" + commit + "\tHEAD", nil
		case strings.Contains(command, " rev-parse --verify HEAD"):
			return commit, nil
		case strings.Contains(command, " fetch "), strings.Contains(command, " checkout "):
		default:
			t.Fatalf("unexpected Git command: %v", args)
		}
		return "", nil
	}, &commands
}

func TestSkillCheckoutDefaultAndExplicitRefs(t *testing.T) {
	for _, ref := range []string{"", "feature/nested", "v1.2.3", strings.Repeat("b", 40)} {
		t.Run(ref, func(t *testing.T) {
			source := completionSkillSource()
			if ref != "" {
				source.Ref = stringPtr(ref)
			}
			checkout := filepath.Join(t.TempDir(), "cache")
			commit := strings.Repeat("a", 40)
			run, commands := completionGit(t, source, checkout, "", "release/default", commit)
			origin, err := prepareSkillCheckout(context.Background(), source, checkout, run)
			if err != nil {
				t.Fatal(err)
			}
			wantRef, fetchRef := ref, ref
			if ref == "" {
				wantRef, fetchRef = "release/default", "refs/heads/release/default"
			}
			joined := strings.Join(*commands, "\n")
			if origin.RepositoryURL != source.RepositoryURL || origin.Ref != wantRef || origin.Commit != commit || !strings.Contains(joined, "fetch --depth 1 origin "+fetchRef) || !strings.Contains(joined, "checkout --force --detach FETCH_HEAD") || strings.Contains(joined, "pull") || strings.Contains(joined, "--branch") {
				t.Fatal("checkout lost actual origin/ref/commit or used old branch semantics", origin, joined)
			}
		})
	}
}

func TestSkillCheckoutDifferentIdentityNeverReusesOldOrigin(t *testing.T) {
	source := completionSkillSource()
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	first, err := service.skillCheckoutPath(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []domain.SkillSource{
		{ID: source.ID, Name: source.Name, RepositoryURL: "https://example.invalid/other.git"},
		{ID: source.ID, Name: source.Name, RepositoryURL: source.RepositoryURL, Ref: stringPtr("another-branch")},
		{ID: source.ID, Name: source.Name, RepositoryURL: source.RepositoryURL, SkillRoot: stringPtr("categorized")},
	} {
		path, err := service.skillCheckoutPath(changed)
		if err != nil || path == first {
			t.Fatal("identity reused a different source's cache", path, err)
		}
	}
	checkout := filepath.Join(t.TempDir(), "cache")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	run, commands := completionGit(t, source, checkout, "https://example.invalid/old.git", "main", strings.Repeat("a", 40))
	if _, err := prepareSkillCheckout(context.Background(), source, checkout, run); err != nil {
		t.Fatal(err)
	}
	if len(*commands) < 3 || !strings.HasPrefix((*commands)[1], "clone ") {
		t.Fatal("mismatched origin was fetched before fresh cloning", *commands)
	}
}

func TestSkillCheckoutDefaultBranchRefreshAndCancellation(t *testing.T) {
	source := completionSkillSource()
	checkout := filepath.Join(t.TempDir(), "cache")
	for _, branch := range []string{"main", "new-default"} {
		run, _ := completionGit(t, source, checkout, source.RepositoryURL, branch, strings.Repeat("a", 40))
		origin, err := prepareSkillCheckout(context.Background(), source, checkout, run)
		if err != nil || origin.Ref != branch {
			t.Fatal("default branch was guessed from old checkout", origin, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run, commands := completionGit(t, source, checkout, source.RepositoryURL, "main", strings.Repeat("a", 40))
	stopping := func(args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), " fetch ") {
			cancel()
			return "", context.Canceled
		}
		return run(args)
	}
	if origin, err := prepareSkillCheckout(ctx, source, checkout, stopping); !errors.Is(err, context.Canceled) || origin != nil {
		t.Fatal(origin, err)
	}
	if strings.Contains(strings.Join(*commands, "\n"), " checkout ") {
		t.Fatal("canceled fetch published a checkout")
	}
	if _, err := skillDefaultBranch(strings.Repeat("a", 40) + "\tHEAD"); err == nil {
		t.Fatal("unknown default branch was guessed")
	}
}

func TestSkillCheckoutInstallRejectsChangedCache(t *testing.T) {
	source := completionSkillSource()
	status := completionSkillSnapshot(source)
	for _, scenario := range []string{"url", "commit"} {
		remote, commit := source.RepositoryURL, status.ResolvedOrigin.Commit
		if scenario == "url" {
			remote = "https://example.invalid/wrong.git"
		} else {
			commit = strings.Repeat("b", 40)
		}
		run := func(args []string) (string, error) {
			if strings.Contains(strings.Join(args, " "), " config ") {
				return remote, nil
			}
			return commit, nil
		}
		if _, err := verifySkillCheckoutOrigin(source, status, "unused", run); err == nil {
			t.Fatal("changed checkout acquired old provenance", scenario)
		}
	}
}
