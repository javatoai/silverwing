package editor

import "silverwing-go/internal/markdowntext"

// CommentImages retains the editor API while sharing the pure syntax model.
func CommentImages(source string) []Node { return markdowntext.CommentImages(source) }

func RewriteCommentImages(source string, replacements map[string]*string, annotations ...map[string]string) string {
	return markdowntext.RewriteCommentImages(source, replacements, annotations...)
}
