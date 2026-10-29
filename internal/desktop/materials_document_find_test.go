package desktop

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/application"
)

// Use the audit's collapsed-tree fixture, including valid timestamps so a
// standalone lookup cannot hide full index construction behind parse failures.
func materialsStandaloneFindFirstFixture(root string, count int) []application.Document {
	items := materialsPerformanceFixture(root, count)
	items[0].Modified = "2026-10-29T04:00:00Z"
	for i := range items[0].Children {
		items[0].Children[i].Modified = "2026-10-29T04:00:00Z"
	}
	return items
}

func TestMaterialsStandaloneFindUsesSuppliedDFSAndNormalizedPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MaterialsAlias")
	cachedPath := filepath.Join(root, "cached.txt")
	s := materialsCacheFixture(root, []application.Document{{Path: cachedPath}})
	cached := s.materialsDocumentProjection()
	firstPath := filepath.Join(root, "First.TXT")
	rawFirst := root + string(filepath.Separator) + "unused" + string(filepath.Separator) + ".." + string(filepath.Separator) + "First.TXT"
	lastPath := filepath.Join(root, "folder", "Last.txt")
	items := []application.Document{
		// Root is not an entry, but its children still participate in lookup.
		{Path: root, Kind: "directory", Children: []application.Document{{Path: rawFirst}}},
		{Path: firstPath, Kind: "folder"}, // The earlier child wins this duplicate.
		{Path: filepath.Join(root, "folder"), Kind: "text", Children: []application.Document{{Path: lastPath}}},
	}
	entry, found := s.findMaterialsEntry(firstPath, items)
	if !found || entry.Path != firstPath || entry.Relative != "First.TXT" || entry.Directory {
		t.Fatalf("first DFS match or clean-path entry changed: %+v, %v", entry, found)
	}
	if entry, found := s.findMaterialsEntry(lastPath, items); !found || entry.Relative != "folder/Last.txt" {
		t.Fatalf("final descendant beneath a non-directory was skipped: %+v, %v", entry, found)
	}
	if _, found := s.findMaterialsEntry(filepath.Join(root, "missing.txt"), items); found {
		t.Fatal("missing path produced an entry")
	}
	if _, found := s.findMaterialsEntry(strings.ToUpper(firstPath), items); found != (runtime.GOOS == "windows") {
		t.Fatal("normalized lookup changed its native case contract")
	}
	for _, supplied := range [][]application.Document{nil, items} {
		if _, found := s.findMaterialsEntry(cachedPath, supplied); found {
			t.Fatal("standalone lookup ignored the explicit slice and used the warm app catalog")
		}
	}
	for _, path := range []string{"", root, filepath.Dir(root), filepath.Join(filepath.Dir(root), "outside.txt")} {
		if _, found := s.findMaterialsEntry(path, []application.Document{{Path: path}}); found {
			t.Fatalf("invalid/root/outside path became an entry: %q", path)
		}
	}
	if cached.index != s.materialsDocumentProjection().index {
		t.Fatal("standalone lookup replaced the controller's catalog")
	}
	s.taskUnit.state.tasks[0].materialsPath = ""
	if _, found := s.findMaterialsEntry(firstPath, items); found {
		t.Fatal("standalone lookup ignored the current empty materials root")
	}
}

func TestMaterialsStandaloneFindPreservesInvalidFirstAndDuplicateTraversal(t *testing.T) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cwd, "standalone-materials-find-fixture")
	target := filepath.Join(root, "file.txt")
	relative := filepath.Join("standalone-materials-find-fixture", "file.txt")
	s := materialsCacheFixture(root, nil)
	// Relative and absolute paths normalize to the same identity, while entry
	// rejects the relative document against an absolute root. Baseline returns
	// that first invalid match instead of falling through within the same level.
	if entry, found := s.findMaterialsEntry(target, []application.Document{{Path: relative}, {Path: target}}); found || entry != (materialsFileSelectionEntry{}) {
		t.Fatalf("invalid first match fell through to its sibling: %+v, %v", entry, found)
	}
	items := []application.Document{
		{Path: root, Kind: "directory", Children: []application.Document{{Path: relative}, {Path: target, Kind: "directory"}}},
		{Path: target},
	}
	if entry, found := s.findMaterialsEntry(target, items); !found || entry.Directory {
		t.Fatalf("invalid child match did not return control to its parent's next sibling: %+v, %v", entry, found)
	}
	if entry, found := s.findMaterialsEntry(target, []application.Document{{Path: target, Kind: "folder"}, {Path: target}}); !found || !entry.Directory {
		t.Fatalf("valid duplicate stopped using its first DFS match: %+v, %v", entry, found)
	}
}

