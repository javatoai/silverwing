package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/storage"

	"github.com/egoist/mygo/ui"
)

// desktopReadingSession bridges the renderer-independent reading cache to the
// native UI. It stores browsing position and selection only; document bytes and
// unsaved drafts remain in their existing document services.
type desktopReadingSession struct {
	store                  *ReadingStateStore
	cache                  ReadingSnapshot
	home                   string
	loaded                 bool
	closed                 bool
	restoreLastTask        bool
	restoreAttempted       bool
	userInteracted         bool
	startupIdentity        readingUIIdentity
	lastObserved           []byte
	observation            readingObservation
	dirty                  bool
	restoredMaterials      map[string]bool
	scrollStates           map[string]readingScrollBinding
	pendingMaterials       bool
	pendingTaskPath        string
	pendingRoot            string
	pendingMaterialsUIKey  string
	pendingMaterialsUIPath string
	pendingMaterialsUITabs []string
	errorTimer             *time.Timer
}

type readingUIIdentity struct {
	taskPath    string
	page        string
	tab         int
	document    string
	documentTab string
	dirty       bool
}

type readingScrollBinding struct {
	taskPath        string
	root            string
	document        string
	requirementLink string
	id              string
	state           *ui.ScrollState
	initialY        float32
}

func (s *app) initializeReadingSession(home string, restoreLastTask bool) {
	if s.desktopPreferences != nil && !s.desktopPreferences.enabled {
		return
	}
	if strings.TrimSpace(home) == "" {
		return
	}
	win := s.win
	if win == nil || win.IsDestroyed() {
		return
	}
	cleanHome := filepath.Clean(home)
	if s.readingSession != nil && normalizedReadingPath(s.readingSession.home) == normalizedReadingPath(cleanHome) {
		return
	}
	paths := storage.NewPaths(cleanHome)
	session := &desktopReadingSession{
		store:             NewReadingStateStore(filepath.Join(paths.Cache, "reading-state.json")),
		cache:             EmptyReadingSnapshot(),
		home:              cleanHome,
		restoreLastTask:   restoreLastTask,
		startupIdentity:   s.readingUIIdentity(),
		restoredMaterials: make(map[string]bool),
		scrollStates:      make(map[string]readingScrollBinding),
	}
	s.readingSession = session

	go func() {
		value, err := session.store.LoadWithError()
		apply := func() {
			if s.readingSession != session || session.closed {
				return
			}
			session.cache = value
			session.dirty = true
			session.loaded = true
			session.lastObserved, _ = json.Marshal(value)
			if err != nil {
				s.lastError = "读取阅读状态失败：" + err.Error()
			}
			s.restoreReadingSessionOnSnapshot()
			if !session.userInteracted {
				s.restoreReadingScrollPointers()
			}
			if !win.IsDestroyed() {
				win.Invalidate()
			}
		}
		if win.IsDestroyed() {
			return
		}
		win.Update(apply)
	}()
}

// restoreReadingSessionOnSnapshot is idempotent. An explicit page/tab
// selection or any observed interaction wins over this asynchronous load.
func (s *app) restoreReadingSessionOnSnapshot() {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed || session.restoreAttempted || s.loading {
		return
	}
	session.restoreAttempted = true
	if session.restoreLastTask && !session.userInteracted && readingStartupIdentityMatches(session.startupIdentity, s.readingUIIdentity()) {
		if tab, ok := readingTabForView(session.cache.View); ok {
			s.tab = tab
		}
		if session.cache.LastTaskPath != "" {
			last := normalizedReadingPath(session.cache.LastTaskPath)
			previousTask := s.taskUnit.currentTask()
			for index, candidate := range s.taskUnit.state.tasks {
				if candidate.path != "" && normalizedReadingPath(candidate.path) == last {
					if previousTask.path != "" && normalizedReadingPath(previousTask.path) != last {
						s.resetReadingMaterialsForTaskRestore()
					}
					s.taskUnit.restoreSelection(index)
					if candidate.archived {
						s.page = "archive"
					} else {
						s.page = "tasks"
					}
					break
				}
			}
		}
	}
	s.completePendingReadingMaterials()
}

func (s *app) beforeReadingTaskSelection() {
	if s.readingSession != nil {
		s.readingSession.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(s.readingSession)
	}
	s.markReadingInteraction()
	s.observeReadingSession()
}

