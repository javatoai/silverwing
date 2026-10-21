package editor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSyntaxReadyWaitsForFinalTokensToBePainted(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	wake := make(chan struct{}, 8)
	var once sync.Once
	var background *backgroundTokenHighlighter
	language := &Language{Name: "blocked", Tokenizer: TokenizerFunc(func(line string, _ uint64, dst []Token) ([]Token, uint64) {
		once.Do(func() { close(started); <-release })
		return append(dst, Token{End: len(line), Kind: TokenKeyword}), 0
	})}
	language.Highlighter = func(snapshot Snapshot, invalidate func()) Highlighter {
		background = NewBackgroundTokenHighlighter(language.Tokenizer, snapshot, func() {
			invalidate()
			select {
			case wake <- struct{}{}:
			default:
			}
		}).(*backgroundTokenHighlighter)
		return background
	}
	state := &State{Buffer: NewBuffer("SELECT 42;")}
	state.Buffer.SetLanguage(language)
	if state.SyntaxReady() || state.SyntaxVisible() || background != nil {
		t.Fatal("an unpainted query started syntax or reported ready")
	}
	tester := ui.NewTester(func(c *ui.Context) { View(c, state).Fill() }, 500, 200)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background tokenizer did not start")
	}
	if !state.SyntaxVisible() || state.SyntaxReady() {
		t.Fatal("first plain-looking paint was reported as syntax-ready")
	}
	close(release)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		background.mu.Lock()
		idle := !background.running
		cached := background.cache[0]
		background.mu.Unlock()
		if idle && cached.exact {
			break
		}
		select {
		case <-wake:
		case <-deadline.C:
			t.Fatal("background token cache did not complete")
		}
	}
	if state.SyntaxReady() {
		t.Fatal("cache completion was confused with final painted pixels")
	}
	tester.Frame()
	if !state.SyntaxReady() {
		t.Fatal("repaint with final tokens did not become ready")
	}
	line := state.ed.view.lines.m[0]
	if line == nil || !line.exact || len(line.toks) != 1 || line.toks[0].Kind != TokenKeyword {
		t.Fatal("ready paint did not use the completed keyword tokens")
	}
	state.Buffer.Edit(Edit{Start: 0, End: 6, Text: "UPDATE"})
	if state.SyntaxReady() {
		t.Fatal("buffer edit retained readiness from an older version")
	}
	state.Buffer.SetLanguage(nil)
	if state.SyntaxReady() {
		t.Fatal("language change retained old syntax readiness")
	}
	tester.Frame()
	if !state.SyntaxReady() {
		t.Fatal("painted plain text should be ready")
	}
}

func TestSyntaxVisibleDoesNotWaitForOffscreenOrHiddenFields(t *testing.T) {
	visible := &State{Buffer: NewBuffer("plain visible")}
	offscreen := &State{Buffer: NewBuffer("unseen keyword")}
	var hiddenJobs atomic.Int32
	offscreen.Buffer.SetLanguage(&Language{Name: "never painted", Highlighter: func(snapshot Snapshot, invalidate func()) Highlighter {
		hiddenJobs.Add(1)
		return NewBackgroundTokenHighlighter(LanguageNamed("sql").Tokenizer, snapshot, invalidate)
	}})
	outer := &ui.ScrollState{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().TrackScroll(outer).Children(func() {
			ui.Column(c).FillWidth().Shrink(0).Children(func() {
				View(c, visible).FillWidth().Height(80).Shrink(0)
				ui.Box(c).Height(500).Shrink(0)
				View(c, offscreen).FillWidth().Height(80).Shrink(0)
			})
		})
	}, 500, 200)
	if !visible.SyntaxReady() || !visible.SyntaxVisible() {
		t.Fatal("visible plain field did not become ready")
	}
	if offscreen.SyntaxVisible() || offscreen.SyntaxReady() || hiddenJobs.Load() != 0 {
		t.Fatal("offscreen field participated in syntax readiness")
	}
	outer.Y = 200
	tester.Frame()
	if visible.SyntaxVisible() {
		t.Fatal("a newly clipped field kept its previous-frame visibility")
	}
	if offscreen.SyntaxVisible() {
		t.Fatal("field below the scrolled viewport was marked visible")
	}
}
