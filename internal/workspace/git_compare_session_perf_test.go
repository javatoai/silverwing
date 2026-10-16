package workspace

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestGitCompareSessionPerformanceEvidence keeps the fixture private to t.TempDir
// so the optimization can be checked against the old prepare-per-preview path.
func TestGitCompareSessionPerformanceEvidence(t *testing.T) {
	repo := newCompareTestRepo(t)
	body := strings.Repeat("baseline line\n", 64)
	for index := 0; index < 160; index++ {
		writeCompareFile(t, repo.path, fmt.Sprintf("src/pkg%02d/file%03d.go", index%16, index), body)
	}
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	for index := 0; index < 8; index++ {
		writeCompareFile(t, repo.path, fmt.Sprintf("src/pkg%02d/file%03d.go", index%16, index), strings.ReplaceAll(body, "baseline", "modified"))
	}
	service, ws := repo.serviceWorkspace("feature")
	request := CompareRequest{Mode: CompareUncommitted}
	filesStarted := time.Now()
	files, err := service.FilesContext(context.Background(), ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	filesElapsed := time.Since(filesStarted)
	if len(files.Files) != 8 {
		t.Fatalf("fixture changed files = %d, want 8", len(files.Files))
	}
	previewDurations := make([]time.Duration, 0, len(files.Files))
	for _, change := range files.Files {
		started := time.Now()
		if _, err := service.PreviewContext(context.Background(), ws, request, change, WorkspaceMainBranch{}); err != nil {
			t.Fatal(err)
		}
		previewDurations = append(previewDurations, time.Since(started))
	}
	t.Logf("prepare_and_list_ms=%d preview_ms=%v", filesElapsed.Milliseconds(), durationMillis(previewDurations))
}

func TestGitCompareSessionReusePerformanceEvidence(t *testing.T) {
	body := strings.Repeat("baseline line\n", 64)
	runGitCompareSessionReusePerformanceEvidence(t, body)
}

func TestGitCompareSessionQuarterMiBPerformanceEvidence(t *testing.T) {
	body := strings.Repeat("baseline line for bounded file preview performance measurement. \n", 4200)
	body = body[:256*1024]
	runGitCompareSessionReusePerformanceEvidence(t, body)
}

func runGitCompareSessionReusePerformanceEvidence(t *testing.T, body string) {
	t.Helper()
	repo := newCompareTestRepo(t)
	for index := 0; index < 160; index++ {
		writeCompareFile(t, repo.path, fmt.Sprintf("src/pkg%02d/file%03d.go", index%16, index), body)
	}
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	for index := 0; index < 8; index++ {
		writeCompareFile(t, repo.path, fmt.Sprintf("src/pkg%02d/file%03d.go", index%16, index), strings.ReplaceAll(body, "baseline", "modified"))
	}
	service, ws := repo.serviceWorkspace("feature")
	request := CompareRequest{Mode: CompareUncommitted}
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	started := time.Now()
	files, err := service.FilesSessionContext(context.Background(), manager, "perf-task", "perf-config", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	prepareAndList := time.Since(started)
	if len(files.Files) != 8 || files.SessionID == "" {
		t.Fatalf("session returned %d files and ID %q", len(files.Files), files.SessionID)
	}
	request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
	firstReads := make([]time.Duration, 0, len(files.Files))
	for _, change := range files.Files {
		started = time.Now()
		preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "perf-task", "perf-config", ws, request, change, WorkspaceMainBranch{})
		if err != nil {
			t.Fatal(err)
		}
		if preview.Comparison == nil || preview.Content == nil || preview.Content.Truncated {
			t.Fatalf("performance sample must return a complete comparison: notice=%q", preview.Notice)
		}
		firstReads = append(firstReads, time.Since(started))
	}
	if cached := len(manager.sessions[files.SessionID].cache); cached != len(files.Files) {
		t.Fatalf("performance fixture cached %d of %d complete ordinary files", cached, len(files.Files))
	}
	hotReads := make([]time.Duration, 0, len(files.Files))
	for _, change := range files.Files {
		started = time.Now()
		if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "perf-task", "perf-config", ws, request, change, WorkspaceMainBranch{}); err != nil {
			t.Fatal(err)
		}
		hotReads = append(hotReads, time.Since(started))
	}
	t.Logf("file_bytes=%d session_prepare_and_list_ms=%d first_preview_ms=%v cached_preview_ms=%v", len(body), prepareAndList.Milliseconds(), durationMillis(firstReads), durationMillis(hotReads))
}

