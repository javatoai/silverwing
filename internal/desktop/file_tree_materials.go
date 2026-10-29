package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) materialsTreeRows(projection materialsDocumentProjection, order string) []FileTreeRow {
	projected := projection.treeRows(order)
	rows := make([]FileTreeRow, 0, len(projected))
	for _, row := range projected {
		entry := row.entry
		rows = append(rows, FileTreeRow{ID: entry.Key, Name: row.name, Path: entry.Path, Depth: row.depth, Directory: entry.Directory, ReadOnly: !s.materialsWritable(entry.Path, true)})
	}
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
	for _, tab := range s.materialsUnit.state.documentTabs {
		if tab == path && tab != previousPreview {
			pinned = true
			break
		}
	}
	s.selectDocument(path)
	if s.materialsUnit.state.documentPath != path {
		return
	}
	if previousPreview != "" && previousPreview != path && !s.materialsUnit.state.documentDirtyMap[previousPreview] {
		tabs := s.materialsUnit.state.documentTabs[:0]
		for _, tab := range s.materialsUnit.state.documentTabs {
			if tab != previousPreview {
				tabs = append(tabs, tab)
			}
		}
		s.materialsUnit.state.documentTabs = tabs
	}
	state.previewPath = ""
	if !pin && !pinned {
		state.previewPath = path
	}
}

func (s *app) materialsContextSelection(state *materialsFileManagementState, entry materialsFileSelectionEntry) []materialsFileSelectionEntry {
	return state.materialsSelection.contextEntries(entry)
}

// Context commands capture the clicked item's scope without changing the
// user's selection or active document merely by opening a menu.
func (s *app) withMaterialsContextSelection(state *materialsFileManagementState, entry materialsFileSelectionEntry, action func()) {
	previous := state.materialsSelection
	state.materialsSelection = previous.forContext(entry)
	defer func() { state.materialsSelection = previous }()
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