func (s *app) afterReadingTaskSelection() {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return
	}
	current := s.taskUnit.currentTask()
	if current.path != "" {
		delete(session.restoredMaterials, readingMaterialsKey(current.path, current.materialsPath))
	}
	if tab, ok := readingTabForView(session.cache.View); ok {
		s.tab = tab
	}
	s.observeReadingSession()
}

// markReadingInteraction protects manual route/tab changes from a cache load
// that is still in flight. Call it before applying the selection.
func (s *app) markReadingInteraction() {
	if s.readingSession != nil && !s.readingSession.loaded {
		s.readingSession.userInteracted = true
	}
}

func (s *app) observeReadingSession() {
	session := s.readingSession
	if session == nil || session.closed || s.loading {
		return
	}
	s.completePendingReadingMaterials()
	if !session.loaded {
		for _, binding := range session.scrollStates {
			if binding.state != nil && readingScrollCoordinate(binding.id, binding.state) != binding.initialY {
				session.userInteracted = true
				break
			}
		}
		return
	}
	if session.store != nil {
		if err := session.store.LastError(); err != nil {
			s.lastError = "保存阅读状态失败：" + err.Error()
		}
	}
	inputs := s.readingObservationInputs()
	if !session.dirty && session.observation.unchanged(inputs, s.materialsUnit.state.documentTabs, session.scrollStates) {
		return
	}
	value := s.captureReadingSnapshot(session)
	session.cache = value
	if session.store == nil {
		session.dirty = false
		session.observation.remember(inputs, s.materialsUnit.state.documentTabs, session.scrollStates)
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		s.lastError = "编码阅读状态失败：" + err.Error()
		return
	}
	if !bytes.Equal(encoded, session.lastObserved) {
		if err := session.store.ScheduleSave(value); err != nil {
			s.lastError = "保存阅读状态失败：" + err.Error()
			return
		}
		session.lastObserved = append(session.lastObserved[:0], encoded...)
		s.scheduleReadingErrorCheck(session)
	}
	session.dirty = false
	session.observation.remember(inputs, s.materialsUnit.state.documentTabs, session.scrollStates)
}

func (s *app) captureReadingSnapshot(session *desktopReadingSession) ReadingSnapshot {
	view := readingViewForTab(s.tab)
	current := s.taskUnit.currentTask()
	lastTaskPath := ""
	if current.path != "" {
		lastTaskPath = normalizedReadingPath(current.path)
	}

	trackMaterials := current.path != "" && (s.tab == 1 || s.materialsUnit.state.documentsLoaded || s.materialsUnit.state.documentsRequested || s.materialsUnit.state.documentPath != "" || len(s.materialsUnit.state.documentTabs) > 0)
	if trackMaterials {
		material := readingMaterialsFor(session, current.path, current.materialsPath, true)
		if material != nil {
			if s.materialsUnit.state.documentsLoaded {
				material.DocumentTabs.Initialized = true
			}
			hasDocumentUI := s.materialsUnit.state.documentsLoaded || s.materialsUnit.state.documentPath != "" || len(s.materialsUnit.state.documentTabs) > 0
			if hasDocumentUI {
				material.DirectoryCollapsed = s.materialsUnit.state.documentsCollapsed
				material.DocumentTabs.Tabs = material.DocumentTabs.Tabs[:0]
				for _, path := range s.materialsUnit.state.documentTabs {
					if relative, ok := readingRelativePathFromRoot(current.materialsPath, path); ok {
						material.DocumentTabs.Tabs = append(material.DocumentTabs.Tabs, MaterialsDocumentTab{Path: relative, IsPinned: true})
					}
				}
				material.SelectedPath = nil
				if relative, ok := readingRelativePathFromRoot(current.materialsPath, s.materialsUnit.state.documentPath); ok {
					material.SelectedPath = &relative
					active := relative
					material.DocumentTabs.ActivePath = &active
				} else {
					material.DocumentTabs.ActivePath = nil
				}
			}
			for _, binding := range session.scrollStates {
				if binding.state == nil || binding.taskPath != normalizedReadingPath(current.path) || binding.root != normalizedReadingRoot(current.materialsPath) || binding.requirementLink != "" {
					continue
				}
				scroll := int(math.Round(float64(readingScrollCoordinate(binding.id, binding.state))))
				if binding.id == "directory" {
					material.DirectoryPosition.SetScrollPosition(binding.id, scroll)
					continue
				}
				if binding.document != "" {
					position := material.Files[binding.document]
					if position.Mode == "" {
						position = DefaultReadingPositionSnapshot()
					}
					position.SetScrollPosition(binding.id, scroll)
					material.SetFileReading(binding.document, position)
				}
			}
		}
	}
	if s.tab == 2 && current.path != "" && strings.TrimSpace(current.requirementURL) != "" {
		link := strings.TrimSpace(current.requirementURL)
		position := readingRequirementFor(session, current.path, link, true)
		for _, binding := range session.scrollStates {
			if binding.state != nil && binding.taskPath == normalizedReadingPath(current.path) && binding.requirementLink == link {
				position.SetScrollPosition(binding.id, int(math.Round(float64(readingScrollCoordinate(binding.id, binding.state)))))
			}
		}
	}
	value := session.cache
	value.Schema = 1
	value.View = view
	value.LastTaskPath = lastTaskPath
	value.Materials = append([]MaterialsSnapshot(nil), session.cache.Materials...)
	value.Requirements = append([]RequirementReadingSnapshot(nil), session.cache.Requirements...)
	return value.Bounded()
}

