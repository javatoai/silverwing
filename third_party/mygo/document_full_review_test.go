//go:build !js

package mygo

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Keep the old equality decision suspended while another Set finishes. The
// fixture is deliberately unregistered: it opens no window, changes no app
// paths, and can only persist inside this test's temporary directory.
func TestDocumentFullReviewStaleEqualKeepsNewerDirtyStateAndRecovery(t *testing.T) {
	equalEntered := make(chan string, 1)
	dirtyCompared := make(chan string, 1)
	releaseEqual := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseEqual) }) }
	t.Cleanup(release)

	var document *Document[string]
	kind := &docKind{docKindConfig: docKindConfig{autosave: AutosaveInPlace, autosaveDelay: time.Hour}}
	documents := &Documents[string]{kind: kind, opts: DocumentsOptions[string]{
		Write: func(w io.Writer, text string) error {
			_, err := io.WriteString(w, text)
			return err
		},
		Equal: func(candidate, saved string) bool {
			// Reenter the document before reaching the barrier. Moving Equal
			// back under the document mutex would fail the bounded wait.
			current := document.Content()
			if candidate == "saved" && saved == "saved" {
				equalEntered <- current
				<-releaseEqual
			}
			if candidate == "dirty" {
				dirtyCompared <- current
			}
			return candidate == saved
		},
	}}
	document = documents.newDocument("saved", "concurrent-set")
	document.path = filepath.Join(t.TempDir(), "document.txt")
	if err := os.WriteFile(document.path, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Model a real older unsaved revision that already has a recovery copy.
	// A stale clean receipt must not clear either its marker or its file.
	document.id = "captured-recovery"
	document.content, document.edited = "older dirty", true
	document.changes, document.savedAt, document.backedUp = 2, 1, 2
	document.dirtySince = time.Now()
	recoveryDirectory := t.TempDir()
	kind.recovery = &recoveryStore{k: kind, dir: recoveryDirectory, entries: map[string]recoveryEntry{
		document.id: {ID: document.id, Path: document.path},
	}}
	recoveryPath := filepath.Join(recoveryDirectory, document.id)
	if err := os.WriteFile(recoveryPath, []byte("older dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use the normal persistence callback but keep it far outside the test.
	// Holding this stable timer also permits a nonblocking armed-state check.
	document.timer = time.AfterFunc(time.Hour, func() { docWork.Go(document.persist) })
	timer := document.timer
	t.Cleanup(func() { timer.Stop() })

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		document.Set("saved")
	}()
	if current := fullReviewDocumentEqualityRead(t, "first equality callback", equalEntered); current != "saved" {
		t.Fatalf("first callback read %q, want its saved candidate", current)
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		document.Set("dirty")
	}()
	fullReviewDocumentWait(t, "newer unequal Set while Equal is paused", secondDone)
	if current := fullReviewDocumentEqualityRead(t, "newer equality callback", dirtyCompared); current != "dirty" {
		t.Fatalf("newer callback read %q, want dirty", current)
	}
	if document.Content() != "dirty" || !document.IsEdited() {
		t.Fatal("newer Set did not establish the dirty state before the old callback resumed")
	}

	release()
	fullReviewDocumentWait(t, "older equal Set", firstDone)
	// If a stale clean decision queued recovery removal, make it observable
	// before checking the file, without polling or relying on a timer firing.
	docWork.Wait()
	if got := document.Content(); got != "dirty" {
		t.Errorf("older equality callback replaced the newer content: %q", got)
	}
	if !document.IsEdited() {
		t.Error("older equality callback marked the newer dirty content clean")
	}
	document.mu.Lock()
	dirtySince, backedUp := document.dirtySince, document.backedUp
	armed := timer.Stop()
	document.mu.Unlock()
	if dirtySince.IsZero() || !armed {
		t.Errorf("stale equality suppressed persistence: dirtySince=%v timerArmed=%v", dirtySince, armed)
	}
	if backedUp != 2 {
		t.Errorf("stale equality discarded the older recovery marker: %d", backedUp)
	}
	if data, err := os.ReadFile(recoveryPath); err != nil || string(data) != "older dirty" {
		t.Errorf("stale equality discarded the recovery copy: %q, %v", data, err)
	}
	if data, err := os.ReadFile(document.path); err != nil || string(data) != "saved" {
		t.Errorf("fixture unexpectedly persisted content during the barrier: %q, %v", data, err)
	}
}

func fullReviewDocumentWait(t *testing.T, operation string, done <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case <-done:
	case <-deadline.C:
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func fullReviewDocumentEqualityRead(t *testing.T, operation string, observed <-chan string) string {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case current := <-observed:
		return current
	case <-deadline.C:
		t.Fatalf("timed out waiting for unlocked Content() in %s", operation)
		return ""
	}
}
