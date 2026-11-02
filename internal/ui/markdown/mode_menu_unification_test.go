package markdownui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
)

func TestEditorModeMenusStayFixedAndBelongToEditor(t *testing.T) {
	for _, mode := range []Mode{Live, Preview, Source} {
		for _, readOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode-%d-readonly-%t", mode, readOnly), func(t *testing.T) {
				const source = "body\r\n"
				state := NewEditorState(Document{ID: "mode-menu", Source: source})
				defer state.Close()
				if err := state.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				buffer := state.native.Buffer()
				view := ui.NewTester(func(c *ui.Context) {
					MarkdownEditor(c, state, EditorOptions{ReadOnly: readOnly, Label: "模式菜单编辑区", ReaderOptions: ReaderOptions{
						Native:      &md.Options{ContextMenu: func(_ *ui.Context, menu *ui.Menu) { menu.Item("宿主原生菜单") }},
						ContextMenu: func(_ *ui.Context, menu *ui.Menu) { menu.Item("宿主阅读菜单") },
					}}).Fill()
				}, 700, 450)
				for _, label := range []string{"实时编辑", "只读预览", "查看 Markdown 源码"} {
					if view.HasText(label) {
						t.Fatalf("mode leaked into permanent toolbar: %q", label)
					}
				}
				anchor := "模式菜单编辑区"
				if mode == Preview {
					anchor = "body"
				}
				if err := view.RightClick(anchor); err != nil {
					t.Fatal(err)
				}
				menu := view.Menu()
				for _, label := range []string{"实时编辑", "只读预览", "查看 Markdown 源码", "宿主原生菜单", "宿主阅读菜单"} {
					count := 0
					for _, item := range menu {
						if item == label {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("menu %q occurs %d times: %v", label, count, menu)
					}
				}
				if strings.Contains(strings.Join(menu, "\n"), "查看 Markdown 预览") {
					t.Fatalf("reader injected its toggle into editor: %v", menu)
				}
				currentLabel := map[Mode]string{Live: "实时编辑", Preview: "只读预览", Source: "查看 Markdown 源码"}[mode]
				if err := view.ChooseMenuItem(currentLabel); err == nil || !strings.Contains(err.Error(), "disabled") {
					t.Fatalf("current mode must be disabled: %v", err)
				}
				view.CloseMenu()
				if state.Mode() != mode || state.Snapshot().Source != source || state.native.Buffer() != buffer {
					t.Fatal("opening menu changed source or its owner")
				}
			})
		}
	}
}

func TestEditorModeMenuRoundTripKeepsOneBufferAndHostSave(t *testing.T) {
	const source = "\ufeffbody\r\n\r\n<!-- retained -->\r\n"
	state := NewEditorState(Document{ID: "mode-roundtrip", Source: source})
	defer state.Close()
	buffer := state.native.Buffer()
	saves := 0
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{Label: "模式编辑区", OnSave: func(snapshot Snapshot) {
			saves++
			if snapshot.Source != state.Snapshot().Source {
				t.Fatal("host save received a disconnected source")
			}
		}}).Fill()
	}, 700, 450)
	for index, next := range []Mode{Source, Preview, Live} {
		anchor := "模式编辑区"
		if state.Mode() == Preview {
			anchor = "body"
		}
		if err := view.RightClick(anchor); err != nil {
			t.Fatal(err)
		}
		if err := view.ChooseMenuItem(map[Mode]string{Live: "实时编辑", Preview: "只读预览", Source: "查看 Markdown 源码"}[next]); err != nil {
			t.Fatal(err)
		}
		if state.Mode() != next || state.native.Buffer() != buffer || state.Snapshot().Source != source {
			t.Fatalf("mode %d lost the original draft", next)
		}
		if next != Preview {
			state.native.Focus()
			view.Frame()
		} else if err := view.Click("body"); err != nil {
			t.Fatal(err)
		}
		view.Key(ui.Cmd, ui.KeyS)
		if saves != index+1 {
			t.Fatalf("mode %d host save count: %d", next, saves)
		}
	}
}

func TestCaptureInputSettlesIMEAndRejectsOnlyPendingChanges(t *testing.T) {
	const source = "body\r\n"
	state, view := dailyPublicEditor(source)
	defer state.Close()
	state.native.SetSelection(4, 4)
	view.Frame()
	view.Compose("中文😀", 3)
	if !state.Snapshot().Composing || state.Snapshot().Source != source {
		t.Fatal("composition should remain virtual before capture")
	}
	snapshot, err := state.CaptureInput()
	if err != nil || snapshot.Composing || snapshot.Source != "body中文😀\r\n" {
		t.Fatalf("IME capture: %+v %v", snapshot, err)
	}
	state.link = &linkEdit{open: true, label: "same", originalLabel: "same", url: "https://example.com", originalURL: "https://example.com"}
	if _, err := state.CaptureInput(); err != nil {
		t.Fatalf("untouched popover blocked capture: %v", err)
	}
	state.link.label = "changed"
	if _, err := state.CaptureInput(); err == nil || state.link.label != "changed" {
		t.Fatal("unconfirmed link change was discarded")
	}
	state.link = nil
	state.insert = &insertDialog{open: true, originalText: "same", text: "same"}
	if _, err := state.CaptureInput(); err != nil {
		t.Fatalf("untouched insert blocked capture: %v", err)
	}
	state.insert.errorText = "invalid"
	if _, err := state.CaptureInput(); err == nil || state.insert.errorText != "invalid" {
		t.Fatal("invalid pending insertion was discarded")
	}
}

