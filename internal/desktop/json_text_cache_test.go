package desktop

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"silverwing-go/internal/tools"
)

func TestJSONTextIndexCoordinates(t *testing.T) {
	for _, source := range []string{"", "ASCII", "😀中\r\nA\rB\n🪴\r\n", "\r\n\r\r\n\n", "a\xff😀\r\n"} {
		t.Run(fmt.Sprintf("%q", source), func(t *testing.T) {
			index := newJSONTextIndex(source)
			for offset := -1; offset <= utf8.RuneCountInString(source)+2; offset++ {
				if got, want := index.lfByteAtRune(offset), textEditorProjectedByteOffset(source, offset); got != want {
					t.Fatalf("rune %d -> LF byte %d, want %d", offset, got, want)
				}
			}
			for offset := -1; offset <= len(source)+2; offset++ {
				if got, want := index.runeAtLFByte(offset), textEditorSourceRuneOffset(source, offset); got != want {
					t.Fatalf("LF byte %d -> source rune %d, want %d", offset, got, want)
				}
			}
			for offset := -1; offset <= tools.UTF16Length(source)+2; offset++ {
				if got, want := index.runeAtUTF16(offset), utf16OffsetToRune(source, offset); got != want {
					t.Fatalf("UTF-16 %d -> source rune %d, want %d", offset, got, want)
				}
			}
			if index.lineCount() != jsonSourceLineCount(source) || index.utf16Length() != tools.UTF16Length(source) {
				t.Fatal("source summary changed for mixed line endings or surrogate pairs")
			}
			for line := 0; line <= index.lineCount()+1; line++ {
				for column := 0; column < 12; column++ {
					if got, want := index.errorRuneOffset(line, column), jsonErrorRuneOffset(source, line, column); got != want {
						t.Fatalf("error %d:%d -> rune %d, want %d", line, column, got, want)
					}
				}
			}
		})
	}
}

func TestJSONProjectionCacheTracksCoordinatesWhenDisplayedTextIsEqual(t *testing.T) {
	// A synthetic span equal to the placeholder isolates the mapping contract:
	// identical displayed bytes must not make a fold an identity projection.
	state := &toolPageState{jsonSource: " … ", jsonValid: true, jsonDoc: tools.JSONDocument{Nodes: []tools.JSONSourceNode{{
		Pointer: "/same-display", StartOffset: 0, EndOffset: 3, StartLine: 1, EndLine: 2, IsContainer: true,
	}}}}
	initializeJSONEditor(state)
	before, generation := state.jsonEditor.projection.text, state.jsonEditor.projectionRevision
	if originalOffset(state.jsonEditor.projection, 1) != 1 {
		t.Fatal("identity fixture has the wrong source boundary")
	}
	(&app{}).toggleJSONFold(state, "/same-display")
	if state.jsonEditor.projection.text != before || state.jsonEditor.projectionRevision == generation || originalOffset(state.jsonEditor.projection, 1) != 0 {
		t.Fatal("equal display text kept stale source coordinates after folding")
	}
	generation = state.jsonEditor.projectionRevision
	state.syncJSONEditor()
	if state.jsonEditor.projectionRevision != generation {
		t.Fatal("unchanged projection was rebuilt")
	}
	(&app{}).expandAllJSONFolds(state)
	if originalOffset(state.jsonEditor.projection, 1) != 1 || state.jsonEditor.projectionRevision == generation {
		t.Fatal("expanding the same displayed text kept folded source coordinates")
	}
}

