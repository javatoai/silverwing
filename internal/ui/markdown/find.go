package markdownui

import (
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// FindController may be shared by related reader sections. Owners choose when
// to open it; text, source ranges and UI focus remain scoped to each component.
type FindController struct {
	Replacement         string
	ReplaceOpen         bool
	Query               string
	Open                bool
	Current             int
	matches             []findMatch
	documents           map[string][]findMatch
	order               []string
	selected            findMatch
	hasSelected, reveal bool
	// Native selection follows user intent, independently of match coordinates
	// that may move while editing or remapping between preview and source.
	selectionRequest uint64
	observedQuery    string
	observedCurrent  int
}
type findMatch struct {
	block, start, end int
	documentID        string
}

func (f *FindController) update(id string, matches []findMatch) {
	if f.documents == nil {
		f.documents = map[string][]findMatch{}
	}
	if _, exists := f.documents[id]; !exists {
		f.order = append(f.order, id)
	}
	f.documents[id] = matches
	f.matches = nil
	for _, key := range f.order {
		f.matches = append(f.matches, f.documents[key]...)
	}
	f.current()
}
func (f *FindController) remove(id string) {
	if f.documents == nil {
		return
	}
	delete(f.documents, id)
	for i, key := range f.order {
		if key == id {
			f.order = append(f.order[:i], f.order[i+1:]...)
			break
		}
	}
	f.matches = nil
	for _, key := range f.order {
		f.matches = append(f.matches, f.documents[key]...)
	}
	f.current()
}
func (f *FindController) MatchCount() int { return len(f.matches) }

// current normalizes even direct writes to the public Current field. Every
// lookup goes through it because input can change Current after prepare ran.
func (f *FindController) current() (findMatch, bool) {
	if f.Query != f.observedQuery || f.Current != f.observedCurrent {
		f.selectionRequest++
		f.reveal = true
	}
	f.observedQuery = f.Query
	if len(f.matches) == 0 {
		f.Current, f.hasSelected, f.reveal = 0, false, false
		f.observedCurrent = 0
		return findMatch{}, false
	}
	f.Current = (f.Current%len(f.matches) + len(f.matches)) % len(f.matches)
	f.observedCurrent = f.Current
	hit := f.matches[f.Current]
	if !f.hasSelected || f.selected != hit {
		f.selected, f.hasSelected, f.reveal = hit, true, true
	}
	return hit, true
}

func (f *FindController) move(delta int) {
	if _, ok := f.current(); !ok {
		return
	}
	f.Current += delta
	f.requestReveal()
}

func (f *FindController) requestReveal() {
	if _, ok := f.current(); ok {
		f.selectionRequest++
		f.reveal = true
	}
}

// A shared request belongs to its destination, whichever reader initiated it.
func (f *FindController) revealFor(id string) (findMatch, bool) {
	hit, ok := f.current()
	return hit, ok && f.reveal && hit.documentID == id
}

func (f *FindController) revealed(hit findMatch) {
	if current, ok := f.current(); ok && current == hit {
		f.reveal = false
	}
}

func foldedMatches(text, query string) [][2]int {
	if query == "" {
		return nil
	}
	var folded strings.Builder
	positions := []int{}
	for at, ch := range text {
		low := string(unicode.ToLower(ch))
		for range len(low) {
			positions = append(positions, at)
		}
		folded.WriteString(low)
	}
	positions = append(positions, len(text))
	q := strings.ToLower(query)
	hay := folded.String()
	var result [][2]int
	for at := 0; at <= len(hay)-len(q); {
		found := strings.Index(hay[at:], q)
		if found < 0 {
			break
		}
		start := at + found
		end := start + len(q)
		result = append(result, [2]int{positions[start], positions[end]})
		at = end
	}
	return result
}

func highlight(spans []ui.Span, offset int, hits [][2]int, color ui.Color) []ui.Span {
	if len(hits) == 0 {
		return spans
	}
	var out []ui.Span
	for _, sp := range spans {
		end := offset + len(sp.Text)
		cursor := offset
		for _, h := range hits {
			a, z := max(cursor, h[0]), min(end, h[1])
			if a >= z {
				continue
			}
			if cursor < a {
				part := sp
				part.Text = sp.Text[cursor-offset : a-offset]
				out = append(out, part)
			}
			part := sp
			part.Text = sp.Text[a-offset : z-offset]
			part.Background = color
			out = append(out, part)
			cursor = z
		}
		if cursor < end {
			part := sp
			part.Text = sp.Text[cursor-offset:]
			out = append(out, part)
		}
		offset = end
	}
	return out
}
