package desktop

import (
	"math"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

func normalizeNativeRichTextSpans(text string, spans []ui.Span) []ui.Span {
	copySpans := cloneNativeRichTextSpans(spans)
	if richTextSpansText(copySpans) == text {
		return copySpans
	}
	if text == "" {
		return nil
	}
	return []ui.Span{{Text: text}}
}

func cloneNativeRichTextSpans(spans []ui.Span) []ui.Span {
	return slices.Clone(spans)
}

func richTextSpansText(spans []ui.Span) string {
	var text strings.Builder
	for _, span := range spans {
		text.WriteString(span.Text)
	}
	return text.String()
}

func equalNativeRichTextSpans(a, b []ui.Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalNativeRichTextSpanStyle(a[i], b[i]) || a[i].Text != b[i].Text {
			return false
		}
	}
	return true
}

func equalNativeRichTextSpanStyle(a, b ui.Span) bool {
	return a.Font == b.Font && a.Size == b.Size && a.Weight == b.Weight && a.Italic == b.Italic && a.Color == b.Color &&
		a.Underline == b.Underline && a.WavyUnderline == b.WavyUnderline && a.Strikethrough == b.Strikethrough &&
		a.DecorationColor == b.DecorationColor && a.DecorationThickness == b.DecorationThickness && a.Background == b.Background &&
		a.LetterSpacing == b.LetterSpacing && a.Features == b.Features
}

func normalizeNativeRichTextRange(text string, r ui.TextInputRange) ui.TextInputRange {
	limit := ui.UTF16Len(text)
	a, b := max(0, min(r.Start, limit)), max(0, min(r.End, limit))
	if a > b {
		a, b = b, a
	}
	startByte, endByte := ui.UTF16ByteOffset(text, a), ui.UTF16ByteOffset(text, b)
	return ui.TextInputRange{Start: ui.UTF16Len(text[:startByte]), End: ui.UTF16Len(text[:endByte])}
}

func replaceNativeRichTextSpans(text string, spans []ui.Span, start, end int, replacement string) []ui.Span {
	spans = normalizeNativeRichTextSpans(text, spans)
	startByte, endByte := ui.UTF16ByteOffset(text, start), ui.UTF16ByteOffset(text, end)
	style := nativeRichTextStyleAt(spans, startByte)
	result := make([]ui.Span, 0, len(spans)+2)
	appendNativeRichTextSlice(&result, spans, 0, startByte, false)
	if replacement != "" {
		style.Text = replacement
		result = append(result, style)
	}
	appendNativeRichTextSlice(&result, spans, endByte, len(text), false)
	result = mergeNativeRichTextSpans(result)
	candidate := text[:startByte] + replacement + text[endByte:]
	if richTextSpansText(result) != candidate {
		return normalizeNativeRichTextSpans(candidate, nil)
	}
	return result
}

func nativeRichTextStyleAt(spans []ui.Span, byteOffset int) ui.Span {
	if len(spans) == 0 {
		return ui.Span{}
	}
	position := 0
	for i, span := range spans {
		end := position + len(span.Text)
		if byteOffset <= end || i == len(spans)-1 {
			span.Text = ""
			return span
		}
		position = end
	}
	return ui.Span{}
}

func appendNativeRichTextSlice(dst *[]ui.Span, spans []ui.Span, startByte, endByte int, marked bool) {
	if startByte >= endByte {
		return
	}
	position := 0
	for _, span := range spans {
		spanStart, spanEnd := position, position+len(span.Text)
		position = spanEnd
		left, right := max(startByte, spanStart), min(endByte, spanEnd)
		if left >= right {
			continue
		}
		part := span
		part.Text = span.Text[left-spanStart : right-spanStart]
		if marked {
			part.WavyUnderline = true
		}
		*dst = append(*dst, part)
	}
}

func nativeRichTextMarkedSpans(text string, spans []ui.Span, mark *ui.TextInputRange) []ui.Span {
	spans = normalizeNativeRichTextSpans(text, spans)
	if mark == nil || mark.Start >= mark.End {
		return spans
	}
	r := normalizeNativeRichTextRange(text, *mark)
	startByte, endByte := ui.UTF16ByteOffset(text, r.Start), ui.UTF16ByteOffset(text, r.End)
	result := make([]ui.Span, 0, len(spans)+2)
	appendNativeRichTextSlice(&result, spans, 0, startByte, false)
	appendNativeRichTextSlice(&result, spans, startByte, endByte, true)
	appendNativeRichTextSlice(&result, spans, endByte, len(text), false)
	return mergeNativeRichTextSpans(result)
}

func mergeNativeRichTextSpans(spans []ui.Span) []ui.Span {
	if len(spans) < 2 {
		return spans
	}
	merged := make([]ui.Span, 0, len(spans))
	for _, span := range spans {
		if span.Text == "" {
			continue
		}
		if n := len(merged); n > 0 && equalNativeRichTextSpanStyle(merged[n-1], span) {
			merged[n-1].Text += span.Text
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func nativeRichTextDiff(oldText, newText string) (start, end int, replacement string, changed bool) {
	if oldText == newText {
		return 0, 0, "", false
	}
	oldRunes, newRunes := []rune(oldText), []rune(newText)
	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	oldEnd, newEnd := len(oldRunes), len(newRunes)
	for oldEnd > prefix && newEnd > prefix && oldRunes[oldEnd-1] == newRunes[newEnd-1] {
		oldEnd--
		newEnd--
	}
	start = ui.UTF16Len(string(oldRunes[:prefix]))
	end = ui.UTF16Len(string(oldRunes[:oldEnd]))
	replacement = string(newRunes[prefix:newEnd])
	return start, end, replacement, true
}

func absNativeRichTextFloat(value float32) float32 { return float32(math.Abs(float64(value))) }

func nativeRichTextUTF16Length(text string) int { return len(utf16.Encode([]rune(text))) }

func nativeRichTextValidSpanText(text string) bool { return utf8.ValidString(text) }
