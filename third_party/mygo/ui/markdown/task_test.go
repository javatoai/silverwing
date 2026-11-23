package markdown

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// taskCall is a call of Options.OnTask.
type taskCall struct {
	line    int
	checked bool
}

// checkBoxes returns what assistive technology sees of the check boxes of
// the last frame, in reading order.
func checkBoxes(tt *ui.Tester) []ui.AccessInfo {
	var boxes []ui.AccessInfo
	for _, a := range tt.AccessAll() {
		if a.Role == ui.RoleCheckBox {
			boxes = append(boxes, a)
		}
	}
	return boxes
}

// clickBox clicks the center of a check box.
func clickBox(tt *ui.Tester, a ui.AccessInfo) {
	tt.ClickAt(a.Bounds.X+a.Bounds.W/2, a.Bounds.Y+a.Bounds.H/2)
}

// lines joins ls with sep, ending the last one too: line k of the
// result, from 1, is ls[k-1].
func lines(sep string, ls ...string) string { return strings.Join(ls, sep) + sep }

// toggleTask checks or unchecks the task on line of src, from 1, as an
// app's OnTask writes the change into its Markdown.
func toggleTask(src string, line int, checked bool) string {
	ls := strings.SplitAfter(src, "\n")
	if checked {
		ls[line-1] = strings.Replace(ls[line-1], "[ ]", "[x]", 1)
	} else {
		ls[line-1] = strings.Replace(ls[line-1], "[x]", "[ ]", 1)
	}
	return strings.Join(ls, "")
}

// TestTaskClickReportsLineAndState clicks every check box of a note whose
// tasks come after front matter and a code block holding what looks like
// a task, in nested lists, in an ordered list and in a quote: each reports
// its item's line in the source and the other state, whatever the line
// endings.
func TestTaskClickReportsLineAndState(t *testing.T) {
	note := []string{
		"---",                    // 1, front matter: a rule, then a heading
		"title: Groceries",       // 2
		"tags: [home]",           // 3
		"---",                    // 4
		"",                       // 5
		"```md",                  // 6
		"- [ ] in code, no task", // 7
		"```",                    // 8
		"",                       // 9
		"- [ ] milk",             // 10
		"- [x] eggs",             // 11
		"  - [ ] free range",     // 12
		"    1. [X] a dozen",     // 13
		"- bread",                // 14, no task
		"",                       // 15
		"> - [ ] quoted",         // 16
	}
	want := []taskCall{{10, true}, {11, false}, {12, true}, {13, false}, {16, true}}
	names := []string{"milk", "eggs", "free range", "a dozen", "quoted"}
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		var got []taskCall
		o := Options{OnTask: func(line int, checked bool) { got = append(got, taskCall{line, checked}) }}
		src := lines(sep, note...)
		tt := ui.NewTester(func(c *ui.Context) { View(c, src, &o) }, 500, 700)
		boxes := checkBoxes(tt)
		if len(boxes) != len(want) {
			t.Fatalf("%q: %d check boxes, want %d", sep, len(boxes), len(want))
		}
		for k, a := range boxes {
			if a.Name != names[k] || a.Checked == want[k].checked || !a.Focusable || a.ReadOnly {
				t.Errorf("%q: box %d is %+v, want %q, checked %v, focusable and not read-only", sep, k, a, names[k], !want[k].checked)
			}
			clickBox(tt, a)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: OnTask got %v, want %v", sep, got, want)
		}
	}
}

