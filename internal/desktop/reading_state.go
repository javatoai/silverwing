package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const (
	maxReadingTasks        = 100
	maxReadingFilesPerTask = 100
	maxReadingPositions    = 30
	maxReadingCacheBytes   = 4_000_000
	maxReadingCacheAge     = 90 * 24 * time.Hour
	readingSaveDebounce    = 600 * time.Millisecond
)

var ErrReadingStateStoreClosed = errors.New("reading state store is closed")

// NativeMarkdownReadingState is a renderer-independent reading cursor.
type NativeMarkdownReadingState struct {
	Anchor    string  `json:"anchor"`
	Offset    float64 `json:"offset"`
	ScrollTop float64 `json:"scrollTop"`
	Zoom      float64 `json:"zoom"`
	Wide      bool    `json:"wide"`
}

func DefaultNativeMarkdownReadingState() NativeMarkdownReadingState {
	return NativeMarkdownReadingState{Zoom: 1, Wide: true}
}

func (s NativeMarkdownReadingState) bounded() NativeMarkdownReadingState {
	s.Anchor = truncateUTF16(s.Anchor, 512)
	s.Offset = clampFinite(s.Offset, -100_000_000, 100_000_000, 0)
	s.ScrollTop = clampFinite(s.ScrollTop, 0, 100_000_000, 0)
	s.Zoom = clampFinite(s.Zoom, 0.7, 1.6, 1)
	// The native reader uses a wide layout. Old persisted false values are ignored.
	s.Wide = true
	return s
}

func (s *NativeMarkdownReadingState) UnmarshalJSON(data []byte) error {
	type wire NativeMarkdownReadingState
	value := wire(DefaultNativeMarkdownReadingState())
	if err := rejectNullFields(data, "anchor", "offset", "scrollTop", "zoom", "wide"); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = NativeMarkdownReadingState(value)
	return nil
}

// ReadingPositionSnapshot mirrors Source's per-document position DTO. The order
// slices retain LRU order through JSON even though callers can use ordinary maps.
type ReadingPositionSnapshot struct {
	Mode        string                     `json:"mode"`
	Zoom        int                        `json:"zoom"`
	Scrolls     map[string]int             `json:"scrolls"`
	Lists       map[string][]int           `json:"lists"`
	Chromium    NativeMarkdownReadingState `json:"chromium"`
	scrollOrder []string
	listOrder   []string
}

func DefaultReadingPositionSnapshot() ReadingPositionSnapshot {
	return ReadingPositionSnapshot{
		Mode: "RENDERED", Zoom: 100, Scrolls: map[string]int{}, Lists: map[string][]int{},
		Chromium: DefaultNativeMarkdownReadingState(),
	}
}

func (p *ReadingPositionSnapshot) SetScrollPosition(key string, value int) {
	if p.Scrolls == nil {
		p.Scrolls = make(map[string]int)
	}
	p.Scrolls[key] = value
	p.scrollOrder = moveKeyToEnd(p.scrollOrder, key)
}

func (p *ReadingPositionSnapshot) SetListPosition(key string, index, offset int) {
	if p.Lists == nil {
		p.Lists = make(map[string][]int)
	}
	p.Lists[key] = []int{index, offset}
	p.listOrder = moveKeyToEnd(p.listOrder, key)
}

func (p ReadingPositionSnapshot) bounded() ReadingPositionSnapshot {
	if p.Mode != "RENDERED" && p.Mode != "SOURCE" {
		p.Mode = "RENDERED"
	}
	p.Zoom = clampInt(p.Zoom, 25, 200)
	p.Scrolls, p.scrollOrder = boundedOrderedMap(p.Scrolls, p.scrollOrder, maxReadingPositions, func(key string) bool {
		return key != "" && utf16Length(key) <= 64
	}, func(value int) int { return clampInt(value, 0, 100_000_000) })
	p.Lists, p.listOrder = boundedOrderedMap(p.Lists, p.listOrder, maxReadingPositions, func(key string) bool {
		return key != "" && utf16Length(key) <= 64
	}, func(value []int) []int {
		index, offset := 0, 0
		if len(value) > 0 {
			index = value[0]
		}
		if len(value) > 1 {
			offset = value[1]
		}
		return []int{clampInt(index, 0, 1_000_000), clampInt(offset, 0, 100_000_000)}
	})
	p.Chromium = p.Chromium.bounded()
	return p
}

