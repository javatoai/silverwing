package desktop

import (
	"context"
	"path/filepath"
	"testing"

	"silverwing-go/internal/application"
)

func TestCodexSnapshotTracksSkillLoadAndDropsStaleSkillPaths(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "isolated-codex-skills"})
	s.snapshot.Home = t.TempDir()
	cwd := t.TempDir()
	s.taskUnit.state.tasks = []task{{id: "task", createdAt: "created", path: cwd}}
	s.taskUnit.state.selected = 0
	identity := codexAIIdentity{home: s.snapshot.Home, taskID: "task", taskCreatedAt: "created", taskPath: cwd, cwd: cwd}
	state := &codexAIState{Open: true, identity: identity, identityRevision: 1,
		requests: make(map[string]*codexAIRequest), generations: make(map[string]uint64)}
	s.toolsUnit.state.codexAI = state
	skill := filepath.Join(cwd, "SKILL.md")
	apply := func(includeSkills bool) {
		t.Helper()
		state.generations["snapshot"]++
		request := &codexAIRequest{key: "snapshot", ctx: context.Background(), identity: identity, identityRevision: 1,
			generation: state.generations["snapshot"], action: application.Action{Kind: "codex.ai/snapshot", Payload: map[string]any{"includeSkills": includeSkills}}}
		state.requests["snapshot"] = request
		snapshot := codexAISnapshot{CWD: &cwd}
		if includeSkills {
			snapshot.Skills = []codexAISkill{{Name: "fixture", Path: skill}}
		}
		s.finishCodexAIRequest(request, application.ActionResult{Data: snapshot}, nil)
		if state.snapshotError != "" {
			t.Fatal(state.snapshotError)
		}
	}
	apply(false)
	if state.skillsLoaded || !state.snapshotLoaded || codexAIFileAllowed(state.snapshot, skill) {
		t.Fatal("ordinary configuration snapshot eagerly authorized an unloaded Skill")
	}
	apply(true)
	if !state.skillsLoaded || !codexAIFileAllowed(state.snapshot, skill) {
		t.Fatal("explicit Skills load did not expose its preview")
	}
	apply(false)
	if state.skillsLoaded || codexAIFileAllowed(state.snapshot, skill) {
		t.Fatal("later configuration refresh retained an old Skill preview path")
	}
	apply(true)
	other := t.TempDir()
	s.taskUnit.state.tasks = []task{{id: "other", createdAt: "other-created", path: other}}
	s.openCodexAI(other, codexAITabSkills)
	if state.skillsLoaded || state.snapshot != nil || state.identity.cwd != other {
		t.Fatal("Skills load state survived a task identity change")
	}
}