// TestTaskKeysToggle moves the focus to the check boxes with Tab and
// toggles them with Space and Enter, the app writing each change into the
// source, which the boxes then show.
func TestTaskKeysToggle(t *testing.T) {
	src := lines("\n", "Intro.", "", "- [ ] one", "  - [x] two")
	var got []taskCall
	o := Options{OnTask: func(line int, checked bool) {
		got = append(got, taskCall{line, checked})
		src = toggleTask(src, line, checked)
	}}
	tt := ui.NewTester(func(c *ui.Context) { View(c, src, &o) }, 400, 300)
	focus := func(k int) {
		t.Helper()
		for range 5 {
			if checkBoxes(tt)[k].Focused {
				return
			}
			tt.Key(0, ui.KeyTab)
		}
		t.Fatalf("Tab never reached box %d: %+v", k, checkBoxes(tt))
	}
	focus(0)
	tt.TypeKey(0, ui.KeySpace, " ")
	if b := checkBoxes(tt); !b[0].Checked || !b[0].Focused {
		t.Fatalf("after Space, the first box is %+v, want checked and focused", b[0])
	}
	tt.Key(0, ui.KeyEnter)
	if b := checkBoxes(tt); b[0].Checked {
		t.Fatalf("after Enter, the first box is %+v, want unchecked", b[0])
	}
	focus(1)
	tt.Key(0, ui.KeyEnter)
	want := []taskCall{{3, true}, {3, false}, {4, false}}
	if !slices.Equal(got, want) {
		t.Errorf("OnTask got %v, want %v", got, want)
	}
	if wantSrc := lines("\n", "Intro.", "", "- [ ] one", "  - [ ] two"); src != wantSrc {
		t.Errorf("source %q, want %q", src, wantSrc)
	}
	if b := checkBoxes(tt); b[0].Checked || b[1].Checked {
		t.Errorf("the boxes show %+v, not the source", b)
	}
}

// TestTaskNameFollowsItsText checks that a check box is named by its
// task's text as it is now: after an edit, after a definition appended
// below makes its text a link, and of formatted text, its text alone.
func TestTaskNameFollowsItsText(t *testing.T) {
	src := "- [ ] milk\n- [ ] **oat** `milk`\n"
	o := Options{OnTask: func(int, bool) {}}
	tt := ui.NewTester(func(c *ui.Context) { View(c, src, &o) }, 400, 200)
	for _, edit := range []struct{ src, second string }{
		{src, "oat milk"},
		{"- [ ] milk\n- [ ] [silk][s]\n", "[silk][s]"},
		{"- [ ] milk\n- [ ] [silk][s]\n\n[s]: https://example.com\n", "silk"},
		{"- [ ] milk\n- [ ] [wool][s]\n\n[s]: https://example.com\n", "wool"},
	} {
		src = edit.src
		tt.Frame()
		if b := checkBoxes(tt); len(b) != 2 || b[0].Name != "milk" || b[1].Name != edit.second {
			t.Errorf("after %q, the boxes are named %+v, want %q and %q", edit.src, b, "milk", edit.second)
		}
	}
}

// TestTasksWithoutOnTaskAreReadOnly checks that check boxes without
// Options.OnTask show their state, read-only, and take neither the focus
// nor clicks.
func TestTasksWithoutOnTaskAreReadOnly(t *testing.T) {
	src := "- [ ] milk\n- [x] eggs\n"
	for _, o := range []*Options{nil, {OnLink: func(string) {}}} {
		tt := ui.NewTester(func(c *ui.Context) { View(c, src, o) }, 400, 200)
		boxes := checkBoxes(tt)
		if len(boxes) != 2 || boxes[0].Checked || !boxes[1].Checked {
			t.Fatalf("boxes %+v, want milk unchecked and eggs checked", boxes)
		}
		for _, a := range boxes {
			if !a.ReadOnly || a.Focusable {
				t.Errorf("box %+v: want read-only and not focusable", a)
			}
			clickBox(tt, a)
		}
		tt.Key(0, ui.KeyTab)
		for _, a := range checkBoxes(tt) {
			if a.Focused {
				t.Errorf("Tab focused the read-only box %+v", a)
			}
		}
		if b := checkBoxes(tt); b[0].Checked || !b[1].Checked {
			t.Errorf("clicks changed the boxes: %+v", b)
		}
	}
}