func (p *ReadingPositionSnapshot) UnmarshalJSON(data []byte) error {
	type wire ReadingPositionSnapshot
	value := wire(DefaultReadingPositionSnapshot())
	if err := rejectNullFields(data, "mode", "zoom", "scrolls", "lists", "chromium"); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = ReadingPositionSnapshot(value)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.scrollOrder = jsonObjectKeys(raw["scrolls"])
	p.listOrder = jsonObjectKeys(raw["lists"])
	return nil
}

func (p ReadingPositionSnapshot) MarshalJSON() ([]byte, error) {
	scrolls, err := marshalOrderedObject(p.Scrolls, p.scrollOrder)
	if err != nil {
		return nil, err
	}
	lists, err := marshalOrderedObject(p.Lists, p.listOrder)
	if err != nil {
		return nil, err
	}
	type wire struct {
		Mode     string                     `json:"mode"`
		Zoom     int                        `json:"zoom"`
		Scrolls  json.RawMessage            `json:"scrolls"`
		Lists    json.RawMessage            `json:"lists"`
		Chromium NativeMarkdownReadingState `json:"chromium"`
	}
	return json.Marshal(wire{p.Mode, p.Zoom, scrolls, lists, p.Chromium})
}

type MaterialsDocumentTab struct {
	Path     string `json:"path"`
	IsPinned bool   `json:"isPinned"`
}

type MaterialsDocumentTabsSnapshot struct {
	Tabs        []MaterialsDocumentTab `json:"tabs"`
	ActivePath  *string                `json:"activePath"`
	Initialized bool                   `json:"initialized"`
}

type MaterialsSnapshot struct {
	TaskPath              string                             `json:"taskPath"`
	Root                  *string                            `json:"root"`
	SelectedPath          *string                            `json:"selectedPath"`
	ExpandedDirectories   []string                           `json:"expandedDirectories"`
	ExpansionInitialized  bool                               `json:"expansionInitialized"`
	DirectoryCollapsed    bool                               `json:"directoryCollapsed"`
	CompactShowingPreview bool                               `json:"compactShowingPreview"`
	DirectoryPosition     ReadingPositionSnapshot            `json:"directoryPosition"`
	Files                 map[string]ReadingPositionSnapshot `json:"files"`
	DocumentTabs          MaterialsDocumentTabsSnapshot      `json:"documentTabs"`
	SortOrder             string                             `json:"sortOrder"`
	fileOrder             []string
}

func DefaultMaterialsSnapshot() MaterialsSnapshot {
	return MaterialsSnapshot{
		ExpandedDirectories: []string{}, DirectoryPosition: DefaultReadingPositionSnapshot(),
		Files: map[string]ReadingPositionSnapshot{}, DocumentTabs: MaterialsDocumentTabsSnapshot{Tabs: []MaterialsDocumentTab{}},
		SortOrder: "NAME",
	}
}

func (m *MaterialsSnapshot) SetFileReading(path string, value ReadingPositionSnapshot) {
	if m.Files == nil {
		m.Files = make(map[string]ReadingPositionSnapshot)
	}
	m.Files[path] = value
	m.fileOrder = moveKeyToEnd(m.fileOrder, path)
}

