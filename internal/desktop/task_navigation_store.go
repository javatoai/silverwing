package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/storage"
)

const (
	taskNavigationVersion  = 1
	taskNavigationMaxBytes = 1 << 20
	taskNavigationRecent   = 20
)

type taskNavigationPreferences struct {
	Pinned []string `json:"pinned"`
	Recent []string `json:"recent"`
}

type taskNavigationWire struct {
	Version int `json:"version"`
	taskNavigationPreferences
}

// taskNavigationStore keeps user navigation choices separate from display
// preferences and the expiring reading cache. It never caches a disk snapshot.
type taskNavigationStore struct {
	path string
}

func newTaskNavigationStore(path string) *taskNavigationStore {
	return &taskNavigationStore{path: path}
}

func emptyTaskNavigationPreferences() taskNavigationPreferences {
	return taskNavigationPreferences{Pinned: []string{}, Recent: []string{}}
}

// Load needs no lock: writers publish a complete file with atomicWriteFile.
func (s *taskNavigationStore) Load() (taskNavigationPreferences, error) {
	empty := emptyTaskNavigationPreferences()
	if s == nil || strings.TrimSpace(s.path) == "" {
		return empty, errors.New("task navigation path is empty")
	}
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("stat task navigation: %w", err)
	}
	if !info.Mode().IsRegular() {
		return empty, errors.New("task navigation is not a regular file")
	}
	if info.Size() > taskNavigationMaxBytes {
		return empty, errors.New("task navigation exceeds 1 MiB")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return empty, fmt.Errorf("open task navigation: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, taskNavigationMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return empty, fmt.Errorf("read task navigation: %w", readErr)
	}
	if closeErr != nil {
		return empty, fmt.Errorf("close task navigation: %w", closeErr)
	}
	if len(content) > taskNavigationMaxBytes {
		return empty, errors.New("task navigation exceeds 1 MiB")
	}
	if !utf8.Valid(content) {
		return empty, errors.New("task navigation is not valid UTF-8")
	}
	var wire taskNavigationWire
	if err := json.Unmarshal(content, &wire); err != nil {
		return empty, fmt.Errorf("decode task navigation: %w", err)
	}
	if wire.Version != taskNavigationVersion {
		return empty, fmt.Errorf("unsupported task navigation version: %d", wire.Version)
	}
	pinned, err := normalizeTaskNavigationKeys(wire.Pinned, 0)
	if err != nil {
		return empty, fmt.Errorf("invalid pinned tasks: %w", err)
	}
	recent, err := normalizeTaskNavigationKeys(wire.Recent, taskNavigationRecent)
	if err != nil {
		return empty, fmt.Errorf("invalid recent tasks: %w", err)
	}
	return taskNavigationPreferences{Pinned: pinned, Recent: recent}, nil
}

func (s *taskNavigationStore) SetPinned(key string, pinned bool) (taskNavigationPreferences, error) {
	return s.update(key, func(value *taskNavigationPreferences) {
		value.Pinned = taskNavigationMoveFirst(value.Pinned, key, pinned)
	})
}

func (s *taskNavigationStore) Visit(key string) (taskNavigationPreferences, error) {
	return s.update(key, func(value *taskNavigationPreferences) {
		value.Recent = taskNavigationMoveFirst(value.Recent, key, true)
		if len(value.Recent) > taskNavigationRecent {
			value.Recent = value.Recent[:taskNavigationRecent]
		}
	})
}

// The OS lock covers the entire read-modify-write, including between separate
// stores or processes. An unreadable or newer file is never replaced.
func (s *taskNavigationStore) update(key string, change func(*taskNavigationPreferences)) (taskNavigationPreferences, error) {
	empty := emptyTaskNavigationPreferences()
	if s == nil || strings.TrimSpace(s.path) == "" {
		return empty, errors.New("task navigation path is empty")
	}
	if err := validateTaskNavigationKey(key); err != nil {
		return empty, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return empty, fmt.Errorf("create task navigation directory: %w", err)
	}
	release, err := storage.AcquireLock(s.path + ".lock")
	if err != nil {
		return empty, fmt.Errorf("lock task navigation: %w", err)
	}
	defer release()
	value, err := s.Load()
	if err != nil {
		return empty, err
	}
	change(&value)
	content, err := json.MarshalIndent(taskNavigationWire{Version: taskNavigationVersion, taskNavigationPreferences: value}, "", "  ")
	if err != nil {
		return empty, fmt.Errorf("encode task navigation: %w", err)
	}
	content = append(content, '\n')
	if len(content) > taskNavigationMaxBytes {
		return empty, errors.New("task navigation exceeds 1 MiB")
	}
	if err := atomicWriteFile(s.path, content, 0o600); err != nil {
		return empty, fmt.Errorf("save task navigation: %w", err)
	}
	return value, nil
}

func validateTaskNavigationKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("task navigation key is empty")
	}
	if !utf8.ValidString(key) {
		return errors.New("task navigation key is not valid UTF-8")
	}
	return nil
}

func normalizeTaskNavigationKeys(keys []string, limit int) ([]string, error) {
	result := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if err := validateTaskNavigationKey(key); err != nil {
			return nil, err
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if limit == 0 || len(result) < limit {
			result = append(result, key)
		}
	}
	return result, nil
}

func taskNavigationMoveFirst(keys []string, key string, include bool) []string {
	result := make([]string, 0, len(keys)+1)
	if include {
		result = append(result, key)
	}
	for _, existing := range keys {
		if existing != key {
			result = append(result, existing)
		}
	}
	return result
}
