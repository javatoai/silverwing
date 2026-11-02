package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

const codeTailLabel = "在代码块后插入内容"

func TestLiveCodeTailWritingRowDoesNotChangeSourceOnFocus(t *testing.T) {
	for _, suffix := range []string{"", "\n", "\r\n", "\r\n\r\n\r\n"} {
		t.Run(strings.ReplaceAll(suffix, "\n", "LF"), func(t *testing.T) {
			ending := "\n"
			if strings.Contains(suffix, "\r") {
				ending = "\r\n"
			}
			source := strings.ReplaceAll("\ufeff```js title=kept\nconsole.log(1);\n```", "\n", ending) + suffix
			s, view := keyboardViewportTester(source, strings.Index(source, "console"), 700, 1, false, false)
			card, ok := view.Find("Code, js")
			gap, found := view.Find(codeTailLabel)
			if !ok || !found || gap.Y < card.Y+card.H-.1 || gap.H < 12 {
				t.Fatal("last closed code card has no usable outside-code writing row")
			}
			view.ClickAt(gap.X+12, gap.Y+min(float32(12), gap.H/2))
			if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source || s.focusedCodeGap() == nil {
				t.Fatal("tail click wrote bytes or failed to focus its own source position")
			}
			caret, active := view.TextCaret()
			if !active || caret.Y < gap.Y-.1 || caret.Y+caret.H > gap.Y+gap.H+.1 {
				t.Fatalf("tail has no actual native caret: %+v row=%+v", caret, gap)
			}
			s.client.ReplaceText(nil, "")
			view.Frame()
			if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
				t.Fatal("empty tail input materialized source")
			}
		})
	}
}

func TestLiveCodeTailTypingPasteIMEAndEnterAreOutsideTheFence(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		for _, newline := range []bool{false, true} {
			for _, operation := range []string{"type", "paste", "ime", "enter", "cancel"} {
				t.Run(operation+strings.ReplaceAll(ending, "\n", "LF")+map[bool]string{false: "-none", true: "-eol"}[newline], func(t *testing.T) {
					source := strings.ReplaceAll("\ufeff```js title=kept\nconsole.log(1);\n```", "\n", ending)
					if newline {
						source += ending
					}
					s, view := keyboardViewportTester(source, strings.Index(source, "console"), 700, 1, false, false)
					if err := view.Click(codeTailLabel); err != nil {
						t.Fatal(err)
					}
					buffer := s.Buffer()
					payload := "尾段😀"
					switch operation {
					case "paste":
						payload = "粘贴😀" + ending + "下一行"
						view.SetClipboard(payload)
						view.Command("paste")
					case "ime", "cancel":
						view.Compose("候选😀", 4)
						if buffer.Version() != 0 || buffer.OriginalText() != source {
							t.Fatal("tail preedit committed source")
						}
						shown := 0
						for _, run := range s.runs {
							shown += strings.Count(run.text, "候选😀")
						}
						if shown != 1 {
							t.Fatalf("tail preedit displayed %d times", shown)
						}
						if operation == "cancel" {
							s.client.SetMarkedText(nil, "", ui.TextInputRange{})
							s.client.UnmarkText()
							view.Frame()
							if buffer.Version() != 0 || buffer.OriginalText() != source {
								t.Fatal("cancelled tail preedit wrote source")
							}
							return
						}
						view.Type(payload)
					case "enter":
						payload = ending
						view.Key(0, ui.KeyEnter)
					default:
						view.Type(payload)
					}
					separator := ""
					if !newline {
						separator = ending
					}
					want := source + separator + payload
					if buffer.OriginalText() != want || buffer.Version() != 1 || s.Buffer() != buffer {
						t.Fatalf("tail edit lost exact outside-fence transaction: %q want %q", buffer.OriginalText(), want)
					}
					d := parser.Parse(buffer.Snapshot().String())
					codes, paragraph := 0, false
					for i := 1; i < d.Len(); i++ {
						b := d.Block(i)
						if b.Kind == parser.CodeBlock {
							codes++
							if d.Lines(i) != "console.log(1);\n" || b.Open {
								t.Fatal("tail input changed code body or closing fence")
							}
						}
						if b.Kind == parser.Paragraph {
							paragraph = true
						}
					}
					if codes != 1 || operation != "enter" && !paragraph {
						t.Fatal("tail input was not outside the closed code card")
					}
					view.Command("undo")
					if buffer.OriginalText() != source {
						t.Fatal("tail edit requires more than one undo")
					}
					view.Command("redo")
					if buffer.OriginalText() != want {
						t.Fatal("tail redo changed source bytes")
					}
				})
			}
		}
	}
}

func TestLiveCodeTailNavigationAndNewFenceRegenerateTheWritingRow(t *testing.T) {
	const source = "```js\nconsole.log(1);\n```"
	s, view := keyboardViewportTester(source, strings.Index(source, "console")+len("console.log(1);"), 700, 1, false, false)
	view.Key(0, ui.KeyDown)
	if s.focusedCodeGap() == nil || s.caret != len(source) {
		t.Fatal("Down did not leave the code body for the tail writing row")
	}
	view.Key(0, ui.KeyDown)
	view.Key(0, ui.KeyRight)
	if s.caret != len(source) || s.Buffer().Version() != 0 {
		t.Fatal("tail navigation crossed EOF or wrote source")
	}
	view.Key(0, ui.KeyUp)
	if s.caret != strings.Index(source, "console")+len("console.log(1);") {
		t.Fatal("Up from tail did not return to the previous code body")
	}
	if err := view.Click(codeTailLabel); err != nil {
		t.Fatal(err)
	}
	view.Type("```python\nprint(2)\n```")
	gap, ok := view.Find(codeTailLabel)
	card, exists := view.Find("Code, python")
	if !ok || !exists || gap.Y < card.Y+card.H-.1 {
		t.Fatal("new final closed fence did not get a fresh writing row")
	}
	view.ClickAt(gap.X+12, gap.Y+gap.H/2)
	view.Type("after second")
	if !strings.HasSuffix(s.Buffer().OriginalText(), "```\nafter second") {
		t.Fatal("typing after a newly appended code card entered its body")
	}
}

func TestLiveCodeTailKeepsReadonlySourceAndUnclosedContracts(t *testing.T) {
	for _, variant := range []string{"readonly", "source", "unclosed", "limit"} {
		t.Run(variant, func(t *testing.T) {
			source := "```js\nconsole.log(1);\n```"
			if variant == "unclosed" {
				source = "```js\nconsole.log(1);"
			}
			s, view := keyboardViewportTester(source, strings.Index(source, "console"), 700, 1, variant == "source", variant == "readonly")
			gap, found := view.Find(codeTailLabel)
			if variant == "source" || variant == "unclosed" {
				if found {
					t.Fatal("literal/source or open code acquired an outside-code tail")
				}
				return
			}
			if !found {
				t.Fatal("closed code has no tail row")
			}
			view.ClickAt(gap.X+12, gap.Y+gap.H/2)
			if variant == "limit" {
				s.Buffer().SetMaxSourceBytes(len(source) + len("tail"))
			}
			view.Type("tail")
			if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 {
				t.Fatal("readonly/oversize tail edit wrote partial source")
			}
		})
	}
}
