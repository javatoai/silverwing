//go:build !mygo_noinspector

package ui

import (
	"strings"
	"testing"
)

// copyView is a view whose tree has keys, labels, an element closed (the
// Row inside the toolbar) and one empty.
func copyView(c *context) {
	coreColumn(c).Key("app").Padding(20).Children(func() {
		coreRow(c).Key("toolbar").Children(func() {
			coreRow(c).Children(func() { coreText(c, "Deep") })
			coreBox(c).Label("Spacer").Width(8)
		})
		coreText(c, "Inside")
	})
}

func TestInspectorCopiesAnElement(t *testing.T) {
	tt, _ := inspected(copyView)
	openInspector(tt)
	// The tree shows the Row inside the toolbar closed; the copy shows all.
	if !tt.HasText("<Row>…</Row>") {
		t.Fatalf("the tree shows %q", tt.Texts())
	}
	in := &tt.rt.insp
	id := uint64(0)
	for _, n := range in.nodes {
		if n.keyText == "app" {
			id = n.id
		}
	}
	want := `<Column key="app">
  <Row key="toolbar">
    <Row>
      <Text>Deep</Text>
    </Row>
    <Column label="Spacer"></Column>
  </Row>
  <Text>Inside</Text>
</Column>`
	if got := in.elementText(in.nodeOf(id)); got != want {
		t.Errorf("the element copies as\n%s\nnot\n%s", got, want)
	}
	// A text alone, with its text whole.
	if got := in.elementText(in.nodeOf(id) + 3); got != "<Text>Deep</Text>" {
		t.Errorf("the text copies as %q", got)
	}
}

func TestInspectorCopiesTheElementChosenWithTheShortcut(t *testing.T) {
	tt, _ := inspected(copyView)
	openInspector(tt)
	if err := tt.Click(`<Row key="toolbar">`); err != nil {
		t.Fatal(err)
	}
	tt.Key(Cmd, KeyC)
	want := "<Row key=\"toolbar\">\n  <Row>\n    <Text>Deep</Text>\n  </Row>\n  <Column label=\"Spacer\"></Column>\n</Row>"
	if got := tt.Clipboard(); got != want {
		t.Errorf("Cmd+C copied\n%s\nnot\n%s", got, want)
	}
}

func TestInspectorRowMenuCopies(t *testing.T) {
	tt, _ := inspected(copyView)
	openInspector(tt)
	if err := tt.RightClick("<Text>Inside</Text>"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Copy element", "-", "Copy styles", "Copy computed", "Copy properties"}
	if got := tt.Menu(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("the row's menu is %q", got)
	}
	// Opening the menu chose the element, as in Chrome.
	if !tt.HasText("Text {") {
		t.Errorf("the menu did not choose its element: %q", tt.Texts())
	}
	if err := tt.ChooseMenuItem("Copy element"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != "<Text>Inside</Text>" {
		t.Errorf("Copy element copied %q", got)
	}

	copied := func(item string) string {
		t.Helper()
		tt.SetClipboard("")
		if err := tt.RightClick("<Text>Inside</Text>"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(item); err != nil {
			t.Fatal(err)
		}
		return tt.Clipboard()
	}
	styles := copied("Copy styles")
	for _, s := range []string{"Text {\n", "/* Inherited from ", "  font-size: ", ";\n", "}"} {
		if !strings.Contains(styles, s) {
			t.Errorf("the styles lack %q:\n%s", s, styles)
		}
	}
	computed := copied("Copy computed")
	for _, s := range []string{"Text {\n", "  margin: 0 0 0 0;\n", "  padding: ", "  width: ", "  display: text;\n", "}"} {
		if !strings.Contains(computed, s) {
			t.Errorf("the computed values lack %q:\n%s", s, computed)
		}
	}
	props := copied("Copy properties")
	for _, s := range []string{"Element\n", "  type: Text\n", "Accessibility\n"} {
		if !strings.Contains(props, s) {
			t.Errorf("the properties lack %q:\n%s", s, props)
		}
	}
}

func TestInspectorCopiesStylesTurnedOffAsComments(t *testing.T) {
	in := &inspector{sel: inspDetails{name: "Column", source: "/app/main.go:42", rules: []inspRule{
		{selector: "Column#box", decls: []inspDecl{{name: "width", value: "100px"}, {name: "gap", value: "8px", off: true}}},
		{selector: "Row", inherited: true, decls: []inspDecl{{name: "color", value: "#202124"}}},
	}}}
	want := `Column#box { /* main.go:42 */
  width: 100px;
  /* gap: 8px; */
}
/* Inherited from Row */
Row {
  color: #202124;
}`
	if got := in.stylesText(); got != want {
		t.Errorf("the styles copy as\n%s\nnot\n%s", got, want)
	}
}
