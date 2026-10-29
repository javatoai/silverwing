package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

// Adapted from the audit's synthetic 10/1k/10k collapsed catalog fixture.
// Every path is under an isolated TempDir; nothing reads user materials.
func materialsPerformanceFixture(root string, count int) []application.Document {
	items := []application.Document{{Name: "collapsed", Path: filepath.Join(root, "collapsed"), Kind: "directory"}}
	for i := 0; i < count-1; i++ {
		name := fmt.Sprintf("file%05d.txt", i)
		items[0].Children = append(items[0].Children, application.Document{Name: name, Path: filepath.Join(root, "collapsed", name), Kind: "text"})
	}
	return items
}

func materialsCacheFixture(root string, items []application.Document) *app {
	s := &app{taskUnit: taskController{state: taskState{tasks: []task{{id: "task", path: filepath.Dir(root), createdAt: "created", materialsPath: root}}}}}
	s.materialsUnit.replaceDocuments(items)
	return s
}

func TestMaterialsProjectionGenerationPublishesSameSliceAndChangedChildren(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	items := materialsPerformanceFixture(root, 3)
	s := materialsCacheFixture(root, items)
	first := s.materialsDocumentProjection()
	firstRows := first.treeRows("NAME")
	if first.documentIndex() != s.materialsDocumentProjection().documentIndex() || first.count() != 3 {
		t.Fatal("unchanged generation rebuilt its catalog")
	}
	items[0].Children[0].Name = "renamed.txt"
	items[0].Children[0].Path = filepath.Join(root, "collapsed", "renamed.txt")
	items[0].Children = append(items[0].Children, application.Document{Name: "added.txt", Path: filepath.Join(root, "collapsed", "added.txt")})
	s.materialsUnit.replaceDocuments(items) // same outer slice, new child element and child slice
	second := s.materialsDocumentProjection()
	if second.documentIndex() == first.documentIndex() || second.count() != 4 {
		t.Fatal("publishing the same slice missed child changes")
	}
	if _, found := second.find(filepath.Join(root, "collapsed", "file00000.txt")); found {
		t.Fatal("old path survived publication")
	}
	if entry, found := second.find(filepath.Join(root, "collapsed", "renamed.txt")); !found || entry.Relative != "collapsed/renamed.txt" {
		t.Fatal("new path missing from index")
	}
	if firstRows[1].name != "file00000.txt" || first.count() != 3 {
		t.Fatal("new publication mutated a retained value projection")
	}
	if got := second.quickMatches(" RENAMED "); len(got) != 1 || got[0].name != "renamed.txt" {
		t.Fatalf("quick result stayed stale: %+v", got)
	}
	items[0] = application.Document{Name: "replacement.txt", Path: filepath.Join(root, "replacement.txt")}
	s.materialsUnit.replaceDocuments(items)
	if got := s.materialsDocumentProjection().count(); got != 1 {
		t.Fatalf("same-slice element replacement retained descendants: %d", got)
	}
	current := s.materialsDocumentProjection()
	s.taskUnit.state.tasks[0].id = "other-task"
	if current.documentIndex() == s.materialsDocumentProjection().documentIndex() {
		t.Fatal("task identity did not invalidate the catalog")
	}
	current = s.materialsDocumentProjection()
	s.taskUnit.state.tasks[0].createdAt = "recreated"
	if current.documentIndex() == s.materialsDocumentProjection().documentIndex() {
		t.Fatal("recreated task reused the catalog")
	}
	s.taskUnit.state.tasks[0].materialsPath = filepath.Join(t.TempDir(), "other-root")
	if got := s.materialsDocumentProjection().treeRows("NAME"); len(got) != 0 {
		t.Fatal("root change retained out-of-root selectable rows")
	}
	s.materialsUnit.resetPresentedMaterials()
	if got := s.materialsDocumentProjection().count(); got != 0 {
		t.Fatal("reset retained the prior catalog")
	}
}

func TestMaterialsTreePreparedValuesInvalidatePermissionsExpansionAndOpenedPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	items := []application.Document{{Name: "dir", Path: filepath.Join(root, "dir"), Kind: "directory", Children: []application.Document{
		{Name: "nested", Path: filepath.Join(root, "dir", "nested"), Kind: "directory", Children: []application.Document{{Name: "a.txt", Path: filepath.Join(root, "dir", "nested", "a.txt")}}},
	}}}
	index := newMaterialsDocumentIndex(root, items)
	key := materialsTreePermissionKey{permissions: application.MaterialsPermissions{Kind: documents.MaterialsLocal, ReadRoot: root, OwnedRoot: filepath.Join(root, "dir"), CanWrite: true}}
	var view materialsTreePresentation
	expanded := map[string]bool{}
	opened := "file:dir/nested/a.txt"
	first := view.prepare(index, "NAME", key, expanded, opened)
	if len(first.rows) != 1 || first.rows[0].ReadOnly || !first.openedAncestors["directory:dir"] || !first.openedAncestors["directory:dir/nested"] {
		t.Fatalf("collapsed rows lost hidden-file ancestry or permissions: %+v", first)
	}
	if first != view.prepare(index, "NAME", key, expanded, opened) {
		t.Fatal("unchanged tree preparation was rebuilt")
	}
	expanded["dir/nested"] = true // hidden input is consumed when its parent opens
	if first != view.prepare(index, "NAME", key, expanded, opened) {
		t.Fatal("hidden expansion rebuilt unchanged visible rows")
	}
	expanded["dir"] = true
	second := view.prepare(index, "NAME", key, expanded, opened)
	if second == first || len(second.rows) != 3 || len(first.rows) != 1 {
		t.Fatal("same-map expansion change failed or changed a retained projection")
	}
	key.permissions.CanWrite = false
	third := view.prepare(index, "NAME", key, expanded, opened)
	if third == second || !third.rows[0].ReadOnly || second.rows[0].ReadOnly {
		t.Fatal("permission change reused writable rows or mutated prior values")
	}
	fourth := view.prepare(index, "NAME", key, expanded, "file:missing.txt")
	if fourth == third || len(fourth.openedAncestors) != 0 {
		t.Fatal("opened path retained stale ancestry")
	}
	if got, want := fourth.rows, fileTreeVisibleRows(view.rows, fourth.expanded); !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared visibility changed FileTree semantics: got=%+v want=%+v", got, want)
	}
}

