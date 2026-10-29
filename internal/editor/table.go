package editor

import "silverwing-go/internal/markdowntext"

func TableCells(source string) [][]TableCell { return markdowntext.TableCells(source) }
func ParseTable(source string) (Table, bool) { return markdowntext.ParseTable(source) }
func TableFragment(table Table, firstRow, lastRow, firstColumn, lastColumn int) string {
	return markdowntext.TableFragment(table, firstRow, lastRow, firstColumn, lastColumn)
}
func clamp(value, minimum, maximum int) int { return markdowntext.Clamp(value, minimum, maximum) }
