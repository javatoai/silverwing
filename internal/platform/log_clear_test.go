package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/storage"
)

func applicationLogsClearFixture(t *testing.T) (*Service, storage.Paths, []string) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	if err := os.MkdirAll(paths.Logs, 0o700); err != nil {
		t.Fatal(err)
	}
	names := []string{"application-2026-10-14.jsonl", "application-2026-10-15.jsonl"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(paths.Logs, name), []byte("old log content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(paths, nil, nil), paths, names
}

func TestApplicationLogsClearOnlyOwnDailyFiles(t *testing.T) {
	s, paths, names := applicationLogsClearFixture(t)
	protected := []string{filepath.Join(paths.Logs, "evidence.log"), filepath.Join(paths.Logs, "application-2026-10-14.jsonl.gz"), filepath.Join(paths.Logs, "nested", "application-2026-10-14.jsonl"), filepath.Join(paths.Tasks, "application-2026-10-14.jsonl"), filepath.Join(paths.Home, "repository", "task.log")}
	for _, path := range protected {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := s.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home})
	if err != nil {
		t.Fatal(err)
	}
	result := data.(ApplicationLogClearResult)
	if result.ClearedFiles != 2 || result.FailedFiles != 0 || !result.Changed {
		t.Fatalf("clear result: %+v", result)
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(paths.Logs, name))
		if err != nil || info.Size() != 0 {
			t.Fatal("daily file not cleared", err)
		}
	}
	for _, path := range protected {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "keep" {
			t.Fatal("unrelated file changed", path, err)
		}
	}
	if data, err := s.executeLogs("logs.list", nil); err != nil || len(data.([]applicationEvent)) != 0 {
		t.Fatal("cleared view not empty", err)
	}
}

func TestApplicationLogsClearCancelEmptyAndPathBoundary(t *testing.T) {
	s, paths, names := applicationLogsClearFixture(t)
	for _, payload := range []map[string]any{{"expectedHome": paths.Home}, {"confirmed": true, "expectedHome": t.TempDir()}, {"confirmed": true, "expectedHome": paths.Home, "path": t.TempDir()}} {
		if _, err := s.Execute(context.Background(), "logs.clear", payload); err == nil {
			t.Fatal("unconfirmed/external operation accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Execute(ctx, "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(paths.Logs, name))
		if err != nil || string(data) != "old log content\n" {
			t.Fatal("canceled operation changed logs")
		}
	}
	outside := t.TempDir()
	s.paths.Logs = outside
	if _, err := s.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home}); err == nil {
		t.Fatal("non-home log directory accepted")
	}
	emptyPaths := storage.NewPaths(filepath.Join(t.TempDir(), "absent-home"))
	empty := NewService(emptyPaths, nil, nil)
	data, err := empty.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": emptyPaths.Home})
	if err != nil || data.(ApplicationLogClearResult).ClearedFiles != 0 {
		t.Fatal("empty logs failed", err)
	}
	if _, err := os.Stat(emptyPaths.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty clear initialized home")
	}
}

func TestApplicationLogsClearPreflightFailureKeepsAllContent(t *testing.T) {
	s, paths, names := applicationLogsClearFixture(t)
	release, err := storage.AcquireLock(filepath.Join(paths.Logs, names[1]) + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home})
	release()
	if err == nil {
		t.Fatal("writer lock was ignored")
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(paths.Logs, name))
		if err != nil || string(data) != "old log content\n" {
			t.Fatal("preflight failure changed logs")
		}
	}
	if err := os.Mkdir(filepath.Join(paths.Logs, "application-2026-10-13.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home}); err == nil {
		t.Fatal("directory accepted as application log")
	}
}

func TestApplicationLogsClearReturnsActualPartialOutcome(t *testing.T) {
	_, paths, names := applicationLogsClearFixture(t)
	files := []*os.File{}
	for _, name := range names {
		file, err := os.OpenFile(filepath.Join(paths.Logs, name), os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
		defer file.Close()
	}
	_ = files[1].Close()
	result := truncatePreparedApplicationLogs(context.Background(), files)
	if !result.Changed || result.ClearedFiles != 1 || result.FailedFiles != 1 || result.Error == "" {
		t.Fatalf("partial result lost changes: %+v", result)
	}
	first, _ := os.ReadFile(filepath.Join(paths.Logs, names[0]))
	second, _ := os.ReadFile(filepath.Join(paths.Logs, names[1]))
	if len(first) != 0 || string(second) != "old log content\n" {
		t.Fatal("partial result disagrees with actual files")
	}
}
