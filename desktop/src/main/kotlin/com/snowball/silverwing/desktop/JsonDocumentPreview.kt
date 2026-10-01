package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement

private val prettyJsonFormat = Json { prettyPrint = true }

internal fun prettyJsonOrNull(source: String): String? = runCatching {
    val element = Json.parseToJsonElement(source)
    prettyJsonFormat.encodeToString(JsonElement.serializer(), element)
}.getOrNull()

@Composable
internal fun JsonDocumentPreview(content: String, modifier: Modifier = Modifier) {
    val pretty = remember(content) { prettyJsonOrNull(content) }
    Column(modifier) {
        if (pretty == null) Text("JSON 解析失败，显示原文。", color = MaterialTheme.colorScheme.error)
        SelectionContainer {
            Text(
                if (pretty == null) AnnotatedString(content) else highlightedJson(pretty),
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
                fontFamily = FontFamily.Monospace,
            )
        }
    }
}

private val jsonTokens = Regex("\"(?:\\\\.|[^\"\\\\])*\"(?=\\s*:)|\"(?:\\\\.|[^\"\\\\])*\"|\\b(?:true|false|null)\\b|-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?")

private fun highlightedJson(text: String): AnnotatedString = buildAnnotatedString {
    append(text)
    jsonTokens.findAll(text).forEach { match ->
        val token = match.value
        val color = when {
            token.startsWith('"') && text.drop(match.range.last + 1).trimStart().startsWith(':') -> Color(0xFF6750A4)
            token.startsWith('"') -> Color(0xFF087F5B)
            else -> Color(0xFFB05A00)
        }
        addStyle(SpanStyle(color = color), match.range.first, match.range.last + 1)
    }
}
