package markdownui

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	"strings"
	"testing"
)

func TestPublicImageEditPopupLifecycleAndIsolation(t *testing.T) {
	const source = "before\n\n![旧图](./old.png)\n\n![另一个](./old.png)\n\n<!-- keep -->"
	s, v := dailyPublicEditor(source)
	if err := v.Click("图片：旧图"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	if s.image == nil || s.image.target != "./old.png" || s.image.alt != "旧图" {
		t.Fatalf("popup not prefilled: %+v link=%+v selected=%+v", s.image, s.link, s.native.Images())
	}
	originalVersion := s.Snapshot().Version
	s.applyImageEdit(s.image)
	if s.Snapshot().Version != originalVersion {
		t.Fatal("no change created undo")
	}
	info := s.native.Images()[0]
	s.editImage(info, ui.Rect{})
	s.image.target = "https://offline.invalid/photo.png"
	s.image.alt = "新图"
	s.applyImageEdit(s.image)
	if want := strings.Replace(source, "![旧图](./old.png)", "![新图](<https://offline.invalid/photo.png>)", 1); s.Snapshot().Source != want {
		t.Fatalf("edit %q", s.Snapshot().Source)
	}
	v.Frame()
	v.Frame()
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("edit not one undo")
	}
	info = s.native.Images()[0]
	s.editImage(info, ui.Rect{})
	s.image.alt = "cancelled"
	s.cancelImageEdit()
	if s.Snapshot().Source != source {
		t.Fatal("cancel changed body")
	}
	s.editImage(s.native.Images()[0], ui.Rect{})
	s.image.target = "javascript:alert(1)"
	s.applyImageEdit(s.image)
	if s.image == nil || s.image.errorText == "" || s.Snapshot().Source != source {
		t.Fatal("invalid target applied")
	}
	s.cancelImageEdit()
	s.editImage(s.native.Images()[0], ui.Rect{})
	s.image.target = "changed.png"
	_ = s.native.ReplaceRange(0, 0, "new\n\n", s.Snapshot().Version)
	s.applyImageEdit(s.image)
	if s.image == nil || s.image.errorText == "" || !strings.Contains(s.Snapshot().Source, "old.png") {
		t.Fatal("stale dialog overwrote newer text")
	}
}

func TestPublicImageAsyncImportNoPrematureMutation(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "stale", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			s := NewEditorState(Document{ID: "image-import", Path: "D:/synthetic/doc.md", Source: "hello"})
			var complete func(ImportResult, error)
			opts := EditorOptions{OnImportResources: func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) { complete = done }}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 600)
			v.Frame()
			s.native.SetSelection(5, 5)
			s.importResources(ImportRequest{ImagePNG: []byte("synthetic")})
			if complete == nil || s.Snapshot().Source != "hello" {
				t.Fatal("import modified before success")
			}
			switch outcome {
			case "success":
				complete(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/a.png", Text: "a", Image: true}, {Target: "doc.assets/b.png", Text: "b", Image: true}}}, nil)
				if s.Snapshot().Source != "hello\n\n![a](<doc.assets/a.png>)\n\n![b](<doc.assets/b.png>)\n\n" {
					t.Fatalf("paste paragraphs %q", s.Snapshot().Source)
				}
				v.Frame()
				v.Command("undo")
				if s.Snapshot().Source != "hello" {
					t.Fatal("image paste was not one undo")
				}
			case "failure":
				complete(ImportResult{}, fmt.Errorf("failed"))
				if s.Snapshot().Source != "hello" {
					t.Fatal("failed import added blank")
				}
			case "stale":
				_ = s.native.ReplaceRange(5, 5, "!", s.Snapshot().Version)
				complete(ImportResult{Resources: []ImportedResource{{Target: "a.png", Image: true}}}, nil)
				if s.Snapshot().Source != "hello!" {
					t.Fatal("late import mutated newer text")
				}
			case "cancel":
				complete(ImportResult{}, nil)
				if s.Snapshot().Source != "hello" {
					t.Fatal("cancel added blank")
				}
			}
		})
	}
}

func TestPublicImageValidationCancelEscAndHostPicker(t *testing.T) {
	s := NewEditorState(Document{ID: "edit", Path: "D:/synthetic/doc.md", Source: "![a](old.png)"})
	var complete func(ImportResult, error)
	opts := EditorOptions{Native: &md.Options{ValidateImage: func(target string) error {
		if strings.Contains(target, "denied") {
			return fmt.Errorf("denied")
		}
		return nil
	}}, OnImportResources: func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) { complete = done }}
	v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 600)
	v.Frame()
	_ = s.native.SelectImage(s.native.Images()[0])
	s.editImage(s.native.Images()[0], ui.Rect{})
	s.image.target = "denied.png"
	s.applyImageEdit(s.image)
	if s.image.errorText != "denied" {
		t.Fatal("host validation skipped")
	}
	s.image.target = "old.png"
	s.image.errorText = ""
	s.importReplacement(s.image)
	if complete == nil || s.Snapshot().Source != "![a](old.png)" {
		t.Fatal("picker prematurely patched image")
	}
	complete(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/new.png", Image: true}}}, nil)
	if s.image.target != "doc.assets/new.png" {
		t.Fatal("picker did not update draft")
	}
	s.applyImageEdit(s.image)
	if s.Snapshot().Source != "![a](<doc.assets/new.png>)" {
		t.Fatalf("picker replacement %q", s.Snapshot().Source)
	}
	s.editImage(s.native.Images()[0], ui.Rect{})
	v.Frame()
	if err := v.Click("图片地址"); err != nil {
		t.Fatal(err)
	}
	v.Key(0, ui.KeyEscape)
	if s.image != nil {
		t.Fatal("Escape did not cancel image dialog")
	}
}