func TestJSONProjectionCacheInvalidationAndSourceHistory(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("%q", newline), func(t *testing.T) {
			source := strings.ReplaceAll("{\n  \"emoji\": \"😀\",\n  \"nested\": {\n    \"key\": \"needle🪴\"\n  },\n  \"tail\": \"needle\"\n}\n", "\n", newline)
			state := newToolPageState(source)
			defer state.cancelWork()
			s := &app{toolsUnit: toolsController{state: toolsState{tools: state}}}
			state.syncJSONEditor()
			index, generation := state.jsonEditor.sourceIndex, state.jsonEditor.projectionRevision
			for range 3 {
				state.syncJSONEditor()
			}
			if state.jsonEditor.sourceIndex != index || state.jsonEditor.projectionRevision != generation {
				t.Fatal("stable source rebuilt coordinates or projection")
			}
			if allocations := testing.AllocsPerRun(10, state.syncJSONEditor); allocations != 0 {
				t.Fatalf("unchanged source allocates %g times", allocations)
			}
			s.toggleJSONFold(state, "/nested")
			if len(state.jsonEditor.projection.folds) != 1 || state.jsonEditor.sourceIndex != index {
				t.Fatal("folding rebuilt source coordinates or lost the fold")
			}
			if lines := jsonEditorGutterLines(state); !slices.Equal(lines, []int{1, 2, 3, 6, 7, 8}) {
				t.Fatalf("folded original line numbers = %v", lines)
			}
			generation = state.jsonEditor.projectionRevision
			state.validateJSON(source) // a fresh parse of the same source
			state.syncJSONEditor()
			if state.jsonEditor.projectionRevision == generation || state.jsonEditor.sourceIndex != index || len(state.jsonEditor.projection.folds) != 1 {
				t.Fatal("parse arrival did not refresh projection independently of source coordinates")
			}
			matches := state.jsonEditor.sourceMatches.get(source, "NEEDLE")
			revealJSONProjectionRange(state, matches[0].start, matches[0].end)
			if len(state.jsonEditor.projection.folds) != 0 || state.jsonSource != source || len(state.jsonEditor.history) != 1 {
				t.Fatal("revealing a hidden match changed source history or retained its ancestor fold")
			}
			state.commitProgrammaticJSONSource(state.jsonDoc.Formatted)
			formatted := state.jsonSource
			state.validateJSON(formatted)
			state.syncJSONEditor()
			if state.jsonEditor.sourceIndex.source != formatted || state.jsonEditor.viewSource != formatted {
				t.Fatal("formatting kept old source coordinates")
			}
			s.moveJSONEditorHistory(state, false)
			state.cancelWork()
			state.validateJSON(state.jsonSource)
			state.syncJSONEditor()
			if state.jsonSource != source || state.jsonEditor.sourceIndex.source != source {
				t.Fatal("undo did not restore original source and coordinates")
			}
			s.moveJSONEditorHistory(state, true)
			state.cancelWork()
			state.validateJSON(state.jsonSource)
			state.syncJSONEditor()
			if state.jsonSource != formatted || state.jsonEditor.sourceIndex.source != formatted {
				t.Fatal("redo did not restore formatted source and coordinates")
			}
			s.toggleJSONFold(state, "/nested")
			state.jsonSource = "[\r\n \"external😀\"\r\n]\r"
			state.syncJSONEditor()
			state.validateJSON(state.jsonSource)
			state.syncJSONEditor()
			if len(state.jsonEditor.folded) != 0 || state.jsonEditor.sourceIndex.source != state.jsonSource || state.jsonEditor.projection.text != "[\r\n \"external😀\"\r\n]\n" {
				t.Fatal("external input kept a stale fold, source or bare-CR projection")
			}
		})
	}
}

