package markdown

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestFullReviewTSVBudgetUsesBufferSourceRepresentation(t *testing.T) {
	const source = "|  |  |\r\n|---|---|\r\n| x | y |"
	const expected = "|  |  |\n| --- | --- |\n|  |  |"
	for _, preserving := range []bool{false, true} {
		for _, exact := range []bool{false, true} {
			t.Run(fmt.Sprintf("preserving=%v/exact=%v", preserving, exact), func(t *testing.T) {
				buffer := editor.NewBuffer(source)
				want := expected
				if preserving {
					buffer = editor.NewBufferPreservingLineEndings(source)
					want = strings.ReplaceAll(want, "\n", "\r\n")
				}
				limit := len(want)
				if !exact {
					limit--
				}
				buffer.SetMaxSourceBytes(limit)
				s := NewEditorState(buffer)
				at := strings.Index(buffer.String(), "x")
				s.SetSelection(at, at)
				s.Focus()
				opts := dailyOptions()
				errors := 0
				opts.OnError = func(error) { errors++ }
				view := ui.NewTester(func(c *ui.Context) { Editor(c, s, opts).Fill() }, 700, 500)
				view.Frame()
				before, version := buffer.OriginalText(), buffer.Version()
				if !s.tablePaste("\t") {
					t.Fatal("TSV paste was not handled")
				}
				if !exact {
					if buffer.OriginalText() != before || buffer.Version() != version || errors == 0 {
						t.Fatal("over-limit paste was not rejected atomically")
					}
					return
				}
				if buffer.OriginalText() != want || errors != 0 {
					t.Fatalf("exact-fit paste rejected: got %q, want %q (%d bytes), errors=%d", buffer.OriginalText(), want, limit, errors)
				}
				view.Command("undo")
				if buffer.OriginalText() != before {
					t.Fatal("paste Undo changed original byte representation")
				}
			})
		}
	}
}

func TestFullReviewWritingEnterUndoRestoresLiteralFence(t *testing.T) {
	for _, prefix := range []string{"- ", "> "} {
		for _, ending := range []string{"\n", "\r\n"} {
			for _, reversed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%q/%q/reversed=%v", prefix, ending, reversed), func(t *testing.T) {
					s, view := liveTester(prefix+ending+ending+"TAIL", len(prefix), dailyOptions())
					buffer := s.Buffer()
					view.Type("```")
					draft := prefix + "```" + ending + ending + "TAIL"
					if buffer.OriginalText() != draft {
						t.Fatalf("typed fence source = %q, want %q", buffer.OriginalText(), draft)
					}
					fullReviewAssertLiteralFence(t, s, true)
					a, z := len(prefix), len(prefix)+3
					if reversed {
						a, z = z, a
					}
					s.SetSelection(a, z)
					view.Frame()
					view.Key(0, ui.KeyEnter)
					continued := prefix + ending + prefix + ending + ending + "TAIL"
					if buffer.OriginalText() != continued {
						t.Fatalf("selected fence Enter = %q, want %q", buffer.OriginalText(), continued)
					}
					fullReviewAssertLiteralFence(t, s, false)
					for range 2 {
						view.Command("undo")
						if s.Buffer() != buffer || buffer.OriginalText() != draft {
							t.Fatalf("Undo did not restore exact draft in the same Buffer: %q", buffer.OriginalText())
						}
						if anchor, caret := s.Selection(); anchor != a || caret != z {
							t.Fatalf("Undo selection = %d:%d, want %d:%d", anchor, caret, a, z)
						}
						fullReviewAssertLiteralFence(t, s, true)
						view.Command("redo")
						if buffer.OriginalText() != continued {
							t.Fatalf("Redo changed the continuation's original bytes: %q", buffer.OriginalText())
						}
						fullReviewAssertLiteralFence(t, s, false)
					}
				})
			}
		}
	}
}