func (m *MaterialsSnapshot) UnmarshalJSON(data []byte) error {
	type wire struct {
		TaskPath              string                             `json:"taskPath"`
		Root                  *string                            `json:"root"`
		SelectedPath          *string                            `json:"selectedPath"`
		ExpandedDirectories   []string                           `json:"expandedDirectories"`
		ExpansionInitialized  bool                               `json:"expansionInitialized"`
		DirectoryCollapsed    bool                               `json:"directoryCollapsed"`
		CompactShowingPreview bool                               `json:"compactShowingPreview"`
		DirectoryPosition     ReadingPositionSnapshot            `json:"directoryPosition"`
		Files                 map[string]ReadingPositionSnapshot `json:"files"`
		DocumentTabs          MaterialsDocumentTabsSnapshot      `json:"documentTabs"`
		SortOrder             string                             `json:"sortOrder"`
	}
	defaults := DefaultMaterialsSnapshot()
	value := wire{
		ExpandedDirectories: defaults.ExpandedDirectories, DirectoryPosition: defaults.DirectoryPosition,
		Files: defaults.Files, DocumentTabs: defaults.DocumentTabs, SortOrder: defaults.SortOrder,
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var required map[string]json.RawMessage
	if err := json.Unmarshal(data, &required); err != nil {
		return err
	}
	if !hasNonNullJSONField(required, "taskPath") {
		return errors.New("materials reading snapshot is missing taskPath")
	}
	*m = MaterialsSnapshot{
		TaskPath: value.TaskPath, Root: value.Root, SelectedPath: value.SelectedPath,
		ExpandedDirectories: value.ExpandedDirectories, ExpansionInitialized: value.ExpansionInitialized,
		DirectoryCollapsed: value.DirectoryCollapsed, CompactShowingPreview: value.CompactShowingPreview,
		DirectoryPosition: value.DirectoryPosition, Files: value.Files, DocumentTabs: value.DocumentTabs,
		SortOrder: value.SortOrder,
	}
	if m.Files == nil {
		m.Files = map[string]ReadingPositionSnapshot{}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.fileOrder = jsonObjectKeys(raw["files"])
	return nil
}

func (m MaterialsSnapshot) MarshalJSON() ([]byte, error) {
	files, err := marshalOrderedObject(m.Files, m.fileOrder)
	if err != nil {
		return nil, err
	}
	type wire struct {
		TaskPath              string                        `json:"taskPath"`
		Root                  *string                       `json:"root"`
		SelectedPath          *string                       `json:"selectedPath"`
		ExpandedDirectories   []string                      `json:"expandedDirectories"`
		ExpansionInitialized  bool                          `json:"expansionInitialized"`
		DirectoryCollapsed    bool                          `json:"directoryCollapsed"`
		CompactShowingPreview bool                          `json:"compactShowingPreview"`
		DirectoryPosition     ReadingPositionSnapshot       `json:"directoryPosition"`
		Files                 json.RawMessage               `json:"files"`
		DocumentTabs          MaterialsDocumentTabsSnapshot `json:"documentTabs"`
		SortOrder             string                        `json:"sortOrder"`
	}
	return json.Marshal(wire{
		m.TaskPath, m.Root, m.SelectedPath, m.ExpandedDirectories, m.ExpansionInitialized,
		m.DirectoryCollapsed, m.CompactShowingPreview, m.DirectoryPosition, files, m.DocumentTabs, m.SortOrder,
	})
}

type RequirementReadingSnapshot struct {
	TaskPath string                  `json:"taskPath"`
	Link     string                  `json:"link"`
	Reading  ReadingPositionSnapshot `json:"reading"`
}

func (r *RequirementReadingSnapshot) UnmarshalJSON(data []byte) error {
	type wire RequirementReadingSnapshot
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"taskPath", "link", "reading"} {
		if !hasNonNullJSONField(fields, key) {
			return fmt.Errorf("requirement reading snapshot is missing %s", key)
		}
	}
	*r = RequirementReadingSnapshot(value)
	return nil
}

type ReadingSnapshot struct {
	Schema             int                          `json:"schema"`
	LastTaskPath       string                       `json:"lastTaskPath,omitempty"`
	View               string                       `json:"view"`
	Materials          []MaterialsSnapshot          `json:"materials"`
	Requirements       []RequirementReadingSnapshot `json:"requirements"`
	SavedAtEpochMillis int64                        `json:"savedAtEpochMillis"`
}

func EmptyReadingSnapshot() ReadingSnapshot {
	return ReadingSnapshot{Schema: 1, View: "DETAIL", Materials: []MaterialsSnapshot{}, Requirements: []RequirementReadingSnapshot{}}
}

func (s *ReadingSnapshot) UnmarshalJSON(data []byte) error {
	type wire ReadingSnapshot
	value := wire(EmptyReadingSnapshot())
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*s = ReadingSnapshot(value)
	if s.Materials == nil {
		s.Materials = []MaterialsSnapshot{}
	}
	if s.Requirements == nil {
		s.Requirements = []RequirementReadingSnapshot{}
	}
	return nil
}

func (s ReadingSnapshot) Bounded() ReadingSnapshot {
	if s.Schema != 1 {
		return EmptyReadingSnapshot()
	}
	if !validReadingPath(s.LastTaskPath) {
		s.LastTaskPath = ""
	}
	if !validTaskContentView(s.View) {
		s.View = "DETAIL"
	}
	activeKey := ""
	if s.LastTaskPath != "" {
		activeKey = normalizedReadingPath(s.LastTaskPath)
	}

	materialsByTask := make(map[string]MaterialsSnapshot)
	materialOrder := make([]string, 0, len(s.Materials))
	for _, record := range s.Materials {
		bounded, ok := record.bounded()
		if !ok {
			continue
		}
		key := bounded.TaskPath
		materialOrder = moveKeyToEnd(materialOrder, key)
		materialsByTask[key] = bounded
	}
	materialOrder = prioritizeLast(materialOrder, activeKey)
	if len(materialOrder) > maxReadingTasks {
		materialOrder = materialOrder[len(materialOrder)-maxReadingTasks:]
	}
	s.Materials = make([]MaterialsSnapshot, 0, len(materialOrder))
	for _, key := range materialOrder {
		s.Materials = append(s.Materials, materialsByTask[key])
	}

	requirementsByTask := make(map[string]RequirementReadingSnapshot)
	requirementOrder := make([]string, 0, len(s.Requirements))
	for _, record := range s.Requirements {
		if !validReadingPath(record.TaskPath) || strings.TrimSpace(record.Link) == "" || utf16Length(record.Link) > 8192 {
			continue
		}
		record.TaskPath = normalizedReadingPath(record.TaskPath)
		record.Link = strings.TrimSpace(record.Link)
		record.Reading = record.Reading.bounded()
		requirementOrder = moveKeyToEnd(requirementOrder, record.TaskPath)
		requirementsByTask[record.TaskPath] = record
	}
	requirementOrder = prioritizeLast(requirementOrder, activeKey)
	if len(requirementOrder) > maxReadingTasks {
		requirementOrder = requirementOrder[len(requirementOrder)-maxReadingTasks:]
	}
	s.Requirements = make([]RequirementReadingSnapshot, 0, len(requirementOrder))
	for _, key := range requirementOrder {
		s.Requirements = append(s.Requirements, requirementsByTask[key])
	}
	return s
}

func (m MaterialsSnapshot) bounded() (MaterialsSnapshot, bool) {
	if !validReadingPath(m.TaskPath) {
		return MaterialsSnapshot{}, false
	}
	if m.Root != nil && strings.TrimSpace(*m.Root) != "" && !validReadingPath(*m.Root) {
		return MaterialsSnapshot{}, false
	}
	m.TaskPath = normalizedReadingPath(m.TaskPath)
	if m.Root != nil {
		if strings.TrimSpace(*m.Root) == "" {
			m.Root = nil
		} else {
			root := normalizedReadingPath(*m.Root)
			m.Root = &root
		}
	}
	if m.SelectedPath != nil {
		if path, ok := validReadingRelativePath(*m.SelectedPath); ok {
			m.SelectedPath = &path
		} else {
			m.SelectedPath = nil
		}
	}
	expanded := make([]string, 0, readingMinInt(len(m.ExpandedDirectories), 500))
	seenDirectories := make(map[string]bool)
	for _, candidate := range m.ExpandedDirectories {
		if path, ok := validReadingRelativePath(candidate); ok && !seenDirectories[path] {
			seenDirectories[path] = true
			expanded = append(expanded, path)
			if len(expanded) == 500 {
				break
			}
		}
	}
	m.ExpandedDirectories = expanded
	m.DirectoryPosition = m.DirectoryPosition.bounded()
	originalFiles := m.Files
	originalOrder := m.fileOrder
	allFiles := make(map[string]ReadingPositionSnapshot, len(originalFiles))
	allOrder := make([]string, 0, len(originalFiles))
	for _, key := range orderedMapKeys(originalFiles, originalOrder) {
		path, ok := validReadingRelativePath(key)
		if !ok {
			continue
		}
		allFiles[path] = originalFiles[key].bounded()
		allOrder = moveKeyToEnd(allOrder, path)
	}
	m.Files, m.fileOrder = boundedOrderedMap(allFiles, allOrder, maxReadingFilesPerTask,
		func(key string) bool { return true }, func(value ReadingPositionSnapshot) ReadingPositionSnapshot { return value })
	if m.SelectedPath != nil {
		selected := *m.SelectedPath
		if reading, exists := allFiles[selected]; exists {
			if _, retained := m.Files[selected]; !retained {
				keys := orderedMapKeys(m.Files, m.fileOrder)
				if len(keys) >= maxReadingFilesPerTask {
					keys = keys[1:]
				}
				keys = append(keys, selected)
				m.Files = make(map[string]ReadingPositionSnapshot, len(keys))
				for _, key := range keys {
					if key == selected {
						m.Files[key] = reading
					} else {
						m.Files[key] = allFiles[key]
					}
				}
				m.fileOrder = keys
			}
		}
	}
	m.DocumentTabs = m.DocumentTabs.bounded()
	if m.SortOrder != "NAME" && m.SortOrder != "MODIFIED_DESC" {
		m.SortOrder = "NAME"
	}
	return m, true
}

func (t MaterialsDocumentTabsSnapshot) bounded() MaterialsDocumentTabsSnapshot {
	merged := make([]MaterialsDocumentTab, 0, len(t.Tabs))
	indexByPath := make(map[string]int)
	for _, tab := range t.Tabs {
		path, ok := validReadingRelativePath(tab.Path)
		if !ok {
			continue
		}
		if index, exists := indexByPath[path]; exists {
			merged[index].IsPinned = merged[index].IsPinned || tab.IsPinned
			continue
		}
		indexByPath[path] = len(merged)
		merged = append(merged, MaterialsDocumentTab{Path: path, IsPinned: tab.IsPinned})
	}
	preview := ""
	for _, tab := range merged {
		if !tab.IsPinned {
			preview = tab.Path
		}
	}
	valid := make([]MaterialsDocumentTab, 0, len(merged))
	for _, tab := range merged {
		if tab.IsPinned || tab.Path == preview {
			valid = append(valid, tab)
		}
	}
	requestedActive := ""
	if t.ActivePath != nil {
		if path, ok := validReadingRelativePath(*t.ActivePath); ok {
			for _, tab := range valid {
				if tab.Path == path {
					requestedActive = path
					break
				}
			}
		}
	}
	limited := valid
	if len(limited) > maxReadingFilesPerTask {
		limited = append([]MaterialsDocumentTab(nil), limited[len(limited)-maxReadingFilesPerTask:]...)
	}
	if requestedActive != "" && !containsTabPath(limited, requestedActive) {
		if len(limited) >= maxReadingFilesPerTask {
			limited = limited[1:]
		}
		for _, tab := range valid {
			if tab.Path == requestedActive {
				limited = append([]MaterialsDocumentTab{tab}, limited...)
				break
			}
		}
	}
	active := requestedActive
	if active == "" && len(limited) > 0 {
		active = limited[len(limited)-1].Path
	}
	t.ActivePath = nil
	if active != "" {
		t.ActivePath = &active
	}
	t.Tabs = limited
	t.Initialized = t.Initialized || len(limited) > 0
	if t.Tabs == nil {
		t.Tabs = []MaterialsDocumentTab{}
	}
	return t
}

// ReadingStateStore persists the source-compatible bounded reading cache. Its
// debounce is deliberately owned here so callers can schedule snapshots without
// adding timers or revision counters to the desktop app state.
type ReadingStateStore struct {
	path     string
	mu       sync.Mutex
	writeMu  sync.Mutex
	revision uint64
	timer    *time.Timer
	closed   bool
	lastErr  error
}

func NewReadingStateStore(path string) *ReadingStateStore {
	return &ReadingStateStore{path: path}
}

// Load mirrors Source's tolerant startup policy: unreadable, malformed,
// unsupported, oversized, or stale caches produce an empty snapshot.
func (s *ReadingStateStore) Load() ReadingSnapshot {
	value, _ := s.LoadWithError()
	return value
}

// LoadWithError exposes diagnostics while retaining Source's empty-state
// behavior for expected cache invalidation cases.
func (s *ReadingStateStore) LoadWithError() (ReadingSnapshot, error) {
	empty := EmptyReadingSnapshot()
	if strings.TrimSpace(s.path) == "" {
		return empty, errors.New("reading state path is empty")
	}
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, fmt.Errorf("stat reading cache: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxReadingCacheBytes {
		return empty, nil
	}
	file, err := os.Open(s.path)
	if err != nil {
		return empty, fmt.Errorf("open reading cache: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxReadingCacheBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return empty, fmt.Errorf("read reading cache: %w", readErr)
	}
	if closeErr != nil {
		return empty, fmt.Errorf("close reading cache: %w", closeErr)
	}
	if len(content) > maxReadingCacheBytes {
		return empty, nil
	}
	var saved ReadingSnapshot
	if err := json.Unmarshal(content, &saved); err != nil {
		return empty, fmt.Errorf("decode reading cache: %w", err)
	}
	if saved.Schema != 1 {
		return empty, nil
	}
	writtenAt := saved.SavedAtEpochMillis
	if writtenAt == 0 {
		writtenAt = info.ModTime().UnixMilli()
	}
	now := time.Now().UnixMilli()
	if writtenAt < 0 || writtenAt < now-maxReadingCacheAge.Milliseconds() || writtenAt > now+int64(24*time.Hour/time.Millisecond) {
		return empty, nil
	}
	return saved.Bounded(), nil
}

// ReserveSaveRevision lets callers capture a revision before delaying a save.
// A later snapshot always wins over an older timer callback.
func (s *ReadingStateStore) ReserveSaveRevision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	return s.revision
}

// Save writes immediately and invalidates any pending older debounced snapshot.
func (s *ReadingStateStore) Save(value ReadingSnapshot) error {
	return s.Flush(value)
}

// SaveRevision atomically writes a previously reserved snapshot. Older
// revisions are discarded without touching the current file.
func (s *ReadingStateStore) SaveRevision(value ReadingSnapshot, revision uint64) error {
	return s.writeRevision(value, revision)
}

// ScheduleSave records a new snapshot and writes it after Source's 600ms idle
// interval. Background write failures are available from LastError.
func (s *ReadingStateStore) ScheduleSave(value ReadingSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrReadingStateStoreClosed
	}
	s.revision++
	revision := s.revision
	if s.timer != nil {
		s.timer.Stop()
	}
	// The caller keeps editing its live maps, tabs and reading positions.
	// Own a bounded deep snapshot before handing it to the delayed writer.
	snapshot := value.Bounded()
	s.timer = time.AfterFunc(readingSaveDebounce, func() {
		s.mu.Lock()
		if revision == s.revision {
			s.timer = nil
		}
		s.mu.Unlock()
		err := s.writeRevision(snapshot, revision)
		s.mu.Lock()
		if revision == s.revision {
			s.lastErr = err
		}
		s.mu.Unlock()
	})
	return nil
}

