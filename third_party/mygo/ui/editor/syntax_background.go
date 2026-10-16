package editor

import (
	"slices"
	"sync"
	"time"
)

// NewBackgroundTokenHighlighter keeps the native incremental tokenizer off the
// UI thread. The worker owns its lexer state; the UI only reads immutable token
// slices for requested lines. Obsolete snapshot results are never published.
func NewBackgroundTokenHighlighter(tokenizer Tokenizer, snapshot Snapshot, invalidate func()) Highlighter {
	return &backgroundTokenHighlighter{tokenizer: tokenizer, text: snapshot, invalidate: invalidate, target: -1}
}

type backgroundSyntaxEdit struct {
	old, next Snapshot
	changes   []Change
}

type backgroundTokens struct {
	text   string
	tokens []Token
	exact  bool
}

type backgroundTokenHighlighter struct {
	mu           sync.Mutex
	tokenizer    Tokenizer
	text         Snapshot
	invalidate   func()
	target       int
	running      bool
	edits        []backgroundSyntaxEdit
	requests     map[int]string
	cache        map[int]backgroundTokens
	readyVersion uint64
	ready        bool
	// work belongs solely to the one running worker and retains the SDK's
	// incremental line states between bounded jobs.
	work *tokenHighlighter
}

func (h *backgroundTokenHighlighter) Edit(old, next Snapshot, changes []Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text, h.ready, h.cache = next, false, nil
	h.requests = nil
	h.edits = append(h.edits, backgroundSyntaxEdit{old, next, slices.Clone(changes)})
	if len(h.edits) > 32 {
		h.edits = nil
	} // the worker then restarts at the latest snapshot
}

func (h *backgroundTokenHighlighter) Highlight(last int, _ time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.target = max(h.target, last)
	ready := h.ready && h.readyVersion == h.text.Version()
	if !ready {
		h.startLocked()
	}
	return ready
}

func (h *backgroundTokenHighlighter) Tokens(line int, text string, dst []Token) ([]Token, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cached, ok := h.cache[line]; ok && cached.text == text {
		return append(dst, cached.tokens...), cached.exact
	}
	if h.requests == nil {
		h.requests = make(map[int]string)
	}
	h.requests[line] = text
	h.target = max(h.target, line)
	h.startLocked()
	return dst, false
}

func (h *backgroundTokenHighlighter) startLocked() {
	if !h.running {
		h.running = true
		go h.run()
	}
}

func (h *backgroundTokenHighlighter) run() {
	for {
		h.mu.Lock()
		snapshot, edits, target, requests := h.text, h.edits, h.target, h.requests
		h.edits, h.requests = nil, nil
		work := h.work
		h.mu.Unlock()
		if work != nil {
			for _, edit := range edits {
				if work.text.Version() != edit.old.Version() {
					work = nil
					break
				}
				work.Edit(edit.old, edit.next, edit.changes)
			}
		}
		if work == nil || work.text.Version() != snapshot.Version() {
			work = NewTokenHighlighter(h.tokenizer, snapshot).(*tokenHighlighter)
		}
		complete := work.Highlight(target, 8*time.Millisecond)
		result := make(map[int]backgroundTokens, len(requests))
		for line, text := range requests {
			if line >= snapshot.LineCount() || snapshot.Line(line) != text {
				continue
			}
			tokens, exact := work.Tokens(line, text, nil)
			result[line] = backgroundTokens{text, tokens, exact}
		}
		h.mu.Lock()
		h.work = work
		current := h.text.Version() == snapshot.Version()
		if current {
			h.ready, h.readyVersion = complete && target >= h.target, snapshot.Version()
			if h.cache == nil || len(h.cache) > 2048 {
				h.cache = make(map[int]backgroundTokens)
			}
			for line, tokens := range result {
				if tokens.exact {
					h.cache[line] = tokens
				} else {
					if h.requests == nil {
						h.requests = make(map[int]string)
					}
					h.requests[line] = tokens.text
				}
			}
		}
		more := !current || !complete || target < h.target || len(h.edits) > 0 || len(h.requests) > 0
		if !more {
			h.running = false
		}
		h.mu.Unlock()
		if current && h.invalidate != nil {
			h.invalidate()
		}
		if !more {
			return
		}
	}
}
