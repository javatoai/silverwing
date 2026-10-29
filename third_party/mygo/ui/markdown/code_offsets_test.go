package markdown

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCodeChunkOffsetsRemainExactAcrossLineChanges(t *testing.T) {
	for _, count := range []int{0, 1, 31, 32, 33, 64, 65, 97} {
		lines := make([]string, count)
		for i := range lines {
			lines[i] = fmt.Sprintf("line%d😀中", i)
		}
		cs := codeState{lines: lines, chunks: make([]codeChunk, max(1, (count+chunkLines-1)/chunkLines))}
		cs.updateChunkOffsets()
		for k, chunk := range cs.chunks {
			prefix := strings.Join(lines[:min(k*chunkLines, len(lines))], "\n")
			want := len(prefix)
			if k > 0 {
				want++ // the LF separating this chunk from its predecessor
			}
			if chunk.offset != want {
				t.Fatalf("%d lines, chunk %d offset = %d, want %d", count, k, chunk.offset, want)
			}
		}
		if count > chunkLines {
			before := cs.chunks[1].offset
			cs.lines[0] += "🪴"
			cs.updateChunkOffsets()
			if cs.chunks[1].offset != before+len("🪴") {
				t.Fatal("an earlier line edit kept a stale later chunk offset")
			}
		}
	}
}

func TestCodeChunkOnTextOffsetsPreserveUnicodeCRLFAndStreaming(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrap_%v", wrap), func(t *testing.T) {
			var source, code string
			seen := make(map[int]string)
			options := &Options{WrapCode: wrap, OnText: func(c *ui.Context, element ui.Element, block, offset int, spans []ui.Span) {
				var text strings.Builder
				for _, span := range spans {
					text.WriteString(span.Text)
				}
				seen[offset] = text.String()
			}}
			setSource := func(count int, changed bool) []string {
				lines := make([]string, count)
				for i := range lines {
					lines[i] = fmt.Sprintf("line%03d😀中", i)
				}
				if changed && len(lines) > 0 {
					lines[0] += " earlier🪴"
				}
				code = strings.Join(lines, "\n")
				source = "```text\r\n" + strings.Join(lines, "\r\n") + "\r\n```\r\n"
				seen = make(map[int]string)
				return lines
			}
			lines := setSource(65, false)
			tester, _ := rvView(&source, options)
			check := func() {
				t.Helper()
				if len(seen) != (len(lines)+chunkLines-1)/chunkLines {
					t.Fatalf("OnText delivered %d chunks for %d lines", len(seen), len(lines))
				}
				for offset, text := range seen {
					if offset < 0 || offset+len(text) > len(code) || code[offset:offset+len(text)] != text {
						t.Fatalf("OnText chunk %q at byte %d does not match normalized code", text, offset)
					}
				}
			}
			check()
			for _, phase := range []struct {
				lines   int
				changed bool
			}{{97, false}, {97, true}, {33, true}, {32, false}, {1, false}} {
				lines = setSource(phase.lines, phase.changed)
				tester.Frame()
				check()
			}
		})
	}
}

var codeChunkOffsetBenchSink int

// The audit's long-code fixture has 32,768 lines: the former prefix loops made
// 16,760,832 additions per build. This benchmark isolates the offset operation.
func BenchmarkCodeChunkOffsets(b *testing.B) {
	for _, count := range []int{1024, 32768} {
		b.Run(fmt.Sprintf("lines_%d", count), func(b *testing.B) {
			cs := codeState{lines: make([]string, count), chunks: make([]codeChunk, (count+chunkLines-1)/chunkLines)}
			for i := range cs.lines {
				cs.lines[i] = "const value = 42"
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				cs.updateChunkOffsets()
			}
			codeChunkOffsetBenchSink = cs.chunks[len(cs.chunks)-1].offset
		})
	}
}