func (s *app) restoreReadingMaterials() bool {
	session := s.readingSession
	if session == nil || session.closed || !s.materialsUnit.state.documentsLoaded {
		return false
	}
	current := s.taskUnit.currentTask()
	if current.path == "" {
		return false
	}
	if !session.loaded {
		session.pendingMaterials = true
		session.pendingTaskPath = normalizedReadingPath(current.path)
		session.pendingRoot = normalizedReadingRoot(current.materialsPath)
		return true
	}
	material := readingMaterialsFor(session, current.path, current.materialsPath, false)
	if material == nil {
		return false
	}
	key := readingMaterialsKey(current.path, current.materialsPath)
	if session.pendingMaterialsUIKey != "" && session.pendingMaterialsUIKey != key {
		s.clearPendingReadingMaterialsUI(session)
	}
	if session.restoredMaterials[key] {
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		return true
	}
	// A selection made while document.list was pending is already current UI
	// state and must not be replaced by the saved tab set. A retry after a busy
	// read may see the tabs this method itself installed; preserve those as a
	// pending restore rather than mistaking them for a user selection.
	pendingUI := session.pendingMaterialsUIKey == key &&
		session.pendingMaterialsUIPath == s.materialsUnit.state.documentPath &&
		equalReadingPaths(session.pendingMaterialsUITabs, s.materialsUnit.state.documentTabs)
	if (s.materialsUnit.state.documentPath != "" || len(s.materialsUnit.state.documentTabs) > 0 || s.materialsUnit.state.documentDirty) && !pendingUI {
		session.restoredMaterials[key] = true
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		return true
	}

	s.materialsUnit.state.documentsCollapsed = material.DirectoryCollapsed
	s.currentReadingMaterialsSortDocuments()
	documents := flattenReadingDocuments(s.materialsUnit.state.documents)
	currentRoot := current.materialsPath
	currentRootPath := &currentRoot
	tabs := make([]string, 0, len(material.DocumentTabs.Tabs))
	for _, saved := range material.DocumentTabs.Tabs {
		if path, ok := resolveReadingDocument(currentRootPath, saved.Path); ok {
			if _, exists := documents[normalizedReadingPath(path)]; exists && !readingContainsPath(tabs, path) {
				tabs = append(tabs, path)
			}
		}
	}
	active := ""
	activeCandidate := ""
	if material.DocumentTabs.ActivePath != nil {
		activeCandidate, _ = resolveReadingDocument(currentRootPath, *material.DocumentTabs.ActivePath)
	}
	if activeCandidate == "" && material.SelectedPath != nil {
		activeCandidate, _ = resolveReadingDocument(currentRootPath, *material.SelectedPath)
	}
	active = activeCandidate
	if activeCandidate != "" {
		if _, exists := documents[normalizedReadingPath(activeCandidate)]; !exists {
			active = nextReadingDocumentAfterMissingTab(material.DocumentTabs.Tabs, activeCandidate, currentRootPath, documents)
			if active == "" {
				active = firstDocument(s.materialsUnit.state.documents)
			}
		}
	}
	if active != "" && !readingContainsPath(tabs, active) {
		tabs = append(tabs, active)
	}
	s.materialsUnit.state.documentTabs = tabs
	if active != "" {
		if s.busy {
			session.pendingMaterials = true
			session.pendingTaskPath = normalizedReadingPath(current.path)
			session.pendingRoot = normalizedReadingRoot(current.materialsPath)
			s.rememberPendingReadingMaterialsUI(session, key)
			return true
		}
		// Restoring an active tab must preserve the user's saved folder state.
		// Explicitly opening or locating a document still reveals its parents.
		s.selectDocumentWithReveal(active, false)
		if s.materialsUnit.state.documentPath != active {
			session.pendingMaterials = true
			session.pendingTaskPath = normalizedReadingPath(current.path)
			session.pendingRoot = normalizedReadingRoot(current.materialsPath)
			s.rememberPendingReadingMaterialsUI(session, key)
			return true
		}
		session.restoredMaterials[key] = true
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		return true
	}
	if material.DocumentTabs.Initialized {
		session.restoredMaterials[key] = true
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentHash = "", "", ""
		s.materialsUnit.state.documentKind, s.materialsUnit.state.documentReadOnly = "", false
		s.materialsUnit.state.documentDirty, s.materialsUnit.state.documentEditing = false, false
		return true
	}
	if len(tabs) > 0 {
		session.restoredMaterials[key] = true
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		return true
	}
	return false
}