// Flush cancels pending debounce work and synchronously writes the supplied
// final snapshot before returning.
func (s *ReadingStateStore) Flush(final ReadingSnapshot) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrReadingStateStoreClosed
	}
	s.revision++
	revision := s.revision
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	err := s.writeRevision(final, revision)
	s.recordError(revision, err)
	return err
}

// Close cancels delayed writes and persists the final state exactly once.
func (s *ReadingStateStore) Close(final ReadingSnapshot) error {
	s.mu.Lock()
	if s.closed {
		err := s.lastErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	s.revision++
	revision := s.revision
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	err := s.writeRevision(final, revision)
	s.recordError(revision, err)
	return err
}

// CancelPending invalidates any timer callback without writing its snapshot.
func (s *ReadingStateStore) CancelPending() {
	s.mu.Lock()
	s.revision++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
}

func (s *ReadingStateStore) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *ReadingStateStore) recordError(revision uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision == s.revision {
		s.lastErr = err
	}
}

func (s *ReadingStateStore) writeRevision(value ReadingSnapshot, revision uint64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	stale := revision < s.revision
	s.mu.Unlock()
	if stale {
		return nil
	}
	if strings.TrimSpace(s.path) == "" {
		return errors.New("reading state path is empty")
	}
	value = value.Bounded()
	value.SavedAtEpochMillis = time.Now().UnixMilli()
	var content []byte
	for {
		var err error
		content, err = json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode reading cache: %w", err)
		}
		if len(content) <= maxReadingCacheBytes {
			break
		}
		next := value.trimOldestReading()
		if len(next.Materials) == len(value.Materials) && len(next.Requirements) == len(value.Requirements) &&
			readingContentEquivalent(next, value) {
			return fmt.Errorf("reading cache cannot be reduced below %d bytes", maxReadingCacheBytes)
		}
		value = next
	}
	s.mu.Lock()
	stale = revision < s.revision
	s.mu.Unlock()
	if stale {
		return nil
	}
	if err := atomicWriteFile(s.path, content, 0o600); err != nil {
		return fmt.Errorf("save reading cache: %w", err)
	}
	return nil
}

