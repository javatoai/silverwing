package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type DiffOptions struct {
	IgnoreLineWhitespace bool `json:"ignoreLineWhitespace"`
	IgnoreCase           bool `json:"ignoreCase"`
	IgnoreEmptyLines     bool `json:"ignoreEmptyLines"`
	ContextLines         int  `json:"contextLines"`
}
type TextSpan struct {
	Start        int `json:"start"`
	EndExclusive int `json:"endExclusive"`
}
type DiffLine struct {
	Number       int        `json:"number"`
	Text         string     `json:"text"`
	ChangedSpans []TextSpan `json:"changedSpans"`
}
type DiffRow struct {
	Left  *DiffLine `json:"left"`
	Right *DiffLine `json:"right"`
	Kind  string    `json:"kind"`
}
type DiffHunk struct {
	StartRow        int  `json:"startRow"`
	EndRowExclusive int  `json:"endRowExclusive"`
	LeftStartLine   *int `json:"leftStartLine"`
	RightStartLine  *int `json:"rightStartLine"`
	AddedLines      int  `json:"addedLines"`
	RemovedLines    int  `json:"removedLines"`
	ChangedLines    int  `json:"changedLines"`
}
type DiffResult struct {
	Rows          []DiffRow  `json:"rows"`
	Hunks         []DiffHunk `json:"hunks"`
	AddedLines    int        `json:"addedLines"`
	RemovedLines  int        `json:"removedLines"`
	ChangedLines  int        `json:"changedLines"`
	ElapsedMillis int64      `json:"elapsedMillis"`
	Unified       string     `json:"unified"`
}

// DifferencesText matches the source tool's clipboard export: only changed
// rows, with a marker and a space before each original line of text.
func (result DiffResult) DifferencesText() string {
	var text strings.Builder
	for _, row := range result.Rows {
		switch row.Kind {
		case "REMOVED", "CHANGED":
			if row.Left != nil {
				text.WriteString("- " + row.Left.Text + "\n")
			}
		}
		switch row.Kind {
		case "ADDED", "CHANGED":
			if row.Right != nil {
				text.WriteString("+ " + row.Right.Text + "\n")
			}
		}
	}
	return text.String()
}

type comparisonLine struct {
	number           int
	text, comparison string
}
type edit struct {
	kind        byte
	left, right int
}