func nextReadingDocumentAfterMissingTab(tabs []MaterialsDocumentTab, missingPath string, root *string, available map[string]application.Document) string {
	paths := make([]string, len(tabs))
	activeIndex := -1
	for index, tab := range tabs {
		path, ok := resolveReadingDocument(root, tab.Path)
		if !ok {
			continue
		}
		paths[index] = path
		if normalizedReadingPath(path) == normalizedReadingPath(missingPath) {
			activeIndex = index
		}
	}
	isAvailable := func(path string) bool {
		if path == "" {
			return false
		}
		_, exists := available[normalizedReadingPath(path)]
		return exists
	}
	if activeIndex >= 0 {
		for index := activeIndex + 1; index < len(paths); index++ {
			if isAvailable(paths[index]) {
				return paths[index]
			}
		}
		for index := activeIndex - 1; index >= 0; index-- {
			if isAvailable(paths[index]) {
				return paths[index]
			}
		}
	}
	for _, path := range paths {
		if isAvailable(path) {
			return path
		}
	}
	return ""
}

func (s *app) closeReadingSession() error {
	session := s.readingSession
	if session == nil || session.closed {
		return nil
	}
	if session.store == nil {
		session.closed = true
		return nil
	}
	if !session.loaded {
		session.closed = true
		session.store.CancelPending()
		return nil
	}
	if session.errorTimer != nil {
		session.errorTimer.Stop()
		session.errorTimer = nil
	}
	final := s.captureReadingSnapshot(session)
	session.cache = final
	session.closed = true
	if err := session.store.Close(final); err != nil {
		return fmt.Errorf("保存阅读状态失败：%w", err)
	}
	return nil
}

func (s *app) completePendingReadingMaterials() {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed || !session.pendingMaterials || !s.materialsUnit.state.documentsLoaded {
		return
	}
	if s.busy {
		return
	}
	current := s.taskUnit.currentTask()
	if current.path == "" || normalizedReadingPath(current.path) != session.pendingTaskPath || normalizedReadingRoot(current.materialsPath) != session.pendingRoot {
		session.pendingMaterials = false
		s.clearPendingReadingMaterialsUI(session)
		return
	}
	if s.restoreReadingMaterials() {
		return
	}
	session.pendingMaterials = false
	if s.materialsUnit.state.documentPath == "" {
		if first := firstDocument(s.materialsUnit.state.documents); first != "" {
			s.selectDocument(first)
		}
	}
}

func (s *app) rememberPendingReadingMaterialsUI(session *desktopReadingSession, key string) {
	if session == nil {
		return
	}
	session.pendingMaterialsUIKey = key
	session.pendingMaterialsUIPath = s.materialsUnit.state.documentPath
	session.pendingMaterialsUITabs = append(session.pendingMaterialsUITabs[:0], s.materialsUnit.state.documentTabs...)
}