func fullReviewAssertLiteralFence(t *testing.T, s *EditorState, want bool) {
	t.Helper()
	drafts := s.literalFenceLines()
	if want && (len(drafts) != 1 || drafts[0] != 0) || !want && len(drafts) != 0 {
		t.Fatalf("literal fence policy = %v, want draft=%v", drafts, want)
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.CodeBlock {
			t.Fatal("unconfirmed/removed fence acquired a code card")
		}
	}
}

func TestFullReviewWritingEnterUndoRestoresGeneratedPair(t *testing.T) {
	for _, prefix := range []string{"- ", "> "} {
		t.Run(prefix, func(t *testing.T) {
			s, view := liveTester(prefix+"\n\nTAIL", len(prefix), dailyOptions())
			view.Type("(")
			paired := prefix + "()\n\nTAIL"
			if s.Buffer().OriginalText() != paired {
				t.Fatalf("generated pair = %q", s.Buffer().OriginalText())
			}
			s.SetSelection(len(prefix), len(prefix)+2)
			view.Frame()
			view.Key(0, ui.KeyEnter)
			view.Command("undo")
			view.Command("redo")
			view.Command("undo")
			if s.Buffer().OriginalText() != paired {
				t.Fatalf("writing Undo lost paired source: %q", s.Buffer().OriginalText())
			}
			s.SetSelection(len(prefix)+1, len(prefix)+1)
			view.Frame()
			version := s.Buffer().Version()
			view.Type(")")
			if s.Buffer().OriginalText() != paired || s.Buffer().Version() != version {
				t.Fatalf("restored generated closer was inserted twice: %q", s.Buffer().OriginalText())
			}
			if a, z := s.Selection(); a != len(prefix)+2 || z != a {
				t.Fatalf("skipped generated closer caret = %d:%d", a, z)
			}
		})
	}
}

func TestFullReviewRaggedTSVRejectsBeforeRectangularExpansion(t *testing.T) {
	for _, fixture := range []struct {
		name, payload string
		limit         int
	}{
		// About 1 KiB of TSV would become over 80,000 cells, despite the
		// document's 1 KiB limit. It is below the separate cell-count cap.
		{"source-budget", fullReviewRaggedTSV(1000, 81), 1024},
		// The serialized empty table would fit 1 MiB, but this small input
		// exceeds the interactive cell budget. Never use the OOM example.
		{"cell-budget", fullReviewRaggedTSV(1000, 101), 1 << 20},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			const source = "| A | B |\r\n|---|---|\r\n| x | y |\r\n\r\nTAIL"
			opts := dailyOptions()
			reported := 0
			opts.OnError = func(error) { reported++ }
			at := strings.Index(normalizedText(source), "x")
			s, view := liveTester(source, at, opts)
			buffer := s.Buffer()
			buffer.SetMaxSourceBytes(fixture.limit)
			// Keep a real redo branch so rejection cannot silently replace or
			// truncate the existing single-buffer history.
			view.Type("q")
			view.Command("undo")
			if buffer.OriginalText() != source || !buffer.CanRedo() {
				t.Fatal("fixture did not retain a redoable ordinary edit")
			}
			s.SetSelection(at+1, at)
			view.Frame()
			version, canUndo, canRedo := buffer.Version(), buffer.CanUndo(), buffer.CanRedo()
			// Measure only the paste, not tester/layout construction. Taking
			// the minimum of three small attempts tolerates unrelated runtime
			// allocations; the old rectangular string headers alone exceed
			// 1.2 MiB on the supported 64-bit desktop targets.
			least := ^uint64(0)
			for range 3 {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				handled := s.tablePaste(fixture.payload)
				runtime.ReadMemStats(&after)
				least = min(least, after.TotalAlloc-before.TotalAlloc)
				if !handled {
					t.Fatal("rejected TSV fell through to ordinary text paste")
				}
				if s.Buffer() != buffer || buffer.OriginalText() != source || buffer.Version() != version {
					t.Fatalf("rejected paste changed the source/Buffer/version: version=%d source bytes=%d", buffer.Version(), len(buffer.OriginalText()))
				}
				if a, z := s.Selection(); a != at+1 || z != at {
					t.Fatalf("rejected paste changed reversed cell selection: %d:%d", a, z)
				}
				if buffer.CanUndo() != canUndo || buffer.CanRedo() != canRedo {
					t.Fatal("rejected paste changed existing undo/redo availability")
				}
			}
			if reported == 0 {
				t.Fatal("budget rejection did not report an error")
			}
			// This is a resource boundary, not a wall-clock assertion. It is
			// intentionally generous relative to the roughly 1 KiB input, yet
			// cannot pass after allocating the amplified rectangular grid.
			if least > 512<<10 {
				t.Fatalf("small rejected TSV allocated %d bytes; expected rejection before rectangular expansion (budget %d)", least, 512<<10)
			}
			view.Command("redo")
			if got, want := buffer.OriginalText(), strings.Replace(source, "| x |", "| qx |", 1); got != want {
				t.Fatalf("rejection damaged pre-existing redo: %q, want %q", got, want)
			}
		})
	}
}

