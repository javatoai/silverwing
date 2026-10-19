package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func readingObservationFixture(tb testing.TB) *app {
	tb.Helper()
	s := taskNavigationFixture()
	root := tb.TempDir()
	s.tasks[0].path = filepath.Join(root, "current")
	s.tasks[0].materialsPath = filepath.Join(s.tasks[0].path, "materials")
	s.tasks[1].path = filepath.Join(root, "other")
	s.tab, s.documentsLoaded = 1, true
	s.documentPath = filepath.Join(s.tasks[0].materialsPath, "current.md")
	s.documentTabs = []string{s.documentPath}
	s.readingSession = newMemoryReadingSession(root)
	for i := 0; i < 20; i++ {
		record := DefaultMaterialsSnapshot()
		record.TaskPath = filepath.Join(root, fmt.Sprintf("history-%d", i))
		for file := 0; file < 20; file++ {
			position := DefaultReadingPositionSnapshot()
			position.SetScrollPosition("markdown", 42)
			record.SetFileReading(fmt.Sprintf("file-%d.md", file), position)
		}
		s.readingSession.cache.Materials = append(s.readingSession.cache.Materials, record)
	}
	s.readingSession.cache = s.readingSession.cache.Bounded()
	s.readingSession.store = NewReadingStateStore(filepath.Join(root, "reading-state.json"))
	tb.Cleanup(func() { _ = s.closeReadingSession() })
	s.observeReadingSession()
	return s
}

func TestReadingObservationSkipsUnchangedHistory(t *testing.T) {
	s := readingObservationFixture(t)
	s.setReadingZoom(125)
	read := func() {
		_ = s.readingMode()
		_ = s.readingZoom()
		_ = s.nativeMarkdownReadingState()
		s.observeReadingSession()
	}
	if allocations := testing.AllocsPerRun(20, read); allocations > 64 {
		t.Fatalf("unchanged reading observation allocated %.0f objects for historical documents", allocations)
	}
}

func TestReadingQueriesDoNotMutateHistoricalState(t *testing.T) {
	s := readingObservationFixture(t)
	before, _ := json.Marshal(s.readingSession.cache)
	historical := s.readingSession.cache.Materials[0]
	readingMaterialsFor(s.readingSession, historical.TaskPath, readingRootValue(historical.Root), false)
	_, _ = readingPositionForBinding(s.readingSession, readingScrollBinding{
		taskPath: historical.TaskPath, document: "file-0.md", id: "markdown",
	}, false)
	after, _ := json.Marshal(s.readingSession.cache)
	if string(before) != string(after) || s.readingSession.dirty {
		t.Fatal("read-only reading queries reordered or dirtied historical records")
	}
}

func TestReadingObservationPersistsUIAndExplicitChanges(t *testing.T) {
	s := readingObservationFixture(t)
	session := s.readingSession
	previous := string(session.lastObserved)
	check := func(label string) {
		t.Helper()
		s.observeReadingSession()
		if string(session.lastObserved) == previous {
			t.Fatalf("%s was not observed", label)
		}
		previous = string(session.lastObserved)
	}
	binding := readingScrollBinding{
		taskPath: normalizedReadingPath(s.tasks[0].path), root: normalizedReadingRoot(s.tasks[0].materialsPath),
		document: "current.md", id: "markdown", state: &ui.ScrollState{Y: 100},
	}
	session.scrollStates[readingScrollBindingKey(binding)] = binding
	check("new scroll binding")
	binding.state.Y = 250
	check("native scrolling")
	s.documentTabs = append(s.documentTabs, filepath.Join(s.tasks[0].materialsPath, "second.md"))
	check("document tabs")
	s.documentsCollapsed = true
	check("directory collapse")
	s.setReadingZoom(125)
	if string(session.lastObserved) == previous || s.readingZoom() != 125 {
		t.Fatal("explicit reading settings were not observed")
	}
	previous = string(session.lastObserved)
	s.setReadingDirectoryExpanded("folder", true)
	if string(session.lastObserved) == previous {
		t.Fatal("directory expansion was not observed")
	}
	previous = string(session.lastObserved)
	s.remapMaterialsReading(s.tasks[0].path, s.tasks[0].materialsPath, "folder", "renamed")
	check("renamed reading paths")
	s.tab = 2
	s.tasks[0].requirementURL = "https://project.feishu.cn/demo/userstory/detail/123"
	check("requirement view")
	s.setReadingMarkdownZoom(1.2)
	if s.readingMarkdownZoom() != 1.2 {
		t.Fatal("requirement reading position was not saved")
	}
	previous = string(session.lastObserved)
	s.selected = 1
	check("task selection")
	if session.cache.LastTaskPath != normalizedReadingPath(s.tasks[1].path) {
		t.Fatal("reading state retained the earlier task")
	}
	var encoded ReadingSnapshot
	if err := json.Unmarshal(session.lastObserved, &encoded); err != nil {
		t.Fatal(err)
	}
	if err := session.store.Flush(session.cache); err != nil {
		t.Fatal(err)
	}
	loaded, err := session.store.LoadWithError()
	if err != nil || loaded.LastTaskPath != encoded.LastTaskPath || loaded.View != encoded.View {
		t.Fatalf("persisted reading state differs: %v", err)
	}
}

func BenchmarkDesktopReadingObservation(b *testing.B) {
	s := readingObservationFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		s.observeReadingSession()
	}
}

func TestReadingObservationRetriesRejectedSave(t *testing.T) {
	s := readingObservationFixture(t)
	session := s.readingSession
	previous := string(session.lastObserved)
	session.store.mu.Lock()
	session.store.closed = true
	session.store.mu.Unlock()
	s.documentsCollapsed = true
	s.observeReadingSession()
	if s.lastError == "" || string(session.lastObserved) != previous {
		t.Fatal("a rejected reading save was acknowledged as persisted")
	}
	session.store.mu.Lock()
	session.store.closed = false
	session.store.mu.Unlock()
	s.observeReadingSession()
	if string(session.lastObserved) == previous {
		t.Fatal("a rejected save prevented a later retry")
	}
}

func TestReadingStoreScheduledSnapshotOwnsNestedValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reading.json")
	store := NewReadingStateStore(path)
	t.Cleanup(store.CancelPending)
	value := EmptyReadingSnapshot()
	record := DefaultMaterialsSnapshot()
	record.TaskPath = filepath.Join(root, "task")
	position := DefaultReadingPositionSnapshot()
	position.SetScrollPosition("markdown", 42)
	record.SetFileReading("original.md", position)
	record.DocumentTabs.Initialized = true
	record.DocumentTabs.Tabs = []MaterialsDocumentTab{{Path: "original.md", IsPinned: true}}
	value.Materials = []MaterialsSnapshot{record}
	if err := store.ScheduleSave(value); err != nil {
		t.Fatal(err)
	}
	// UI edits after scheduling must not alter a snapshot the writer owns.
	value.Materials[0].Files["original.md"].Scrolls["markdown"] = 999
	value.Materials[0].DocumentTabs.Tabs[0].Path = "unsaved.md"
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduled snapshot did not persist: %v", store.LastError())
		}
		time.Sleep(10 * time.Millisecond)
	}
	loaded, err := store.LoadWithError()
	if err != nil || len(loaded.Materials) != 1 || loaded.Materials[0].Files["original.md"].Scrolls["markdown"] != 42 ||
		loaded.Materials[0].DocumentTabs.Tabs[0].Path != "original.md" {
		t.Fatalf("scheduled reading snapshot shared later UI edits: %+v, %v", loaded, err)
	}
}
