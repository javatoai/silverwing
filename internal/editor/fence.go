package editor

import "silverwing-go/internal/markdowntext"

type Fence = markdowntext.Fence

func FindFence(source string) *Fence              { return markdowntext.FindFence(source) }
func ReplaceFenceCode(source, code string) string { return markdowntext.ReplaceFenceCode(source, code) }
func DisplaySource(source string) string          { return markdowntext.DisplaySource(source) }
func RichReplacement(original, serialized, documentNewline string) string {
	return markdowntext.RichReplacement(original, serialized, documentNewline)
}