func (s *app) clearPendingReadingMaterialsUI(session *desktopReadingSession) {
	if session == nil {
		return
	}
	session.pendingMaterialsUIKey = ""
	session.pendingMaterialsUIPath = ""
	session.pendingMaterialsUITabs = nil
}

func equalReadingPaths(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *app) scheduleReadingErrorCheck(session *desktopReadingSession) {
	if session == nil || session.store == nil || s.win == nil {
		return
	}
	if session.errorTimer != nil {
		session.errorTimer.Stop()
	}
	win := s.win
	session.errorTimer = time.AfterFunc(700*time.Millisecond, func() {
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if s.readingSession != session || session.closed {
				return
			}
			session.errorTimer = nil
			if err := session.store.LastError(); err != nil {
				s.lastError = "保存阅读状态失败：" + err.Error()
				win.Invalidate()
			}
		})
	})
}

// readingScrollState supplies MyGO's native TrackScroll state. The caller must
// attach it to the actual ui.Scroll; merely observing this pointer cannot track
// the viewport.
func (s *app) readingScrollState(id string) *ui.ScrollState {
	session := s.readingSession
	if session == nil || session.closed || strings.TrimSpace(id) == "" {
		return nil
	}
	current := s.taskUnit.currentTask()
	if current.path == "" {
		return nil
	}
	taskPath := normalizedReadingPath(current.path)
	root := normalizedReadingRoot(current.materialsPath)
	binding := readingScrollBinding{taskPath: taskPath, root: root, id: id}
	if s.tab == 2 && strings.TrimSpace(current.requirementURL) != "" {
		binding.requirementLink = strings.TrimSpace(current.requirementURL)
	} else if id != "directory" {
		binding.document, _ = readingRelativePathFromRoot(current.materialsPath, s.materialsUnit.state.documentPath)
	}
	key := readingScrollBindingKey(binding)
	if found, ok := session.scrollStates[key]; ok {
		return found.state
	}
	state := &ui.ScrollState{}
	if position, _ := readingPositionForBinding(session, binding, false); position.Mode != "" {
		if readingScrollIsHorizontal(id) {
			state.X = float32(position.Scrolls[id])
		} else {
			state.Y = float32(position.Scrolls[id])
		}
	}
	binding.state = state
	binding.initialY = readingScrollCoordinate(id, state)
	session.scrollStates[key] = binding
	return state
}

func (s *app) readingMode() string {
	if position, _ := s.currentReadingPosition(false); position.Mode == "SOURCE" || position.Mode == "RENDERED" {
		return position.Mode
	}
	return "RENDERED"
}

func (s *app) setReadingMode(mode string) {
	if mode != "SOURCE" && mode != "RENDERED" {
		return
	}
	if position, save := s.currentReadingPosition(true); save != nil {
		position.Mode = mode
		save(position)
		s.observeReadingSession()
	}
}

func (s *app) readingZoom() int {
	if position, _ := s.currentReadingPosition(false); position.Zoom >= 25 && position.Zoom <= 200 {
		return position.Zoom
	}
	return 100
}

func (s *app) setReadingZoom(zoom int) {
	if position, save := s.currentReadingPosition(true); save != nil {
		position.Zoom = clampInt(zoom, 25, 200)
		save(position)
		s.observeReadingSession()
	}
}

func (s *app) nativeMarkdownReadingState() NativeMarkdownReadingState {
	if position, _ := s.currentReadingPosition(false); position.Mode != "" {
		return position.Chromium.bounded()
	}
	return DefaultNativeMarkdownReadingState()
}

func (s *app) readingMarkdownZoom() float64 {
	return s.nativeMarkdownReadingState().Zoom
}

func (s *app) setReadingMarkdownZoom(zoom float64) {
	state := s.nativeMarkdownReadingState()
	state.Zoom = zoom
	s.setNativeMarkdownReadingState(state)
}

func (s *app) setNativeMarkdownReadingState(value NativeMarkdownReadingState) {
	if position, save := s.currentReadingPosition(true); save != nil {
		position.Chromium = value.bounded()
		save(position)
		s.observeReadingSession()
	}
}

func (s *app) readingExpandedDirectories() map[string]bool {
	result := make(map[string]bool)
	if material := s.currentReadingMaterials(false); material != nil {
		for _, path := range material.ExpandedDirectories {
			result[path] = true
		}
	}
	return result
}

