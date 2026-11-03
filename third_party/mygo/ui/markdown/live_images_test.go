package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
	"testing"
)

func imageNativeTester(source string) (*EditorState, *ui.Tester) {
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	view := ui.NewTester(func(c *ui.Context) {
		Editor(c, s, EditorOptions{InPlaceImages: true, InPlaceCode: true, InPlaceTables: true, RenderedLinks: true, ContinueBlocks: true, SmartEditing: true}).Fill()
	}, 800, 600)
	s.Focus()
	view.Frame()
	return s, view
}

func TestImageReferencePatchPreservesOriginalAndOneUse(t *testing.T) {
	source := "\ufeff---\r\ntitle: unchanged\r\n---\r\n\r\n![a &amp; b][pic] <!-- image:{\"width\":300} -->\r\n\r\n![other][pic]\r\n\r\n[pic]: ./old.png 'Keep title'\r\n<!-- untouched -->"
	s, v := imageNativeTester(source)
	images := s.Images()
	if len(images) != 2 {
		t.Fatalf("images %v", images)
	}
	version := s.Buffer().Version()
	if err := s.ReplaceImage(images[0], images[0].Destination, images[0].Alt); err != nil || s.Buffer().Version() != version {
		t.Fatal("no-op changed source", err)
	}
	if err := s.ReplaceImage(images[0], "./new.png", images[0].Alt); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(source, "![a &amp; b][pic]", "![a &amp; b](<./new.png> \"Keep title\")", 1)
	if s.Buffer().OriginalText() != want {
		t.Fatalf("range patch:\n%q", s.Buffer().OriginalText())
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("replace did not undo")
	}
	if err := s.ReplaceImage(images[0], "new.png", "late"); err == nil {
		t.Fatal("stale reference accepted")
	}
}

func TestImageSelectionDeletionAndVirtualGaps(t *testing.T) {
	const source = "![a](a.png)\n\n![b](b.png)\n\nTail"
	s, v := imageNativeTester(source)
	if err := v.Click("图片：a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.SelectedImage(); !ok {
		t.Fatalf("click did not select: %d %d", s.anchor, s.caret)
	}
	if s.Buffer().OriginalText() != source {
		t.Fatal("selection rewrote source")
	}
	if err := v.Click("在图片前插入内容"); err != nil {
		t.Fatal(err)
	}
	if s.Buffer().OriginalText() != source {
		t.Fatal("virtual gap wrote source")
	}
	v.Type("前面中文😀")
	if !strings.HasPrefix(s.Buffer().String(), "前面中文😀\n\n![a]") {
		t.Fatalf("gap typing %q", s.Buffer().String())
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("gap did not undo")
	}
	info := s.Images()[0]
	s.SetSelection(info.End, info.End)
	s.Focus()
	v.Frame()
	v.Key(0, ui.KeyBackspace)
	if _, ok := s.SelectedImage(); !ok || s.Buffer().OriginalText() != source {
		t.Fatal("adjacent backspace must select first")
	}
	v.Key(0, ui.KeyBackspace)
	if strings.Contains(s.Buffer().String(), "a.png") || !strings.Contains(s.Buffer().String(), "b.png") {
		t.Fatalf("deletion %q", s.Buffer().String())
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("delete did not undo")
	}
	info = s.Images()[0]
	_ = s.SelectImage(info)
	v.Frame()
	v.Key(0, ui.KeyEnter)
	if s.caret <= info.End || s.Buffer().Version() == info.Version {
		t.Fatal("Enter did not create following paragraph")
	}
}

func TestImageBlockInsertionContextsAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		at           int
		want         string
	}{
		{"tail", "hello", 5, "hello\n\n![a](a.png)\n\n"},
		{"middle", "hello world", 5, "hello\n\n![a](a.png)\n\n world"},
		{"reuse", "hello\n\nworld", 5, "hello\n\n![a](a.png)\n\nworld"},
		{"start", "hello", 0, "![a](a.png)\n\nhello"},
		{"empty", "", 0, "![a](a.png)\n\n"},
		{"bold", "**hello world**", 7, "**hello**\n\n![a](a.png)\n\n **world**"},
		{"quote", "> hello", 7, "> hello\n>\n> ![a](a.png)\n>\n> "},
		{"list", "- hello", 7, "- hello\n\n  ![a](a.png)\n\n  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v := imageNativeTester(tc.source)
			s.SetSelection(tc.at, tc.at)
			if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
				t.Fatal(err)
			}
			if got := s.Buffer().OriginalText(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != tc.source {
				t.Fatal("not single undo")
			}
		})
	}
	s, _ := imageNativeTester("text")
	s.SetSelection(4, 4)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)", "![b](b.png)"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Buffer().String(); got != "text\n\n![a](a.png)\n\n![b](b.png)\n\n" {
		t.Fatal(got)
	}
}

func TestImageMovesAreSameLevelAndPreserveAttachment(t *testing.T) {
	const source = "Before\n\n![a](a.png) <!-- image:{\"width\":42} -->\n\nAfter\n\nLast"
	s, v := imageNativeTester(source)
	info := s.Images()[0]
	if err := s.MoveImage(info, 1); err != nil {
		t.Fatal(err)
	}
	got := s.Buffer().String()
	if strings.Index(got, "After") > strings.Index(got, "a.png") || strings.Index(got, "a.png") > strings.Index(got, "Last") {
		t.Fatalf("move: %q", got)
	}
	if !strings.Contains(got, "![a](a.png) <!-- image:{\"width\":42} -->") {
		t.Fatal("metadata changed")
	}
	if selected, ok := s.SelectedImage(); !ok || selected.Destination != "a.png" {
		t.Fatal("move lost image selection")
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("move undo changed original")
	}
	s, _ = imageNativeTester("> ![a](a.png)\n\noutside")
	if s.CanMoveImageTo(s.Images()[0], len(s.source)) {
		t.Fatal("moved image outside quote")
	}
}

func TestImagePasteTableCodeAndSourceBoundaries(t *testing.T) {
	s, _ := imageNativeTester("| A | B |\n| --- | --- |\n| x | y |")
	at := strings.Index(s.source, "x")
	s.SetSelection(at, at)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)", "![b](b.png)"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(s.Buffer().String(), "\n") != 2 || !strings.Contains(s.Buffer().String(), "<br>") {
		t.Fatalf("table broken %q", s.Buffer().String())
	}
	d := parser.Parse(s.Buffer().String())
	if d.Block(1).Kind != parser.Table {
		t.Fatal("table parse lost")
	}
	s, _ = imageNativeTester("plain")
	s.SourceMode = true
	s.SetSelection(5, 5)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
		t.Fatal(err)
	}
	if s.Buffer().String() != "plain![a](a.png)" {
		t.Fatal("source paste acquired paragraphs")
	}
}

