package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier

internal fun parseCsvOrNull(source: String): List<List<String>>? {
    if (source.isEmpty()) return emptyList()
    val rows = mutableListOf<List<String>>()
    val row = mutableListOf<String>()
    val cell = StringBuilder()
    var quoted = false
    var closedQuote = false
    var index = 0
    while (index < source.length) {
        val ch = source[index]
        when {
            quoted && ch == '"' && index + 1 < source.length && source[index + 1] == '"' -> {
                cell.append('"'); index++
            }
            quoted && ch == '"' -> { quoted = false; closedQuote = true }
            quoted -> cell.append(ch)
            ch == '"' && cell.isEmpty() && !closedQuote -> quoted = true
            ch == ',' -> { row += cell.toString(); cell.clear(); closedQuote = false }
            ch == '\n' || ch == '\r' -> {
                row += cell.toString(); cell.clear(); rows += row.toList(); row.clear(); closedQuote = false
                if (ch == '\r' && index + 1 < source.length && source[index + 1] == '\n') index++
            }
            closedQuote || ch == '"' -> return null
            else -> cell.append(ch)
        }
        index++
    }
    if (quoted) return null
    if (row.isNotEmpty() || cell.isNotEmpty() || source.last() == ',') {
        row += cell.toString(); rows += row.toList()
    }
    return rows
}

internal fun csvFragment(rows: List<List<String>>, firstRow: Int, lastRow: Int, firstColumn: Int, lastColumn: Int): String =
    (firstRow..lastRow).joinToString("\r\n") { row ->
        (firstColumn..lastColumn).joinToString(",") { column ->
            val value = rows.getOrNull(row)?.getOrNull(column).orEmpty()
            if (value.any { it == '"' || it == ',' || it == '\n' || it == '\r' })
                "\"${value.replace("\"", "\"\"")}\"" else value
        }
    }

@Composable
internal fun CsvDocumentPreview(content: String, onCopy: (String) -> Unit, modifier: Modifier = Modifier) {
    val rows = remember(content) { parseCsvOrNull(content) }
    if (rows == null) Column(modifier) {
        Text("CSV 解析失败，显示原文。", color = MaterialTheme.colorScheme.error)
        PlainTextDocumentPreview(content, Modifier.fillMaxSize())
    } else SelectableDataGrid(rows, modifier, ownVerticalScroll = true) { firstRow, lastRow, firstColumn, lastColumn ->
        onCopy(csvFragment(rows, firstRow, lastRow, firstColumn, lastColumn))
    }
}
