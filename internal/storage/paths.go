package storage

import "path/filepath"

import (
	"errors"
	"regexp"
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
	Home               string
	Config             string
	Logs               string
	Backups            string
	Diagnostics        string
	Locks              string
	Temp               string
	Cache              string
	Codex              string
	Agents             string
	Tasks              string
	GlobalAgents       string
	AgentTaskTemplates string
}

// NewPaths treats home as the application data directory itself.
func NewPaths(home string) Paths {
	home = filepath.Clean(home)
	return Paths{
		Home:               home,
		Config:             filepath.Join(home, "config"),
		Logs:               filepath.Join(home, "logs"),
		Backups:            filepath.Join(home, "backups"),
		Diagnostics:        filepath.Join(home, "diagnostics"),
		Locks:              filepath.Join(home, "locks"),
		Temp:               filepath.Join(home, "temp"),
		Cache:              filepath.Join(home, "cache"),
		Codex:              filepath.Join(home, "codex"),
		Agents:             filepath.Join(home, "agents"),
		Tasks:              filepath.Join(home, "tasks"),
		GlobalAgents:       filepath.Join(home, "agents", "global", "AGENTS.md"),
		AgentTaskTemplates: filepath.Join(home, "agents", "task-templates.json"),
	}
}

// GroupAgents returns the AGENTS.md path for a safe group identifier.
func (p Paths) GroupAgents(groupID string) (string, error) {
	if !safeGroupID(groupID) {
		return "", ErrUnsafePathSegment
	}
	return filepath.Join(p.Agents, "groups", groupID, "AGENTS.md"), nil
}
