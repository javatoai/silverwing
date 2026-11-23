package storage

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrUnsafePathSegment = errors.New("group ID cannot be used in a path")
	groupIDPattern       = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

func safeGroupID(value string) bool {
	return value != "" && value != "." && value != ".." && groupIDPattern.MatchString(value)
}

// Paths groups the durable and runtime locations under one application home.
// The caller supplies an isolated home; this package never guesses a user directory.
type Paths struct {
	Home         string
	Config       string
	Logs         string
	Backups      string
	Diagnostics  string
	Locks        string
	Temp         string
	Cache        string
	Codex        string
	Agents       string
	Tasks        string
	GlobalAgents string
	ProfileID    string
}

// NewPaths treats home as the application data directory itself.
func NewPaths(home string) Paths {
	home = filepath.Clean(home)
	return Paths{
		Home:         home,
		Config:       filepath.Join(home, "config"),
		Logs:         filepath.Join(home, "logs"),
		Backups:      filepath.Join(home, "backups"),
		Diagnostics:  filepath.Join(home, "diagnostics"),
		Locks:        filepath.Join(home, "locks"),
		Temp:         filepath.Join(home, "temp"),
		Cache:        filepath.Join(home, "cache"),
		Codex:        filepath.Join(home, "codex"),
		Agents:       filepath.Join(home, "agents"),
		Tasks:        filepath.Join(home, "tasks"),
		GlobalAgents: filepath.Join(home, "agents", "global", "AGENTS.md"),
	}
}

// TaskWorktreeNamespace returns the profile's temporary Git worktree directory.
func (p Paths) TaskWorktreeNamespace(taskRoot *string) string {
	if taskRoot == nil || !filepath.IsAbs(*taskRoot) || !safeGroupID(p.ProfileID) {
		return ""
	}
	home, homeErr := canonicalPathWithMissingTail(p.Home, filepath.EvalSymlinks)
	root, rootErr := canonicalPathWithMissingTail(*taskRoot, filepath.EvalSymlinks)
	if homeErr != nil || rootErr != nil || isUnderDirectory(home, root) {
		return ""
	}
	if info, err := os.Stat(root); err == nil && !info.IsDir() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	requested := filepath.Join(root, "silverwing-"+p.ProfileID)
	namespace, err := canonicalPathWithMissingTail(requested, filepath.EvalSymlinks)
	if err != nil || !sameDirectoryPath(requested, namespace) || isUnderDirectory(home, namespace) || isUnderDirectory(namespace, home) {
		return ""
	}
	if info, err := os.Lstat(namespace); err == nil && !info.IsDir() || err != nil && !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if _, err := os.Lstat(filepath.Join(namespace, TaskManifestFileName)); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	return namespace
}

func sameDirectoryPath(first, second string) bool {
	relative, err := filepath.Rel(first, second)
	return err == nil && relative == "."
}

func canonicalPathWithMissingTail(path string, resolve func(string) (string, error)) (string, error) {
	path = filepath.Clean(path)
	for parent := path; ; parent = filepath.Dir(parent) {
		resolved, err := resolve(parent)
		if err == nil {
			tail, err := filepath.Rel(parent, path)
			return filepath.Join(resolved, tail), err
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return "", err
		}
	}
}

func isUnderDirectory(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

const profilePathPrefix = "@profile/"

// StoreProfilePath leaves deliberately external paths untouched.
func StoreProfilePath(home, path string) string {
	if home == "" || !filepath.IsAbs(path) || !isUnderDirectory(home, path) {
		return path
	}
	relative, err := filepath.Rel(home, path)
	if err != nil {
		return path
	}
	return profilePathPrefix + filepath.ToSlash(relative)
}

func ResolveProfilePath(home, path string) string {
	if home == "" || !strings.HasPrefix(path, profilePathPrefix) {
		return path
	}
	relative := filepath.FromSlash(strings.TrimPrefix(path, profilePathPrefix))
	if relative == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return ""
	}
	resolved := filepath.Join(home, relative)
	if !isUnderDirectory(home, resolved) {
		return ""
	}
	return resolved
}

// GroupAgents returns the AGENTS.md path for a safe group identifier.
func (p Paths) GroupAgents(groupID string) (string, error) {
	if !safeGroupID(groupID) {
		return "", ErrUnsafePathSegment
	}
	return filepath.Join(p.Agents, "groups", groupID, "AGENTS.md"), nil
}