func TestImageContainerWritingIMEAndInlineBoundaries(t *testing.T) {
	for _, tc := range []struct {
		source, prefix string
		after          bool
	}{
		{"> ![a](a.png)", "> 中文\n>\n> ![a]", false},
		{"> ![a](a.png)", "> ![a](a.png)\n>\n> 中文", true},
		{"- ![a](a.png)", "- 中文\n\n  ![a]", false},
		{"- ![a](a.png)", "- ![a](a.png)\n\n  中文", true},
	} {
		s, v := imageNativeTester(tc.source)
		s.ImageParagraph(s.Images()[0], tc.after)
		v.Frame()
		v.Type("中文")
		if !strings.HasPrefix(s.Buffer().String(), tc.prefix) {
			t.Fatalf("container writing %q", s.Buffer().String())
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != tc.source {
			t.Fatal("container undo")
		}
	}
	s, v := imageNativeTester("before ![a](a.png)![b](b.png) after")
	images := s.Images()
	s.imagePosition(images[0], true, false)
	v.Frame()
	v.Type("中间")
	if got := s.Buffer().String(); got != "before ![a](a.png)中间![b](b.png) after" {
		t.Fatalf("inline writing %q", got)
	}
	v.Command("undo")
	if err := s.DeleteImage(s.Images()[0]); err != nil {
		t.Fatal(err)
	}
	if got := s.Buffer().String(); got != "before ![b](b.png) after" {
		t.Fatalf("inline delete removed surrounding text %q", got)
	}
}

func TestImageFeatureDefaultsAndReadOnlyGuards(t *testing.T) {
	s := NewEditorState(editor.NewBuffer("![a](a.png)"))
	v := ui.NewTester(func(c *ui.Context) { Editor(c, s, EditorOptions{}).Fill() }, 800, 400)
	v.Frame()
	if _, ok := s.SelectedImage(); ok || s.opts.InPlaceImages {
		t.Fatal("images enabled by default")
	}
	s.opts.InPlaceImages = true
	s.opts.ReadOnly = true
	source := s.Buffer().OriginalText()
	info := s.Images()[0]
	if err := s.ReplaceImage(info, "b.png", "b"); err == nil {
		t.Fatal("read-only image modified")
	}
	if err := s.DeleteImage(info); err == nil {
		t.Fatal("read-only image deleted")
	}
	if s.Buffer().OriginalText() != source {
		t.Fatal("read-only source changed")
	}
}

func TestImageAltEscapingAndHeadingInsertion(t *testing.T) {
	s, _ := imageNativeTester("![a](a.png)")
	if err := s.ReplaceImage(s.Images()[0], "a.png", "a &amp; [b]"); err != nil {
		t.Fatal(err)
	}
	if got := s.Images()[0].Alt; got != "a &amp; [b]" {
		t.Fatalf("alt changed semantic value %q", got)
	}
	s, _ = imageNativeTester("# Heading")
	s.SetSelection(len(s.source), len(s.source))
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Buffer().String(); got != "# Heading\n\n![a](a.png)\n\n" {
		t.Fatalf("heading paste %q", got)
	}
}

func TestImageNestedMovesRetainContainerAndLineEndings(t *testing.T) {
	for _, source := range []string{
		"> Before\n>\n> ![a](a.png)\n>\n> After\n\noutside",
		"- Before\n\n  ![a](a.png)\n\n  After\n\noutside",
		"\ufeffBefore\r\n\r\n![a](a.png)\r\n\r\nAfter",
	} {
		s, v := imageNativeTester(source)
		info := s.Images()[0]
		if err := s.MoveImage(info, 1); err != nil {
			t.Fatalf("move nested down %q: %v", source, err)
		}
		if strings.Index(s.source, "After") > strings.Index(s.source, "a.png") {
			t.Fatalf("did not move down: %q", s.source)
		}
		if _, selected := s.SelectedImage(); !selected {
			t.Fatal("nested move lost selection")
		}
		moved := s.Images()[0]
		if s.editingDocument().Block(moved.Block).Parent == 0 && strings.HasPrefix(source, ">") {
			t.Fatal("image escaped quote")
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("nested move undo changed bytes")
		}
		if err := s.MoveImage(s.Images()[0], -1); err != nil {
			t.Fatal(err)
		}
		if strings.Index(s.source, "a.png") > strings.Index(s.source, "Before") {
			t.Fatalf("did not move up: %q", s.source)
		}
		if strings.HasPrefix(source, "\ufeff") && !strings.HasPrefix(s.Buffer().OriginalText(), "\ufeff") {
			t.Fatal("moving image relocated BOM")
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("up move undo changed bytes")
		}
	}
}

func TestImageGapIMEAndCRLFPasteRemainAtomic(t *testing.T) {
	const source = "\ufeffBefore\r\n\r\n![a](a.png)\r\n\r\nAfter"
	s, v := imageNativeTester(source)
	s.ImageParagraph(s.Images()[0], true)
	v.Frame()
	version := s.Buffer().Version()
	v.Compose("候选😀", 4)
	if s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
		t.Fatal("preedit changed source")
	}
	if _, ok := v.Find("图片：a"); !ok {
		t.Fatal("IME expanded image source")
	}
	v.Type("中文😀")
	if !strings.Contains(s.Buffer().OriginalText(), "\r\n\r\n中文😀") {
		t.Fatalf("IME outside image %q", s.Buffer().OriginalText())
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("IME undo changed source")
	}
	s.SetSelection(s.Images()[0].Start, s.Images()[0].End)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![b](b.png)"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Buffer().OriginalText(), "\ufeffBefore\r\n\r\n") || !strings.HasSuffix(s.Buffer().OriginalText(), "\r\n\r\nAfter") {
		t.Fatal("CRLF/BOM source damaged")
	}
}

