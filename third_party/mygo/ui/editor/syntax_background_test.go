package editor

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBackgroundHighlighterDiscardsOldSnapshotAndStopsIdle(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	tokenizer := TokenizerFunc(func(line string, _ uint64, dst []Token) ([]Token, uint64) {
		kind := TokenString
		if strings.Contains(line, "old") {
			once.Do(func() { close(started); <-release })
			kind = TokenKeyword
		}
		return append(dst, Token{End: len(line), Kind: kind}), 0
	})
	buffer := NewBuffer("old\nline")
	old := buffer.Snapshot()
	h := NewBackgroundTokenHighlighter(tokenizer, old, func() {}).(*backgroundTokenHighlighter)
	h.Highlight(1, time.Millisecond)
	h.Tokens(0, "old", nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background lexer did not start")
	}
	buffer.Set("new\nline")
	next := buffer.Snapshot()
	callStart := time.Now()
	h.Edit(old, next, []Change{{OldStart: 0, OldEnd: 3, NewStart: 0, NewEnd: 3}})
	h.Tokens(0, "new", nil)
	if time.Since(callStart) > 100*time.Millisecond {
		t.Fatal("UI request waited on the old worker")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.Highlight(1, time.Millisecond)
		tokens, exact := h.Tokens(0, "new", nil)
		h.mu.Lock()
		idle := !h.running
		version := h.readyVersion
		h.mu.Unlock()
		if exact && idle {
			if version != next.Version() || !slices.Equal(tokens, []Token{{End: 3, Kind: TokenString}}) {
				t.Fatalf("stale tokens published: %v, version=%v", tokens, version)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new snapshot was not published or idle worker did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	if tokens, exact := h.Tokens(0, "old", nil); exact || len(tokens) != 0 {
		t.Fatal("cached tokens escaped their source text")
	}
}

func TestBackgroundHighlighterReusesNativeIncrementalState(t *testing.T) {
	buffer := NewBuffer(strings.Repeat("var x = 1\n", 1000))
	h := NewBackgroundTokenHighlighter(LanguageNamed("go").Tokenizer, buffer.Snapshot(), func() {}).(*backgroundTokenHighlighter)
	wait := func(line int, text string) {
		deadline := time.Now().Add(3 * time.Second)
		for {
			h.Highlight(line, time.Millisecond)
			_, exact := h.Tokens(line, text, nil)
			h.mu.Lock()
			idle := !h.running
			h.mu.Unlock()
			if exact && idle {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("background highlighter timeout")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait(999, "var x = 1")
	h.mu.Lock()
	work := h.work
	h.mu.Unlock()
	old := buffer.Snapshot()
	start := buffer.Offset(Pos{Line: 999, Col: 8})
	buffer.Edit(Edit{Start: start, End: start + 1, Text: "2"})
	h.Edit(old, buffer.Snapshot(), []Change{{OldStart: start, OldEnd: start + 1, NewStart: start, NewEnd: start + 1}})
	wait(999, "var x = 2")
	h.mu.Lock()
	same := h.work == work
	h.mu.Unlock()
	if !same {
		t.Fatal("an ordinary edit discarded the native incremental lexer state")
	}
}
