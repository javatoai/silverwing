package com.snowball.silverwing.desktop

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import org.intellij.markdown.MarkdownElementTypes
import org.intellij.markdown.MarkdownTokenTypes
import org.intellij.markdown.ast.ASTNode
import org.intellij.markdown.ast.findChildOfType
import org.intellij.markdown.flavours.gfm.GFMFlavourDescriptor
import org.intellij.markdown.parser.MarkdownParser
import java.nio.file.Path

internal data class MarkdownOutlineEntry(
    val level: Int,
    val title: String,
    val anchor: String,
    /** Offset in the same normalized, image-resolved input supplied to the renderer. */
    val startOffset: Int,
)

internal data class MarkdownOutlineNavigation(val anchor: String, val sequence: Long)

/** Transient navigation only: reading positions remain in MaterialsReadingState. */
internal class MarkdownOutlineState(val entries: List<MarkdownOutlineEntry>) {
    var navigationRequest by mutableStateOf<MarkdownOutlineNavigation?>(null)
        private set
    private var sequence = 0L

    fun navigateTo(entry: MarkdownOutlineEntry) {
        if (entry in entries) navigationRequest = MarkdownOutlineNavigation(entry.anchor, ++sequence)
    }

    fun finishNavigation(request: MarkdownOutlineNavigation) {
        if (navigationRequest == request) navigationRequest = null
    }
}

@Composable
internal fun rememberMarkdownOutlineState(
    content: String,
    documentKey: Any? = content,
    sourcePath: Path? = null,
    allowedRoot: Path? = null,
): MarkdownOutlineState = remember(documentKey, content, sourcePath, allowedRoot) {
    MarkdownOutlineState(parseMarkdownOutline(content, sourcePath, allowedRoot))
}

internal fun parseMarkdownOutline(
    content: String,
    sourcePath: Path? = null,
    allowedRoot: Path? = null,
): List<MarkdownOutlineEntry> = markdownOutlineFromRenderInput(
    markdownPreviewRenderInput(splitLeadingMarkdownFrontMatter(content)?.remainingContent ?: content, sourcePath, allowedRoot),
)

/** Use the renderer's GFM parser so fences, indented code, and setext headings agree. */
internal fun markdownOutlineFromRenderInput(content: String): List<MarkdownOutlineEntry> {
    val tree = MarkdownParser(GFMFlavourDescriptor()).buildMarkdownTreeFromString(content)
    val result = mutableListOf<MarkdownOutlineEntry>()
    val usedAnchors = mutableSetOf<String>()
    fun visit(node: ASTNode) {
        val level = when (node.type) {
            MarkdownElementTypes.ATX_1, MarkdownElementTypes.SETEXT_1 -> 1
            MarkdownElementTypes.ATX_2, MarkdownElementTypes.SETEXT_2 -> 2
            MarkdownElementTypes.ATX_3 -> 3
            MarkdownElementTypes.ATX_4 -> 4
            MarkdownElementTypes.ATX_5 -> 5
            MarkdownElementTypes.ATX_6 -> 6
            else -> null
        }
        if (level != null) {
            val raw = content.substring(node.startOffset, node.endOffset)
            val textNode = node.findChildOfType(MarkdownTokenTypes.ATX_CONTENT)
                ?: node.findChildOfType(MarkdownTokenTypes.SETEXT_CONTENT) ?: node
            val title = markdownOutlineText(textNode, content).replace(Regex("\\s+"), " ").trim()
                .ifEmpty { "（无标题）" }
            // Keep existing link slugs; duplicate headings gain stable suffixes in source order.
            val base = markdownHeadingSlug(raw).ifEmpty { "section" }
            var anchor = base
            var suffix = 0
            while (!usedAnchors.add(anchor)) anchor = "$base-${++suffix}"
            result += MarkdownOutlineEntry(level, title, anchor, node.startOffset)
        } else node.children.forEach(::visit)
    }
    visit(tree)
    return result
}

private fun markdownOutlineText(node: ASTNode, content: String): String {
    if (node.type in setOf(MarkdownElementTypes.INLINE_LINK, MarkdownElementTypes.FULL_REFERENCE_LINK,
            MarkdownElementTypes.SHORT_REFERENCE_LINK, MarkdownElementTypes.IMAGE)) {
        val label = node.findChildOfType(MarkdownElementTypes.LINK_TEXT)
            ?: node.findChildOfType(MarkdownElementTypes.LINK_LABEL)
        return label?.let { markdownOutlineText(it, content).trim('[', ']') }.orEmpty()
    }
    if (node.children.isNotEmpty()) return node.children.joinToString("") { markdownOutlineText(it, content) }
    if (node.type in setOf(MarkdownTokenTypes.EMPH, MarkdownTokenTypes.BACKTICK,
            MarkdownTokenTypes.ATX_HEADER, MarkdownTokenTypes.HTML_TAG)) return ""
    return content.substring(node.startOffset, node.endOffset)
        .replace(Regex("""\\([\\`*_{}\[\]()#+\-.!>])"""), "$1")
}
