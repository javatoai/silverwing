package desktop

import (
	"context"
	"errors"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/workspace"
)

type documentsGitDraftPlan struct {
	engine *editor.Autosave
	keys   []string
	scope  workspace.MaterialsGitScope
}

func (s *app) documentsGitDraftPlan(action application.Action) *documentsGitDraftPlan {
	if action.Kind != "config.documents-git.bind" && action.Kind != "config.documents-git.unbind" {
		return nil
	}
	scope, ok := action.Payload["scope"].(workspace.MaterialsGitScope)
	if !ok || scope.Root == "" {
		return nil
	}
	owner := &s.materialsUnit
	if payloadString(action.Payload, "role") == projectInstructionsGit.role() {
		view := s.projectInstructions
		if !s.projectInstructionsCurrent(view) || view.browser == nil {
			return nil
		}
		owner = &view.browser.materialsUnit
	}
	if owner.state.markdownAutosave == nil {
		return nil
	}
	plan := &documentsGitDraftPlan{engine: owner.state.markdownAutosave, scope: scope}
	owner.state.documentSaveMu.Lock()
	defer owner.state.documentSaveMu.Unlock()
	for key, ref := range owner.state.documentSaveRefs {
		if _, within := pathRelativeToRoot(scope.Root, ref.path); within && (ref.kind == "material-text" || ref.kind == "material-markdown") {
			plan.keys = append(plan.keys, key)
		}
	}
	if len(plan.keys) == 0 {
		return nil
	}
	return plan
}

func (p *documentsGitDraftPlan) prepare(ctx context.Context) (*editor.AutosaveLease, error) {
	if p == nil {
		return nil, nil
	}
	lease, err := p.engine.Quiesce(ctx, p.keys)
	if err != nil {
		return nil, err
	}
	for _, key := range p.keys {
		state := p.engine.Status(key)
		if state.Dirty && state.DraftSavedRevision < state.Revision {
			lease.Release()
			return nil, errors.New("原仓库中有尚未缓存的草稿")
		}
	}
	return lease, nil
}

func (p *documentsGitDraftPlan) finish(lease *editor.AutosaveLease, kind string, result application.ActionResult, err error) error {
	if lease == nil {
		return nil
	}
	settings, valid := result.Data.(domain.MaterialsGitSyncSettings)
	changed := kind == "config.documents-git.unbind" || valid && settings.BindingID != p.scope.BindingID
	if err != nil || !changed {
		lease.Release()
		return nil
	}
	if err := lease.DetachCachedDrafts(); err != nil {
		lease.FailClosed("原仓库的编辑已暂停，请重新打开文件恢复草稿")
		return err
	}
	return nil
}
