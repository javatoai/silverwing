package markdownui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRemovingInnerLinkPreservesOriginalBytesAroundEscapedOuterBracket(t *testing.T) {
	const prefix = "\ufeffbefore\r\n\r\n"
	const label = "A\r\nB"
	const tail = "](/other)\r\n\r\ntail\r\n"
	source := prefix + "[[" + label + "](https://example.com)" + tail
	state := NewEditorState(Document{ID: "outer-bytes", Source: source})
	defer state.Close()
	view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 450)
	if err := view.Click("A\nB"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("移除链接"); err != nil {
		t.Fatal(err)
	}
	want := prefix + "&#91;<i></i>" + label + "<i></i>" + tail
	if got := state.Snapshot().Source; got != want {
		t.Fatalf("outer escape changed original bytes: %q want %q", got, want)
	}
	if state.Snapshot().Version != 1 {
		t.Fatal("outer bracket escape was not atomic")
	}
	state.native.Focus()
	view.Frame()
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("outer escape undo lost original bytes")
	}
}

func TestLinkURLOnlyAndRemovePreserveOriginalLabelNewlineBytes(t *testing.T) {
	for _, label := range []string{"A\r\nB", "A\rB\nC\r\nD"} {
		for _, prefix := range []string{"", "\ufeffbefore\r\n\r\n"} {
			for _, remove := range []bool{false, true} {
				t.Run(fmt.Sprintf("%q/%q/remove=%v", label, prefix, remove), func(t *testing.T) {
					const suffix = "\r\n\r\ntail\r\n"
					source := prefix + "[" + label + "](https://example.com)" + suffix
					state := NewEditorState(Document{ID: "link-source-bytes", Source: source})
					defer state.Close()
					state.native.Focus()
					view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 450)
					normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(label)
					if err := view.Click(normalized); err != nil {
						t.Fatalf("%v texts=%q", err, view.Texts())
					}
					want := prefix + label + suffix
					if remove {
						if err := view.Click("移除链接"); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := view.Click("链接地址"); err != nil {
							t.Fatal(err)
						}
						view.Command("selectAll")
						view.Type("https://changed.example")
						if err := view.Click("保存"); err != nil {
							t.Fatal(err)
						}
						want = prefix + "[" + label + "](<https://changed.example>)" + suffix
					}
					if got := state.Snapshot().Source; got != want {
						t.Fatalf("unchanged label bytes changed: got %q want %q", got, want)
					}
					if state.Snapshot().Version != 1 {
						t.Fatal("link operation was not one transaction")
					}
					state.native.Focus()
					view.Frame()
					view.Command("undo")
					if state.Snapshot().Source != source {
						t.Fatal("undo did not restore every original source byte")
					}
				})
			}
		}
	}
}
