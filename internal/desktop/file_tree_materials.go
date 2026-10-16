package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) materialsTreeRows(documents []application.Document) []FileTreeRow {
	rows := make([]FileTreeRow, 0, countDocuments(documents))
	var visit func([]application.Document, int)
	visit = func(items []application.Document, depth int) {
		for _, document := range items {
			entry, ok := s.materialsEntryForDocument(document)
			if !ok {
				continue
			}
			rows = append(rows, FileTreeRow{ID: entry.Key, Name: document.Name, Path: entry.Path, Depth: depth, Directory: entry.Directory})
			visit(document.Children, depth+1)
		}
	}
	visit(documents, 0)
	return rows
}

func (s *app) materialsTreeInput(state *materialsFileManagementState, input FileTreeInput) {
	if input.Kind == FileTreeDrop {
		directory := s.materialsRelativePath(input.Row.Path)
		if !input.Row.Directory {
			directory = filepath.ToSlash(filepath.Dir(filepath.FromSlash(directory)))
			if directory == "." {
				directory = ""
			}
		}
		s.startMaterialsImport(input.Files, "document.drop", directory)
		return
	}
	document := application.Document{Name: input.Row.Name, Path: input.Row.Path}
	if input.Row.Directory {
		document.Kind = "directory"
	}
	entry, ok := s.materialsEntryForDocument(document)
	if !ok {
		return
	}
	switch input.Kind {
	case FileTreeToggle:
		s.setReadingDirectoryExpanded(entry.Relative, !s.readingExpandedDirectories()[entry.Relative])
	case FileTreeContext:
		s.selectMaterialsEntry(state, document, 0, true)
	case FileTreeSelect:
		s.selectMaterialsEntry(state, document, input.Modifiers, false)
	case FileTreeActivate:
		s.selectMaterialsEntry(state, document, input.Modifiers, false)
		if input.Modifiers&(ui.Ctrl|ui.Super|ui.Shift) != 0 {
			return
		}
		if entry.Directory {
			if input.ClickCount == 1 {
				s.setReadingDirectoryExpanded(entry.Relative, !s.readingExpandedDirectories()[entry.Relative])
			}
			return
		}
		s.openMaterialsTreeFile(state, entry.Path, input.ClickCount > 1)
	}
}

func (s *app) openMaterialsTreeFile(state *materialsFileManagementState, path string, pin bool) {
	previousPreview := state.previewPath
	pinned := false
	for _, tab := range s.documentTabs {
		if tab == path && tab != previousPreview {
			pinned = true
			break
		}
	}
	s.selectDocument(path)
	if s.documentPath != path {
		return
	}
	if previousPreview != "" && previousPreview != path && !s.documentDirtyMap[previousPreview] {
		tabs := s.documentTabs[:0]
		for _, tab := range s.documentTabs {
			if tab != previousPreview {
				tabs = append(tabs, tab)
			}
		}
		s.documentTabs = tabs
	}
	state.previewPath = ""
	if !pin && !pinned {
		state.previewPath = path
	}
}

func (s *app) materialsContextSelection(state *materialsFileManagementState, entry materialsFileSelectionEntry) []materialsFileSelectionEntry {
	if _, selected := state.selected[entry.Key]; selected {
		return s.materialsFileSelection(state)
	}
	return []materialsFileSelectionEntry{entry}
}

// Context commands capture the clicked item's scope without changing the
// user's selection or active document merely by opening a menu.
func (s *app) withMaterialsContextSelection(state *materialsFileManagementState, entry materialsFileSelectionEntry, action func()) {
	selected, anchor := state.selected, state.anchor
	if _, contains := selected[entry.Key]; !contains {
		state.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
		state.anchor = entry.Key
	}
	defer func() { state.selected, state.anchor = selected, anchor }()
	action()
}

func fileTreeIconButton(c *ui.Context, p palette, key, image, title string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(22, 22).Radius(3).Center().Label(title).Tooltip(title)
	if b.Hovered() {
		b.Background(p.blue.Alpha(.075))
	}
	b.Children(func() { icon(c, image, 14, p.muted) })
	return b
}