func (s *app) readingExpansionInitialized() bool {
	material := s.currentReadingMaterials(false)
	return material != nil && material.ExpansionInitialized && material.ExpansionRevision == s.materialsExpansionRevision()
}

func (s *app) setReadingDirectoryExpanded(path string, expanded bool) {
	material := s.currentReadingMaterials(true)
	if material == nil {
		return
	}
	relative, ok := validReadingRelativePath(path)
	if !ok {
		relative, ok = readingRelativePath(material.Root, path)
	}
	if !ok {
		return
	}
	index := -1
	for i, candidate := range material.ExpandedDirectories {
		if candidate == relative {
			index = i
			break
		}
	}
	if expanded && index < 0 {
		material.ExpandedDirectories = append(material.ExpandedDirectories, relative)
	} else if !expanded && index >= 0 {
		material.ExpandedDirectories = append(material.ExpandedDirectories[:index], material.ExpandedDirectories[index+1:]...)
	}
	material.ExpansionInitialized = true
	s.observeReadingSession()
}

func (s *app) markReadingExpansionInitialized() {
	if material := s.currentReadingMaterials(true); material != nil {
		material.ExpansionInitialized = true
		material.ExpansionRevision = s.materialsExpansionRevision()
		s.observeReadingSession()
	}
}

func (s *app) readingMaterialsSortOrder() string {
	if material := s.currentReadingMaterials(false); material != nil && material.SortOrder == "MODIFIED_DESC" {
		return "MODIFIED_DESC"
	}
	return "NAME"
}

func (s *app) setReadingMaterialsSortOrder(order string) {
	if order != "NAME" && order != "MODIFIED_DESC" {
		return
	}
	if material := s.currentReadingMaterials(true); material != nil {
		material.SortOrder = order
		s.currentReadingMaterialsSortDocuments()
		s.observeReadingSession()
	}
}

// The current Go document tree is a native Scroll rather than a virtual List.
// These helpers preserve Source's row anchor if a future native list exposes it.
func (s *app) readingDirectoryPosition() (int, int) {
	material := s.currentReadingMaterials(false)
	if material == nil {
		return 0, 0
	}
	return readingPair(material.DirectoryPosition.Lists["directory"])
}

func (s *app) setReadingDirectoryPosition(index, offset int) {
	if material := s.currentReadingMaterials(true); material != nil {
		material.DirectoryPosition.SetListPosition("directory", index, offset)
		s.observeReadingSession()
	}
}

func (s *app) currentReadingPosition(create bool) (ReadingPositionSnapshot, func(ReadingPositionSnapshot)) {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return ReadingPositionSnapshot{}, nil
	}
	current := s.taskUnit.currentTask()
	if current.path == "" {
		return ReadingPositionSnapshot{}, nil
	}
	if s.tab == 2 && strings.TrimSpace(current.requirementURL) != "" {
		position := readingRequirementFor(session, current.path, strings.TrimSpace(current.requirementURL), create)
		if position == nil {
			return ReadingPositionSnapshot{}, nil
		}
		return *position, func(value ReadingPositionSnapshot) {
			*position = value
			session.dirty = true
		}
	}
	material := readingMaterialsFor(session, current.path, current.materialsPath, create)
	if material == nil {
		return ReadingPositionSnapshot{}, nil
	}
	relative, ok := readingRelativePath(material.Root, s.materialsUnit.state.documentPath)
	if !ok {
		return ReadingPositionSnapshot{}, nil
	}
	position, exists := material.Files[relative]
	if !exists && !create {
		return ReadingPositionSnapshot{}, nil
	}
	if !exists || position.Mode == "" {
		position = DefaultReadingPositionSnapshot()
	}
	if create {
		material.SetFileReading(relative, position)
		session.dirty = true
	}
	return position, func(value ReadingPositionSnapshot) {
		material.SetFileReading(relative, value)
		session.dirty = true
	}
}

func (s *app) currentReadingMaterials(create bool) *MaterialsSnapshot {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return nil
	}
	current := s.taskUnit.currentTask()
	if current.path == "" {
		return nil
	}
	if create {
		session.dirty = true
	}
	return readingMaterialsFor(session, current.path, current.materialsPath, create)
}