func TestJSONFindCachesPreserveSourceOrdinalsAndByteRanges(t *testing.T) {
	const source = "{\r\n \"first\": {\r\n  \"value\": \"needle😀\"\r\n },\r\n \"tail\": \"NEEDLE🪴\"\r\n}\r\n"
	state := newToolPageState(source)
	defer state.cancelWork()
	state.syncJSONEditor()
	state.jsonEditor.finding, state.jsonEditor.findQuery = true, "needle"
	matches := state.jsonEditor.sourceMatches.get(source, "needle")
	if len(matches) != 2 {
		t.Fatal("fixture must have two source matches")
	}
	first := &matches[0]
	if repeated := state.jsonEditor.sourceMatches.get(source, "needle"); &repeated[0] != first {
		t.Fatal("stable query discarded source matches")
	}
	(&app{}).toggleJSONFold(state, "/first")
	e := &state.jsonEditor
	e.findIndex = 1
	e.syncNativeFindHighlights(state, matches)
	if len(e.native.FindHighlights) != 1 || !e.native.FindHighlights[0].Current {
		t.Fatal("hidden earlier source match shifted the current visible ordinal")
	}
	highlight := e.native.FindHighlights[0]
	nativeText := strings.ReplaceAll(e.projection.text, "\r\n", "\n")
	if nativeText[highlight.Start:highlight.End] != "NEEDLE" {
		t.Fatal("LF-byte highlights selected the wrong Unicode/CRLF span")
	}
	storage := &e.native.FindHighlights[0]
	e.findIndex = 0
	e.syncNativeFindHighlights(state, matches)
	if &e.native.FindHighlights[0] != storage || e.native.FindHighlights[0].Current {
		t.Fatal("changing only the current match rebuilt ranges or highlighted a hidden match")
	}
	revealJSONProjectionRange(state, matches[0].start, matches[0].end)
	e.syncNativeFindHighlights(state, matches)
	if len(e.native.FindHighlights) != 2 || !e.native.FindHighlights[0].Current || e.native.FindHighlights[1].Current {
		t.Fatal("reveal did not refresh visible ranges while retaining source ordinal")
	}
	e.finding = false
	e.syncNativeFindHighlights(state, matches)
	if len(e.native.FindHighlights) != 0 {
		t.Fatal("closed find left native highlights visible")
	}
	if changed := e.sourceMatches.get(source, "absent"); len(changed) != 0 {
		t.Fatal("query change reused old matches")
	}
	if changed := e.sourceMatches.get("needle", "absent"); len(changed) != 0 || e.sourceMatches.source != "needle" {
		t.Fatal("source change retained the previous cache identity")
	}
}

func TestJSONTextMatchesUnicodeAndNonOverlappingPrefixes(t *testing.T) {
	for _, test := range []struct {
		source, query string
		want          []jsonEditorMatch
	}{
		{"aaaaa", "aa", []jsonEditorMatch{{0, 2}, {2, 4}}},
		{"ababababac", "ababac", []jsonEditorMatch{{4, 10}}},
		{"İ😀K中 k😀K中", "k😀k", []jsonEditorMatch{{5, 8}}},
		{"😀NEEDLE\r\nneedle🪴", "needle", []jsonEditorMatch{{1, 7}, {9, 15}}},
		{"any", "", nil},
		{"short", "longer", nil},
	} {
		if got := jsonEditorMatches(test.source, test.query); !slices.Equal(got, test.want) {
			t.Fatalf("matches(%q, %q) = %v, want %v", test.source, test.query, got, test.want)
		}
	}
}

var jsonCacheBenchProjection jsonTextProjection
var jsonCacheBenchMatches []jsonEditorMatch

// Same ASCII, unparsed, unfolded sources and sizes as the frozen audit fixture.
func BenchmarkJSONProjectionUnchanged(b *testing.B) {
	for _, size := range []int{64 * 1024, 1024 * 1024, 2 * 1024 * 1024} {
		source := strings.Repeat("x", size)
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			state := toolPageState{jsonSource: source}
			initializeJSONEditor(&state)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				state.syncJSONEditor()
			}
			jsonCacheBenchProjection = state.jsonEditor.projection
		})
	}
}

func BenchmarkJSONFindUnchanged(b *testing.B) {
	for _, size := range []int{64 * 1024, 1024 * 1024, 2 * 1024 * 1024} {
		source := strings.Repeat("needle😀\r\n", size/12)
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			state := toolPageState{jsonSource: source}
			initializeJSONEditor(&state)
			e := &state.jsonEditor
			e.finding, e.findQuery = true, "needle"
			matches := e.sourceMatches.get(source, e.findQuery)
			e.syncNativeFindHighlights(&state, matches)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				e.findIndex = (e.findIndex + 1) % len(matches)
				matches = e.sourceMatches.get(source, e.findQuery)
				e.syncNativeFindHighlights(&state, matches)
			}
			jsonCacheBenchMatches = matches
		})
	}
}
