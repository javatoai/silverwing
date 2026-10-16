package desktop

import (
	"encoding/json"
	"errors"
	"strings"

	"silverwing-go/internal/workspace"
)

type markdownTaskRootReadingRelocation struct {
	session                                           *desktopReadingSession
	cache                                             ReadingSnapshot
	startup                                           readingUIIdentity
	scrolls                                           map[string]readingScrollBinding
	restored                                          map[string]bool
	pendingTask, pendingRoot, pendingKey, pendingPath string
	pendingTabs                                       []string
}

func rebaseMarkdownTaskRootReadingKey(key string, mappings []workspace.TaskRootMigrationPathMapping) (string, error) {
	if key == "" {
		return key, nil
	}
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return "", errors.New("阅读状态任务身份格式错误")
	}
	taskPath, _, err := workspace.RebaseTaskRootMigrationPath(parts[0], mappings)
	if err != nil {
		return "", err
	}
	root, _, err := workspace.RebaseTaskRootMigrationPath(parts[1], mappings)
	if err != nil {
		return "", err
	}
	return readingMaterialsKey(taskPath, root), nil
}

func (s *app) buildMarkdownTaskRootReadingRelocation(plan *markdownTaskRootMigrationPlan) (*markdownTaskRootReadingRelocation, error) {
	session := s.readingSession
	if session == nil || session.closed {
		return nil, nil
	}
	if !session.loaded {
		return nil, errors.New("阅读状态尚未加载")
	}
	state := &markdownTaskRootReadingRelocation{session: session, cache: session.cache, startup: session.startupIdentity, scrolls: map[string]readingScrollBinding{}, restored: map[string]bool{}, pendingTabs: append([]string(nil), session.pendingMaterialsUITabs...)}
	state.cache.Materials = append([]MaterialsSnapshot(nil), session.cache.Materials...)
	state.cache.Requirements = append([]RequirementReadingSnapshot(nil), session.cache.Requirements...)
	mappings := plan.preview.PathMappings
	rebase := func(path string) (string, error) {
		value, _, err := workspace.RebaseTaskRootMigrationPath(path, mappings)
		return value, err
	}
	var err error
	state.cache.LastTaskPath, err = rebase(state.cache.LastTaskPath)
	if err != nil {
		return nil, err
	}
	materialKeys := map[string]bool{}
	for index := range state.cache.Materials {
		record := &state.cache.Materials[index]
		record.TaskPath, err = rebase(record.TaskPath)
		if err != nil {
			return nil, err
		}
		if record.Root != nil {
			root, err := rebase(*record.Root)
			if err != nil {
				return nil, err
			}
			record.Root = &root
		}
		key := readingMaterialsKey(record.TaskPath, readingRootValue(record.Root))
		if materialKeys[key] {
			return nil, errors.New("迁移目标已有不同的资料阅读身份")
		}
		materialKeys[key] = true
		// All relative files, tabs, selection, expansion and scroll positions
		// remain unchanged when their complete task/materials root moves.
	}
	requirementKeys := map[string]bool{}
	for index := range state.cache.Requirements {
		record := &state.cache.Requirements[index]
		record.TaskPath, err = rebase(record.TaskPath)
		if err != nil {
			return nil, err
		}
		key := normalizedReadingPath(record.TaskPath) + "\x00" + record.Link
		if requirementKeys[key] {
			return nil, errors.New("迁移目标已有不同的需求阅读身份")
		}
		requirementKeys[key] = true
	}
	for _, binding := range session.scrollStates {
		binding.taskPath, err = rebase(binding.taskPath)
		if err != nil {
			return nil, err
		}
		binding.taskPath = normalizedReadingPath(binding.taskPath)
		binding.root, err = rebase(binding.root)
		if err != nil {
			return nil, err
		}
		binding.root = normalizedReadingRoot(binding.root)
		key := readingScrollBindingKey(binding)
		if previous, exists := state.scrolls[key]; exists && previous.state != binding.state {
			return nil, errors.New("迁移目标已有不同的阅读滚动状态")
		}
		state.scrolls[key] = binding
	}
	for key, value := range session.restoredMaterials {
		key, err := rebaseMarkdownTaskRootReadingKey(key, mappings)
		if err != nil {
			return nil, err
		}
		state.restored[key] = value
	}
	state.pendingTask, err = rebase(session.pendingTaskPath)
	if err != nil {
		return nil, err
	}
	if state.pendingTask != "" {
		state.pendingTask = normalizedReadingPath(state.pendingTask)
	}
	state.pendingRoot, err = rebase(session.pendingRoot)
	if err != nil {
		return nil, err
	}
	state.pendingRoot = normalizedReadingRoot(state.pendingRoot)
	state.pendingKey, err = rebaseMarkdownTaskRootReadingKey(session.pendingMaterialsUIKey, mappings)
	if err != nil {
		return nil, err
	}
	state.pendingPath, err = rebase(session.pendingMaterialsUIPath)
	if err != nil {
		return nil, err
	}
	for index, path := range state.pendingTabs {
		state.pendingTabs[index], err = rebase(path)
		if err != nil {
			return nil, err
		}
	}
	state.startup.taskPath, err = rebase(state.startup.taskPath)
	if err != nil {
		return nil, err
	}
	if state.startup.taskPath != "" {
		state.startup.taskPath = normalizedReadingPath(state.startup.taskPath)
	}
	state.startup.document, err = rebase(state.startup.document)
	if err != nil {
		return nil, err
	}
	if state.startup.document != "" {
		state.startup.document = normalizedReadingPath(state.startup.document)
	}
	if state.startup.documentTab != "" {
		tabs := strings.Split(state.startup.documentTab, "\x00")
		for index, path := range tabs {
			tabs[index], err = rebase(path)
			if err != nil {
				return nil, err
			}
		}
		state.startup.documentTab = strings.Join(tabs, "\x00")
	}
	return state, nil
}

func (s *app) applyMarkdownTaskRootReadingRelocation(state *markdownTaskRootReadingRelocation) error {
	if state == nil {
		return nil
	}
	session := state.session
	if s.readingSession != session || session.closed {
		return errors.New("任务迁移期间阅读状态身份已变化")
	}
	session.store.CancelPending()
	session.cache, session.startupIdentity = state.cache, state.startup
	session.scrollStates, session.restoredMaterials = state.scrolls, state.restored
	session.pendingTaskPath, session.pendingRoot = state.pendingTask, state.pendingRoot
	session.pendingMaterialsUIKey, session.pendingMaterialsUIPath, session.pendingMaterialsUITabs = state.pendingKey, state.pendingPath, state.pendingTabs
	encoded, err := json.Marshal(session.cache)
	if err != nil {
		return err
	}
	session.lastObserved = encoded
	return session.store.ScheduleSave(session.cache)
}