func TestMaterialsStandaloneFindSeesLiveSliceWhileIndexedGenerationStaysDetached(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MaterialsAlias")
	items := materialsStandaloneFindFirstFixture(root, 3)
	s := materialsCacheFixture(root, items)
	cached := s.materialsDocumentProjection()
	generation := s.materialsUnit.state.documentsGeneration
	oldPath := items[0].Children[0].Path
	changedPath := filepath.Join(root, "collapsed", "changed.txt")
	items[0].Children[0].Path = changedPath
	if _, found := s.findMaterialsEntry(oldPath, items); found {
		t.Fatal("standalone lookup retained a changed element without publication")
	}
	if _, found := s.findMaterialsEntry(changedPath, items); !found {
		t.Fatal("standalone lookup did not observe the supplied element mutation")
	}
	childPath := filepath.Join(root, "collapsed", "new-child.txt")
	items[0].Children = []application.Document{{Path: childPath}}
	if _, found := s.findMaterialsEntry(childPath, items); !found {
		t.Fatal("standalone lookup did not observe replacement Children")
	}
	replacementPath := filepath.Join(root, "replacement.txt")
	items[0] = application.Document{Path: replacementPath}
	if _, found := s.findMaterialsEntry(replacementPath, items); !found {
		t.Fatal("standalone lookup did not observe same-slice replacement")
	}
	if _, found := s.findMaterialsEntry(childPath, items); found {
		t.Fatal("standalone lookup retained children of a replaced element")
	}
	if _, found := cached.find(oldPath); !found {
		t.Fatal("live standalone input changed an already-published index")
	}
	if _, found := cached.find(replacementPath); found {
		t.Fatal("indexed lookup traversed the unpublished borrowed slice")
	}
	if s.materialsUnit.state.documentsGeneration != generation || s.materialsDocumentProjection().index != cached.index {
		t.Fatal("standalone reads published a new controller generation")
	}
	s.materialsUnit.replaceDocuments(items)
	next := s.materialsDocumentProjection()
	if next.index == cached.index || s.materialsUnit.state.documentsGeneration != generation+1 {
		t.Fatal("explicit publication did not create a new detached generation")
	}
	if _, found := next.find(replacementPath); !found {
		t.Fatal("published index missed the replacement")
	}
	if _, found := cached.find(oldPath); !found {
		t.Fatal("new generation changed a retained index")
	}
}

var materialsStandaloneFindResult materialsFileSelectionEntry
var materialsStandaloneFindFound bool

func TestMaterialsStandaloneFindFirstAllocationsIgnoreUnrelatedNodes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "MaterialsAlias")
	var smallAllocations float64
	for _, count := range []int{1000, 10000} {
		items := materialsStandaloneFindFirstFixture(root, count)
		s := materialsCacheFixture(root, items)
		cached := s.materialsDocumentProjection() // Local links also use the standalone path after warmup.
		target := items[0].Children[0].Path
		allocations := testing.AllocsPerRun(3, func() {
			materialsStandaloneFindResult, materialsStandaloneFindFound = s.findMaterialsEntry(target, items)
		})
		if !materialsStandaloneFindFound || materialsStandaloneFindResult.Path != target {
			t.Fatalf("first-child lookup failed with %d nodes", count)
		}
		// Generous across platforms; the baseline used 21 allocations on Windows.
		if allocations > 64 {
			t.Errorf("first-child lookup allocated %.0f times for %d nodes; unrelated nodes should not be indexed", allocations, count)
		}
		if count == 1000 {
			smallAllocations = allocations
		} else if allocations > smallAllocations+2 {
			t.Errorf("first-child allocations grew with later nodes: 1k=%.0f, 10k=%.0f", smallAllocations, allocations)
		}
		if s.materialsDocumentProjection().index != cached.index {
			t.Fatal("standalone lookup rebuilt the warm controller catalog")
		}
	}
}

func BenchmarkMaterialsStandaloneFindFirst(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := filepath.Join(b.TempDir(), "MaterialsAlias")
			items := materialsStandaloneFindFirstFixture(root, count)
			s := materialsCacheFixture(root, items)
			s.materialsDocumentProjection()
			target := items[0].Children[0].Path
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				materialsStandaloneFindResult, materialsStandaloneFindFound = s.findMaterialsEntry(target, items)
			}
		})
	}
}
