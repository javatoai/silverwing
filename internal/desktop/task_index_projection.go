package desktop

import "strings"

// Publication is the authority for content changes. This constant-time stamp
// also tolerates legacy callers replacing or splicing the task slice without
// publishing; it deliberately does not detect arbitrary in-place field edits.
type taskIndexStructure struct {
	length int
	first  *task
}

func taskIndexStructureOf(tasks []task) taskIndexStructure {
	structure := taskIndexStructure{length: len(tasks)}
	if len(tasks) > 0 {
		structure.first = &tasks[0]
	}
	return structure
}

type taskIndexCatalog struct {
	structure  taskIndexStructure
	keys       []string
	byID       map[string]int
	categories [2][]int
}

func newTaskIndexCatalog(tasks []task) *taskIndexCatalog {
	index := &taskIndexCatalog{structure: taskIndexStructureOf(tasks), keys: make([]string, len(tasks)), byID: make(map[string]int, len(tasks))}
	for i, item := range tasks {
		index.keys[i] = taskNavigationKey(item)
		if _, exists := index.byID[item.id]; !exists {
			index.byID[item.id] = i
		}
		category := 0
		if item.archived {
			category = 1
		}
		index.categories[category] = append(index.categories[category], i)
	}
	return index
}

func (s *taskController) taskCatalog() *taskIndexCatalog {
	if index := s.state.taskCatalog; index != nil && index.structure != taskIndexStructureOf(s.state.tasks) {
		s.invalidateTaskIndex()
	}
	if s.state.taskCatalog == nil {
		s.state.taskCatalog = newTaskIndexCatalog(s.state.tasks)
	}
	return s.state.taskCatalog
}

func (s *taskController) invalidateTaskIndex() {
	s.state.tasksRevision++
	s.state.taskCatalog = nil
	s.state.taskSearch = taskIndexSearchProjection{}
}

type taskIndexProjectionKey struct {
	tasks, groups, metadata, pins uint64
	home, search                  string
}

type taskIndexProjection struct {
	key          taskIndexProjectionKey
	valid        bool
	rows         []int
	positions    map[int]int
	keyPositions map[string]int
}

type taskIndexSearchProjection struct {
	tasks, groups, metadata uint64
	valid                   bool
	text                    []string
}

func (s *app) cachedTaskIndexRows(archived bool) []int {
	// Check legacy structural mutations before the revision-only cache hit.
	catalog := s.taskUnit.taskCatalog()
	state := s.taskIndexBrowsingFor(archived)
	navigation := s.taskNavigationFor()
	key := taskIndexProjectionKey{tasks: s.taskUnit.state.tasksRevision, groups: s.taskUnit.state.groupsRevision, metadata: s.taskUnit.state.metadataRevision, pins: navigation.revision, home: navigation.home, search: strings.ToLower(strings.TrimSpace(state.search))}
	if state.projection.valid && state.projection.key == key {
		return state.projection.rows
	}
	terms := strings.Fields(key.search)
	var text []string
	if len(terms) > 0 {
		search := &s.taskUnit.state.taskSearch
		if !search.valid || search.tasks != key.tasks || search.groups != key.groups || search.metadata != key.metadata {
			*search = taskIndexSearchProjection{tasks: key.tasks, groups: key.groups, metadata: key.metadata, valid: true, text: make([]string, len(s.taskUnit.state.tasks))}
			for i, item := range s.taskUnit.state.tasks {
				search.text[i] = s.taskSearchText(item)
			}
		}
		text = search.text
	}
	category := 0
	if archived {
		category = 1
	}
	rows := projectTaskIndexOrder(catalog.categories[category], catalog.keys, navigation.value.Pinned, func(i int) bool {
		for _, term := range terms {
			if !strings.Contains(text[i], term) {
				return false
			}
		}
		return true
	})
	projection := taskIndexProjection{key: key, valid: true, rows: rows, positions: make(map[int]int, len(rows)), keyPositions: make(map[string]int, len(rows))}
	for position, index := range rows {
		projection.positions[index] = position
		projection.keyPositions[catalog.keys[index]] = position
	}
	state.projection = projection
	return rows
}

// Both paths retain pin order followed by the source's stable order. The
// standalone path can consume a mutable next-snapshot slice before publication.
func projectTaskIndexOrder(candidates []int, keys, pins []string, matches func(int) bool) []int {
	rows := make([]int, 0, len(candidates))
	byKey := make(map[string]int, len(candidates))
	for _, i := range candidates {
		if matches(i) {
			rows = append(rows, i)
			byKey[keys[i]] = i
		}
	}
	ordered := make([]int, 0, len(rows))
	for _, key := range pins {
		if i, found := byKey[key]; found {
			ordered = append(ordered, i)
			delete(byKey, key)
		}
	}
	for _, i := range rows {
		if _, found := byKey[keys[i]]; found {
			ordered = append(ordered, i)
		}
	}
	return ordered
}

func (s *app) taskSearchText(item task) string {
	fields := []string{item.id, item.title, item.requirementID, item.requirementTitle, item.requirementURL, item.project, item.groupID, s.taskGroupName(item)}
	metadata := s.taskRequirementMetadataFor(item).Metadata
	fields = append(fields, metadata.ID)
	if metadata.Title != nil {
		fields = append(fields, *metadata.Title)
	}
	return strings.ToLower(strings.Join(fields, "\n"))
}

// Store detached strings, rather than retaining metadata pointers/maps, so a
// producer that mutates a Title pointer and republishes still invalidates.
type taskIndexMetadataValue struct {
	url, id, title, status string
	state                  requirementMetadataState
	hasTitle, hasStatus    bool
}

func taskIndexMetadataProjection(value taskRequirementMetadataState) taskIndexMetadataValue {
	projection := taskIndexMetadataValue{url: value.URL, state: value.Status, id: value.Metadata.ID}
	if value.Metadata.Title != nil {
		projection.title, projection.hasTitle = *value.Metadata.Title, true
	}
	if value.Metadata.Status != nil {
		projection.status, projection.hasStatus = *value.Metadata.Status, true
	}
	return projection
}

func (s *app) setTaskRequirementMetadata(taskID string, value taskRequirementMetadataState) {
	if s.requirementUnit.state.taskRequirementMetadata == nil {
		s.requirementUnit.state.taskRequirementMetadata = make(map[string]taskRequirementMetadataState)
	}
	if s.taskUnit.state.metadataValues == nil {
		s.taskUnit.state.metadataValues = make(map[string]taskIndexMetadataValue)
	}
	projection := taskIndexMetadataProjection(value)
	previous, known := s.taskUnit.state.metadataValues[taskID]
	if !known || previous != projection {
		s.taskUnit.state.metadataRevision++
	}
	s.taskUnit.state.metadataValues[taskID] = projection
	s.requirementUnit.state.taskRequirementMetadata[taskID] = value
}

func (s *app) deleteTaskRequirementMetadata(taskID string) {
	if _, exists := s.requirementUnit.state.taskRequirementMetadata[taskID]; exists {
		s.taskUnit.state.metadataRevision++
	}
	delete(s.requirementUnit.state.taskRequirementMetadata, taskID)
	delete(s.taskUnit.state.metadataValues, taskID)
}

type taskMetadataDiscoveryKey struct {
	tasks, metadata uint64
	config          string
}