func TestCaptureInputPreservesRejectedComposition(t *testing.T) {
	state, view := dailyPublicEditor("ab")
	defer state.Close()
	state.native.SetSelection(2, 2)
	state.native.Buffer().SetMaxSourceBytes(3)
	view.Frame()
	view.Compose("中文", 2)
	if snapshot, err := state.CaptureInput(); err != ErrComposing || snapshot.Source != "ab" || !state.native.Composing() {
		t.Fatalf("rejected IME was lost: %+v %v", snapshot, err)
	}
}

func TestPendingInsertRequiresExplicitCancelOrApply(t *testing.T) {
	state, view := dailyPublicEditor("body")
	defer state.Close()
	if err := state.Execute(EditorCommand{Kind: InsertLink}); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("链接地址"); err != nil {
		t.Fatal(err)
	}
	view.Type("javascript:blocked")
	if err := view.Click("插入"); err != nil {
		t.Fatal(err)
	}
	dialog := state.insert
	if dialog == nil || dialog.errorText == "" {
		t.Fatal("invalid insert did not retain an error")
	}
	view.ClickAt(880, 580)
	view.Frame()
	if state.insert != dialog || !dialog.open {
		t.Fatal("outside dismissal silently discarded pending insert")
	}
	if _, err := state.CaptureInput(); err == nil {
		t.Fatal("host captured an invalid unconfirmed insertion")
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CaptureInput(); err != nil || state.insert != nil || state.Snapshot().Source != "body" {
		t.Fatalf("explicit cancellation could not release pending insert: %v", err)
	}
	if err := state.Execute(EditorCommand{Kind: InsertLink}); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("链接地址"); err != nil {
		t.Fatal(err)
	}
	view.Type("https://example.com")
	if err := view.Click("插入"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CaptureInput(); err != nil || state.insert != nil || !strings.Contains(state.Snapshot().Source, "https://example.com") {
		t.Fatalf("applied insertion stayed pending: %v", err)
	}
}

func TestPendingLinkEditSurvivesOutsideDismissalAndCanCancel(t *testing.T) {
	const source = "[label](https://example.com)"
	state, view := dailyPublicEditor(source)
	defer state.Close()
	if err := view.Click("label"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("显示文字"); err != nil {
		t.Fatal(err)
	}
	view.Command("selectAll")
	view.Type("changed")
	dialog := state.link
	view.ClickAt(880, 580)
	view.Frame()
	if dialog == nil || state.link != dialog || !dialog.open || dialog.label != "changed" {
		t.Fatal("outside dismissal lost unconfirmed link label")
	}
	if _, err := state.CaptureInput(); err == nil {
		t.Fatal("host captured an unconfirmed changed link")
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CaptureInput(); err != nil || state.link != nil || state.Snapshot().Source != source {
		t.Fatalf("explicit link cancellation failed: %v", err)
	}
}

func TestPublicImportResourcesUsesCapturedSelectionAndOneBuffer(t *testing.T) {
	const source = "before selected after\r\n"
	state := NewEditorState(Document{ID: "public-import", Source: source})
	defer state.Close()
	buffer := state.native.Buffer()
	var request ImportRequest
	var complete func(ImportResult, error)
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{OnImportResources: func(_ context.Context, incoming ImportRequest, done func(ImportResult, error)) {
			request, complete = incoming, done
		}}).Fill()
	}, 700, 400)
	a := strings.Index(buffer.String(), "selected")
	state.native.SetSelection(a, a+len("selected"))
	state.native.Focus()
	view.Frame()
	state.ImportResources(ImportRequest{Pick: true})
	if complete == nil || !request.Pick || request.DocumentID != "public-import" || request.Anchor != a || request.Caret != a+len("selected") {
		t.Fatalf("public import lost captured insertion: %+v", request)
	}
	complete(ImportResult{Resources: []ImportedResource{{Target: "https://example.com/image.png", Text: "图片", Image: true}}}, nil)
	view.Frame()
	if state.native.Buffer() != buffer || !strings.Contains(state.Snapshot().Source, "![图片](") || strings.Contains(state.Snapshot().Source, "selected") {
		t.Fatalf("public import did not replace selected source: %q", state.Snapshot().Source)
	}
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("resource insertion did not undo as one source edit")
	}
}

func TestStandaloneReaderRetainsSourcePreviewMenu(t *testing.T) {
	reader := NewReaderState(Document{ID: "standalone-menu", Source: "body"})
	defer reader.Close()
	view := ui.NewTester(func(c *ui.Context) { MarkdownReader(c, reader, ReaderOptions{}).Fill() }, 600, 400)
	if err := view.RightClick("body"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	if reader.Mode() != Source {
		t.Fatal("standalone reader lost source mode")
	}
	if err := view.RightClick("Markdown 源码只读预览"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("查看 Markdown 预览"); err != nil {
		t.Fatal(err)
	}
	if reader.Mode() != Preview {
		t.Fatal("standalone reader lost preview mode")
	}
}
