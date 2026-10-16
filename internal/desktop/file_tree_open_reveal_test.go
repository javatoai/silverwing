package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterialsFileTreeOpeningRevealsOnlyNewDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "materials")
	s := &app{page: "tasks", tab: 1, documentsLoaded: true,
		tasks:          []task{{id: "reveal", path: filepath.Dir(root), materialsPath: root, materialsStatus: "READY"}},
		documentDrafts: map[string]string{}, documentDirtyMap: map[string]bool{}}
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
