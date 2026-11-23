package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestViewAnchorInitialAndRepeated(t *testing.T) {
	var scroll ui.ScrollState
	o := Options{Anchor: "#target"}
	source := "# Intro\n\n" + strings.Repeat("A paragraph before the destination.\n\n", 30) + "# Target\n\nDestination.\n"
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() { View(c, source, &o) })
	}, 400, 160)
	if scroll.Y <= 0 {
		t.Fatal("initial anchor did not reveal the heading")
	}
	scroll.Y = 0
	tt.Frame()
	if scroll.Y != 0 {
		t.Fatalf("unchanged anchor kept forcing scroll: %g", scroll.Y)
	}
	o.AnchorVersion++
	tt.Frame()
	if scroll.Y <= 0 {
		t.Fatal("a new version did not repeat the same anchor jump")
	}
	scroll.Y = 0
	source = "# Another document\n\n" + strings.Repeat("Different text before the target.\n\n", 20) + "# Target\n\nNew destination.\n"
	tt.Frame()
	if scroll.Y <= 0 {
		t.Fatal("a replacement document did not apply the same anchor")
	}
}

// TestViewOnLinkSetsAnchor checks that OnLink keeps getting #a-heading
// links, and scrolls the view to the heading by setting Anchor.
func TestViewOnLinkSetsAnchor(t *testing.T) {
	var scroll ui.ScrollState
	var clicked []string
	var o Options
	o.OnLink = func(dest string) {
		clicked = append(clicked, dest)
		if strings.HasPrefix(dest, "#") {
			o.Anchor = dest
		}
	}
	source := "[Jump](#target) [File](next.md)\n\n" + strings.Repeat("A paragraph before the destination.\n\n", 30) + "# Target\n\nDestination.\n"
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() { View(c, source, &o) })
	}, 400, 160)
	if err := tt.Click("File"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Jump"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(clicked) != 2 || clicked[0] != "next.md" || clicked[1] != "#target" {
		t.Fatalf("OnLink got %q, want next.md and #target", clicked)
	}
	if scroll.Y <= 0 {
		t.Fatal("the anchor OnLink set did not reveal its heading")
	}
	if len(tt.OpenedURLs()) != 0 {
		t.Fatalf("links escaped to the system opener: %q", tt.OpenedURLs())
	}
}

// blockIDs is an Options.AnchorBlock that finds what an anchor of
// Obsidian's names, ^block-id: the paragraph ending in it, or, for one
// ending in "-item", that paragraph's list item, and a table's cell
// ending in it. It notes the names it is asked for, and returns -1 for
// others.
func blockIDs(asked *[]string) func(d *parser.Document, name string) int {
	return func(d *parser.Document, name string) int {
		*asked = append(*asked, name)
		if !strings.HasPrefix(name, "^") {
			return -1
		}
		for i := 1; i < d.Len(); i++ {
			b := d.Block(i)
			if (b.Kind == parser.Paragraph || b.Kind == parser.TableCell) && strings.HasSuffix(parser.PlainText(d.Inlines(i)), " "+name) {
				if strings.HasSuffix(name, "-item") {
					return b.Parent
				}
				return i
			}
		}
		return -1
	}
}

// anchorView builds source in a view of 400 by 160 that scrolls, and
// returns the Tester, whether it shows text s whole, and a function that
// scrolls back to the top, where it shows none of the anchors' blocks.
func anchorView(t *testing.T, source *string, o *Options) (*ui.Tester, func(s string) bool, func()) {
	var scroll ui.ScrollState
	tt := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() { View(c, *source, o) })
	}, 400, 160)
	shows := func(s string) bool {
		r, ok := tt.Find(s)
		return ok && r.Y >= 0 && r.Y+r.H <= 160
	}
	return tt, shows, func() {
		t.Helper()
		scroll.Y = 0
		tt.Frame()
		if !shows("Before.") {
			t.Fatal("the view did not scroll back to the top")
		}
	}
}

// TestAnchorBlockScrollsToABlock checks that Anchor scrolls to the
// paragraph AnchorBlock finds, given the anchor without its "#", to the
// list item and to the table of a cell it finds, and to the heading of the
// slug when it finds nothing or answers a block the document does not have.
func TestAnchorBlockScrollsToABlock(t *testing.T) {
	var asked []string
	o := Options{Anchor: "#^para", AnchorBlock: blockIDs(&asked)}
	source := "Before.\n\n" + strings.Repeat("Before.\n\n", 30) + "Destination. ^para\n\n" +
		strings.Repeat("Between.\n\n", 30) + "- first\n- far away ^far-item\n\n" +
		strings.Repeat("Between.\n\n", 30) + "| a | b |\n|---|---|\n| 1 | cell ^cell |\n\n" +
		strings.Repeat("Between.\n\n", 30) + "# Target\n\n" + strings.Repeat("After.\n\n", 30)
	tt, shows, top := anchorView(t, &source, &o)
	if !shows("Destination. ^para") {
		t.Error("Anchor did not scroll to the paragraph AnchorBlock found")
	}
	if len(asked) != 1 || asked[0] != "^para" {
		t.Errorf("AnchorBlock was asked for %q, want ^para", asked)
	}
	for _, anchor := range []struct{ name, text string }{
		{"^far-item", "far away ^far-item"},
		{"^cell", "cell ^cell"},
		{"#target", "Target"},
		{"^para", "Destination. ^para"},
	} {
		top()
		o.Anchor = anchor.name
		tt.Frame()
		if !shows(anchor.text) {
			t.Errorf("Anchor %q did not scroll to %q", anchor.name, anchor.text)
		}
	}
	// Blocks the document does not have, the root among them, leave the
	// heading of the slug.
	for _, i := range []int{0, -2, 1} {
		o.AnchorBlock = func(d *parser.Document, name string) int {
			if i > 0 {
				return d.Len() + i - 1
			}
			return i
		}
		top()
		o.Anchor, o.AnchorVersion = "target", o.AnchorVersion+1
		tt.Frame()
		if !shows("Target") {
			t.Errorf("AnchorBlock answering %d did not leave the heading", i)
		}
	}
}

// TestAnchorBlockFollowsLinks checks that links to #an-anchor the view
// follows itself go where AnchorBlock says, or to a heading.
func TestAnchorBlockFollowsLinks(t *testing.T) {
	var asked []string
	o := Options{AnchorBlock: blockIDs(&asked)}
	source := "[Block](#^para) and [Heading](#target)\n\nBefore.\n\n" + strings.Repeat("Before.\n\n", 30) +
		"Destination. ^para\n\n" + strings.Repeat("Between.\n\n", 30) + "# Target\n\n" + strings.Repeat("After.\n\n", 30)
	for _, link := range []struct{ text, name, shows string }{
		{"Block", "^para", "Destination. ^para"},
		{"Heading", "target", "Target"},
	} {
		asked = nil
		tt, shows, _ := anchorView(t, &source, &o)
		if err := tt.Click(link.text); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !shows(link.shows) || len(asked) != 1 || asked[0] != link.name {
			t.Errorf("%s: AnchorBlock asked for %q, shows %q: %v", link.text, asked, link.shows, shows(link.shows))
		}
		if len(tt.OpenedURLs()) != 0 {
			t.Errorf("%s: links escaped to the system opener: %q", link.text, tt.OpenedURLs())
		}
	}
}
