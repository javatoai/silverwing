package com.snowball.silverwing.desktop

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import com.mikepenz.markdown.annotator.annotatorSettings
import com.mikepenz.markdown.annotator.buildMarkdownAnnotatedString
import com.mikepenz.markdown.compose.LocalMarkdownExtendedSpans
import com.mikepenz.markdown.compose.elements.MarkdownText
import org.intellij.markdown.IElementType
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.ast.findChildOfType

@Composable internal fun SearchableMarkdownText(content: String, node: ASTNode, style: TextStyle,
    childType: IElementType? = null, modifier: Modifier = Modifier) {
    val settings = annotatorSettings()
    val annotated = buildAnnotatedString {
        pushStyle(style.toSpanStyle())
        buildMarkdownAnnotatedString(content, childType?.let(node::findChildOfType) ?: node, annotatorSettings = settings)
        pop()
    }
    // AST offsets retain reading order when earlier blocks are inserted or re-created.
    val (value, binding) = searchableText(annotated, modifier, sourceOrder = node.startOffset.toLong())
    MarkdownText(value, node, modifier = binding.first, style = style,
        onTextLayout = { layout, _ -> binding.second(layout) }, sourceContent = content,
        extendedSpans = LocalMarkdownExtendedSpans.current.extendedSpans?.invoke())
}