func TestImageAdjacentParagraphKeysSelectBeforeDeleting(t *testing.T) {
	for _, tc := range []struct {
		source string
		key    ui.Key
		at     int
	}{
		{"![a](a.png)\n\nText", ui.KeyBackspace, len("![a](a.png)\n\n")},
		{"Text\n\n![a](a.png)", ui.KeyDelete, len("Text")},
		{"> ![a](a.png)\n>\n> Text", ui.KeyBackspace, len("> ![a](a.png)\n>\n> ")},
	} {
		s, v := imageNativeTester(tc.source)
		s.SetSelection(tc.at, tc.at)
		s.Focus()
		v.Frame()
		v.Key(0, tc.key)
		if _, selected := s.SelectedImage(); !selected || s.Buffer().OriginalText() != tc.source {
			t.Fatalf("adjacent key changed source before selecting image: %q", s.Buffer().OriginalText())
		}
	}
}

func TestReviewImageTableAltAndReferenceTitlesRemainLiteral(t *testing.T) {
	const source = "| A | B |\n| --- | --- |\n| ![a](old.png) | keep |"
	s, v := imageNativeTester(source)
	if err := s.ReplaceImage(s.Images()[0], "new.png", "a|b"); err != nil {
		t.Fatal(err)
	}
	images := s.Images()
	if len(images) != 1 || images[0].Alt != "a|b" || !v.HasText("keep") {
		v.Frame()
		if len(images) != 1 || images[0].Alt != "a|b" || !v.HasText("keep") {
			t.Fatalf("table image edit damaged cell: %q images=%+v", s.source, images)
		}
	}
	v.Frame()
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("table image edit did not undo")
	}
	for _, rawTitle := range []string{"&amp;copy;", "a|b", "line one\nline two"} {
		source := "| A | B |\n| --- | --- |\n| ![a][pic] | keep |\n\n[pic]: old.png \"" + rawTitle + "\""
		s, _ := imageNativeTester(source)
		before := s.Images()
		if len(before) != 1 {
			t.Fatalf("invalid title fixture %q", source)
		}
		if err := s.ReplaceImage(before[0], "new.png", "a"); err != nil {
			t.Fatal(err)
		}
		after := s.Images()
		if len(after) != 1 || after[0].Title != before[0].Title || strings.Count(s.source, "\n") != strings.Count(source, "\n") {
			t.Fatalf("title semantics/cell changed: before=%+v after=%+v source=%q", before, after, s.source)
		}
	}
}

func TestReviewImageAtomicExtendedSelectionAndRestoredInsertion(t *testing.T) {
	s, v := imageNativeTester("![a](old.png)")
	_ = s.SelectImage(s.Images()[0])
	v.Frame()
	v.Key(0, ui.KeyEscape)
	v.Key(ui.Shift, ui.KeyLeft)
	if info, ok := s.SelectedImage(); !ok || info.Destination != "old.png" {
		t.Fatalf("extended selection entered image syntax: %d,%d", s.anchor, s.caret)
	}
	v.Key(0, ui.KeyDelete)
	if strings.Contains(s.source, "![") || strings.Contains(s.source, "old.png") {
		t.Fatalf("partial reference remains %q", s.source)
	}
	s, _ = imageNativeTester("![a](old.png)")
	s.SetSelection(7, 7)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![b](new.png)"}); err != nil {
		t.Fatal(err)
	}
	images := s.Images()
	if len(images) != 2 || !strings.Contains(s.source, "![a](old.png)") {
		t.Fatalf("hidden source insertion split image %q", s.source)
	}
}

