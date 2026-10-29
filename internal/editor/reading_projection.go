package editor

import "silverwing-go/internal/markdowntext"

type ReadingProjection = markdowntext.ReadingProjection

func ProjectReading(source string) ReadingProjection { return markdowntext.ProjectReading(source) }
func ProjectRichTextReading(source string) ReadingProjection {
	return markdowntext.ProjectRichTextReading(source)
}
func isHTMLSpanToken(token []byte) bool { return markdowntext.IsHTMLSpanToken(token) }
