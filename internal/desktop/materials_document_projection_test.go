package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/application"
)

func materialsProjectionRelatives(entries []materialsFileSelectionEntry) []string {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.Relative)
	}
	return values
}

func TestMaterialsDocumentProjectionSortAndVisibleTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ConfiguredAlias")
	items := []application.Document{
		{Name: "Zoo.txt", Path: filepath.Join(root, "Zoo.txt")},
		{Name: "Beta", Path: filepath.Join(root, "Beta"), Kind: "folder", Children: []application.Document{{Name: "hidden.txt", Path: filepath.Join(root, "Beta", "hidden.txt")}}},
		{Name: "alpha", Path: filepath.Join(root, "alpha"), Kind: "dir", Children: []application.Document{
			{Name: "z.txt", Path: filepath.Join(root, "alpha", "z.txt")}, {Name: "a.txt", Path: filepath.Join(root, "alpha", "a.txt")},
		}},
		{Name: "same", Path: filepath.Join(root, "same-b")},
		{Name: "SAME", Path: filepath.Join(root, "same-a")},
	}
	before, _ := json.Marshal(items)
	projection := materialsDocumentProjection{root: root, documents: items}
	var rows []string
	for _, row := range projection.treeRows("NAME") {
		rows = append(rows, fmt.Sprintf("%s:%d", row.entry.Relative, row.depth))
	}
	wantRows := []string{"alpha:0", "alpha/a.txt:1", "alpha/z.txt:1", "Beta:0", "Beta/hidden.txt:1", "same-a:0", "same-b:0", "Zoo.txt:0"}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("directory/name/path order changed: got=%v want=%v", rows, wantRows)
	}
	visible := materialsProjectionRelatives(projection.visibleEntries("NAME", map[string]bool{"alpha": true}))
	if want := []string{"alpha", "alpha/a.txt", "alpha/z.txt", "Beta", "same-a", "same-b", "Zoo.txt"}; !reflect.DeepEqual(visible, want) {
		t.Fatalf("visible range included a collapsed descendant: got=%v want=%v", visible, want)
	}
	after, _ := json.Marshal(items)
	if string(before) != string(after) {
		t.Fatal("tree ordering changed the backend snapshot or its child slices")
	}
}

func TestMaterialsDocumentProjectionModifiedOrderContracts(t *testing.T) {
	for _, item := range []struct {
		name        string
		left, right application.Document
		wantFirst   string
	}{
		{"actual-time-before-lexical", application.Document{Name: "left", Path: "left", Modified: "2026-10-28T10:00:00+08:00"}, application.Document{Name: "right", Path: "right", Modified: "2026-10-28T04:00:00Z"}, "right"},
		{"invalid-time-uses-raw-string", application.Document{Name: "left", Path: "left", Modified: "z-invalid"}, application.Document{Name: "right", Path: "right", Modified: "2026-10-28T04:00:00Z"}, "left"},
		{"equal-instant-uses-raw-string", application.Document{Name: "left", Path: "left", Modified: "2026-10-28T10:00:00+08:00"}, application.Document{Name: "right", Path: "right", Modified: "2026-10-28T02:00:00Z"}, "left"},
		{"directory-first", application.Document{Name: "left", Path: "left", Kind: "directory", Modified: "2020-01-01T00:00:00Z"}, application.Document{Name: "right", Path: "right", Modified: "2026-10-28T04:00:00Z"}, "left"},
	} {
		t.Run(item.name, func(t *testing.T) {
			got := sortDocumentsByOrder([]application.Document{item.left, item.right}, "MODIFIED_DESC")
			if got[0].Path != item.wantFirst {
				t.Fatalf("modified order changed: %+v", got)
			}
		})
	}
	ties := []application.Document{{Name: "same", Path: "same", Size: 2}, {Name: "same", Path: "same", Size: 1}}
	for _, order := range []string{"NAME", "MODIFIED_DESC"} {
		if got := sortDocumentsByOrder(ties, order); got[0].Size != 2 || got[1].Size != 1 {
			t.Fatalf("exact ties lost input stability for %s: %+v", order, got)
		}
	}
}