func TestPublicImageImportCancelAndDocumentSwitchRejectLateResult(t *testing.T) {
	for _, operation := range []string{"cancel", "close", "switch"} {
		t.Run(operation, func(t *testing.T) {
			s := NewEditorState(Document{ID: "first", Path: "D:/synthetic/first.md", Source: "![a](a.png)"})
			var complete func(ImportResult, error)
			var importContext context.Context
			opts := EditorOptions{OnImportResources: func(ctx context.Context, _ ImportRequest, done func(ImportResult, error)) {
				importContext = ctx
				complete = done
			}}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 700, 500)
			v.Frame()
			s.editImage(s.native.Images()[0], ui.Rect{})
			s.importReplacement(s.image)
			switch operation {
			case "cancel":
				s.cancelImageEdit()
				if importContext.Err() == nil {
					t.Fatal("import context not cancelled")
				}
			case "close":
				s.Close()
			case "switch":
				s.Close()
				s = NewEditorState(Document{ID: "second", Path: "D:/synthetic/second.md", Source: "second"})
			}
			complete(ImportResult{Resources: []ImportedResource{{Image: true, Target: "late.png"}}}, nil)
			want := "![a](a.png)"
			if operation == "switch" {
				want = "second"
			}
			if s.Snapshot().Source != want {
				t.Fatalf("late import patched wrong document %q", s.Snapshot().Source)
			}
		})
	}
}

func TestPublicImageContextMenuAndRealPopupSave(t *testing.T) {
	s, v := dailyPublicEditor("![a](a.png)")
	if err := v.RightClick("图片：a"); err != nil {
		t.Fatal(err)
	}
	if err := v.ChooseMenuItem("编辑图片链接"); err != nil {
		t.Fatal(err)
	}
	if s.image == nil {
		t.Fatal("context menu did not open popup")
	}
	if err := v.Click("图片地址"); err != nil {
		t.Fatal(err)
	}
	v.Key(ui.Cmd, ui.KeyA)
	v.Type("https://offline.invalid/b.png")
	if err := v.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if s.image != nil || s.Snapshot().Source != "![a](<https://offline.invalid/b.png>)" {
		t.Fatalf("popup save %q %+v", s.Snapshot().Source, s.image)
	}
	v.Command("undo")
	if s.Snapshot().Source != "![a](a.png)" {
		t.Fatal("popup save did not undo")
	}
}

func TestReviewPublicImageDirtyAndImportingEscapeCancel(t *testing.T) {
	for _, importing := range []bool{false, true} {
		s := NewEditorState(Document{ID: "escape", Source: "![a](a.png)"})
		var importContext context.Context
		var done func(ImportResult, error)
		opts := EditorOptions{OnImportResources: func(ctx context.Context, _ ImportRequest, complete func(ImportResult, error)) {
			importContext = ctx
			done = complete
		}}
		v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 600)
		v.Frame()
		s.editImage(s.native.Images()[0], ui.Rect{})
		v.Frame()
		s.image.alt = "unsaved"
		if importing {
			s.importReplacement(s.image)
		}
		v.Frame()
		v.Key(0, ui.KeyEscape)
		if s.image != nil || s.Snapshot().Source != "![a](a.png)" {
			t.Errorf("Escape did not cancel dirty/importing popup, importing=%v", importing)
		}
		if importing {
			if importContext.Err() == nil {
				t.Error("Escape did not cancel import context")
			}
			done(ImportResult{}, nil)
		}
	}
}

func TestReviewPublicImageURLAndNavigationContracts(t *testing.T) {
	s, v := dailyPublicEditor("![a](a.png)")
	s.editImage(s.native.Images()[0], ui.Rect{})
	s.image.target = "https://:443/a.png"
	s.applyImageEdit(s.image)
	if s.image == nil || s.image.errorText == "" || s.Snapshot().Source != "![a](a.png)" {
		t.Error("image popup saved URL without hostname")
	}
	s = NewEditorState(Document{ID: "navigate", Source: "![a](https://example.invalid/a.png)"})
	opens := 0
	opts := EditorOptions{ReaderOptions: ReaderOptions{OnNavigate: func(LinkTarget) { opens++ }}}
	v = ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 600)
	if err := v.Click("图片：a"); err != nil {
		t.Fatal(err)
	}
	if err := v.Click("图片：a"); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("double click did not use navigation bridge: %d", opens)
	}
	opts.ReadOnly = true
	v.Frame()
	opens = 0
	if err := v.Click("图片：a"); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatal("read-only Live image did not open on single click")
	}
}
