package editor

import (
	"sync/atomic"

	"silverwing-go/internal/markdowntext"
)

var parsedPartID atomic.Uint64

// Parse classifies source with the shared pure parser, then assigns editor IDs.
func Parse(source string) Snapshot {
	parts, _ := parseDocument(source)
	for i := range parts {
		parts[i].ID = parsedPartID.Add(1)
	}
	return Snapshot{Source: source, Parts: parts, Version: 0, CanUndo: false, CanRedo: false}
}

func parseDocument(source string) ([]Part, []Node) { return markdowntext.ParseDocument(source) }
func collectNodes(parts []Part) []Node             { return markdowntext.CollectNodes(parts) }
func FeishuCommentImageURLs(source string) []string {
	return markdowntext.FeishuCommentImageURLs(source)
}

type sourceLine struct {
	start, contentEnd, end int
	text                   string
}

func scanLines(source string) []sourceLine {
	lines := markdowntext.SourceLines(source)
	result := make([]sourceLine, len(lines))
	for i, line := range lines {
		result[i] = sourceLine{line.Start, line.ContentEnd, line.End, line.Text}
	}
	return result
}

func normalizeLines(source string) (string, []int) { return markdowntext.NormalizeLines(source) }