func (s *app) readingUIIdentity() readingUIIdentity {
	current := s.taskUnit.currentTask()
	path := ""
	if current.path != "" {
		path = normalizedReadingPath(current.path)
	}
	document := ""
	if s.materialsUnit.state.documentPath != "" {
		document = normalizedReadingPath(s.materialsUnit.state.documentPath)
	}
	return readingUIIdentity{
		taskPath: path, page: s.page, tab: s.tab,
		document: document, documentTab: strings.Join(s.materialsUnit.state.documentTabs, "\x00"), dirty: s.materialsUnit.state.documentDirty,
	}
}

func (s *app) resetReadingMaterialsForTaskRestore() {
	s.materialsUnit.resetTaskPresentation(TaskTransition{origin: taskTransitionSnapshot}, s)
}

func readingStartupIdentityMatches(before, after readingUIIdentity) bool {
	// Run initializes this bridge before the first task snapshot. The app's
	// normal default task selection is a snapshot result, not user input.
	if before.taskPath == "" && before.document == "" && before.documentTab == "" {
		before.taskPath = after.taskPath
	}
	return before == after
}

func (s *app) restoreReadingScrollPointers() {
	session := s.readingSession
	if session == nil || !session.loaded {
		return
	}
	for _, binding := range session.scrollStates {
		if binding.state == nil {
			continue
		}
		if position, _ := readingPositionForBinding(session, binding, false); position.Mode != "" {
			if value, exists := position.Scrolls[binding.id]; exists {
				if readingScrollIsHorizontal(binding.id) {
					binding.state.X = float32(value)
				} else {
					binding.state.Y = float32(value)
				}
			}
		}
	}
}

func (s *app) currentReadingMaterialsSortDocuments() {
	// Reading order is a value projection. Sorting the published snapshot in
	// place would also change quick-open's raw DFS order and bypass generation.
	s.materialsDocumentProjection().treeRows(s.readingMaterialsSortOrder())
}

func readingMaterialsFor(session *desktopReadingSession, taskPath, root string, create bool) *MaterialsSnapshot {
	if session == nil || taskPath == "" {
		return nil
	}
	taskKey, rootKey := normalizedReadingPath(taskPath), normalizedReadingRoot(root)
	index := -1
	// Bounded snapshots and captures keep the active task at the end. Prefer
	// recent records, and reuse canonical keys before normalizing legacy input.
	for i := len(session.cache.Materials) - 1; i >= 0; i-- {
		candidate := &session.cache.Materials[i]
		if candidate.TaskPath != taskKey && normalizedReadingPath(candidate.TaskPath) != taskKey {
			continue
		}
		candidateRoot := readingRootValue(candidate.Root)
		if candidateRoot == rootKey || normalizedReadingRoot(candidateRoot) == rootKey {
			index = i
			break
		}
	}
	if index >= 0 {
		if create && index != len(session.cache.Materials)-1 {
			record := session.cache.Materials[index]
			session.cache.Materials = append(session.cache.Materials[:index], session.cache.Materials[index+1:]...)
			session.cache.Materials = append(session.cache.Materials, record)
			session.dirty = true
			index = len(session.cache.Materials) - 1
		}
		return &session.cache.Materials[index]
	}
	if !create {
		return nil
	}
	// Source discards an old materials record when the same task is associated
	// with a different root.
	kept := session.cache.Materials[:0]
	for _, record := range session.cache.Materials {
		if normalizedReadingPath(record.TaskPath) != taskKey {
			kept = append(kept, record)
		}
	}
	session.cache.Materials = kept
	record := DefaultMaterialsSnapshot()
	record.TaskPath = taskKey
	if rootKey != "" {
		record.Root = &rootKey
	}
	session.cache.Materials = append(session.cache.Materials, record)
	session.dirty = true
	return &session.cache.Materials[len(session.cache.Materials)-1]
}

func readingRequirementFor(session *desktopReadingSession, taskPath, link string, create bool) *ReadingPositionSnapshot {
	if session == nil || taskPath == "" || strings.TrimSpace(link) == "" {
		return nil
	}
	taskKey := normalizedReadingPath(taskPath)
	for i := len(session.cache.Requirements) - 1; i >= 0; i-- {
		record := &session.cache.Requirements[i]
		if record.Link == link && (record.TaskPath == taskKey || normalizedReadingPath(record.TaskPath) == taskKey) {
			return &record.Reading
		}
	}
	if !create {
		return nil
	}
	position := DefaultReadingPositionSnapshot()
	session.cache.Requirements = append(session.cache.Requirements, RequirementReadingSnapshot{TaskPath: taskKey, Link: link, Reading: position})
	session.dirty = true
	return &session.cache.Requirements[len(session.cache.Requirements)-1].Reading
}

