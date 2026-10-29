package desktop

import (
	"strings"

	"silverwing-go/internal/domain"
)

// taskModuleDraftValues contains only editable module values and their last
// observed values. Repository requests, focus and caches belong to the editor.
type taskModuleDraftValues struct {
	ID, Name                       string
	LastName                       string
	Strategy                       domain.WorkspaceStrategy
	BaseRemote, BaseRef            string
	TargetBranch                   string
	TargetEdited                   bool
	LastBaseRemote, LastBaseRef    string
	LastTargetBranch               string
	TagEnabled                     bool
	TagMode                        domain.TagBuildMode
	TagTargetRef, TagMessagePrefix string
	Source                         domain.TaskModuleSource
}

// These facts describe input changes relative to Last values, before remote/ref
// synchronization. Derived changes do not become another user edit next frame.
type taskModuleDraftChanges struct {
	Name, BaseRemote, BaseRef, Target bool
}

func (changes taskModuleDraftChanges) any() bool {
	return changes.Name || changes.BaseRemote || changes.BaseRef || changes.Target
}

func newTaskModuleDraftValues(service domain.GroupServiceConfig, module domain.ServiceModuleConfig, feature string) taskModuleDraftValues {
	strategy := module.Strategy
	if strategy == "" {
		strategy = domain.WorkspaceStandardWorktree
	}
	tagMode := module.TagMode
	if tagMode == "" {
		tagMode = domain.TagBuildMergeToTargetBranch
	}
	messagePrefix := module.TagMessagePrefix
	if messagePrefix == "" {
		messagePrefix = "Tag"
	}
	draft := taskModuleDraftValues{
		ID: module.ID, Name: module.Name, Strategy: strategy,
		BaseRemote: service.EffectiveMasterRemote(module), BaseRef: service.EffectiveMasterBranch(module),
		TargetBranch: taskCreationTargetBranch(feature, service, module),
		TagEnabled:   module.TagEnabled, TagMode: tagMode, TagTargetRef: taskCreationOptionalString(service.EffectiveTagTargetRef(module)),
		TagMessagePrefix: messagePrefix, Source: domain.TaskModuleConfigured,
	}
	draft.LastName = draft.Name
	draft.LastBaseRemote, draft.LastBaseRef, draft.LastTargetBranch = draft.BaseRemote, draft.BaseRef, draft.TargetBranch
	return draft
}

func syncTaskModuleDraftValues(draft taskModuleDraftValues) (taskModuleDraftValues, taskModuleDraftChanges) {
	changes := taskModuleDraftChanges{
		Name:       draft.Name != draft.LastName,
		BaseRemote: draft.BaseRemote != draft.LastBaseRemote,
		BaseRef:    draft.BaseRef != draft.LastBaseRef,
		Target:     draft.TargetBranch != draft.LastTargetBranch,
	}
	if changes.BaseRemote {
		remote := strings.TrimSpace(draft.BaseRemote)
		if remote != "" {
			if ref, err := domain.ParseRemoteBranchRef(draft.BaseRef); err == nil {
				draft.BaseRef = remote + "/" + ref.Branch
			} else if _, after, ok := strings.Cut(strings.TrimSpace(draft.BaseRef), "/"); ok && after != "" {
				draft.BaseRef = remote + "/" + after
			}
		}
	} else if changes.BaseRef {
		if ref, err := domain.ParseRemoteBranchRef(draft.BaseRef); err == nil {
			draft.BaseRemote = ref.Remote
		}
	}
	if changes.Target {
		draft.TargetEdited = true
	}
	draft.LastName = draft.Name
	draft.LastBaseRemote, draft.LastBaseRef, draft.LastTargetBranch = draft.BaseRemote, draft.BaseRef, draft.TargetBranch
	return draft, changes
}

func retargetTaskModuleDraftValues(draft taskModuleDraftValues, feature string, moduleCount int) taskModuleDraftValues {
	if !draft.TargetEdited {
		draft.TargetBranch = taskCreationTargetBranchForCount(feature, draft.Name, draft.ID, moduleCount)
		draft.LastTargetBranch = draft.TargetBranch
	}
	return draft
}

func taskCreationModuleName(module domain.ServiceModuleConfig) string {
	if name := strings.TrimSpace(module.Name); name != "" {
		return name
	}
	if id := strings.TrimSpace(module.ID); id != "" {
		return id
	}
	return "default"
}

func taskCreationOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func taskCreationTargetBranch(feature string, service domain.GroupServiceConfig, module domain.ServiceModuleConfig) string {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return ""
	}
	return taskCreationTargetBranchForCount(feature, taskCreationModuleName(module), module.ID, len(service.Modules))
}

func taskCreationTargetBranchForCount(feature, moduleName, moduleID string, moduleCount int) string {
	feature = strings.TrimSpace(feature)
	if feature == "" {
		return ""
	}
	if moduleCount <= 1 {
		return feature
	}
	name := strings.TrimSpace(moduleName)
	if name == "" {
		name = strings.TrimSpace(moduleID)
	}
	if name == "" {
		name = "default"
	}
	return feature + "-" + name
}
