package desktop

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

func TestMaterialsSelectionCachedOperationsKeepFullCopyAndInvalidateGestures(t *testing.T) {
	root := t.TempDir()
	parent := materialsSelectionTestEntry(root, "dir", true)
	child := materialsSelectionTestEntry(root, "dir/a.txt", false)
	sibling := materialsSelectionTestEntry(root, "dir-other/a.txt", false)
	selection := materialsSelection{}
	selection.replace(map[string]materialsFileSelectionEntry{parent.Key: parent, child.Key: child, sibling.Key: sibling}, child.Key)
	first := selection.operationEntries()
	if got := materialsProjectionRelatives(first); !reflect.DeepEqual(got, []string{"dir", "dir-other/a.txt"}) {
		t.Fatalf("ancestor boundary changed: %v", got)
	}
	if len(selection.copyPaths()) != 3 {
		t.Fatal("operation deduplication changed complete path copying")
	}
	if again := selection.operationEntries(); &again[0] != &first[0] {
		t.Fatal("unchanged selection rebuilt operations")
	}
	selection.apply(parent, nil, true, false)
	if got := materialsProjectionRelatives(selection.operationEntries()); !reflect.DeepEqual(got, []string{"dir-other/a.txt", "dir/a.txt"}) {
		t.Fatalf("toggle reused stale operations: %v", got)
	}
	if first[0] != parent {
		t.Fatal("selection edit mutated retained operation values")
	}
	selection.reset()
	if got := selection.operationEntries(); got != nil {
		t.Fatal("reset retained operation entries")
	}
}

func TestMaterialsSelectionAncestorsRespectNativeCaseAndRelativeBoundary(t *testing.T) {
	root := t.TempDir()
	parent := materialsSelectionTestEntry(root, "Folder", true)
	caseChild := materialsSelectionTestEntry(root, "folder/child.txt", false)
	selection := materialsSelection{selected: map[string]materialsFileSelectionEntry{parent.Key: parent, caseChild.Key: caseChild}}
	want := 2
	if runtime.GOOS == "windows" {
		want = 1
	}
	if got := selection.operationEntries(); len(got) != want {
		t.Fatalf("native case semantics changed: %+v", got)
	}
	selection.replace(map[string]materialsFileSelectionEntry{
		"directory:.": {Key: "directory:.", Path: ".", Relative: ".", Directory: true},
		"file:..":     {Key: "file:..", Path: "..", Relative: ".."},
	}, "")
	if got := selection.operationEntries(); len(got) != 2 {
		t.Fatal("relative parent traversal became a selected descendant")
	}
}

func TestMaterialsSelectionRenameReconcileAndRootReplaceInvalidateCachedScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	item := application.Document{Name: "old.txt", Path: filepath.Join(root, "old.txt")}
	s := materialsCacheFixture(root, []application.Document{item})
	state := s.ensureMaterialsFileManagementState()
	entry, _ := s.materialsDocumentProjection().find(item.Path)
	state.materialsSelection.selectOnly(entry)
	state.materialsSelection.operationEntries()
	newPath := filepath.Join(root, "new.txt")
	s.remapMaterialsFileSelectionAfterRename(s.taskUnit.currentTask(), documents.FileOperationResult{RelativePath: "new.txt"}, item.Path, newPath)
	if got := state.materialsSelection.operationEntries(); len(got) != 1 || got[0].Path != newPath {
		t.Fatalf("rename reused old operation path: %+v", got)
	}
	s.materialsUnit.replaceDocuments(nil)
	state.materialsSelection.reconcile(s.materialsDocumentProjection())
	if len(state.materialsSelection.operationEntries()) != 0 {
		t.Fatal("catalog reconciliation retained deleted operations")
	}
	state.materialsSelection.selectOnly(entry)
	state.materialsSelection.operationEntries()
	s.taskUnit.state.tasks[0].materialsPath = filepath.Join(t.TempDir(), "new-root")
	state = s.ensureMaterialsFileManagementState()
	if len(state.materialsSelection.operationEntries()) != 0 || state.anchor != "" {
		t.Fatal("root replacement retained another root's selection cache")
	}
}

func TestMaterialsSelectionThousandIndependentDirectoriesKeepAllEntries(t *testing.T) {
	selection := materialsPerformanceSelection(t.TempDir(), 1000)
	if got := selection.operationEntries(); len(got) != 1000 || got[0].Relative != "dir00000" || got[999].Relative != "dir00999" {
		t.Fatal("independent directories were mistaken for descendants")
	}
}

func materialsPerformanceSelection(root string, n int) materialsSelection {
	selection := materialsSelection{selected: make(map[string]materialsFileSelectionEntry, n)}
	for i := 0; i < n; i++ {
		entry := materialsSelectionTestEntry(root, fmt.Sprintf("dir%05d", i), true)
		selection.selected[entry.Key] = entry
	}
	return selection
}

func BenchmarkMaterialsSelection(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		selection := materialsPerformanceSelection(b.TempDir(), n)
		selection.operationEntries()
		b.Run(fmt.Sprintf("operations-cold/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkEntries = selection.projectOperationEntries()
			}
		})
		b.Run(fmt.Sprintf("operations-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				materialsBenchmarkEntries = selection.operationEntries()
			}
		})
	}
}