func CompareText(ctx context.Context, left, right string, options DiffOptions) (DiffResult, error) {
	if err := checkSize(left); err != nil {
		return DiffResult{}, err
	}
	if err := checkSize(right); err != nil {
		return DiffResult{}, err
	}
	if options.ContextLines < 0 {
		return DiffResult{}, errors.New("上下文行数不能小于 0")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	l, err := diffLines(ctx, left, options)
	if err != nil {
		return DiffResult{}, err
	}
	r, err := diffLines(ctx, right, options)
	if err != nil {
		return DiffResult{}, err
	}
	edits, err := myers(ctx, l, r)
	if err != nil {
		return DiffResult{}, err
	}
	result := DiffResult{Rows: []DiffRow{}, Hunks: []DiffHunk{}}
	line := func(lines []comparisonLine, i int) *DiffLine {
		v := lines[i]
		return &DiffLine{Number: v.number, Text: v.text, ChangedSpans: []TextSpan{}}
	}
	for i := 0; i < len(edits); {
		if err := diffBudget(ctx, i); err != nil {
			return DiffResult{}, err
		}
		if edits[i].kind == '=' {
			e := edits[i]
			result.Rows = append(result.Rows, DiffRow{Left: line(l, e.left), Right: line(r, e.right), Kind: "EQUAL"})
			i++
			continue
		}
		var removed, added []*DiffLine
		for i < len(edits) && edits[i].kind != '=' {
			if err := diffBudget(ctx, i); err != nil {
				return DiffResult{}, err
			}
			e := edits[i]
			if e.kind == '-' {
				removed = append(removed, line(l, e.left))
			} else {
				added = append(added, line(r, e.right))
			}
			i++
		}
		count := len(removed)
		if len(added) > count {
			count = len(added)
		}
		for j := 0; j < count; j++ {
			if err := diffBudget(ctx, j); err != nil {
				return DiffResult{}, err
			}
			row := DiffRow{}
			if j < len(removed) {
				row.Left = removed[j]
			}
			if j < len(added) {
				row.Right = added[j]
			}
			switch {
			case row.Left != nil && row.Right != nil:
				row.Kind = "CHANGED"
				result.ChangedLines++
				row.Left.ChangedSpans, row.Right.ChangedSpans, err = changedSpan(ctx, row.Left.Text, row.Right.Text)
				if err != nil {
					return DiffResult{}, err
				}
			case row.Left != nil:
				row.Kind = "REMOVED"
				row.Left.ChangedSpans = fullSpan(row.Left.Text)
				result.RemovedLines++
			default:
				row.Kind = "ADDED"
				row.Right.ChangedSpans = fullSpan(row.Right.Text)
				result.AddedLines++
			}
			result.Rows = append(result.Rows, row)
		}
	}
	// Source hunks span the first through last changed row. Context is only
	// used to group nearby changes; export padding is a separate projection.
	var ranges [][2]int
	for i, row := range result.Rows {
		if err := diffBudget(ctx, i); err != nil {
			return DiffResult{}, err
		}
		if row.Kind == "EQUAL" {
			continue
		}
		if len(ranges) > 0 && i-ranges[len(ranges)-1][1] <= 2*min(options.ContextLines, len(result.Rows)) {
			ranges[len(ranges)-1][1] = i + 1
		} else {
			ranges = append(ranges, [2]int{i, i + 1})
		}
	}
	var unified strings.Builder
	unified.WriteString("--- 左侧\n+++ 右侧\n")
	for _, bounds := range ranges {
		h := DiffHunk{StartRow: bounds[0], EndRowExclusive: bounds[1]}
		for index, row := range result.Rows[bounds[0]:bounds[1]] {
			if err := diffBudget(ctx, index); err != nil {
				return DiffResult{}, err
			}
			if row.Left != nil {
				if h.LeftStartLine == nil {
					n := row.Left.Number
					h.LeftStartLine = &n
				}
			}
			if row.Right != nil {
				if h.RightStartLine == nil {
					n := row.Right.Number
					h.RightStartLine = &n
				}
			}
			switch row.Kind {
			case "ADDED":
				h.AddedLines++
			case "REMOVED":
				h.RemovedLines++
			case "CHANGED":
				h.ChangedLines++
			}
		}
		result.Hunks = append(result.Hunks, h)
		padding := min(options.ContextLines, len(result.Rows))
		start, end := max(0, bounds[0]-padding), min(len(result.Rows), bounds[1]+padding)
		leftCount, rightCount := 0, 0
		ls, rs := 0, 0
		for _, row := range result.Rows[start:end] {
			if row.Left != nil {
				if leftCount == 0 {
					ls = row.Left.Number
				}
				leftCount++
			}
			if row.Right != nil {
				if rightCount == 0 {
					rs = row.Right.Number
				}
				rightCount++
			}
		}
		fmt.Fprintf(&unified, "@@ -%d,%d +%d,%d @@\n", ls, leftCount, rs, rightCount)
		for index, row := range result.Rows[start:end] {
			if err := diffBudget(ctx, index); err != nil {
				return DiffResult{}, err
			}
			switch row.Kind {
			case "EQUAL":
				unified.WriteString(" " + row.Left.Text + "\n")
			case "CHANGED":
				unified.WriteString("-" + row.Left.Text + "\n+" + row.Right.Text + "\n")
			case "REMOVED":
				unified.WriteString("-" + row.Left.Text + "\n")
			case "ADDED":
				unified.WriteString("+" + row.Right.Text + "\n")
			}
		}
	}
	result.Unified = unified.String()
	if err := diffBudget(ctx, 0); err != nil {
		return DiffResult{}, err
	}
	result.ElapsedMillis = time.Since(started).Milliseconds()
	return result, nil
}
func diffLines(ctx context.Context, text string, options DiffOptions) ([]comparisonLine, error) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	result := []comparisonLine{}
	if text == "" {
		return result, nil
	}
	lower := cases.Lower(language.Und)
	for i, line := range strings.Split(text, "\n") {
		if err := diffBudget(ctx, i); err != nil {
			return nil, err
		}
		if options.IgnoreEmptyLines && strings.TrimFunc(line, sourceWhitespace) == "" {
			continue
		}
		compare := line
		if options.IgnoreLineWhitespace {
			compare = strings.TrimFunc(compare, sourceWhitespace)
		}
		if options.IgnoreCase {
			compare = lower.String(compare)
		}
		result = append(result, comparisonLine{i + 1, line, compare})
	}
	return result, nil
}