func TestMaterialsProjectionSortKeepsRawDFSAndCachesBothOrders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	items := []application.Document{
		{Name: "z.txt", Path: filepath.Join(root, "z.txt"), Modified: "2026-10-28T04:00:00Z"},
		{Name: "a.txt", Path: filepath.Join(root, "a.txt"), Modified: "2026-10-28T10:00:00+08:00"},
	}
	s := materialsCacheFixture(root, items)
	before, _ := json.Marshal(items)
	projection := s.materialsDocumentProjection()
	name := projection.treeRows("NAME")
	modified := projection.treeRows("MODIFIED_DESC")
	if name[0].name != "a.txt" || modified[0].name != "z.txt" {
		t.Fatal("sort order changed")
	}
	s.currentReadingMaterialsSortDocuments()
	after, _ := json.Marshal(items)
	if string(before) != string(after) || s.materialsDocumentProjection().documentIndex() != projection.documentIndex() {
		t.Fatal("reading sort mutated or republished the raw snapshot")
	}
	if got := projection.quickMatches(""); got[0].name != "z.txt" || got[1].name != "a.txt" {
		t.Fatal("tree order leaked into quick-open DFS")
	}
	if got := projection.treeRows("NAME"); &got[0] != &name[0] {
		t.Fatal("switching sort discarded the other bounded order projection")
	}
}

func TestMaterialsProjectionTenThousandCollapsedAndQuickBounds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	s := materialsCacheFixture(root, materialsPerformanceFixture(root, 10000))
	projection := s.materialsDocumentProjection()
	if projection.count() != 10000 || len(projection.treeRows("NAME")) != 10000 || len(projection.visibleEntries("NAME", nil)) != 1 {
		t.Fatal("large collapsed projection changed its count/visibility")
	}
	if got := projection.quickMatches(""); len(got) != 200 || got[199].relative != "collapsed/file00199.txt" {
		t.Fatal("quick-open DFS limit changed")
	}
	if got := projection.quickMatches("__no_match__"); len(got) != 0 {
		t.Fatal("unexpected quick match")
	}
	if got := projection.quickMatches(" __NO_MATCH__ "); len(got) != 0 || projection.documentIndex().quickQuery != "__no_match__" {
		t.Fatal("normalized unchanged query did not reuse no-match result")
	}
	ordered := projection.documentIndex().orderedRows("NAME")
	ordered.treeVisibility.project(ordered.rows, nil, true)
	if len(ordered.treeVisibility.indices) != 1 || len(ordered.treeVisibility.expanded) != 1 {
		t.Fatal("collapsed view retained per-descendant visible work")
	}
}

var materialsBenchmarkRows []materialsDocumentRow
var materialsBenchmarkEntries []materialsFileSelectionEntry
var materialsBenchmarkMatches []quickPickerEntry

func BenchmarkMaterialsProjection(b *testing.B) {
	for _, n := range []int{10, 1000, 10000} {
		root := filepath.Join(b.TempDir(), "materials")
		items := materialsPerformanceFixture(root, n)
		index := newMaterialsDocumentIndex(root, items)
		projection := materialsDocumentProjection{root: root, index: index}
		projection.treeRows("NAME")
		projection.visibleEntries("NAME", nil)
		projection.quickMatches("__never_matches__")
		b.Run(fmt.Sprintf("tree-cold/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkRows = newMaterialsDocumentIndex(root, items).orderedRows("NAME").rows
			}
		})
		b.Run(fmt.Sprintf("tree-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkRows = projection.treeRows("NAME")
			}
		})
		b.Run(fmt.Sprintf("collapsed-visible-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkEntries = projection.visibleEntries("NAME", nil)
			}
		})
		b.Run(fmt.Sprintf("quick-no-match-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkMatches = projection.quickMatches("__never_matches__")
			}
		})
		b.Run(fmt.Sprintf("quick-query-change/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				i++
				if i%2 == 0 {
					materialsBenchmarkMatches = projection.quickMatches("__no_match_a__")
				} else {
					materialsBenchmarkMatches = projection.quickMatches("__no_match_b__")
				}
			}
		})
	}
}