func (s ReadingSnapshot) trimOldestReading() ReadingSnapshot {
	s = s.Bounded()
	switch {
	case len(s.Materials) > 1:
		count := readingMaxInt(1, len(s.Materials)/2)
		s.Materials = append([]MaterialsSnapshot(nil), s.Materials[len(s.Materials)-count:]...)
	case len(s.Requirements) > 1:
		count := readingMaxInt(1, len(s.Requirements)/2)
		s.Requirements = append([]RequirementReadingSnapshot(nil), s.Requirements[len(s.Requirements)-count:]...)
	case len(s.Materials) > 0 && len(s.Materials[0].Files) > 1:
		record := s.Materials[0]
		keys := orderedMapKeys(record.Files, record.fileOrder)
		keepCount := len(keys) / 2
		keep := make([]string, 0, keepCount+1)
		selected := ""
		if record.SelectedPath != nil {
			selected = *record.SelectedPath
		}
		for _, key := range keys {
			if key != selected {
				keep = append(keep, key)
			}
		}
		if len(keep) > keepCount {
			keep = keep[len(keep)-keepCount:]
		}
		if selected != "" {
			if _, exists := record.Files[selected]; exists {
				keep = append(keep, selected)
			}
		}
		newFiles := make(map[string]ReadingPositionSnapshot, len(keep))
		for _, key := range keep {
			newFiles[key] = record.Files[key]
		}
		record.Files, record.fileOrder = newFiles, keep
		s.Materials[0] = record
	case len(s.Materials) > 0 && len(s.Materials[0].ExpandedDirectories) > 1:
		record := s.Materials[0]
		count := len(record.ExpandedDirectories) / 2
		record.ExpandedDirectories = append([]string(nil), record.ExpandedDirectories[len(record.ExpandedDirectories)-count:]...)
		s.Materials[0] = record
	case len(s.Materials) > 0:
		s.Materials = []MaterialsSnapshot{}
	case len(s.Requirements) > 0:
		s.Requirements = []RequirementReadingSnapshot{}
	default:
		s.Materials = []MaterialsSnapshot{}
		s.Requirements = []RequirementReadingSnapshot{}
	}
	return s
}