func TestMaterialsDocumentProjectionPathsKindsAndBounds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ConfiguredAlias")
	raw := root + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "Alias.TXT"
	projection := materialsDocumentProjection{root: root}
	for _, kind := range []string{"directory", "dir", "folder", "Folder", "text"} {
		entry, ok := projection.entry(application.Document{Path: raw, Kind: kind})
		wantDirectory := kind == "directory" || kind == "dir" || kind == "folder"
		if !ok || entry.Path != filepath.Join(root, "Alias.TXT") || entry.Relative != "Alias.TXT" || entry.Directory != wantDirectory {
			t.Fatalf("path spelling or kind alias changed for %q: %+v %t", kind, entry, ok)
		}
	}
	for _, path := range []string{"", root, filepath.Dir(root), filepath.Join(filepath.Dir(root), "outside.txt")} {
		if entry, ok := projection.entry(application.Document{Path: path}); ok {
			t.Fatalf("root/outside path became selectable: %+v", entry)
		}
	}
	for _, name := range []string{".env", ".silverwing-provided-by-backend"} {
		if _, ok := projection.entry(application.Document{Path: filepath.Join(root, name)}); !ok {
			t.Fatalf("projection added a filesystem visibility rule for %s", name)
		}
	}
	projection.documents = []application.Document{{Name: "display label", Path: raw}}
	entry, found := projection.find(strings.ToUpper(filepath.Join(root, "alias.txt")))
	if found != (runtime.GOOS == "windows") || found && entry.Path != filepath.Join(root, "Alias.TXT") {
		t.Fatalf("path comparison changed its OS case contract: entry=%+v found=%t", entry, found)
	}
	matches := projection.quickMatches(" alias.txt ")
	if len(matches) != 1 || matches[0].path != raw || matches[0].name != "Alias.TXT" || matches[0].relative != "Alias.TXT" {
		t.Fatalf("quick-open cleaned the original alias path or matched the display label: %+v", matches)
	}
	projection.documents = []application.Document{{Path: root}, {Path: filepath.Join(filepath.Dir(root), "outside.txt")}}
	matches = projection.quickMatches("")
	if len(matches) != 1 || matches[0].path != root || matches[0].relative != "." {
		t.Fatalf("quick-open inherited the stricter tree-entry root rule: %+v", matches)
	}
	if got := (materialsDocumentProjection{documents: projection.documents}).quickMatches(""); got != nil {
		t.Fatalf("empty materials root produced quick matches: %+v", got)
	}
}

func TestMaterialsDocumentProjectionQuickPickerKeepsDFSAndLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	items := []application.Document{
		{Name: "z.txt", Path: filepath.Join(root, "z.txt")},
		{Name: "folder", Path: filepath.Join(root, "folder"), Kind: "directory", Children: []application.Document{
			{Name: "z.txt", Path: filepath.Join(root, "folder", "z.txt")}, {Name: "Alpha.TXT", Path: filepath.Join(root, "folder", "Alpha.TXT")},
		}},
		{Name: "a.txt", Path: filepath.Join(root, "a.txt")},
	}
	for index := 0; index < 205; index++ {
		name := fmt.Sprintf("bulk-%03d.txt", index)
		items = append(items, application.Document{Name: name, Path: filepath.Join(root, name)})
	}
	projection := materialsDocumentProjection{root: root, documents: items}
	matches := projection.quickMatches("")
	if len(matches) != 200 || matches[0].relative != "z.txt" || matches[1].relative != "folder/z.txt" || matches[2].relative != "folder/Alpha.TXT" || matches[3].relative != "a.txt" || matches[199].relative != "bulk-195.txt" {
		t.Fatalf("quick-open was tree-sorted, expansion-filtered, or lost its limit: %+v", matches)
	}
	visible := projection.visibleEntries("NAME", nil)
	if len(visible) != 208 || visible[0].Relative != "folder" {
		t.Fatalf("tree order/expansion was coupled to quick DFS: %+v", visible)
	}
	filtered := projection.quickMatches(" FOLDER/ALPHA ")
	if len(filtered) != 1 || filtered[0].relative != "folder/Alpha.TXT" {
		t.Fatalf("quick query is no longer trimmed case-insensitive substring matching: %+v", filtered)
	}
}

func TestMaterialsDocumentProjectionReconciliationKeepsChildrenOfInvalidParents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Alias")
	projection := materialsDocumentProjection{root: root, documents: []application.Document{{Name: "root", Path: root, Kind: "directory", Children: []application.Document{{Name: "child.txt", Path: filepath.Join(root, "child.txt")}}}}}
	available, anchored := projection.entryIndex("file:child.txt")
	if !anchored || len(available) != 1 || available[normalizedReadingPath(filepath.Join(root, "child.txt"))].Relative != "child.txt" {
		t.Fatalf("catalog reconciliation inherited the tree's parent rejection: %+v anchored=%t", available, anchored)
	}
	if rows := projection.treeRows("NAME"); len(rows) != 0 {
		t.Fatalf("invalid tree parent leaked selectable child rows: %+v", rows)
	}
}
