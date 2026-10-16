package markdown

import (
	"github.com/egoist/mygo/ui"
	"slices"
	"testing"
)

func TestViewContextMenuAppendKeepsNativeCopy(t *testing.T) {
	calls := 0
	o := &Options{ContextMenu: func(c *ui.Context, m *ui.Menu) {
		m.Separator()
		if m.Item("Document action").Chosen() {
			calls++
		}
	}}
	tt := ui.NewTester(func(c *ui.Context) { View(c, "Some **bold** text.", o) }, 400, 200)
	tt.Click("Some bold text.")
	tt.Command("selectAll")
	tt.RightClick("Some bold text.")
	for _, label := range []string{"Copy", "Select All", "Copy as Markdown", "Document action"} {
		if !slices.Contains(tt.Menu(), label) {
			t.Fatalf("menu %q", tt.Menu())
		}
	}
	if calls != 0 {
		t.Fatal("action executed on open")
	}
	if err := tt.ChooseMenuItem("Copy as Markdown"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "Some **bold** text." {
		t.Fatalf("copy %q", tt.Clipboard())
	}
	tt.RightClick("Some bold text.")
	tt.ChooseMenuItem("Document action")
	if calls != 1 {
		t.Fatalf("action calls %d", calls)
	}
}
