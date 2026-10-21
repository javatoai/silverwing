package ui

import "testing"

// TestInputReachesLocalCopies: a control's input reaches what it binds as
// its constructor returns, so a local copy compared with the value it
// started from, without asking Changed, sees the user's edit, as with the
// widgets of Dear ImGui and egui.
func TestInputReachesLocalCopies(t *testing.T) {
	var language, style, query string
	var sync bool
	var volume float64
	tt := NewTester(func(c *Context) {
		Column(c).Gap(8).Children(func() {
			chosen := language
			RadioGroup(c, func() {
				Radio(c, &chosen, "", "System")
				Radio(c, &chosen, "de", "Deutsch")
				Radio(c, &chosen, "ja", "日本語")
			}).Label("Language")
			if chosen != language {
				language = chosen
			}
			on := sync
			Checkbox(c, &on, "Sync")
			if on != sync {
				sync = on
			}
			s := style
			Select(c, &s, []string{"Body", "Heading"}).Label("Style")
			if s != style {
				style = s
			}
			v := volume
			Slider(c.Key("volume"), &v, 0, 1).Step(0.25).Label("Volume")
			if v != volume {
				volume = v
			}
			q := query
			TextInput(c.Key("query"), &q).Label("Query")
			if q != query {
				query = q
			}
		})
	}, 400, 400)

	if err := tt.Click("Deutsch"); err != nil {
		t.Fatal(err)
	}
	if language != "de" {
		t.Errorf("after clicking Deutsch, language %q", language)
	}
	// The group's arrow keys move among the radios built in it.
	tt.Key(0, KeyDown)
	if language != "ja" {
		t.Errorf("after the down arrow, language %q", language)
	}
	if err := tt.Click("Sync"); err != nil {
		t.Fatal(err)
	}
	if !sync {
		t.Error("the click did not check Sync")
	}
	if err := tt.Click("Style"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Heading"); err != nil {
		t.Fatal(err)
	}
	if style != "Heading" {
		t.Errorf("after choosing Heading, style %q", style)
	}
	if err := tt.Click("Volume"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, KeyHome)
	tt.Key(0, KeyRight)
	if volume != 0.25 {
		t.Errorf("after Home and the right arrow, volume %v, want a step of 0.25", volume)
	}
	if err := tt.Click("Query"); err != nil {
		t.Fatal(err)
	}
	tt.Type("go")
	if query != "go" {
		t.Errorf("after typing, query %q", query)
	}
}

// TestInputReachesLocalCopiesOfParts: a part's method that builds an
// element, as a select's Item, is a constructor too: the choice is in the
// local copy once the popup is built.
func TestInputReachesLocalCopiesOfParts(t *testing.T) {
	size := "S"
	tt := NewTester(func(c *Context) {
		chosen := size
		s := SelectBase(c, &chosen)
		s.Trigger.Label("Size").Size(80, 24)
		s.Popup(func(panel Element) {
			for _, v := range []string{"S", "M", "L"} {
				// No control is built after the item: its own call
				// takes its input.
				s.Item(v).Label("Size "+v).Size(80, 24)
			}
		})
		if chosen != size {
			size = chosen
		}
	}, 300, 300)
	if err := tt.Click("Size"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Size L"); err != nil {
		t.Fatal(err)
	}
	if size != "L" {
		t.Errorf("after choosing Size L, size %q", size)
	}
}
