package editor

import (
	"testing"
)

func TestStateScrollPositionRoundTrip(t *testing.T) {
	state := &State{Buffer: NewBuffer("one\ntwo\nthree")}
	want := ScrollPosition{X: 37.5, Y: 84}
	state.SetScrollPosition(want)
	if got := state.ScrollPosition(); got != want {
		t.Fatalf("scroll position %+v, want %+v", got, want)
	}
}

func TestDiffStateScrollPositionRoundTrip(t *testing.T) {
	state := &DiffState{Old: NewBuffer("old"), New: NewBuffer("new")}
	want := DiffScrollPosition{OldX: 22.5, NewX: 71.25, Y: 96}
	state.SetScrollPosition(want)
	if got := state.ScrollPosition(); got != want {
		t.Fatalf("diff scroll position %+v, want %+v", got, want)
	}
}
