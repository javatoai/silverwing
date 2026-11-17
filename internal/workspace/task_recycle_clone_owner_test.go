package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecycleCloneOwnerCurrentAndLegacyMarkers(t *testing.T) {
	const expected = "0123456789abcdef0123456789abcdef01234567"
	t.Run("current", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeOwnerMarker(t, cloneMarker(target), expected)
		kind, err := recycleCloneOwner(target, expected)
		if err != nil || kind != cloneRecycleCurrent {
			t.Fatalf("current marker: kind=%q err=%v", kind, err)
		}
	})
	t.Run("legacy", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeOwnerMarker(t, filepath.Join(target, ".git", legacyCloneOwnerMarker), expected)
		kind, err := recycleCloneOwner(target, expected)
		if err != nil || kind != cloneRecycleLegacy {
			t.Fatalf("legacy marker: kind=%q err=%v", kind, err)
		}
	})
}

func TestRecycleCloneOwnerMissingAndRejectsMismatchedMarkers(t *testing.T) {
	const expected = "0123456789abcdef0123456789abcdef01234567"
	t.Run("both absent", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		kind, err := recycleCloneOwner(target, expected)
		if err != nil || kind != cloneRecycleMissing {
			t.Fatalf("missing markers: kind=%q err=%v", kind, err)
		}
	})
	t.Run("current mismatch does not fall back", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeOwnerMarker(t, cloneMarker(target), "different-current")
		writeOwnerMarker(t, filepath.Join(target, ".git", legacyCloneOwnerMarker), expected)
		if kind, err := recycleCloneOwner(target, expected); err == nil || kind != "" {
			t.Fatalf("current mismatch fell back to legacy: kind=%q err=%v", kind, err)
		}
	})
	t.Run("legacy mismatch", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeOwnerMarker(t, filepath.Join(target, ".git", legacyCloneOwnerMarker), "different-legacy")
		if kind, err := recycleCloneOwner(target, expected); err == nil || kind != "" {
			t.Fatalf("legacy mismatch accepted: kind=%q err=%v", kind, err)
		}
	})
	t.Run("non-file current marker", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		if err := os.Mkdir(cloneMarker(target), 0700); err != nil {
			t.Fatal(err)
		}
		if kind, err := recycleCloneOwner(target, expected); err == nil || kind != "" {
			t.Fatalf("directory marker accepted: kind=%q err=%v", kind, err)
		}
	})
	t.Run("missing clone", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "gone")
		if kind, err := recycleCloneOwner(target, expected); err == nil || kind != "" {
			t.Fatalf("missing clone accepted: kind=%q err=%v", kind, err)
		}
	})
	t.Run("empty expected owner", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		if kind, err := recycleCloneOwner(target, " "); err == nil || kind != "" {
			t.Fatalf("empty owner accepted: kind=%q err=%v", kind, err)
		}
	})
}

func TestRecycleCloneOwnerRejectsMarkerSymlink(t *testing.T) {
	target := newOwnerMarkerClone(t)
	external := filepath.Join(t.TempDir(), "owner")
	if err := os.WriteFile(external, []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, cloneMarker(target)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if kind, err := recycleCloneOwner(target, "owner"); err == nil || kind != "" {
		t.Fatalf("symlink marker accepted: kind=%q err=%v", kind, err)
	}
}

func TestVerifyUnmarkedArchivedCloneOrigin(t *testing.T) {
	const origin = "D:/local repositories/demo.git"
	zero := strings.Repeat("0", 40)
	commit := strings.Repeat("a", 40)
	valid := fmt.Sprintf("%s %s Former User <user@example.invalid> 1731840000 +0800\tclone: from %s\n", zero, commit, origin)
	t.Run("matching initial clone event", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeHeadReflog(t, target, valid)
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("matching SHA256 initial clone event", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		zero256 := strings.Repeat("0", 64)
		commit256 := strings.Repeat("b", 64)
		line := fmt.Sprintf("%s %s Former User <user@example.invalid> 1731840000 +0800\tclone: from %s\n", zero256, commit256, origin)
		writeHeadReflog(t, target, line)
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("origin mismatch", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeHeadReflog(t, target, valid)
		if err := verifyUnmarkedArchivedCloneOrigin(target, "D:/other/repo.git"); err == nil {
			t.Fatal("mismatched origin accepted")
		}
	})
	t.Run("nonzero old object id", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeHeadReflog(t, target, strings.Replace(valid, zero, strings.Repeat("1", 40), 1))
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err == nil {
			t.Fatal("non-initial reflog event accepted")
		}
	})
	t.Run("later matching event is not enough", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		first := strings.Replace(valid, "clone: from "+origin, "commit: later event", 1)
		writeHeadReflog(t, target, first+valid)
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err == nil {
			t.Fatal("later clone event accepted as first event")
		}
	})
	t.Run("oversized first line", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeHeadReflog(t, target, strings.Repeat("x", maxCloneReflogLine+1)+"\n")
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err == nil {
			t.Fatal("oversized reflog line accepted")
		}
	})
	t.Run("missing reflog", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		if err := verifyUnmarkedArchivedCloneOrigin(target, origin); err == nil {
			t.Fatal("missing reflog accepted")
		}
	})
	t.Run("empty origin", func(t *testing.T) {
		target := newOwnerMarkerClone(t)
		writeHeadReflog(t, target, valid)
		if err := verifyUnmarkedArchivedCloneOrigin(target, " "); err == nil {
			t.Fatal("empty origin accepted")
		}
	})
}

func newOwnerMarkerClone(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "clone")
	if err := os.MkdirAll(filepath.Join(target, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	return target
}

func writeOwnerMarker(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeHeadReflog(t *testing.T, target, contents string) {
	t.Helper()
	logs := filepath.Join(target, ".git", "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logs, "HEAD"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