func sourceWhitespace(r rune) bool {
	return unicode.Is(unicode.Z, r) || r >= '\t' && r <= '\r' || r >= 0x1c && r <= 0x1f
}

func diffBudget(ctx context.Context, index int) error {
	if index&255 == 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("差异计算超过 5 秒或已取消：%w", err)
		}
	}
	return nil
}

// A path retains matching anchors and compresses consecutive insert/delete
// steps. This avoids copying a full frontier for every edit distance.
type diffPathNode struct {
	x, y     int
	matching bool
	previous *diffPathNode
}

func myers(ctx context.Context, left, right []comparisonLine) ([]edit, error) {
	n, m := len(left), len(right)
	origin := &diffPathNode{matching: true}
	frontier := map[int]*diffPathNode{1: origin}
	for distance := 0; distance <= n+m; distance++ {
		if err := diffBudget(ctx, 0); err != nil {
			return nil, err
		}
		for diagonal := -distance; diagonal <= distance; diagonal += 2 {
			if err := diffBudget(ctx, diagonal+distance); err != nil {
				return nil, err
			}
			var previous *diffPathNode
			x := 0
			if diagonal == -distance || diagonal != distance && frontier[diagonal-1].x < frontier[diagonal+1].x {
				previous = frontier[diagonal+1]
				x = previous.x
			} else {
				previous = frontier[diagonal-1]
				x = previous.x + 1
			}
			y := x - diagonal
			anchor := previous
			if !anchor.matching {
				anchor = anchor.previous
			}
			node := &diffPathNode{x: x, y: y, previous: anchor}
			for x < n && y < m && left[x].comparison == right[y].comparison {
				if err := diffBudget(ctx, x); err != nil {
					return nil, err
				}
				x++
				y++
			}
			if x != node.x {
				node = &diffPathNode{x: x, y: y, matching: true, previous: node}
			}
			frontier[diagonal] = node
			if x >= n && y >= m {
				return reconstructPath(ctx, node)
			}
		}
	}
	return nil, errors.New("无法计算差异")
}

func reconstructPath(ctx context.Context, path *diffPathNode) ([]edit, error) {
	var reversed []edit
	for node := path; node != nil && node.previous != nil; node = node.previous {
		previous := node.previous
		if node.matching {
			for x, y := node.x, node.y; x > previous.x && y > previous.y; {
				if err := diffBudget(ctx, len(reversed)); err != nil {
					return nil, err
				}
				x--
				y--
				reversed = append(reversed, edit{'=', x, y})
			}
			continue
		}
		// The display aligns removed/added lines as one block; preserve their
		// respective source order while unwinding a compressed edit segment.
		for y := node.y - 1; y >= previous.y; y-- {
			if err := diffBudget(ctx, len(reversed)); err != nil {
				return nil, err
			}
			reversed = append(reversed, edit{'+', node.x, y})
		}
		for x := node.x - 1; x >= previous.x; x-- {
			if err := diffBudget(ctx, len(reversed)); err != nil {
				return nil, err
			}
			reversed = append(reversed, edit{'-', x, previous.y})
		}
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		if err := diffBudget(ctx, i); err != nil {
			return nil, err
		}
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

// RowChangedSpans exposes the existing inline comparison for an already paired
// row. It does not compute, filter, or reorder comparison rows.
func RowChangedSpans(ctx context.Context, left, right string) ([]TextSpan, []TextSpan, error) {
	return changedSpan(ctx, left, right)
}

func changedSpan(ctx context.Context, left, right string) ([]TextSpan, []TextSpan, error) {
	l, r := []rune(left), []rune(right)
	prefix := 0
	for prefix < len(l) && prefix < len(r) && l[prefix] == r[prefix] {
		if err := diffBudget(ctx, prefix); err != nil {
			return nil, nil, err
		}
		prefix++
	}
	le, re := len(l), len(r)
	for le > prefix && re > prefix && l[le-1] == r[re-1] {
		if err := diffBudget(ctx, len(l)-le); err != nil {
			return nil, nil, err
		}
		le--
		re--
	}
	span := func(points []rune, end int) []TextSpan {
		if prefix == end {
			return []TextSpan{}
		}
		return []TextSpan{{UTF16Length(string(points[:prefix])), UTF16Length(string(points[:end]))}}
	}
	return span(l, le), span(r, re), nil
}

func fullSpan(text string) []TextSpan {
	if text == "" {
		return []TextSpan{}
	}
	return []TextSpan{{0, UTF16Length(text)}}
}
