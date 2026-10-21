package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf16"
)

const (
	recentBranchPreferencesFile = "recent-branches.json"
	recentBranchSeparator       = "\x1f"
	recentBranchMaxEntries      = 5
	recentBranchMaxValueLength  = 8192
)

var recentBranchStores sync.Map // map[home identity]*recentBranchStore

// Source stores recent branches in the OS preferences registry/keychain node.
// This port keeps the same key/value format in an app-home JSON file and never
// reads or writes OS preferences, so isolated homes remain isolated.

type recentBranchStore struct {
	mu      sync.Mutex
	path    string
	loaded  bool
	loadErr error
	values  map[string]string
}

func (s *app) recentTaskBranches(repositoryID, remote string) []string {
	return s.recentBranchStore().list(repositoryID, remote)
}

func (s *app) recordRecentTaskBranch(repositoryID, remote, branch string) {
	// Preferences are auxiliary. A read-only or unavailable preferences home
	// must never block selecting a branch in the task form.
	_ = s.recentBranchStore().record(repositoryID, remote, branch)
}

func (s *app) recentBranchStore() *recentBranchStore {
	home := ""
	if s.desktopPreferences != nil {
		home = strings.TrimSpace(s.desktopPreferences.home)
	}
	identity := "memory:"
	path := ""
	if home != "" {
		absolute, err := filepath.Abs(home)
		if err == nil {
			home = filepath.Clean(absolute)
		}
		identity = "home:" + home
		path = filepath.Join(home, recentBranchPreferencesFile)
	} else {
		identity = fmt.Sprintf("memory:%p", s)
	}
	store, _ := recentBranchStores.LoadOrStore(identity, &recentBranchStore{path: path, values: make(map[string]string)})
	return store.(*recentBranchStore)
}

func (s *recentBranchStore) list(repositoryID, remote string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return []string{}
	}
	value := s.values[recentBranchKey(repositoryID, remote)]
	return recentBranchEntries(value)
}

func (s *recentBranchStore) record(repositoryID, remote, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	key := recentBranchKey(repositoryID, remote)
	existing := s.values[key]
	entries := make([]string, 0, recentBranchMaxEntries)
	seen := make(map[string]bool, recentBranchMaxEntries)
	remaining, accepted := recentBranchMaxValueLength, 0
	for _, entry := range append([]string{branch}, recentBranchEntries(existing)...) {
		if seen[entry] {
			continue
		}
		length := recentBranchUTF16Length(entry)
		if accepted > 0 {
			length++ // The Source store joins values with one UTF-16 separator.
		}
		if accepted >= recentBranchMaxEntries || length > remaining {
			continue
		}
		remaining -= length
		accepted++
		seen[entry] = true
		entries = append(entries, entry)
	}
	joined := strings.Join(entries, recentBranchSeparator)
	if recentBranchUTF16Length(joined) > recentBranchMaxValueLength {
		return errors.New("recent branch preference exceeds its value limit")
	}
	nextValues := make(map[string]string, len(s.values)+1)
	for existingKey, existingValue := range s.values {
		nextValues[existingKey] = existingValue
	}
	nextValues[key] = joined
	if s.path == "" {
		s.values = nextValues
		return nil
	}
	encoded, err := json.MarshalIndent(nextValues, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(s.path, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	s.values = nextValues
	return nil
}

func (s *recentBranchStore) loadLocked() error {
	if s.loaded {
		return s.loadErr
	}
	s.values = make(map[string]string)
	if s.path == "" {
		s.loaded = true
		return nil
	}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.loaded = true
		return nil
	}
	if err != nil {
		s.loaded, s.loadErr = true, err
		return err
	}
	if err := json.Unmarshal(content, &s.values); err != nil || s.values == nil {
		if err == nil {
			err = errors.New("expected an object of recent branch preferences")
		}
		s.loaded, s.loadErr = true, err
		return err
	}
	s.loaded = true
	return nil
}

func recentBranchKey(repositoryID, remote string) string {
	// Kotlin String.length counts UTF-16 code units; preserve that contract.
	identity := fmt.Sprintf("%d:%s%s", recentBranchUTF16Length(repositoryID), repositoryID, remote)
	digest := sha256.Sum256([]byte(identity))
	return "v2-" + hex.EncodeToString(digest[:])
}

func recentBranchEntries(value string) []string {
	entries := make([]string, 0, recentBranchMaxEntries)
	for _, entry := range strings.Split(value, recentBranchSeparator) {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		entries = append(entries, entry)
		if len(entries) == recentBranchMaxEntries {
			break
		}
	}
	return entries
}

func mergeRecentTaskBranches(recent, available []string) []string {
	availableSet := make(map[string]bool, len(available))
	for _, branch := range available {
		availableSet[branch] = true
	}
	merged := make([]string, 0, len(available))
	seen := make(map[string]bool, len(available))
	for _, branch := range recent {
		if availableSet[branch] && !seen[branch] {
			merged = append(merged, branch)
			seen[branch] = true
		}
	}
	for _, branch := range available {
		if !seen[branch] {
			merged = append(merged, branch)
			seen[branch] = true
		}
	}
	return merged
}

func recentBranchUTF16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}
