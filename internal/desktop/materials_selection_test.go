package desktop

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func materialsSelectionTestEntry(root, relative string, directory bool) materialsFileSelectionEntry {
	return materialsFileSelectionEntry{Key: materialsFileEntryKey(relative, directory), Path: filepath.Join(root, filepath.FromSlash(relative)), Relative: relative, Directory: directory}
}

func TestMaterialsSelectionGesturesKeepEmptyAndInvalidAnchors(t *testing.T) {
	root := t.TempDir()
	a, b, c := materialsSelectionTestEntry(root, "a.txt", false), materialsSelectionTestEntry(root, "b.txt", false), materialsSelectionTestEntry(root, "c.txt", false)
	visible := []materialsFileSelectionEntry{a, b, c}
	for _, item := range []struct {
		name, anchor string
		target       materialsFileSelectionEntry
		toggle       bool
		extend       bool
		want         []string
		wantAnchor   string
	}{
		{"shift-empty", "", c, false, true, []string{"c.txt"}, ""},
		{"shift-invalid", "file:removed", c, false, true, []string{"c.txt"}, "file:removed"},
		{"shift-toggle-invalid", "file:removed", c, true, true, []string{"a.txt"}, "file:removed"},
		{"shift-toggle-empty-removes", "", a, true, true, []string{}, ""},
		{"shift-toggle-empty-adds", "", c, true, true, []string{"a.txt", "c.txt"}, ""},
		{"toggle-removes-and-reanchors", a.Key, a, true, false, []string{}, a.Key},
		{"plain-replaces-and-reanchors", a.Key, b, false, false, []string{"b.txt"}, b.Key},
	} {
		t.Run(item.name, func(t *testing.T) {
			selection := materialsSelection{selected: map[string]materialsFileSelectionEntry{a.Key: a}, anchor: item.anchor}
			selection.apply(item.target, visible, item.toggle, item.extend)
			if got := materialsProjectionRelatives(selection.operationEntries()); !reflect.DeepEqual(got, item.want) || selection.anchor != item.wantAnchor {
				t.Fatalf("gesture changed selection/anchor: selected=%v anchor=%q want=%v/%q", got, selection.anchor, item.want, item.wantAnchor)
			}
		})
	}
}

func TestMaterialsSelectionRangesUseOnlyExpandedVisibleEntries(t *testing.T) {
	root := t.TempDir()
	projection := materialsDocumentProjection{root: root, documents: []application.Document{
		{Name: "folder", Path: filepath.Join(root, "folder"), Kind: "dir", Children: []application.Document{{Name: "inside.txt", Path: filepath.Join(root, "folder", "inside.txt")}}},
		{Name: "a.txt", Path: filepath.Join(root, "a.txt")}, {Name: "z.txt", Path: filepath.Join(root, "z.txt")},
	}}
	visible := projection.visibleEntries("NAME", nil)
	target, _ := projection.find(filepath.Join(root, "z.txt"))
	outside := materialsSelectionTestEntry(root, "previous.txt", false)
	selection := materialsSelection{selected: map[string]materialsFileSelectionEntry{outside.Key: outside}, anchor: visible[0].Key}
	selection.apply(target, visible, true, true)
	want := []string{"a.txt", "folder", "previous.txt", "z.txt"}
	if got := materialsProjectionRelatives(selection.operationEntries()); !reflect.DeepEqual(got, want) || len(selection.selected) != 4 || selection.anchor != visible[0].Key {
		t.Fatalf("union range crossed a collapsed folder or discarded previous choices: %v anchor=%s", got, selection.anchor)
	}
	selection.apply(target, visible, false, true)
	if got := materialsProjectionRelatives(selection.operationEntries()); !reflect.DeepEqual(got, []string{"a.txt", "folder", "z.txt"}) {
		t.Fatalf("replacement range retained an unrelated choice: %v", got)
	}
}

func TestMaterialsSelectionOperationsAndCopyPathsHaveDistinctScopes(t *testing.T) {
	root := t.TempDir()
	selection := materialsSelection{}
	for _, entry := range []materialsFileSelectionEntry{
		materialsSelectionTestEntry(root, "alpha", true), materialsSelectionTestEntry(root, "Beta", true),
		materialsSelectionTestEntry(root, "Beta/a.txt", false), materialsSelectionTestEntry(root, "Beta/nested", true),
		materialsSelectionTestEntry(root, "Beta/nested/x.txt", false), materialsSelectionTestEntry(root, "A.txt", false), materialsSelectionTestEntry(root, "z.txt", false),
	} {
		selection.ensure()
		selection.selected[entry.Key] = entry
	}
	if got := materialsProjectionRelatives(selection.operationEntries()); !reflect.DeepEqual(got, []string{"A.txt", "alpha", "Beta", "z.txt"}) {
		t.Fatalf("operation selection did not remove selected-directory descendants in relative order: %v", got)
	}
	var copied []string
	for _, path := range selection.copyPaths() {
		relative, _ := filepath.Rel(root, path)
		copied = append(copied, filepath.ToSlash(relative))
	}
	want := []string{"Beta", "Beta/nested", "alpha", "A.txt", "Beta/a.txt", "Beta/nested/x.txt", "z.txt"}
	if !reflect.DeepEqual(copied, want) {
		t.Fatalf("path copying lost descendants or stopped sorting by key: got=%v want=%v", copied, want)
	}
}

