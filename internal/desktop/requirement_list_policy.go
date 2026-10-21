package desktop

import (
	"encoding/json"
	"strings"
)

type requirementListIdentity struct {
	home, executable, projects, defaultProject, account string
}

func (s *app) currentRequirementListIdentity() requirementListIdentity {
	return requirementListIdentity{
		home: s.snapshot.Home, executable: strings.TrimSpace(s.configString("meegleExecutablePath")),
		projects: s.requirementProjectsIdentity(), defaultProject: strings.TrimSpace(s.configString("meegleDefaultSprintProjectKey")),
		account: s.currentRequirementAccountIdentity(),
	}
}

func (s *app) requirementProjectsIdentity() string {
	identity, _ := s.configReads.requirementProjects.read(s.configJSON, func(string) (string, error) {
		return s.encodeRequirementProjectsIdentity(), nil
	})
	return identity
}

func (s *app) encodeRequirementProjectsIdentity() string {
	type projectIdentity struct {
		Key        string `json:"projectKey"`
		SimpleName string `json:"simpleName"`
	}
	projects := make([]projectIdentity, 0)
	for _, raw := range s.readConfigArray("meegleProjects") {
		project, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		projects = append(projects, projectIdentity{
			Key:        strings.TrimSpace(mapString(project, "project_key")),
			SimpleName: strings.TrimSpace(mapString(project, "simple_name")),
		})
	}
	projectBytes, _ := json.Marshal(projects)
	return string(projectBytes)
}

// Account fields can be replaced or changed in place independently of config.
// Keep this projection live so async jobs cannot accept a previous account.
func (s *app) currentRequirementAccountIdentity() string {
	status := s.settingsCLIStatus["meegle"]
	accountFields := map[string]any{}
	for _, key := range []string{
		"authenticated", "installed", "authenticationState", "host", "userKey", "user_key",
		"accountKey", "account_key", "accountId", "account_id", "userId", "user_id", "email",
	} {
		if value, ok := status[key]; ok {
			accountFields[key] = value
		}
	}
	accountBytes, _ := json.Marshal(accountFields)
	account := string(accountBytes)
	if state := s.requirementIdentityJobs; state != nil && state.confirmed {
		account += "\x00" + state.state + "\x00" + state.identity
	}
	return account
}

func (s *app) observeRequirementListIdentity() {
	state := s.ensureRequirementListJobs()
	identity := s.currentRequirementListIdentity()
	if !state.hasIdentity {
		state.identity, state.hasIdentity = identity, true
		return
	}
	if state.identity == identity {
		return
	}
	s.resetRequirementListForIdentity(state, identity)
}

// Called by account/session invalidation paths even when their visible status
// fields have not changed yet. The next list request must get a new generation.
func (s *app) invalidateRequirementListIdentity() {
	state := s.ensureRequirementListJobs()
	s.retireRequirementListRequest(state)
	state.generation++
	state.hasIdentity = false
	s.clearRequirementListView()
}

func (s *app) resetRequirementListForIdentity(state *requirementListJobState, identity requirementListIdentity) {
	s.retireRequirementListRequest(state)
	state.generation++
	state.identity, state.hasIdentity = identity, true
	s.clearRequirementListView()
}

func (s *app) clearRequirementListView() {
	if backend, ok := s.backend.(interface{ InvalidateRequirementDocuments() }); ok {
		backend.InvalidateRequirementDocuments()
	}
	s.cancelRequirementNamingPreparation()
	if backend, ok := s.backend.(interface{ CancelRequirementNaming() }); ok {
		backend.CancelRequirementNaming()
	}
	s.cancelRequirementDocumentPreparation()
	s.requirements = nil
	s.requirementsFetchedAt = 0
	s.clearRequirementListSelection()
	s.requirementsRequested, s.requirementsLoading, s.requirementsLoaded = false, false, false
	s.requirementsLoadError = ""
	s.requirementsFailures = nil
	s.requirementsCompletedQueries = 0
	s.requirementsSprints = nil
	s.requirementsSelectedSprintKey = ""
	s.requirementsFocusedDetail = false
	s.lastResult = ""
}
