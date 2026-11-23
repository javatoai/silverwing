package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// files is a tree of paths: a directory's children, nil for a file.
var files = map[string][]string{
	"src":       {"src/ui", "src/main.go"},
	"src/ui":    {"src/ui/list.go", "src/ui/tree.go"},
	"docs":      {},
	"README.md": nil,
}

func children(path string) []string { return files[path] }

func baseName(path string) string { return path[strings.LastIndexByte(path, '/')+1:] }

func TestOutline(t *testing.T) {
	sel := -1
	var s OutlineState[string]
	s.List.Selected = &sel
	roots := []string{"src", "docs", "README.md"}
	tt := coreNewTester(func(c *context) {
		coreOutline(c, &s, roots, children, func(path string) {
			coreText(c, baseName(path))
		}).Grow(1)
	}, 400, 400)
	tt.SetPreferences(Preferences{ReduceMotion: true, TextScale: 1})
	if tt.HasText("main.go") {
		t.Fatal("a closed item shows its children")
	}
	// A click on the arrow opens; the row is not chosen.
	src, _ := tt.Find("src")
	tt.ClickAt(src.X-10, src.Y+src.H/2)
	if !s.Open.Has("src") || !tt.HasText("main.go") || sel != -1 {
		t.Fatalf("a click on the arrow: open %v, shows %q, chose %d", s.Open.Has("src"), tt.Texts(), sel)
	}
	// Right opens the item chosen, then goes to its first child; Left goes
	// to the parent, then closes it.
	tt.Click("ui")
	steps := []struct {
		mods   Modifiers
		key    Key
		chosen string
		open   bool
	}{
		{0, KeyRight, "src/ui", true},
		{0, KeyRight, "src/ui/list.go", true},
		{0, KeyLeft, "src/ui", true},
		{0, KeyLeft, "src/ui", false},
		{0, KeyLeft, "src", false},
		{0, KeyLeft, "src", false},
		{allMod, KeyRight, "src", true},
	}
	for _, step := range steps {
		tt.Key(step.mods, step.key)
		if got := s.Item(sel); got != step.chosen || s.Open.Has("src/ui") != step.open {
			t.Fatalf("%v %v: chose %q, src/ui open %v", step.mods, step.key, got, s.Open.Has("src/ui"))
		}
	}
	// Option opened all inside src, which closes with them.
	tt.Key(allMod, KeyLeft)
	if s.Open.Has("src") || s.Open.Has("src/ui") {
		t.Fatalf("Option-Left left open %v %v", s.Open.Has("src"), s.Open.Has("src/ui"))
	}
	// Closing an item moves the choice to it from a row inside.
	tt.Key(allMod, KeyRight)
	tt.Click("tree.go")
	src, _ = tt.Find("src")
	tt.ClickAt(src.X-10, src.Y+src.H/2)
	if s.Item(sel) != "src" || tt.HasText("tree.go") {
		t.Errorf("after closing src, chose %q", s.Item(sel))
	}
	// Assistive technology: a tree of items with their levels, open or not.
	tt.Key(allMod, KeyRight)
	tt.send(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	tree := tt.h.access
	accessNode(t, tree, platform.RoleTree, "")
	for _, want := range []struct {
		name  string
		level int
		state platform.AccessStates
	}{
		{"src", 1, platform.AccessExpandable | platform.AccessExpanded},
		{"ui", 2, platform.AccessExpandable | platform.AccessExpanded},
		{"list.go", 3, 0},
		{"docs", 1, platform.AccessExpandable},
		{"README.md", 1, 0},
	} {
		n := accessNode(t, tree, platform.RoleTreeItem, want.name)
		if got := n.States & (platform.AccessExpandable | platform.AccessExpanded); n.Level != want.level || got != want.state {
			t.Errorf("%s: level %d, states %b", want.name, n.Level, got)
		}
	}
}

// Each level of an outline adds Indent before its rows' arrows, the
// theme's Space(4) when zero.
func TestOutlineIndent(t *testing.T) {
	for _, indent := range []float32{0, 6} {
		s := OutlineState[string]{Indent: indent}
		s.Open.Add("src")
		s.Open.Add("src/ui")
		tt := coreNewTester(func(c *context) {
			coreOutline(c, &s, []string{"src"}, children, func(path string) {
				coreText(c, baseName(path))
			}).Grow(1)
		}, 400, 400)
		parent, _ := tt.Find("ui")
		child, ok := tt.Find("list.go")
		want := indent
		if want == 0 {
			want = LightTheme().Space(4)
		}
		if got := child.X - parent.X; !ok || got != want {
			t.Errorf("Indent %v: the child %v right of its parent, want %v", indent, got, want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("a negative Indent did not panic")
		}
	}()
	s := OutlineState[string]{Indent: -1}
	coreNewTester(func(c *context) { coreOutline(c, &s, []string{"src"}, children, func(string) {}) }, 400, 400)
}

func TestOutlineBuildsWhatShows(t *testing.T) {
	sel := -1
	var s OutlineState[int]
	s.List.Selected = &sel
	const n = 100000
	kids := make([]int, n)
	for i := range kids {
		kids[i] = i + 1
	}
	built := 0
	tt := coreNewTester(func(c *context) {
		coreOutline(c, &s, []int{0}, func(item int) []int {
			if item == 0 {
				return kids
			}
			return nil
		}, func(item int) {
			built++
			coreTextf(c, "Item %d", item)
		}).Grow(1)
	}, 400, 400)
	tt.SetPreferences(Preferences{ReduceMotion: true, TextScale: 1})
	s.Open.Add(0)
	tt.Frame()
	built = 0
	tt.Frame()
	if s.Rows() != n+1 || built == 0 || built > 40 {
		t.Fatalf("%d rows, built %d", s.Rows(), built)
	}
	tt.Click("Item 3")
	tt.Key(0, KeyEnd)
	if sel != n || !tt.HasText(fmt.Sprintf("Item %d", n)) {
		t.Errorf("End chose %d", sel)
	}
	// The choice follows its item as rows close above it.
	tt.Key(0, KeyHome)
	tt.Key(0, KeyDown)
	tt.Key(0, KeyDown)
	if s.Item(sel) != 2 {
		t.Fatalf("chose %d", s.Item(sel))
	}
}

func TestOutlineTable(t *testing.T) {
	sel := -1
	sort := SortOrder{Column: "Name"}
	var s OutlineState[string]
	s.List.Selected, s.List.Sort = &sel, &sort
	roots := []string{"src", "docs", "README.md"}
	cols := []TableColumn{{Title: "Name", Sortable: true}, {Title: "Kind", Width: 100}}
	tt := coreNewTester(func(c *context) {
		coreOutlineTable(c, &s, cols, roots, children, func(path string, col int) {
			if col == 0 {
				coreText(c, baseName(path)).SingleLine()
			} else if files[path] != nil {
				coreText(c, "Folder")
			} else {
				coreText(c, "File")
			}
		}).Grow(1)
	}, 400, 400)
	tt.SetPreferences(Preferences{ReduceMotion: true, TextScale: 1})
	tt.Click("src")
	tt.Key(0, KeyRight)
	tt.Key(0, KeyRight)
	tt.Key(0, KeyRight)
	if s.Item(sel) != "src/ui" || !s.Open.Has("src/ui") || !tt.HasText("list.go") {
		t.Fatalf("chose %q", s.Item(sel))
	}
	// The first column is indented as its items are deep.
	ui, _ := tt.Find("ui")
	srcText, _ := tt.Find("src")
	if ui.X-srcText.X != 16 {
		t.Errorf("ui is %v right of src", ui.X-srcText.X)
	}
	tt.send(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	tree := tt.h.access
	accessNode(t, tree, platform.RoleColumnHeader, "Name")
	if n := accessNode(t, tree, platform.RoleTreeItem, "ui"); n.Level != 2 || n.States&platform.AccessChecked == 0 {
		t.Errorf("ui: %+v", n)
	}
	// Sorting is the app's, the header shows it.
	tt.Click("Name")
	if !sort.Descending {
		t.Error("a click on Name did not reverse the order")
	}
}

func TestOutlineExpandsForAssistiveTechnology(t *testing.T) {
	sel := -1
	var s OutlineState[string]
	s.List.Selected = &sel
	open := false
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Fill().Children(func() {
			coreOutline(c, &s, []string{"src", "README.md"}, children, func(path string) {
				coreText(c, baseName(path))
			}).Grow(1)
			coreTree(c, func() {
				coreTreeItem(c, "lib", &open, func() { coreTreeItem(c, "util.go", nil, nil) })
			})
		})
	}, 400, 400)
	tt.send(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	act := func(name string, a platform.AccessActionKind) {
		t.Helper()
		n := accessNode(t, tt.h.access, platform.RoleTreeItem, name)
		if n.Actions&platform.ActionExpand == 0 {
			t.Fatalf("%s cannot expand: %+v", name, n)
		}
		tt.send(platform.SurfaceEvent{Kind: platform.AccessAction, ID: n.ID, Action: a})
	}
	if n := accessNode(t, tt.h.access, platform.RoleTreeItem, "README.md"); n.Actions&platform.ActionExpand != 0 {
		t.Errorf("a file can expand")
	}
	// Opening and closing, not choosing, which pressing does.
	act("src", platform.AccessExpand)
	if !s.Open.Has("src") || sel != -1 || !tt.HasText("main.go") {
		t.Fatalf("expanding src: open %v, chose %d", s.Open.Has("src"), sel)
	}
	act("src", platform.AccessCollapse)
	if s.Open.Has("src") {
		t.Fatal("collapsing src left it open")
	}
	act("lib", platform.AccessExpand)
	if !open || !tt.HasText("util.go") {
		t.Fatal("expanding a tree's item")
	}
}

// An outline's rows build their arrows without closures of their own in
// every frame: with a Prefix that builds nothing, thirty rows with
// children allocate as much a frame as ten, and with one that builds the
// arrow, each row adds its arrow's click handler alone.
func TestOutlineRowAllocs(t *testing.T) {
	if testing.Short() {
		t.Skip("measures")
	}
	kids := []int{1000}
	frame := func(rows int, prefix func(r OutlineRow)) float64 {
		var s OutlineState[int]
		s.Prefix = prefix
		roots := make([]int, rows)
		for i := range roots {
			roots[i] = i
		}
		tt := coreNewTester(func(c *context) {
			coreOutline(c, &s, roots, func(int) []int { return kids }, func(int) {
				coreText(c, "row")
			}).FillWidth().Height(800)
		}, 300, 800)
		defer tt.rt.close()
		tt.Frame()
		return testing.AllocsPerRun(50, tt.Frame)
	}
	for _, tc := range []struct {
		name   string
		prefix func(r OutlineRow)
		perRow float64
	}{
		{"nothing", func(OutlineRow) {}, 0},
		{"its arrow", func(r OutlineRow) { r.Arrow().Size(16, 16) }, 1},
	} {
		few, many := frame(10, tc.prefix), frame(30, tc.prefix)
		if perRow := (many - few) / 20; perRow > tc.perRow {
			t.Errorf("a Prefix building %s: a row allocates %.1f times a frame, want at most %.0f", tc.name, perRow, tc.perRow)
		}
	}
}

// bigOutline is an outline of folders of files, every folder open, as a
// change set of a work tree shown whole: folders at the top, items
// 0 to folders-1, each with files of its own, and a count of the calls of
// children.
type bigOutline struct {
	s      OutlineState[int]
	roots  []int
	kids   []int
	files  int
	calls  int
	tester *Tester
}

func newBigOutline(folders, files int, generation uint64) *bigOutline {
	o := &bigOutline{roots: make([]int, folders), kids: make([]int, folders*files), files: files}
	o.s.Generation = generation
	for i := range o.roots {
		o.roots[i] = i
		o.s.Open.Add(i)
	}
	for i := range o.kids {
		o.kids[i] = folders + i
	}
	o.tester = coreNewTester(func(c *context) {
		coreOutline(c, &o.s, o.roots, o.children, func(item int) {
			coreTextf(c, "Item %d", item)
		}).Grow(1)
	}, 400, 800)
	o.tester.SetPreferences(Preferences{ReduceMotion: true, TextScale: 1})
	return o
}

func (o *bigOutline) children(item int) []int {
	o.calls++
	if item < len(o.roots) {
		return o.kids[item*o.files : (item+1)*o.files]
	}
	return nil
}

// With a Generation, an outline keeps its rows while nothing they come
// from changes: scrolling a tree of thousands of open items calls children
// for none, where without one it walks them all in every frame.
func TestOutlineKeepsRows(t *testing.T) {
	for _, generation := range []uint64{0, 1} {
		o := newBigOutline(1000, 20, generation)
		tt := o.tester
		o.calls = 0
		for range 5 {
			tt.Scroll(200, 400, 0, 60)
		}
		rows := 1000 * 21
		if want := 5 * rows * int(1-generation); o.calls != want || o.s.Rows() != rows {
			t.Errorf("Generation %d: %d rows, %d calls of children in 5 frames, want %d", generation, o.s.Rows(), o.calls, want)
		}
		tt.rt.close()
	}
}

// Without a Generation, an outline shows what children returns in the
// next frame, as it always did.
func TestOutlineWithoutGenerationFollowsChildren(t *testing.T) {
	tree := map[string][]string{"a": {"a/1"}}
	var s OutlineState[string]
	s.Open.Add("a")
	tt := coreNewTester(func(c *context) {
		coreOutline(c, &s, []string{"a"}, func(k string) []string { return tree[k] }, func(k string) {
			coreText(c, k)
		}).Grow(1)
	}, 400, 400)
	tree["a"] = append(tree["a"], "a/2")
	tt.Frame()
	if !tt.HasText("a/2") || s.Rows() != 3 {
		t.Errorf("a new child: %d rows, shows %q", s.Rows(), tt.Texts())
	}
}

// With a Generation, an outline walks its items again when Open, the
// roots or the Generation change, however they change.
func TestOutlineGeneration(t *testing.T) {
	tree := map[string][]string{
		"a":   {"a/1", "a/b"},
		"a/b": {"a/b/1"},
		"c":   {"c/1"},
		"d":   {},
	}
	roots := []string{"a", "c"}
	sel := -1
	s := OutlineState[string]{Generation: 1}
	s.List.Selected = &sel
	tt := coreNewTester(func(c *context) {
		coreOutline(c, &s, roots, func(k string) []string { return tree[k] }, func(k string) {
			coreText(c, k)
		}).Grow(1)
	}, 400, 400)
	tt.SetPreferences(Preferences{ReduceMotion: true, TextScale: 1})
	rows := func(step string) {
		t.Helper()
		got := make([]string, s.Rows())
		for i := range got {
			got[i] = s.Item(i)
		}
		if shown := strings.Join(got, " "); shown != step[strings.IndexByte(step, ':')+2:] {
			t.Fatalf("%s: rows %s", step[:strings.IndexByte(step, ':')], shown)
		}
	}
	rows("at first: a c")
	// The arrow, the keys and the app open and close items.
	a, _ := tt.Find("a")
	tt.ClickAt(a.X-10, a.Y+a.H/2)
	rows("the arrow: a a/1 a/b c")
	tt.Click("a/b")
	tt.Key(0, KeyRight)
	rows("Right: a a/1 a/b a/b/1 c")
	s.Open.Add("c")
	tt.Frame()
	rows("Open.Add: a a/1 a/b a/b/1 c c/1")
	s.Open.Remove("a")
	tt.Frame()
	rows("Open.Remove: a c c/1")
	s.Open.Clear()
	tt.Frame()
	rows("Open.Clear: a c")
	// Another Selection put in Open's place, even one changed as often.
	var other Selection[string]
	other.Add("a")
	s.Open = other
	tt.Frame()
	rows("another Open: a a/1 a/b c")
	s.Open = Selection[string]{}
	tt.Frame()
	rows("an empty Open: a c")
	// Other roots, or the same slice changed.
	roots = []string{"c", "d", "a"}
	tt.Frame()
	rows("other roots: c d a")
	roots[1], roots[2] = roots[2], roots[1]
	tt.Frame()
	rows("roots changed in place: c a d")
	roots = roots[:2]
	tt.Frame()
	rows("fewer roots: c a")
	// children changing shows once Generation does.
	s.Open.Add("c")
	tt.Frame()
	tree["c"] = []string{"c/1", "c/2"}
	tt.Frame()
	rows("children changed, Generation not: c c/1 a")
	s.Generation++
	tt.Frame()
	rows("Generation changed: c c/1 c/2 a")
	// Back to zero, it walks in every frame.
	s.Generation = 0
	tree["c"] = nil
	tt.Frame()
	rows("Generation zero: c a")
}

// A Selection's stamp changes when its keys do, and only then, to a value
// no other Selection had.
func TestSelectionStamp(t *testing.T) {
	var a, b Selection[int]
	a.Add(1)
	b.Add(1)
	if a.stamp == 0 || a.stamp == b.stamp {
		t.Fatalf("stamps %d and %d", a.stamp, b.stamp)
	}
	st := a.stamp
	a.Add(1)
	a.Remove(2)
	b.Clear()
	b.Clear()
	if a.stamp != st {
		t.Error("adding a key there or removing one not there changed the stamp")
	}
	for _, change := range []func(){func() { a.Add(2) }, func() { a.Remove(2) }, func() { a.Clear() }} {
		change()
		if a.stamp == st {
			t.Fatal("a change kept the stamp")
		}
		st = a.stamp
	}
}

// BenchmarkOutlineOpen scrolls an outline of 16,347 folders open, with
// 392,328 files, a frame at a time: Generation 0 walks them in every
// frame, and 1 keeps the rows.
func BenchmarkOutlineOpen(b *testing.B) {
	for _, generation := range []uint64{0, 1} {
		b.Run(fmt.Sprintf("Generation=%d", generation), func(b *testing.B) {
			o := newBigOutline(16347, 24, generation)
			defer o.tester.rt.close()
			o.calls = 0
			dy := float32(60)
			for b.Loop() {
				o.tester.Scroll(200, 400, 0, dy)
				dy = -dy
			}
			b.ReportMetric(float64(o.calls)/float64(b.N), "children/frame")
		})
	}
}
