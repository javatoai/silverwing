package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFileTreePreparedProjectionOwnsVisibleRowsAndInputScope(t *testing.T) {
	prepared := &FileTreeProjection{
		rows:     []FileTreeRow{{ID: "folder", Name: "folder", Path: "/fixture/folder", Directory: true}},
		expanded: map[string]bool{}, opened: "folder/hidden.txt", openedAncestors: map[string]bool{"folder": true},
	}
	var inputs []FileTreeInput
	state := &FileTreeState{}
	tt := ui.NewTester(func(c *ui.Context) {
		FileTree(c, setTheme(c, false), FileTreeProps{Key: "prepared", Root: FileTreeRow{ID: "root", Name: "materials", Directory: true}, State: state,
			// Legacy inputs intentionally disagree: Prepared is one coherent value.
			Rows: []FileTreeRow{{ID: "wrong", Name: "wrong.txt"}}, Expanded: map[string]bool{"folder": true}, Opened: "wrong",
			Prepared: prepared, OnInput: func(input FileTreeInput) { inputs = append(inputs, input) },
		})
	}, 248, 200)
	if !tt.HasText("folder") || tt.HasText("wrong.txt") || tt.HasText("hidden.txt") {
		t.Fatal("FileTree recomputed prepared visibility from unrelated legacy values")
	}
	if err := tt.Click("展开或收起 folder"); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Kind != FileTreeToggle || inputs[0].Row.ID != "folder" {
		t.Fatalf("prepared row input changed: %+v", inputs)
	}
}
