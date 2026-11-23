package ui

import (
	"fmt"
	"testing"

	"golang.org/x/image/font/gofont/gomono"
)

// TestTwoTestersInParallel runs Testers on different goroutines, as
// t.Parallel does, and fails under go test -race while they share one
// glyph atlas (issue #1722).
func TestTwoTestersInParallel(t *testing.T) {
	for i := range 4 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			frame := 0
			tt := NewTester(func(c *Context) {
				Column(c).Children(func() {
					for j := range 30 {
						r := rune(0x4e00 + i*3000 + frame*30 + j) // new glyphs every frame
						Row(c).Children(func() {
							Text(c, fmt.Sprintf("tester %d frame %d %c", i, frame, r)).FontSize(float32(11 + (frame+j)%9))
						})
					}
				})
			}, 400, 900)
			for range 40 {
				frame++
				tt.Frame()
			}
		})
	}
}

// A Tester's window, which shapes text in a system of its own, draws the
// fonts the app registered, as the app's windows do. Go Mono is
// monospaced, so "iiiiiiii" is as wide as "mmmmmmmm" only in it.
func TestATesterDrawsRegisteredFonts(t *testing.T) {
	const family = "MyGo Test Tester Mono"
	if err := RegisterFont(gomono.TTF, family); err != nil {
		t.Fatal(err)
	}
	tt := NewTester(func(c *Context) {
		Column(c).Children(func() {
			for _, s := range []string{"iiiiiiii", "mmmmmmmm"} {
				Row(c).Children(func() { Text(c, s).Font(family).FontSize(20) })
			}
		})
	}, 400, 300)
	i, _ := tt.Find("iiiiiiii")
	m, _ := tt.Find("mmmmmmmm")
	if i.W < m.W-0.5 || i.W > m.W+0.5 {
		t.Fatalf("the registered font isn't drawn: %v wide, %v in Go Mono", i.W, m.W)
	}
}
