package ui

import "testing"

// Keys typed while an input method composes in the focus are its own, as
// Enter choosing a candidate: a Tester keeps them from the content, as
// each platform's filter of input methods does, so a text input submits
// nothing until the composition is committed (#1815).
func TestComposingEnterSubmitsNothing(t *testing.T) {
	var value string
	submits, composing := 0, false
	tt := NewTester(func(c *Context) {
		in := TextInput(c, &value).AutoFocus()
		if in.Submitted() {
			submits++
		}
		composing = in.Composing()
	}, 400, 200)
	tt.Frame()
	tt.Compose("ご", 1)
	tt.Key(0, KeyEnter)
	tt.Frame()
	tt.Frame()
	if submits != 0 || value != "" || !composing {
		t.Fatalf("Enter while composing: %d submits, value %q, composing %v; want none, \"\", composing", submits, value, composing)
	}
	// The input method commits, and Enter submits then.
	tt.Type("ご")
	tt.Key(0, KeyEnter)
	tt.Frame()
	if submits != 1 || value != "ご" || composing {
		t.Fatalf("Enter once committed: %d submits, value %q, composing %v; want 1, \"ご\", not composing", submits, value, composing)
	}
}

// A shortcut around the focus or the window's is not pressed by a key the
// input method has, but is by a chord with Cmd, which input methods leave,
// as macOS sends it.
func TestComposingKeyPressesNoShortcut(t *testing.T) {
	var value string
	var f5, save int
	tt := NewTester(func(c *Context) {
		TextInput(c, &value).AutoFocus()
		if c.Shortcut(0, KeyF5) {
			f5++
		}
		if c.Shortcut(Cmd, KeyS) {
			save++
		}
	}, 400, 200)
	tt.Frame()
	tt.Compose("にほ", 2)
	tt.Key(0, KeyF5)
	tt.Key(Cmd, KeyS)
	tt.Frame()
	if f5 != 0 || save != 1 {
		t.Fatalf("while composing: F5 pressed %d times, Cmd+S %d; want 0 and 1", f5, save)
	}
	tt.Compose("", 0)
	tt.Key(0, KeyF5)
	tt.Frame()
	if f5 != 1 {
		t.Fatalf("F5 once the composition ended pressed %d times, want 1", f5)
	}
}

// Escape giving a composition up in a dialog's text input closes no
// dialog, and the composition is left to the input method.
func TestComposingEscapeClosesNoDialog(t *testing.T) {
	open := true
	var value string
	composing := false
	tt := NewTester(func(c *Context) {
		Modal(c, &open, func() {
			composing = TextInput(c, &value).AutoFocus().Composing()
		})
	}, 500, 400)
	tt.Frame()
	tt.Compose("か", 1)
	tt.Key(0, KeyEscape)
	tt.Frame()
	tt.Frame()
	if !open || !composing || tt.h.escapes != 0 {
		t.Fatalf("Escape while composing: dialog open %v, composing %v, Escapes for the window %d; want open, composing, none", open, composing, tt.h.escapes)
	}
	tt.Compose("", 0) // the input method gives it up
	tt.Key(0, KeyEscape)
	tt.Frame()
	if open {
		t.Fatal("Escape once the composition ended did not close the dialog")
	}
}
