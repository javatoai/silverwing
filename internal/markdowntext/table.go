package markdowntext

import (
	"strings"
)

// TableCells returns editable source ranges relative to the supplied table
// part. Pipes inside escaped text or an inline-code span do not split cells.
func TableCells(source string) [][]TableCell {
	var rows [][]TableCell
	lineOffset := 0
	for _, line := range scanLines(source) {
		raw := line.text
		if strings.TrimSpace(raw) != "" && !separatorOnly(raw) {
			var pipes []int
			escaped, codeTicks := false, 0
			for i := 0; i < len(raw); {
				char := raw[i]
				if escaped {
					escaped = false
					i++
					continue
				}
				if char == '\\' {
					escaped = true
					i++
					continue
				}
				if char == '`' {
					begin := i
					for i < len(raw) && raw[i] == '`' {
						i++
					}
					count := i - begin
					if codeTicks == 0 {
						codeTicks = count
					} else if count == codeTicks {
						codeTicks = 0
					}
					continue
				}
				if char == '|' && codeTicks == 0 {
					pipes = append(pipes, i)
				}
				i++
			}
			boundaries := make([]int, 0, len(pipes)+2)
			boundaries = append(boundaries, -1)
			boundaries = append(boundaries, pipes...)
			boundaries = append(boundaries, len(raw))
			cells := make([]TableCell, 0, len(boundaries)-1)
			for i := 0; i+1 < len(boundaries); i++ {
				left, right := boundaries[i], boundaries[i+1]
				if (left == -1 && right < len(raw) && strings.TrimSpace(raw[:right]) == "") ||
					(right == len(raw) && left >= 0 && strings.TrimSpace(raw[left+1:]) == "") {
					continue
				}
				cellValue := raw[left+1 : right]
				leading := len(cellValue) - len(strings.TrimLeft(cellValue, " \t"))
				trailing := len(cellValue) - len(strings.TrimRight(cellValue, " \t"))
				start := lineOffset + left + 1 + leading
				end := lineOffset + right - trailing
				if end < start {
					end = start
				}
				cells = append(cells, TableCell{Row: len(rows), Column: len(cells), Range: SourceRange{start, end}, Text: source[start:end]})
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
		}
		lineOffset = line.end
	}
	return rows
}

// ParseTable extracts the GFM display table, honoring the header's column
// count and keeping escaped separators as cell content.
func ParseTable(source string) (Table, bool) {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			content = append(content, line)
		}
	}
	if len(content) < 2 {
		return Table{}, false
	}
	header := splitTableRow(content[0])
	separators := splitTableRow(content[1])
	if len(header) == 0 || len(separators) != len(header) {
		return Table{}, false
	}
	for _, separator := range separators {
		if !tableSepRE.MatchString(strings.TrimSpace(separator)) {
			return Table{}, false
		}
	}
	rows := [][]string{header}
	for _, line := range content[2:] {
		cells := splitTableRow(line)
		row := make([]string, len(header))
		copy(row, cells)
		rows = append(rows, row)
	}
	return Table{Rows: rows, Alignment: separators}, true
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "|") {
		trimmed = trimmed[1:]
	}
	if strings.HasSuffix(trimmed, "|") {
		backslashes := 0
		for i := len(trimmed) - 2; i >= 0 && trimmed[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			trimmed = trimmed[:len(trimmed)-1]
		}
	}
	var cells []string
	var current strings.Builder
	escaped, codeTicks := false, 0
	for i := 0; i < len(trimmed); {
		char := trimmed[i]
		if escaped {
			if char != '|' {
				current.WriteByte('\\')
			}
			current.WriteByte(char)
			escaped = false
			i++
			continue
		}
		if char == '\\' {
			escaped = true
			i++
			continue
		}
		if char == '`' {
			begin := i
			for i < len(trimmed) && trimmed[i] == '`' {
				i++
			}
			count := i - begin
			if codeTicks == 0 {
				codeTicks = count
			} else if count == codeTicks {
				codeTicks = 0
			}
			current.WriteString(trimmed[begin:i])
			continue
		}
		if char == '|' && codeTicks == 0 {
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
			i++
			continue
		}
		current.WriteByte(char)
		i++
	}
	if escaped {
		current.WriteByte('\\')
	}
	cells = append(cells, strings.TrimSpace(current.String()))
	return cells
}

func separatorOnly(line string) bool {
	for i := 0; i < len(line); i++ {
		if !strings.ContainsRune(" \t|:-", rune(line[i])) {
			return false
		}
	}
	return true
}

// TableFragment serializes a rectangular selection as Markdown, preserving
// the source's literal escapes and newlines while making cells rectangular.
func TableFragment(table Table, firstRow, lastRow, firstColumn, lastColumn int) string {
	if len(table.Rows) == 0 || firstColumn < 0 || lastColumn < firstColumn {
		return ""
	}
	firstRow = Clamp(firstRow, 0, len(table.Rows)-1)
	lastRow = Clamp(lastRow, firstRow, len(table.Rows)-1)
	columnCount := len(table.Rows[0])
	if columnCount == 0 {
		return ""
	}
	firstColumn = Clamp(firstColumn, 0, columnCount-1)
	lastColumn = Clamp(lastColumn, firstColumn, columnCount-1)
	line := func(cells []string) string {
		for i := range cells {
			cells[i] = strings.ReplaceAll(strings.ReplaceAll(cells[i], "|", "\\|"), "\n", "<br>")
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	selected := make([]string, 0, lastColumn-firstColumn+1)
	for column := firstColumn; column <= lastColumn; column++ {
		selected = append(selected, rowValue(table.Rows[0], column))
	}
	lines := []string{line(selected)}
	selected = selected[:0]
	for column := firstColumn; column <= lastColumn; column++ {
		alignment := "---"
		if column < len(table.Alignment) && strings.TrimSpace(table.Alignment[column]) != "" {
			alignment = table.Alignment[column]
		}
		selected = append(selected, alignment)
	}
	lines = append(lines, line(selected))
	for row := maxInt(1, firstRow); row <= lastRow; row++ {
		selected = selected[:0]
		for column := firstColumn; column <= lastColumn; column++ {
			selected = append(selected, rowValue(table.Rows[row], column))
		}
		lines = append(lines, line(selected))
	}
	return strings.Join(lines, "\n")
}

func rowValue(row []string, column int) string {
	if column < 0 || column >= len(row) {
		return ""
	}
	return row[column]
}
func Clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