func TestMaterialsSelectionContextCommandRestoresHostChoice(t *testing.T) {
	root := t.TempDir()
	a, b, context := materialsSelectionTestEntry(root, "a.txt", false), materialsSelectionTestEntry(root, "b.txt", false), materialsSelectionTestEntry(root, "context.txt", false)
	state := &materialsFileManagementState{materialsSelection: materialsSelection{selected: map[string]materialsFileSelectionEntry{a.Key: a, b.Key: b}, anchor: b.Key}, contextPath: context.Path}
	s := &app{materialsUnit: materialsController{state: materialsState{documentPath: a.Path}}}
	before := state.materialsSelection.copyPaths()
	originalMap := state.selected
	if got := state.materialsSelection.contextEntries(context); len(got) != 1 || got[0] != context {
		t.Fatalf("unselected context target inherited another group: %+v", got)
	}
	if got := state.materialsSelection.contextEntries(a); len(got) != 2 {
		t.Fatalf("selected context target lost its group: %+v", got)
	}
	s.withMaterialsContextSelection(state, context, func() {
		if got := state.materialsSelection.copyPaths(); !reflect.DeepEqual(got, []string{context.Path}) || state.anchor != context.Key {
			t.Fatalf("command was not scoped to its context target: %v anchor=%s", got, state.anchor)
		}
	})
	if !reflect.DeepEqual(state.materialsSelection.copyPaths(), before) || state.anchor != b.Key || state.contextPath != context.Path || s.materialsUnit.state.documentPath != a.Path {
		t.Fatal("context command changed the original choice, anchor or reader")
	}
	originalMap[context.Key] = context
	if state.selected[context.Key] != context {
		t.Fatal("restoring context selection did not restore its original map")
	}
}

func TestMaterialsSelectionHostMapsSuperAndKeepsContextReadOnly(t *testing.T) {
	root := t.TempDir()
	items := []application.Document{{Name: "a.txt", Path: filepath.Join(root, "a.txt")}, {Name: "b.txt", Path: filepath.Join(root, "b.txt")}}
	s := &app{taskUnit: taskController{state: taskState{tasks: []task{{id: "task", materialsPath: root}}}}, materialsUnit: materialsController{state: materialsState{documents: items, documentPath: items[0].Path}}}
	state := s.ensureMaterialsFileManagementState()
	s.selectMaterialsEntry(state, items[0], 0, false)
	s.selectMaterialsEntry(state, items[1], ui.Super, false)
	if len(state.selected) != 2 || state.anchor != "file:b.txt" || !state.focused {
		t.Fatal("Super no longer toggles the independent materials choice")
	}
	state.focused = false
	s.selectMaterialsEntry(state, items[0], ui.Shift|ui.Super, true)
	if len(state.selected) != 2 || state.anchor != "file:b.txt" || state.focused || state.contextPath != items[0].Path || s.materialsUnit.state.documentPath != items[0].Path {
		t.Fatal("right-click gesture altered selection, focus or the current reader")
	}
}

func TestMaterialsSelectionCurrentDirectoryAndReconcileStayWithinProjection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ConfiguredAlias")
	projection := materialsDocumentProjection{root: root, documents: []application.Document{
		{Name: "folder", Path: filepath.Join(root, "folder"), Kind: "folder", Children: []application.Document{{Name: "a.txt", Path: filepath.Join(root, "folder", "a.txt")}}},
		{Name: "root.txt", Path: filepath.Join(root, "root.txt")},
	}}
	defaultDirectory := filepath.Join(root, "snapshot-owned")
	for _, item := range []struct{ anchor, absolute, relative string }{
		{"file:folder/a.txt", filepath.Join(root, "folder"), "folder"},
		{"directory:folder", filepath.Join(root, "folder"), "folder"},
		{"file:root.txt", defaultDirectory, "snapshot-owned"},
		{"file:../outside.txt", defaultDirectory, "snapshot-owned"},
		{"", defaultDirectory, "snapshot-owned"},
	} {
		selection := materialsSelection{anchor: item.anchor}
		if absolute, relative := selection.currentDirectory(projection, defaultDirectory); absolute != item.absolute || relative != item.relative {
			t.Fatalf("directory selection ignored the explicit default or escaped the root: anchor=%s got=%s/%s", item.anchor, absolute, relative)
		}
	}
	file, _ := projection.find(filepath.Join(root, "folder", "a.txt"))
	removed := materialsSelectionTestEntry(root, "removed.txt", false)
	changedKind := materialsSelectionTestEntry(root, "root.txt", true)
	selection := materialsSelection{selected: map[string]materialsFileSelectionEntry{file.Key: file, removed.Key: removed, changedKind.Key: changedKind}, anchor: removed.Key}
	available := selection.reconcile(projection)
	if len(available) != 3 || len(selection.selected) != 1 || selection.selected[file.Key] != file || selection.anchor != "" {
		t.Fatalf("reconciliation retained removed/kind-changed choices or a stale anchor: selected=%+v anchor=%s", selection.selected, selection.anchor)
	}
	selection.anchor = "directory:folder"
	selection.reconcile(projection)
	if selection.anchor != "directory:folder" {
		t.Fatal("an available unselected directory anchor was discarded")
	}
}