func TestReviewImageQuoteSeparatorAndFollowingCaret(t *testing.T) {
	s, v := imageNativeTester("> hello\n>\n> world")
	s.SetSelection(len("> hello"), len("> hello"))
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
		t.Fatal(err)
	}
	want := "> hello\n>\n> ![a](a.png)\n>\n> world"
	if s.source != want || s.caret != strings.Index(want, "world") {
		t.Fatalf("quote insertion %q caret=%d want=%d", s.source, s.caret, strings.Index(want, "world"))
	}
	s.Focus()
	v.Frame()
	v.Type("X")
	if s.source != "> hello\n>\n> ![a](a.png)\n>\n> Xworld" {
		t.Fatalf("typing damaged quote marker %q", s.source)
	}
}

func TestReviewImageInlineParagraphActionsAndPreedit(t *testing.T) {
	s, v := imageNativeTester("before ![a](a.png) after")
	_ = s.SelectImage(s.Images()[0])
	v.Frame()
	v.Key(0, ui.KeyEnter)
	if !strings.Contains(s.source, "![a](a.png)\n\n") {
		t.Fatalf("image Enter is only soft break %q", s.source)
	}
	s, v = imageNativeTester("before ![a](a.png) after")
	s.ImageParagraph(s.Images()[0], false)
	v.Frame()
	if s.Buffer().Version() != 0 {
		t.Fatal("virtual paragraph action wrote early")
	}
	v.Type("new")
	if !strings.Contains(s.source, "\n\nnew\n\n![a](a.png)") {
		t.Fatalf("paragraph action stayed inline %q", s.source)
	}
}

func TestReviewImagePreeditPreservesImageAndCandidate(t *testing.T) {
	for _, source := range []string{"before ![a](a.png) after", "> before ![a](a.png) after", "- before ![a](a.png) after", "| A | B |\n| --- | --- |\n| before ![a](a.png) after | keep |"} {
		s, v := imageNativeTester(source)
		at := strings.Index(s.source, "before") + 2
		s.SetSelection(at, at)
		s.Focus()
		v.Frame()
		v.Compose("候选😀", 4)
		if _, ok := v.Find("图片：a"); !ok || !v.HasText("候选😀") || s.Buffer().OriginalText() != source {
			t.Errorf("image/candidate lost during preedit: fixture=%q shown=%v", source, v.Texts())
		}
		v.Type("中文")
		if len(s.Images()) != 1 {
			t.Fatalf("IME corrupted image %q", s.source)
		}
	}
}

func TestReviewImageStaleMoveAndLastDropTarget(t *testing.T) {
	s, _ := imageNativeTester("first\n\nsecond\n\n![a](a.png)")
	info := s.Images()[0]
	if err := s.ReplaceRange(0, len(s.source), "short", s.Buffer().Version()); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("stale MoveImage panicked: %v", r)
			}
		}()
		if err := s.MoveImage(info, 1); err == nil {
			t.Error("stale move accepted")
		}
	}()
	s, _ = imageNativeTester("![a](a.png)\n\nLast")
	found := false
	for _, block := range s.blocks {
		found = found || block.start == len(s.source)
	}
	if !found {
		t.Fatal("no terminal block target for dragging after Last")
	}
}

