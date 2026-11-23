package profilelaunch

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryFlagsResolveEquivalentAliasesWithoutCreatingProfile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "New Profile")
	alias := root + string(filepath.Separator) + "." + string(filepath.Separator) + "New Profile"
	var directories DirectoryFlags
	parent := flag.NewFlagSet("parent", flag.ContinueOnError)
	directories.Register(parent)
	if err := parent.Parse([]string{"--home", alias, "tag"}); err != nil {
		t.Fatal(err)
	}
	subcommand := flag.NewFlagSet("subcommand", flag.ContinueOnError)
	directories.Register(subcommand)
	if err := subcommand.Parse([]string{"--conf-dir", home}); err != nil {
		t.Fatal(err)
	}
	actual, err := directories.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if actual != want {
		t.Fatalf("resolved home = %q, want %q", actual, want)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("resolution created the profile: %v", err)
	}
	if err := subcommand.Parse([]string{"--conf-dir", filepath.Join(root, "Different")}); err != nil {
		t.Fatal(err)
	}
	if _, err := directories.Resolve(); err == nil || !strings.Contains(err.Error(), "不同") {
		t.Fatalf("expected a conflicting directory error, got %v", err)
	}
}

func TestCanonicalHomeResolvesDirectoryAliasesAndDesktopLocksArePerProfile(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "First")
	second := filepath.Join(root, "Second")
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatal(err)
	}
	firstHome, err := CanonicalHome(first)
	if err != nil {
		t.Fatal(err)
	}
	release, err := LockDesktop(firstHome)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	if _, err := LockDesktop(firstHome); err == nil || !strings.Contains(err.Error(), "另一个") {
		t.Fatalf("same profile was not locked: %v", err)
	}
	releaseSecond, err := LockDesktop(second)
	if err != nil {
		t.Fatalf("independent profile was blocked: %v", err)
	}
	t.Cleanup(releaseSecond)
	release()
	reopened, err := LockDesktop(firstHome)
	if err != nil {
		t.Fatalf("released profile could not be reopened: %v", err)
	}
	t.Cleanup(reopened)
	alias := filepath.Join(root, "Alternate")
	if err := os.Symlink(first, alias); err != nil {
		t.Logf("directory symlink unavailable: %v", err)
		return
	}
	aliasHome, err := CanonicalHome(alias)
	if err != nil {
		t.Fatal(err)
	}
	if aliasHome != firstHome {
		t.Fatalf("alias %q resolved to %q, want %q", alias, aliasHome, firstHome)
	}
	if _, err := LockDesktop(aliasHome); err == nil {
		t.Fatal("alias acquired the already-locked profile")
	}
}
