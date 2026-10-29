package desktop

import (
	"encoding/json"
	"errors"
	"fmt"

	"silverwing-go/internal/application"
)

type meegleCatalogProject struct {
	Name       string `json:"name"`
	ProjectKey string `json:"projectKey"`
	SimpleName string `json:"simpleName"`
}

// The source project directory is session state. Only the selected project
// configuration is persisted; failures remain distinct from an empty directory.
type meegleProjectCatalogState struct {
	projects         []meegleCatalogProject
	loading          bool
	loaded           bool
	error            string
	generation       uint64
	identity         string
	context          requirementIdentityContext
	verifiedIdentity string

	pickerOpen           bool
	pickerQuery          string
	pickerSelection      string
	pickerSaveError      string
	pickerSaveGeneration uint64
	pickerPendingSave    uint64
	pickerCommittedKey   string
	pickerHighlight      int
	pickerShown          string
	pickerFocusPending   bool
}

func (s *app) requestMeegleProjectCatalog(force bool) {
	state := s.ensureMeegleProjectCatalogState()
	s.syncMeegleProjectCatalogContext(state)
	identity, authenticated := s.currentMeegleProjectCatalogIdentity()
	if !authenticated {
		s.resetMeegleProjectCatalog(state)
		return
	}
	if state.identity != identity {
		s.resetMeegleProjectCatalog(state)
		state.identity = identity
	}
	if state.loading || (!force && state.loaded) {
		return
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		state.loading = false
		state.error = "Meegle 项目目录读取服务不可用。"
		return
	}
	state.generation++
	state.loading, state.error = true, ""
	s.act("meegle.projects", "", "", "", "", "", map[string]any{"catalogGeneration": fmt.Sprint(state.generation)})
}

func (s *app) meegleCatalogActionRelevant(action application.Action) bool {
	if action.Kind != "meegle.projects" || s.settingsUnit.state.meegleProjectCatalog == nil || payloadString(action.Payload, "catalogGeneration") != fmt.Sprint(s.settingsUnit.state.meegleProjectCatalog.generation) {
		return false
	}
	identity, authenticated := s.currentMeegleProjectCatalogIdentity()
	return authenticated && s.settingsUnit.state.meegleProjectCatalog.identity == identity
}

func (s *app) applyMeegleProjectCatalogResult(action application.Action, result application.ActionResult) {
	s.finishMeegleProjectCatalog(action, result, nil)
}

func (s *app) applyMeegleProjectCatalogFailure(action application.Action, err error) bool {
	return s.finishMeegleProjectCatalog(action, application.ActionResult{}, err)
}

func (s *app) finishMeegleProjectCatalog(action application.Action, result application.ActionResult, failure error) bool {
	if action.Kind != "meegle.projects" {
		return false
	}
	if !s.meegleCatalogActionRelevant(action) {
		return true
	}
	state := s.settingsUnit.state.meegleProjectCatalog
	state.loading = false
	if failure != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(failure, &changed) {
			s.resetMeegleProjectCatalog(state)
		}
		state.error = failure.Error()
		return true
	}
	encoded, err := json.Marshal(result.Data)
	var directory struct {
		Projects []meegleCatalogProject `json:"projects"`
	}
	if err == nil {
		err = json.Unmarshal(encoded, &directory)
	}
	if err != nil {
		state.error = "项目目录格式无效：" + err.Error()
		return true
	}
	state.projects, state.loaded, state.error = directory.Projects, true, ""
	return true
}

func (s *app) clearMeegleSessionViews() {
	s.invalidateRequirementIdentityContext()
	s.clearMeegleIdentityBoundViews()
}

func (s *app) clearMeegleIdentityBoundViews() {

	s.invalidateRequirementRefreshJobs()
	s.invalidateTaskRequirementIdentityMetadata()
	s.invalidateRequirementListIdentity()
	s.clearMeegleProjectCatalogCache()
	s.requirementUnit.closeRequirementReaders()
	s.requirementUnit.state.requirementReaderGeneration++
	s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoaded = false, false
}