func readingPositionForBinding(session *desktopReadingSession, binding readingScrollBinding, create bool) (ReadingPositionSnapshot, func(ReadingPositionSnapshot)) {
	if binding.requirementLink != "" {
		position := readingRequirementFor(session, binding.taskPath, binding.requirementLink, create)
		if position == nil {
			return ReadingPositionSnapshot{}, nil
		}
		return *position, func(value ReadingPositionSnapshot) {
			*position = value
			session.dirty = true
		}
	}
	material := readingMaterialsFor(session, binding.taskPath, binding.root, create)
	if material == nil {
		return ReadingPositionSnapshot{}, nil
	}
	if binding.id == "directory" {
		return material.DirectoryPosition, func(value ReadingPositionSnapshot) {
			material.DirectoryPosition = value
			session.dirty = true
		}
	}
	if binding.document == "" {
		return ReadingPositionSnapshot{}, nil
	}
	position, exists := material.Files[binding.document]
	if !exists && !create {
		return ReadingPositionSnapshot{}, nil
	}
	if !exists || position.Mode == "" {
		position = DefaultReadingPositionSnapshot()
	}
	if create {
		material.SetFileReading(binding.document, position)
		session.dirty = true
	}
	return position, func(value ReadingPositionSnapshot) {
		material.SetFileReading(binding.document, value)
		session.dirty = true
	}
}

func readingScrollBindingKey(binding readingScrollBinding) string {
	return strings.Join([]string{binding.taskPath, binding.root, binding.document, binding.requirementLink, binding.id}, "\x00")
}

func readingScrollIsHorizontal(id string) bool {
	return strings.HasSuffix(id, "-horizontal")
}

func readingScrollCoordinate(id string, state *ui.ScrollState) float32 {
	if state == nil {
		return 0
	}
	if readingScrollIsHorizontal(id) {
		return state.X
	}
	return state.Y
}

func readingMaterialsKey(taskPath, root string) string {
	return normalizedReadingPath(taskPath) + "\x00" + normalizedReadingRoot(root)
}

func normalizedReadingRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return normalizedReadingPath(root)
}

func readingRootValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func readingRelativePath(root *string, path string) (string, bool) {
	if root == nil || strings.TrimSpace(*root) == "" {
		return "", false
	}
	return readingRelativePathFromRoot(*root, path)
}

func readingRelativePathFromRoot(root, path string) (string, bool) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return "", false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return validReadingRelativePath(filepath.ToSlash(relative))
}

func resolveReadingDocument(root *string, relative string) (string, bool) {
	if root == nil || strings.TrimSpace(*root) == "" {
		return "", false
	}
	cleanRelative, ok := validReadingRelativePath(relative)
	if !ok {
		return "", false
	}
	path := filepath.Join(*root, filepath.FromSlash(cleanRelative))
	if _, ok := readingRelativePath(root, path); !ok {
		return "", false
	}
	return path, true
}

func flattenReadingDocuments(items []application.Document) map[string]application.Document {
	result := make(map[string]application.Document)
	var walk func([]application.Document)
	walk = func(documents []application.Document) {
		for _, document := range documents {
			result[normalizedReadingPath(document.Path)] = document
			walk(document.Children)
		}
	}
	walk(items)
	return result
}

func readingViewForTab(tab int) string {
	switch tab {
	case 1:
		return "MATERIALS"
	case 2:
		return "REQUIREMENT"
	case 3:
		return "NOTES"
	case taskTagTab:
		return "TAG"
	default:
		return "DETAIL"
	}
}

func readingTabForView(view string) (int, bool) {
	switch view {
	case "DETAIL":
		return 0, true
	case "MATERIALS":
		return 1, true
	case "REQUIREMENT":
		return 2, true
	case "NOTES":
		return 3, true
	case "TAG":
		return taskTagTab, true
	default:
		return 0, false
	}
}

func readingPair(values []int) (int, int) {
	if len(values) == 0 {
		return 0, 0
	}
	if len(values) == 1 {
		return values[0], 0
	}
	return values[0], values[1]
}

func readingContainsPath(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
