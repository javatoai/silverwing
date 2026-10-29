package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

type materialsTreePermissionKey struct {
	permissions                 application.MaterialsPermissions
	legacy                      documents.MaterialsScope
	legacyScope, legacyReadOnly bool
}

func (key materialsTreePermissionKey) writable(path string) bool {
	if key.permissions.Kind != "" {
		return key.permissions.Writable(path, true)
	}
	if key.legacyScope {
		return key.legacy.Writable(path, true)
	}
	return !key.legacyReadOnly
}

func (s *app) materialsTreePermissions() materialsTreePermissionKey {
	key := materialsTreePermissionKey{permissions: s.taskUnit.currentTask().materials}
	if key.permissions.Kind != "" {
		return key
	}
	key.legacy, key.legacyScope = s.legacyMaterialsScope()
	if !key.legacyScope {
		root := s.taskUnit.currentTask().materialsPath
		scope, ok := documents.AnnualScope(filepath.Join(root, "__scope__"))
		key.legacyReadOnly = ok && scope.Root == filepath.Clean(root)
	}
	return key
}

type materialsTreePresentation struct {
	index           *materialsDocumentIndex
	order           int
	permissions     materialsTreePermissionKey
	rows            []FileTreeRow
	visibleRevision uint64
	prepared        *FileTreeProjection
}

func (v *materialsTreePresentation) prepare(index *materialsDocumentIndex, order string, permissions materialsTreePermissionKey, expanded map[string]bool, opened string) *FileTreeProjection {
	ordered := index.orderedRows(order)
	if v.index != index || v.order != materialsDocumentOrderIndex(order) || v.permissions != permissions {
		*v = materialsTreePresentation{index: index, order: materialsDocumentOrderIndex(order), permissions: permissions}
		v.rows = make([]FileTreeRow, 0, len(ordered.rows))
		for _, row := range ordered.rows {
			entry := row.entry
			v.rows = append(v.rows, FileTreeRow{ID: entry.Key, Name: row.name, Path: entry.Path, Depth: row.depth, Directory: entry.Directory, ReadOnly: !permissions.writable(entry.Path)})
		}
	}
	ordered.treeVisibility.project(ordered.rows, expanded, true)
	visibility := &ordered.treeVisibility
	if v.prepared != nil && v.visibleRevision == visibility.revision && v.prepared.opened == opened {
		return v.prepared
	}
	prepared := &FileTreeProjection{opened: opened, expanded: make(map[string]bool), openedAncestors: make(map[string]bool)}
	if v.prepared != nil && v.visibleRevision == visibility.revision {
		// Opening another file changes ancestry, not visible row ownership.
		prepared.rows, prepared.expanded = v.prepared.rows, v.prepared.expanded
	} else {
		prepared.rows = make([]FileTreeRow, 0, len(visibility.indices))
		for _, position := range visibility.indices {
			row := v.rows[position]
			prepared.rows = append(prepared.rows, row)
			if row.Directory {
				prepared.expanded[row.ID] = expanded[ordered.rows[position].entry.Relative]
			}
		}
	}
	if position, found := ordered.positions[opened]; found {
		for parent := ordered.rows[position].parent; parent >= 0; parent = ordered.rows[parent].parent {
			prepared.openedAncestors[ordered.rows[parent].entry.Key] = true
		}
	}
	v.visibleRevision, v.prepared = visibility.revision, prepared
	return prepared
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