func TestReviewR2ImageTitleAndNestedFormatting(t *testing.T) {
	source := "![a](a.png (the \"quoted\" title))"
	s, _ := imageNativeTester(source)
	info := s.Images()[0]
	if err := s.ReplaceImage(info, "b.png", info.Alt); err != nil {
		t.Fatal(err)
	}
	if got := s.Images()[0].Title; got != info.Title {
		t.Errorf("parenthesized title lost words: %q want %q", got, info.Title)
	}
	source = "**a __bcde__ f**"
	s, v := imageNativeTester(source)
	at := strings.Index(s.source, "de")
	s.SetSelection(at, at)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![x](x.png)"}); err != nil {
		t.Fatal(err)
	}
	want := "**a __bc__**\n\n![x](x.png)\n\n**__de__ f**"
	if s.source != want {
		t.Errorf("same-kind nesting lost formatting: %q", s.source)
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("nested formatting undo")
	}
}

func TestReviewR2ImageTableParagraphActionsKeepPhysicalRows(t *testing.T) {
	const source = "| A | B |\n| --- | --- |\n| ![a](a.png) | keep |"
	for _, action := range []string{"enter", "before", "after"} {
		s, v := imageNativeTester(source)
		info := s.Images()[0]
		if action == "enter" {
			_ = s.SelectImage(info)
			v.Frame()
			v.Key(0, ui.KeyEnter)
		} else {
			s.ImageParagraph(info, action == "after")
			v.Frame()
			v.Type("new|text")
		}
		if strings.Count(s.source, "\n") != 2 || len(s.Images()) != 1 || !strings.Contains(s.source, "<br>") {
			t.Errorf("%s split table: %q", action, s.source)
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Errorf("%s not one undo %q", action, s.Buffer().OriginalText())
		}
	}
}

func TestReviewR2ImagePrefixedWhitespaceSeparator(t *testing.T) {
	for _, blank := range []string{"> ", ">  ", ">\t"} {
		source := "> hello\n" + blank + "\n> world"
		s, _ := imageNativeTester(source)
		s.SetSelection(len("> hello"), len("> hello"))
		if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
			t.Fatal(err)
		}
		want := "> hello\n>\n> ![a](a.png)\n" + blank + "\n> world"
		if s.source != want || s.caret != strings.Index(want, "world") {
			t.Errorf("blank %q source=%q caret=%d want=%d", blank, s.source, s.caret, strings.Index(want, "world"))
		}
	}
}

func TestReviewR2ImagePreeditAtEdgesAndSelection(t *testing.T) {
	for _, scenario := range []string{"before", "after", "between", "selected", "unrelated"} {
		source := "**bold** ![a](a.png)![b](b.png) after\n\nplain"
		s, v := imageNativeTester(source)
		images := s.Images()
		at := images[0].Start
		switch scenario {
		case "after":
			at = images[1].End
		case "between":
			at = images[0].End
		case "selected":
			_ = s.SelectImage(images[0])
		case "unrelated":
			at = strings.Index(s.source, "plain") + 2
		}
		if scenario != "selected" {
			s.SetSelection(at, at)
		}
		s.Focus()
		v.Frame()
		v.Compose("候选😀", 4)
		if !v.HasText("候选😀") {
			t.Errorf("%s preedit disappeared: %v", scenario, v.Texts())
		}
		if scenario == "unrelated" && v.HasText("**bold**") {
			t.Errorf("unrelated paragraph exposed source: %v", v.Texts())
		}
		if s.Buffer().OriginalText() != source {
			t.Fatal("preedit wrote source")
		}
		v.Type("中文")
	}
}

func TestReviewR3ImagePreeditHasExactlyOneOwner(t *testing.T) {
	for _, source := range []string{"![a](a.png)", "before ![a](a.png) after\n\nbefore ![b](b.png) after"} {
		s, v := imageNativeTester(source)
		s.SetSelection(0, len(s.source))
		s.Focus()
		v.Frame()
		version := s.Buffer().Version()
		v.Compose("候选😀", 4)
		shown := 0
		for _, run := range s.runs {
			shown += strings.Count(run.text, "候选😀")
		}
		if shown != 1 {
			t.Errorf("preedit owners=%d source=%q shown=%v", shown, source, v.Texts())
		}
		if s.Buffer().Version() != version || s.Buffer().OriginalText() != source {
			t.Fatal("preedit committed")
		}
		v.Type("new")
		if s.source != "new" {
			t.Errorf("composition did not commit once %q", s.source)
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("composition not one undo")
		}
	}
}

