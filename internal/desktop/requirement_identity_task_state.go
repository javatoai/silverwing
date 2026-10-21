package desktop

import (
	"encoding/json"
	"strings"
)

// Account changes invalidate every task's session-only metadata alongside the
// body/list views. Advance generations even for requests that have already
// queued their completion and no longer have a visible loading entry.
func (s *app) invalidateTaskRequirementIdentityMetadata() {
	ids := make(map[string]struct{}, len(s.taskRequirementMetadata)+len(s.taskRequirementMetadataCancels))
	for id := range s.taskRequirementMetadata {
		ids[id] = struct{}{}
	}
	for id := range s.taskRequirementMetadataCancels {
		ids[id] = struct{}{}
	}
	for id := range s.taskRequirementMetadataGeneration {
		ids[id] = struct{}{}
	}
	for id := range ids {
		s.invalidateTaskRequirementMetadataIdentity(id)
	}
}

func (s *app) invalidateTaskRequirementMetadataIdentity(taskID string) {
	if cancel := s.taskRequirementMetadataCancels[taskID]; cancel != nil {
		cancel()
	}
	delete(s.taskRequirementMetadataCancels, taskID)
	delete(s.taskRequirementMetadata, taskID)
	if s.taskRequirementMetadataGeneration == nil {
		s.taskRequirementMetadataGeneration = make(map[string]uint64)
	}
	s.taskRequirementMetadataGeneration[taskID]++
}

// Called after applying a new task/config snapshot on the window thread.
// Source's task page clears its reader when taskPath, requirementLink or the
// configured work-item project identity changes. Its metadata controller also
// resets for the three requirement-configuration fields below.
func (s *app) reconcileTaskRequirementIdentities(previousTasks []task, previousConfigJSON string) {
	previousProjects, previousMetadata := taskRequirementConfigurationIdentity(previousConfigJSON)
	projects, metadata := taskRequirementConfigurationIdentity(s.configJSON)
	if previousMetadata != metadata {
		s.invalidateTaskRequirementIdentityMetadata()
	}
	if previousProjects != projects {
		s.closeRequirementReaders()
		s.requirementReaderGeneration++
	}
	currentTasks := make(map[string]task, len(s.tasks))
	for _, item := range s.tasks {
		currentTasks[item.id] = item
	}
	for _, previous := range previousTasks {
		current, exists := currentTasks[previous.id]
		if exists && current.path == previous.path && current.createdAt == previous.createdAt &&
			strings.TrimSpace(current.requirementURL) == strings.TrimSpace(previous.requirementURL) {
			continue
		}
		s.invalidateTaskRequirementMetadataIdentity(previous.id)
		s.closeTaskRequirementIdentityReaders(previous.id)
	}
}

func (s *app) closeTaskRequirementIdentityReaders(taskID string) {
	for key, reader := range s.requirementReaders {
		if reader.taskID != taskID {
			continue
		}
		if state := s.requirementContentJobs; state != nil {
			for _, request := range state.requests {
				if request.reader == reader {
					s.cancelRequirementContentJob(request)
				}
			}
			if state.visible == key {
				state.visible = ""
			}
		}
		closeMarkdownPreviewState(reader.bodyMarkdown)
		closeMarkdownPreviewState(reader.commentsMarkdown)
		closeRequirementCommentMarkdownStates(reader)
		reader.requestIDs = nil
		delete(s.requirementReaders, key)
	}
}

// Decode and re-encode only Source's requirement configuration fields, so JSON
// whitespace/key order and unrelated settings don't invalidate reader state.
func taskRequirementConfigurationIdentity(configJSON string) (projects, metadata string) {
	var config map[string]any
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return strings.TrimSpace(configJSON), strings.TrimSpace(configJSON)
	}
	projectBytes, _ := json.Marshal(config["meegleProjects"])
	metadataBytes, _ := json.Marshal([]any{
		config["meegleProjects"], config["requirementMaterialsRoot"], config["requirementMaterialsSubdirectory"],
	})
	return string(projectBytes), string(metadataBytes)
}