func fullReviewRaggedTSV(rows, columns int) string {
	return strings.Repeat("\t", columns-1) + strings.Repeat("\n", rows)
}

func TestFullReviewInlineSignaturePreservesSemanticOracle(t *testing.T) {
	for _, fixture := range []struct {
		name, source string
		want         inlineSemanticSignature
	}{
		{
			name:   "nested-styles-links-and-code",
			source: "head **bold *both*** tail ~~gone `code`~~ [**go**](https://example.com \"T\") ![alt](https://images.example/x.png \"I\")",
			want: inlineSemanticSignature{
				plain:  "head bold both tail gone code go alt",
				styles: [4]string{"bold bothgo", "both", "gone code", "code"},
				links:  "https://example.com\x00T\x00https://images.example/x.png\x00I\x00",
			},
		},
		{
			name:   "one-leaf-with-multiple-owners",
			source: "***a*** ~~**b**~~ `c`",
			want: inlineSemanticSignature{
				plain:  "a b c",
				styles: [4]string{"ab", "a", "b", "c"},
			},
		},
		{
			name:   "reference-link-defined-outside-its-block",
			source: "[*go*][ref] and ![alt](https://images.example/x \"I\").\n\n[ref]: https://example.com \"R\"",
			want: inlineSemanticSignature{
				plain:  "go and alt.",
				styles: [4]string{"", "go", "", ""},
				links:  "https://example.com\x00R\x00https://images.example/x\x00I\x00",
			},
		},
		{
			name:   "footnote-reference-is-not-a-style-leaf",
			source: "note[^n].\n\n[^n]: extra",
			want: inlineSemanticSignature{
				plain: "note.extra",
				links: "footnote:n\x00",
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			d := parser.New(parser.Default)
			d.Set(fixture.source)
			if got := signatureOfInlineDocument(d); got != fixture.want {
				t.Fatalf("semantic signature = %#v, want %#v", got, fixture.want)
			}
		})
	}
}

var fullReviewSignatureSink inlineSemanticSignature

// Run with -benchtime=1x to compare 512/2048 segments without a timing gate.
// Parsing is warmed outside the timed interval: the reviewed regression is
// signature ownership work, not the parser's separate complexity contract.
func BenchmarkFullReviewInlineSignatureScale(b *testing.B) {
	for _, segments := range []int{512, 2048} {
		b.Run(fmt.Sprintf("segments-%d", segments), func(b *testing.B) {
			source := strings.Repeat("**a** b ", segments)
			d := parser.New(parser.Default)
			d.Set(source)
			for i := 1; i < d.Len(); i++ {
				d.Inlines(i)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fullReviewSignatureSink = signatureOfInlineDocument(d)
			}
			b.StopTimer()
			if fullReviewSignatureSink.styles[0] != strings.Repeat("a", segments) {
				b.Fatal("scale fixture lost bold semantics")
			}
		})
	}
}
