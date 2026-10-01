package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
internal fun RequirementMaterialsTextPreview(
    content: String,
    extension: String,
    mode: MarkdownPreviewMode,
    onCopyFragment: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (mode == MarkdownPreviewMode.SOURCE) {
        PlainTextDocumentPreview(content, modifier)
        return
    }
    when (extension) {
        "json" -> JsonDocumentPreview(content, modifier)
        "csv" -> CsvDocumentPreview(content, onCopyFragment, modifier)
        else -> SelectionContainer {
            Text(content, modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), softWrap = true)
        }
    }
}
