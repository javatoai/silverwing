package com.snowball.silverwing.desktop

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import com.mikepenz.markdown.compose.elements.MarkdownTableBasicText
import com.mikepenz.markdown.compose.LocalMarkdownColors
import com.mikepenz.markdown.model.MarkdownColors
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.flavours.gfm.GFMElementTypes.HEADER
import org.intellij.markdown.flavours.gfm.GFMElementTypes.ROW
import org.intellij.markdown.flavours.gfm.GFMTokenTypes.CELL

internal data class PreviewMarkdownTable(val rows: List<List<String>>, val alignment: List<String>)

internal fun parsePreviewMarkdownTable(source: String): PreviewMarkdownTable? {
    val lines = source.lines().filter(String::isNotBlank)
    if (lines.size < 2) return null
    val header = splitMarkdownTableRow(lines[0])
    val separators = splitMarkdownTableRow(lines[1])
    if (header.isEmpty() || separators.size != header.size || separators.any { !it.matches(Regex(":?-{3,}:?")) }) return null
    return PreviewMarkdownTable(listOf(header) + lines.drop(2).map(::splitMarkdownTableRow), separators)
}

private fun splitMarkdownTableRow(line: String): List<String> {
    val trimmed = line.trim().removePrefix("|").removeSuffix("|")
    val cells = mutableListOf<String>()
    val current = StringBuilder()
    var escaped = false
    for (char in trimmed) {
        when {
            escaped -> { current.append(char); escaped = false }
            char == '\\' -> escaped = true
            char == '|' -> { cells += current.toString().trim(); current.clear() }
            else -> current.append(char)
        }
    }
    cells += current.toString().trim()
    return cells
}

internal fun markdownTableFragment(table: PreviewMarkdownTable, firstRow: Int, lastRow: Int, firstColumn: Int, lastColumn: Int): String {
    fun line(cells: List<String>): String = "| " + cells.joinToString(" | ") { it.replace("|", "\\|").replace("\n", "<br>") } + " |"
    val columns = firstColumn..lastColumn
    val header = line(columns.map { table.rows[0].getOrNull(it).orEmpty() })
    val alignment = line(columns.map { table.alignment.getOrNull(it) ?: "---" })
    val data = (maxOf(1, firstRow)..lastRow).takeIf { lastRow >= 1 }?.map { row ->
        line(columns.map { table.rows.getOrNull(row)?.getOrNull(it).orEmpty() })
    }.orEmpty()
    return (listOf(header, alignment) + data).joinToString("\n")
}

@Composable
internal fun SelectableMarkdownTable(
    table: PreviewMarkdownTable,
    markdownContent: String,
    node: ASTNode,
    style: TextStyle,
    onCopy: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = LocalMarkdownColors.current
    val selectedColors = remember(colors) {
        object : MarkdownColors by colors {
            override val inlineCodeBackground = Color.Transparent
        }
    }
    val cells = node.children.filter { it.type == HEADER || it.type == ROW }
        .map { row -> row.children.filter { it.type == CELL } }
    SelectableDataGrid(
        rows = table.rows,
        modifier = modifier,
        cellContent = { row, column, selected ->
            cells.getOrNull(row)?.getOrNull(column)?.let { cell ->
                CompositionLocalProvider(LocalMarkdownColors provides if (selected) selectedColors else colors) {
                    MarkdownTableBasicText(
                        content = markdownContent,
                        cell = cell,
                        style = if (row == 0) style.copy(fontWeight = FontWeight.Bold) else style,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
        },
        onCopy = { firstRow, lastRow, firstColumn, lastColumn ->
            onCopy(markdownTableFragment(table, firstRow, lastRow, firstColumn, lastColumn))
        },
    )
}