func durationMillis(values []time.Duration) []int64 {
	result := make([]int64, len(values))
	for index, value := range values {
		result[index] = value.Milliseconds()
	}
	return result
}

func TestGitComparePrefetchPerformanceEvidence(t *testing.T) {
	for _, fileBytes := range []int{8 * 1024, 256 * 1024} {
		t.Run(fmt.Sprintf("bytes_%d", fileBytes), func(t *testing.T) {
			repo := newCompareTestRepo(t)
			body := strings.Repeat("baseline source line for review performance.\n", fileBytes/40+1)[:fileBytes]
			for index := 0; index < 160; index++ {
				writeCompareFile(t, repo.path, fmt.Sprintf("src/file%03d.go", index), body)
			}
			commitCompare(t, repo.path, "base")
			gitCompare(t, repo.path, "checkout", "-b", "feature")
			for index := 0; index < 8; index++ {
				writeCompareFile(t, repo.path, fmt.Sprintf("src/file%03d.go", index), strings.ReplaceAll(body, "baseline", "modified"))
			}
			service, ws := repo.serviceWorkspace("feature")
			manager := NewWorkspaceGitDiffSessionManager()
			t.Cleanup(func() { _ = manager.Close(context.Background()) })
			request := CompareRequest{Mode: CompareUncommitted}
			started := time.Now()
			files, err := service.FilesSessionContext(context.Background(), manager, "perf-task", "perf-config", ws, request, WorkspaceMainBranch{})
			prepare := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			if len(files.Files) != 8 {
				t.Fatalf("fixture changed %d files", len(files.Files))
			}
			request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
			read := func(change CompareChange) time.Duration {
				t.Helper()
				started := time.Now()
				preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "perf-task", "perf-config", ws, request, change, WorkspaceMainBranch{})
				elapsed := time.Since(started)
				if err != nil {
					t.Fatal(err)
				}
				if preview.Comparison == nil || preview.Content == nil || preview.Content.Truncated || preview.Content.Text != strings.ReplaceAll(body, "baseline", "modified") {
					t.Fatalf("incomplete preview for %s", change.Path)
				}
				return elapsed
			}
			cold := read(files.Files[0])
			started = time.Now()
			if err := service.PrefetchSessionContext(context.Background(), manager, files.SessionID, "perf-task", "perf-config", ws, request, files.Files[0], files.Files, WorkspaceMainBranch{}); err != nil {
				t.Fatal(err)
			}
			prefetch := time.Since(started)
			warmed, revisit := make([]float64, 0, 7), make([]float64, 0, 8)
			for _, change := range files.Files[1:] {
				if _, hit := manager.sessions[files.SessionID].cached(change); !hit {
					t.Fatalf("prefetch missed %s", change.Path)
				}
				warmed = append(warmed, float64(read(change).Microseconds())/1000)
			}
			for _, change := range files.Files {
				revisit = append(revisit, float64(read(change).Microseconds())/1000)
			}
			t.Logf("file_bytes=%d files=160 changed=8 prepare_ms=%.3f cold_first_preview_ms=%.3f prefetch_remaining_ms=%.3f warmed_first_select_ms=%v revisit_ms=%v retained_bytes=%d", fileBytes, float64(prepare.Microseconds())/1000, float64(cold.Microseconds())/1000, float64(prefetch.Microseconds())/1000, warmed, revisit, manager.sessions[files.SessionID].cacheSize)
		})
	}
}