func TestReviewR3ImageBeforeNextListItemKeepsWritingContainer(t *testing.T) {
	const source = "- hello\n\n- world"
	s, v := imageNativeTester(source)
	s.SetSelection(len("- hello"), len("- hello"))
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
		t.Fatal(err)
	}
	inserted := s.Buffer().OriginalText()
	s.Focus()
	v.Frame()
	v.Type("X")
	if !strings.HasSuffix(s.source, "\n\n- world") || strings.Contains(s.source, "X- world") {
		t.Fatalf("typing after image damaged next item %q", s.source)
	}
	if !strings.Contains(s.source, "\n\n  X") {
		t.Fatalf("new text left original item %q", s.source)
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != inserted {
		t.Fatal("writing undo")
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("insertion undo")
	}
}

func TestReviewR3ImageNestedEmptyHalvesAndLiteralLabel(t *testing.T) {
	const nested = "**__abc__**"
	for _, at := range []int{4, 7} {
		s, _ := imageNativeTester(nested)
		s.SetSelection(at, at)
		if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![x](x.png)"}); err != nil {
			t.Fatal(err)
		}
		want := "![x](x.png)\n\n" + nested
		if at == 7 {
			want = nested + "\n\n![x](x.png)\n\n"
		}
		if s.source != want {
			t.Errorf("nested empty half at %d: %q want %q", at, s.source, want)
		}
	}
	tick := string(rune(96))
	for _, bracket := range []string{"]", "["} {
		source := "![a " + tick + bracket + tick + " b](a.png)"
		s, v := imageNativeTester(source)
		info := s.Images()[0]
		if err := s.ReplaceImage(info, "b.png", info.Alt); err != nil {
			t.Fatal(err)
		}
		images := s.Images()
		if len(images) != 1 || images[0].Alt != info.Alt || s.source != strings.Replace(source, "(a.png)", "(<b.png>)", 1) {
			t.Errorf("literal label corrupted: %q images=%+v", s.source, images)
		}
		v.Frame()
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("literal label undo")
		}
	}
}

func TestReviewR4ImageWritingRedoAndQuotedContainer(t *testing.T) {
	for _, source := range []string{"- hello\n\n- world", "> hello\n>\n> - world"} {
		s, v := imageNativeTester(source)
		at := strings.Index(s.source, "hello") + len("hello")
		s.SetSelection(at, at)
		if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
			t.Fatal(err)
		}
		v.Frame()
		v.Command("undo")
		v.Command("redo")
		s.Focus()
		v.Frame()
		v.Type("X")
		if strings.Contains(s.source, "X- world") || !strings.Contains(s.source, "- world") {
			t.Errorf("redo/quote wrote on list marker: %q", s.source)
		}
		if !strings.Contains(s.source, "  X") && !strings.Contains(s.source, "> X") {
			t.Errorf("writing lost owning container: %q", s.source)
		}
	}
}

func TestReviewR4ImageFormattingRemainsParsedFormatting(t *testing.T) {
	for _, source := range []string{"**hello world**", "**a __abc__**", "**hello\u3000world**"} {
		s, v := imageNativeTester(source)
		at := strings.Index(s.source, " world")
		if strings.Contains(source, "\u3000") {
			at = strings.Index(source, "\u3000")
		}
		if at < 0 {
			at = strings.Index(s.source, "abc")
		}
		s.SetSelection(at, at)
		if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![x](x.png)"}); err != nil {
			t.Fatal(err)
		}
		d := s.editingDocument()
		texts := []string{}
		for block := 1; block < d.Len(); block++ {
			inlines := d.Inlines(block)
			for index, in := range inlines {
				if in.Kind == parser.Strong {
					texts = append(texts, parser.PlainText(inlines[index+1:in.Next]))
				}
			}
		}
		joined := "|" + strings.Join(texts, "|") + "|"
		if strings.Contains(source, "hello") {
			if !strings.Contains(joined, "|hello|") || !strings.Contains(joined, "|world|") {
				t.Errorf("bold flanking lost semantic formatting: source=%q bold=%q", s.source, joined)
			}
		} else {
			if !strings.Contains(joined, "|a|") || !strings.Contains(joined, "|abc|") {
				t.Errorf("nested flanking lost semantic formatting: source=%q bold=%q", s.source, joined)
			}
		}
		v.Frame()
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("semantic formatting edit not one undo")
		}
	}
}

