package ui_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// book is an address book an app looks recipients up in.
var book = []string{"Ada Lovelace <ada@example.com>", "Adele Goldberg <adele@example.com>", "Grace Hopper <grace@example.com>"}

// lookUp returns the recipients of book containing q, as the app's
// backend would.
func lookUp(q string) []string {
	var found []string
	for _, r := range book {
		if q != "" && strings.Contains(strings.ToLower(r), strings.ToLower(q)) {
			found = append(found, r)
		}
	}
	return found
}

// TestTokenFieldQuery binds the text being typed in a token field, as a
// mail's recipients looked up in an address book (#1714): the text
// follows typing, Backspace and an input method's commit, Changed reports
// each change, suggestions the app passes for it show and are chosen, and
// adding a token clears it.
func TestTokenFieldQuery(t *testing.T) {
	var to, found, seen []string
	var query string
	tt := ui.NewTester(func(c *ui.Context) {
		if ui.TokenField(c, &to, found).Label("To").Width(360).Query(&query).Changed() {
			seen = append(seen, query)
		}
	}, 500, 400)
	text := func() string {
		t.Helper()
		a, ok := tt.AccessByRole(ui.RoleComboBox, "To")
		if !ok {
			t.Fatal("no combo box named To")
		}
		return a.Value
	}
	if err := tt.Click("To"); err != nil {
		t.Fatal(err)
	}
	for _, r := range "adx" {
		tt.Type(string(r))
	}
	if query != "adx" || !slices.Equal(seen, []string{"a", "ad", "adx"}) {
		t.Fatalf("typing: query %q, Changed saw %q", query, seen)
	}
	tt.Key(0, ui.KeyBackspace)
	if query != "ad" || seen[len(seen)-1] != "ad" {
		t.Fatalf("Backspace: query %q, Changed saw %q", query, seen)
	}
	// What an input method composes is not typed yet; its commit is.
	seen = nil
	tt.Compose("e", 1)
	if query != "ad" || len(seen) != 0 {
		t.Fatalf("composing: query %q, Changed saw %q", query, seen)
	}
	tt.Type("é")
	if query != "adé" || !slices.Equal(seen, []string{"adé"}) {
		t.Fatalf("the commit: query %q, Changed saw %q", query, seen)
	}
	tt.Key(0, ui.KeyBackspace)

	// The app looks up what it read and passes what it found back, in a
	// later frame, as an answer of its backend comes.
	if query != "ad" {
		t.Fatalf("query %q", query)
	}
	found = lookUp(query)
	tt.Frame()
	if !tt.HasText("Ada Lovelace") || !tt.HasText("Adele Goldberg") || tt.HasText("Grace Hopper") {
		t.Fatalf("the suggestions for ad show %q", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	seen = nil
	tt.Key(0, ui.KeyEnter)
	if !slices.Equal(to, []string{"Adele Goldberg <adele@example.com>"}) {
		t.Fatalf("Enter on a suggestion added %q", to)
	}
	if query != "" || text() != "" || !slices.Equal(seen, []string{""}) {
		t.Fatalf("adding a suggestion: query %q, input %q, Changed saw %q", query, text(), seen)
	}

	// A click on a suggestion, Enter and a comma clear it too.
	tt.Type("gr")
	found = lookUp(query)
	tt.Frame()
	if err := tt.Click("Grace Hopper <grace@example.com>"); err != nil {
		t.Fatal(err)
	}
	if len(to) != 2 || query != "" || text() != "" {
		t.Fatalf("a click: tokens %q, query %q, input %q", to, query, text())
	}
	tt.Type("bob")
	tt.Key(0, ui.KeyEnter)
	if len(to) != 3 || to[2] != "bob" || query != "" {
		t.Fatalf("Enter: tokens %q, query %q", to, query)
	}
	tt.Type("carl,da")
	if len(to) != 4 || to[3] != "carl" || query != "da" || text() != "da" {
		t.Fatalf("a comma: tokens %q, query %q, input %q", to, query, text())
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Key(0, ui.KeyBackspace)
	// Backspace in the empty input takes out the last token, and leaves
	// the query as it is.
	seen = nil
	tt.Key(0, ui.KeyBackspace)
	if len(to) != 3 || query != "" || !slices.Equal(seen, []string{""}) {
		t.Fatalf("Backspace while empty: tokens %q, query %q, Changed saw %q", to, query, seen)
	}
}

// TestTokenFieldQuerySetByApp writes the query from the app: the input
// shows it, with no change to report, and takes it as typed text.
func TestTokenFieldQuerySetByApp(t *testing.T) {
	var to []string
	query, changes := "gr", 0
	tt := ui.NewTester(func(c *ui.Context) {
		if ui.TokenField(c, &to, book).Label("To").Width(360).Query(&query).Changed() {
			changes++
		}
	}, 500, 400)
	text := func() string {
		t.Helper()
		a, ok := tt.AccessByRole(ui.RoleComboBox, "To")
		if !ok {
			t.Fatal("no combo box named To")
		}
		return a.Value
	}
	if text() != "gr" || changes != 0 {
		t.Fatalf("the query the app started with: input %q, %d changes", text(), changes)
	}
	if err := tt.Click("To"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Type("a")
	if query != "gra" || changes != 1 {
		t.Fatalf("typing after it: query %q, %d changes", query, changes)
	}
	query = "Lin"
	tt.Frame()
	if text() != "Lin" || changes != 1 {
		t.Fatalf("set by the app: input %q, %d changes", text(), changes)
	}
	tt.Key(0, ui.KeyEnter)
	if !slices.Equal(to, []string{"Lin"}) || query != "" || text() != "" {
		t.Fatalf("Enter: tokens %q, query %q, input %q", to, query, text())
	}
	tt.Type("x")
	query = ""
	tt.Frame()
	if text() != "" || len(to) != 1 {
		t.Fatalf("cleared by the app: input %q, tokens %q", text(), to)
	}
}

// TestTokenFieldQueryDebounced looks suggestions up once typing pauses,
// as docs/ui/token-field.md does: Changed marks when the text last
// changed, and each frame until the lookup asks After for the rest of the
// pause.
func TestTokenFieldQueryDebounced(t *testing.T) {
	const pause = 300 * time.Millisecond
	var to, found, lookups []string
	var query, asked string
	var typedAt time.Time
	tt := ui.NewTester(func(c *ui.Context) {
		if ui.TokenField(c, &to, found).Label("To").Width(360).Query(&query).Changed() {
			typedAt = c.Now()
		}
		if query != asked {
			if wait := pause - c.Now().Sub(typedAt); wait > 0 {
				c.After(wait)
			} else {
				asked = query
				lookups = append(lookups, query)
			}
		}
	}, 500, 400)
	if err := tt.Click("To"); err != nil {
		t.Fatal(err)
	}
	tt.Type("gr")
	tt.Advance(pause / 2)
	tt.Type("a")
	tt.Advance(pause / 2)
	if len(lookups) != 0 {
		t.Fatalf("looked up %q while typing", lookups)
	}
	tt.Advance(pause / 2)
	if !slices.Equal(lookups, []string{"gra"}) {
		t.Fatalf("after a pause, looked up %q", lookups)
	}
	found = lookUp(asked)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if !slices.Equal(to, []string{"Grace Hopper <grace@example.com>"}) {
		t.Fatalf("chose %q", to)
	}
	// Adding it cleared the text, which is looked up in turn.
	tt.Advance(pause)
	if !slices.Equal(lookups, []string{"gra", ""}) {
		t.Fatalf("after adding a token, looked up %q", lookups)
	}
}

// TestTokenFieldQuerySuggestionsAsGiven passes suggestions the app matched
// itself, as a lookup folding accents does, none of them containing the
// text: once Query is bound, from the frame that binds it, the field shows
// them as given, in their order, leaving out only those already tokens,
// and they are chosen as any.
func TestTokenFieldQuerySuggestionsAsGiven(t *testing.T) {
	emile, nolde, ada := "Émile Zola <ezola@example.com>", "Emil Nolde <nolde@example.com>", "Ada Lovelace <ada@example.com>"
	to := []string{ada}
	var query string
	var found []string
	bind := false
	tt := ui.NewTester(func(c *ui.Context) {
		f := ui.TokenField(c, &to, found).Label("To").Width(360)
		if bind {
			f.Query(&query)
		}
	}, 500, 400)
	if err := tt.Click("To"); err != nil {
		t.Fatal(err)
	}
	tt.Type("emile")
	found = []string{nolde, ada, emile}
	tt.Frame()
	if tt.HasText(emile) || tt.HasText(nolde) {
		t.Fatalf("without Query, suggestions not containing emile show: %q", tt.Texts())
	}
	bind = true
	tt.Frame()
	texts := tt.Texts()
	if n, e := slices.Index(texts, nolde), slices.Index(texts, emile); n < 0 || e < n {
		t.Fatalf("with Query, the suggestions show as %q, want %q then %q", texts, nolde, emile)
	}
	if n := countOf(texts, ada); n != 1 {
		t.Fatalf("%s, a token, shows %d times: %q", ada, n, texts)
	}
	if query != "emile" {
		t.Fatalf("query %q", query)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if !slices.Equal(to, []string{ada, emile}) || query != "" {
		t.Fatalf("Enter on the second: tokens %q, query %q", to, query)
	}
	tt.Type("emil")
	if err := tt.Click(nolde); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if !slices.Equal(to, []string{ada, emile, nolde}) || query != "" {
		t.Fatalf("a click: tokens %q, query %q", to, query)
	}
}

// countOf returns how many of texts are s.
func countOf(texts []string, s string) int {
	n := 0
	for _, t := range texts {
		if t == s {
			n++
		}
	}
	return n
}

// TestTokenFieldWithoutQuery checks that a field without Query reports
// only changes of its tokens, and shows the suggestions containing the
// text, those starting with it first, as before Query.
func TestTokenFieldWithoutQuery(t *testing.T) {
	emile := "Émile Zola <ezola@example.com>"
	tags, changes := []string{}, 0
	tt := ui.NewTester(func(c *ui.Context) {
		if ui.TokenField(c, &tags, []string{emile, "rust", "zig"}).Label("Tags").Width(360).Changed() {
			changes++
		}
	}, 500, 400)
	if err := tt.Click("Tags"); err != nil {
		t.Fatal(err)
	}
	tt.Type("emile")
	if tt.HasText(emile) {
		t.Fatalf("emile suggests %q", tt.Texts())
	}
	for range 5 {
		tt.Key(0, ui.KeyBackspace)
	}
	tt.Type("zx")
	tt.Key(0, ui.KeyBackspace)
	if changes != 0 {
		t.Fatalf("typing reported %d changes", changes)
	}
	texts := tt.Texts()
	if z, e := slices.Index(texts, "zig"), slices.Index(texts, emile); z < 0 || e < z || slices.Contains(texts, "rust") {
		t.Fatalf("z suggests %q, want zig, then %s", texts, emile)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if !slices.Equal(tags, []string{"zig"}) || changes != 1 {
		t.Fatalf("Enter: tags %q, %d changes", tags, changes)
	}
}