func readingContentEquivalent(left, right ReadingSnapshot) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func normalizedReadingPath(value string) string {
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		absolute = filepath.Clean(value)
	}
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute
}

func validReadingPath(value string) bool {
	return strings.TrimSpace(value) != "" && utf16Length(value) <= 4096 && !strings.ContainsRune(value, '\x00')
}

func validReadingRelativePath(value string) (string, bool) {
	if value == "" || utf16Length(value) > 4096 || strings.ContainsRune(value, '\x00') {
		return "", false
	}
	path := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(path, "/") || hasWindowsDrivePrefix(path) {
		return "", false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return path, true
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':'
}

func validTaskContentView(value string) bool {
	switch value {
	case "DETAIL", "MATERIALS", "REQUIREMENT", "NOTES", "TAG":
		return true
	default:
		return false
	}
}

func boundedOrderedMap[V any](values map[string]V, order []string, limit int, valid func(string) bool, bound func(V) V) (map[string]V, []string) {
	if len(values) == 0 {
		return map[string]V{}, []string{}
	}
	keys := orderedMapKeys(values, order)
	filtered := make(map[string]V, len(values))
	filteredOrder := make([]string, 0, readingMinInt(len(keys), limit))
	for _, key := range keys {
		if !valid(key) {
			continue
		}
		filtered[key] = bound(values[key])
		filteredOrder = append(filteredOrder, key)
	}
	if len(filteredOrder) > limit {
		filteredOrder = filteredOrder[len(filteredOrder)-limit:]
	}
	bounded := make(map[string]V, len(filteredOrder))
	for _, key := range filteredOrder {
		bounded[key] = filtered[key]
	}
	return bounded, filteredOrder
}

func orderedMapKeys[V any](values map[string]V, order []string) []string {
	keys := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, key := range order {
		if _, exists := values[key]; exists && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	remaining := make([]string, 0, len(values)-len(keys))
	for key := range values {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	return append(keys, remaining...)
}

func marshalOrderedObject[V any](values map[string]V, order []string) (json.RawMessage, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range orderedMapKeys(values, order) {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := json.Marshal(values[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return json.RawMessage(buffer.Bytes()), nil
}

func jsonObjectKeys(raw json.RawMessage) []string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return []string{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return []string{}
	}
	keys := make([]string, 0)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return []string{}
		}
		key, ok := token.(string)
		if !ok {
			return []string{}
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			return []string{}
		}
		keys = append(keys, key)
	}
	return keys
}

func rejectNullFields(data []byte, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range keys {
		if value, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("field %s cannot be null", key)
		}
	}
	return nil
}

func hasNonNullJSONField(fields map[string]json.RawMessage, key string) bool {
	value, exists := fields[key]
	return exists && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func moveKeyToEnd(keys []string, key string) []string {
	if key == "" {
		return keys
	}
	result := make([]string, 0, len(keys)+1)
	for _, existing := range keys {
		if existing != key {
			result = append(result, existing)
		}
	}
	return append(result, key)
}

func prioritizeLast(keys []string, active string) []string {
	if active == "" {
		return keys
	}
	for _, key := range keys {
		if key == active {
			return moveKeyToEnd(keys, active)
		}
	}
	return keys
}

func containsTabPath(tabs []MaterialsDocumentTab, path string) bool {
	for _, tab := range tabs {
		if tab.Path == path {
			return true
		}
	}
	return false
}

func clampFinite(value, minimum, maximum, fallback float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return math.Max(minimum, math.Min(maximum, value))
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func truncateUTF16(value string, maximum int) string {
	encoded := utf16.Encode([]rune(value))
	if len(encoded) > maximum {
		encoded = encoded[:maximum]
	}
	return string(utf16.Decode(encoded))
}

func readingMinInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func readingMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
