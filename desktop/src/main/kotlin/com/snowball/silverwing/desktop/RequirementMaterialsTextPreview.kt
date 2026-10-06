package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.HorizontalScrollbar
import androidx.compose.foundation.VerticalScrollbar
import androidx.compose.foundation.rememberScrollbarAdapter
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.WorkspaceFileLanguage

@Composable
internal fun RequirementMaterialsTextPreview(
    content: String,
    extension: String,
    mode: MarkdownPreviewMode,
    onCopyFragment: (String) -> Unit,
    modifier: Modifier = Modifier,
    fileName: String = "file.$extension",
) {
    if (mode == MarkdownPreviewMode.SOURCE) {
        PlainTextDocumentPreview(content, modifier)
        return
    }
    val language = WorkspaceFileLanguage.fromPath(fileName)
    when {
        extension == "json" -> JsonDocumentPreview(content, modifier)
        extension == "csv" -> CsvDocumentPreview(content, onCopyFragment, modifier)
        language != WorkspaceFileLanguage.PLAIN_TEXT -> RequirementMaterialsCodePreview(content, language, modifier)
        else -> SelectionContainer {
            SearchableText(content, modifier.fillMaxSize().verticalScroll(rememberMaterialsScrollState("text")).padding(16.dp), softWrap = true)
        }
    }
}

@Composable
private fun RequirementMaterialsCodePreview(content: String, language: WorkspaceFileLanguage, modifier: Modifier) {
    val palette = workspacePreviewPalette()
    val highlighted = remember(content, language, palette) {
        workspaceHighlightedText(content, language, palette)
    }
    val vertical = rememberMaterialsScrollState("code-vertical")
    val horizontal = rememberMaterialsScrollState("code-horizontal")
    Column(modifier) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            SelectionContainer {
                Box(Modifier.fillMaxSize().padding(end = 12.dp).verticalScroll(vertical).horizontalScroll(horizontal).padding(16.dp)) {
                    SearchableText(highlighted, style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), softWrap = false)
                }
            }
            VerticalScrollbar(rememberScrollbarAdapter(vertical), Modifier.align(Alignment.CenterEnd))
        }
        HorizontalScrollbar(rememberScrollbarAdapter(horizontal), Modifier.fillMaxWidth())
    }
}