// TestCodeBlockSourceLine checks the line each code block starts on:
// fences at the start, after a paragraph, of tildes, in a list item and
// in a quote, indented code, and a fence still open, with each line ending.
// The lines of their code follow it.
func TestCodeBlockSourceLine(t *testing.T) {
	doc := []string{
		"```a",         // 1
		"first",        // 2
		"```",          // 3
		"",             // 4
		"Text.",        // 5
		"```b",         // 6
		"second",       // 7
		"```",          // 8
		"~~~c",         // 9
		"third",        // 10
		"~~~",          // 11
		"",             // 12
		"- item",       // 13
		"",             // 14
		"  ```d",       // 15
		"  in a list",  // 16
		"  ```",        // 17
		"",             // 18
		"> ```e",       // 19
		"> quoted",     // 20
		"> ```",        // 21
		"",             // 22
		"    indented", // 23
		"",             // 24
		"```f",         // 25
		"open",         // 26
	}
	want := map[string]int{"a": 1, "b": 6, "c": 9, "d": 15, "e": 19, "": 23, "f": 25}
	for _, sep := range []string{"\n", "\r\n", "\r"} {
		got := map[string]int{}
		o := Options{CodeBlock: func(c *ui.Context, b *CodeBlockView) {
			line := b.SourceLine()
			got[b.Info] = line
			// The code's first line is on the line after a fence, and on
			// the block's own for indented code.
			first := line
			if b.Info != "" {
				first++
			}
			if code := strings.SplitN(b.Code(), "\n", 2)[0]; !strings.HasSuffix(doc[first-1], code) {
				t.Errorf("%q: the code of %q starts with %q, not line %d, %q", sep, b.Info, code, first, doc[first-1])
			}
			DefaultCodeBlock(c, b)
		}}
		src := strings.Join(doc, sep)
		ui.NewTester(func(c *ui.Context) { View(c, src, &o) }, 500, 900)
		for info, line := range want {
			if got[info] != line {
				t.Errorf("%q: block %q starts on line %d, want %d", sep, info, got[info], line)
			}
		}
	}
}

// TestNestedViewTasksMapToTheSource builds a code block as a view of the
// Markdown in it, whose OnTask adds the block's SourceLine to its lines.
func TestNestedViewTasksMapToTheSource(t *testing.T) {
	src := lines("\n", "# Plan", "", "```tasks", "- [ ] write", "- [x] test", "```", "", "- [ ] ship")
	var got []taskCall
	var inner Options
	o := Options{
		OnTask: func(line int, checked bool) { got = append(got, taskCall{line, checked}) },
		Code: map[string]func(*ui.Context, *CodeBlockView){"tasks": func(c *ui.Context, b *CodeBlockView) {
			start := b.SourceLine()
			inner.OnTask = func(line int, checked bool) { got = append(got, taskCall{start + line, checked}) }
			View(c, b.Code(), &inner)
		}},
	}
	tt := ui.NewTester(func(c *ui.Context) { View(c, src, &o) }, 400, 400)
	for _, a := range checkBoxes(tt) {
		clickBox(tt, a)
	}
	if want := []taskCall{{4, true}, {5, false}, {8, true}}; !slices.Equal(got, want) {
		t.Errorf("OnTask got %v, want %v", got, want)
	}
}

func TestLineAt(t *testing.T) {
	for _, tc := range []struct {
		src  string
		off  int
		want int
	}{
		{"", 0, 1},
		{"abc", 2, 1},
		{"a\nb", 2, 2},
		{"a\r\nb", 3, 2},
		{"a\r\nb", 2, 1}, // within the line's end
		{"a\rb", 2, 2},
		{"a\r\r\nb\nc", 6, 4},
	} {
		if got := lineAt(tc.src, tc.off); got != tc.want {
			t.Errorf("lineAt(%q, %d) = %d, want %d", tc.src, tc.off, got, tc.want)
		}
	}
}