func TestReviewR4ImageCompositionSkipsNestedAltSubtree(t *testing.T) {
	const source = "![outer ![inner](inner.png)](outer.png) rest"
	s, v := imageNativeTester(source)
	at := strings.Index(s.source, "rest") + 2
	s.SetSelection(at, at)
	s.Focus()
	v.Frame()
	v.Compose("候选😀", 4)
	if _, shown := v.Find("图片：inner"); shown || v.HasText("](outer.png)") {
		t.Errorf("nested alt escaped outer image atom: %v", v.Texts())
	}
	if !v.HasText("候选😀") || s.Buffer().OriginalText() != source {
		t.Fatal("nested image composition candidate/source changed")
	}
	v.Type("中文")
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("nested alt composition undo")
	}
}

func TestReviewR4ImageWritingPreservesDraftFencePolicy(t *testing.T) {
	source := strings.Repeat(string(rune(96)), 3) + "\n\n- hello\n\n- world"
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	v := ui.NewTester(func(c *ui.Context) {
		Editor(c, s, EditorOptions{InPlaceImages: true, InPlaceCode: true, ContinueBlocks: true, ConfirmCodeFenceOnEnter: true}).Fill()
	}, 800, 600)
	s.Focus()
	v.Frame()
	s.fenceDrafts = []int{0}
	v.Frame()
	at := strings.Index(source, "hello") + len("hello")
	s.SetSelection(at, at)
	if err := s.InsertImageParagraphsAt(s.CaptureInsertionPoint(), []string{"![a](a.png)"}); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	v.Type("X")
	if !strings.Contains(s.source, "\n- world") || !strings.Contains(s.source, "\n  X") {
		t.Fatalf("draft fence lost the following writing position: %q", s.source)
	}
}

func TestReviewR5InlineImageParagraphHistory(t *testing.T) {
	for _, source := range []string{"before ![a](a.png) after", "| Before ![a](a.png) after |\n| --- |"} {
		for _, after := range []bool{false, true} {
			for _, paste := range []bool{false, true} {
				s, v := imageNativeTester(source)
				s.ImageParagraph(s.Images()[0], after)
				v.Frame()
				if paste {
					if err := s.InsertMarkdownAt(s.CaptureInsertionPoint(), "new"); err != nil {
						t.Fatal(err)
					}
					v.Frame()
				} else {
					v.Type("new")
				}
				written := s.Buffer().OriginalText()
				v.Command("undo")
				if s.Buffer().OriginalText() != source {
					t.Fatalf("undo body %q", s.Buffer().OriginalText())
				}
				point := s.CaptureInsertionPoint()
				if !point.gap.valid || !point.gap.gap.paragraph {
					t.Errorf("lost explicit image paragraph after=%v paste=%v source=%q", after, paste, source)
				}
				v.Command("redo")
				if s.Buffer().OriginalText() != written {
					t.Fatal("redo changed paragraph text")
				}
				v.Command("undo")
				v.Type("X")
				separator := "\n\nX\n\n"
				if strings.HasPrefix(source, "|") {
					separator = "<br>X<br>"
				}
				if !strings.Contains(s.Buffer().OriginalText(), separator) {
					t.Errorf("typing after undo used inline position: %q", s.Buffer().OriginalText())
				}
				v.Command("undo")
				if s.Buffer().OriginalText() != source {
					t.Fatal("continued paragraph requires more than one undo")
				}
			}
		}
	}
}
