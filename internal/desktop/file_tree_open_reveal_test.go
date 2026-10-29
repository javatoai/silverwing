package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterialsFileTreeOpeningRevealsOnlyNewDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "materials")
	s := &app{
		taskUnit: taskController{state: taskState{tasks: []task{{id: "reveal", path: filepath.Dir(root), materialsPath: root, materialsStatus: "READY"}}}},
		materialsUnit: materialsController{state: materialsState{documentsLoaded: true,
			documentDrafts:   map[string]string{},
			documentDirtyMap: map[string]bool{}}},
		page: "tasks",
		tab:  1,
	}
	s.readingSession = newMemoryReadingSession(filepath.Join(filepath.Dir(root), "home"))
	path := filepath.Join(root, "src", "nested", "query.sql")
	s.selectDocument(path)
	expanded := s.readingExpandedDirectories()
	if !expanded["src"] || !expanded["src/nested"] {
		t.Fatal("automatically opened document is hidden behind its collapsed ancestors")
	}
	s.setReadingDirectoryExpanded("src", false)
	s.selectDocument(path)
	if s.readingExpandedDirectories()["src"] {
		t.Fatal("revisiting the current document overrode the user's manual collapse")
	}
}

func TestCaptureReadingSessionDoesNotWriteBrowsingCache(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	s := &app{page: "tasks", tab: 1, readingSession: newMemoryReadingSession(home)}
	s.observeReadingSession()
	if err := s.closeReadingSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("capture created a persistent browsing directory", err)
	}
}
