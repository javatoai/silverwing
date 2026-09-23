package com.snowball.silverwing.desktop

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryScrollableTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.mikepenz.markdown.compose.Markdown
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography

/** One independently previewable Markdown file. */
internal data class MarkdownPreviewFile(
    val path: String,
    val content: String,
) {
    init {
        require(path.isNotBlank()) { "Markdown 预览文件路径不能为空" }
    }

    val fileName: String get() = path.substringAfterLast('/')
}

internal enum class MarkdownPreviewMode {
    RENDERED,
    SOURCE,
}

/** Each newly selected document starts in the rendered Markdown view. */
internal fun initialMarkdownPreviewMode(): MarkdownPreviewMode = MarkdownPreviewMode.RENDERED

internal enum class MarkdownPreviewToggleIcon {
    CODE,
    VISIBILITY,
}

internal data class MarkdownPreviewTogglePresentation(
    val targetMode: MarkdownPreviewMode,
    val label: String,
    val icon: MarkdownPreviewToggleIcon,
)

internal fun markdownPreviewTogglePresentation(mode: MarkdownPreviewMode): MarkdownPreviewTogglePresentation = when (mode) {
    MarkdownPreviewMode.RENDERED -> MarkdownPreviewTogglePresentation(
        targetMode = MarkdownPreviewMode.SOURCE,
        label = "查看 Markdown 源码",
        icon = MarkdownPreviewToggleIcon.CODE,
    )
    MarkdownPreviewMode.SOURCE -> MarkdownPreviewTogglePresentation(
        targetMode = MarkdownPreviewMode.RENDERED,
        label = "查看 Markdown 预览",
        icon = MarkdownPreviewToggleIcon.VISIBILITY,
    )
}

internal fun selectMarkdownPreviewFile(
    files: List<MarkdownPreviewFile>,
    path: String?,
): MarkdownPreviewFile? = files.firstOrNull { it.path == path } ?: files.firstOrNull()

/** Clipboard payload is always the exact raw Markdown for the selected file. */
internal fun markdownPreviewSourceCopyPayload(file: MarkdownPreviewFile): String = file.content

/** Reusable, stateless Markdown content renderer. */
@Composable
internal fun MarkdownDocumentPreview(
    content: String,
    mode: MarkdownPreviewMode,
    modifier: Modifier = Modifier,
) {
    when (mode) {
        MarkdownPreviewMode.RENDERED -> MarkdownRenderedContent(content, modifier)
        MarkdownPreviewMode.SOURCE -> MarkdownSourceContent(content, modifier)
    }
}

/** Reusable read-only source viewer for scripts and other plain-text documents. */
@Composable
internal fun PlainTextDocumentPreview(
    content: String,
    modifier: Modifier = Modifier,
    onCopySource: (() -> Unit)? = null,
) {
    Column(modifier) {
        onCopySource?.let { copy ->
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 6.dp),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                ActionIconButton(
                    label = "复制文件内容",
                    onClick = copy,
                    modifier = Modifier.size(30.dp),
                    enabled = content.isNotEmpty(),
                ) {
                    Icon(Icons.Outlined.ContentCopy, "复制文件内容", Modifier.size(16.dp))
                }
            }
        }
        MarkdownSourceContent(content, Modifier.weight(1f).fillMaxWidth())
    }
}

/**
 * Reusable single-document reader for callers that do not provide their own file selector.
 * The lower overload above remains the stateless rendering surface used by richer containers.
 */
@Composable
internal fun MarkdownDocumentPreview(
    content: String,
    modifier: Modifier = Modifier,
    documentKey: Any? = content,
    onCopySource: (() -> Unit)? = null,
) {
    var mode by remember(documentKey) { mutableStateOf(initialMarkdownPreviewMode()) }
    Column(modifier) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.End,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            MarkdownPreviewToolbarActions(
                mode = mode,
                onModeChange = { mode = it },
                onCopySource = onCopySource,
                copyEnabled = content.isNotEmpty(),
            )
        }
        MarkdownDocumentPreview(
            content = content,
            mode = mode,
            modifier = Modifier.weight(1f).fillMaxWidth(),
        )
    }
}

/** Compact, reusable action that switches between rendered Markdown and raw source. */
@Composable
internal fun MarkdownPreviewModeToggle(
    mode: MarkdownPreviewMode,
    onModeChange: (MarkdownPreviewMode) -> Unit,
    modifier: Modifier = Modifier,
) {
    val presentation = markdownPreviewTogglePresentation(mode)
    ActionIconButton(
        label = presentation.label,
        onClick = { onModeChange(presentation.targetMode) },
        modifier = modifier,
    ) {
        when (presentation.icon) {
            MarkdownPreviewToggleIcon.CODE -> Icon(Icons.Outlined.Code, presentation.label, Modifier.size(16.dp))
            MarkdownPreviewToggleIcon.VISIBILITY -> Icon(Icons.Outlined.Visibility, presentation.label, Modifier.size(16.dp))
        }
    }
}

/** Shared compact actions for Markdown viewers with caller-owned mode and clipboard behavior. */
@Composable
internal fun MarkdownPreviewToolbarActions(
    mode: MarkdownPreviewMode,
    onModeChange: (MarkdownPreviewMode) -> Unit,
    onCopySource: (() -> Unit)? = null,
    copyEnabled: Boolean = true,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier,
        horizontalArrangement = Arrangement.spacedBy(2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        MarkdownPreviewModeToggle(mode, onModeChange, Modifier.size(30.dp))
        onCopySource?.let { copy ->
            ActionIconButton(
                label = "复制 Markdown 源码",
                onClick = copy,
                modifier = Modifier.size(30.dp),
                enabled = copyEnabled,
            ) {
                Icon(Icons.Outlined.ContentCopy, "复制 Markdown 源码", Modifier.size(16.dp))
            }
        }
    }
}

/** One compact file identity row with room for the caller's preview actions. */
@Composable
internal fun DocumentPreviewFileHeader(
    fileName: String?,
    relativePath: String,
    modifier: Modifier = Modifier,
    actions: @Composable () -> Unit,
) {
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            fileName?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodyMedium,
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                relativePath,
                style = if (fileName == null) MaterialTheme.typography.labelMedium else MaterialTheme.typography.labelSmall,
                fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        actions()
    }
}

/**
 * Reusable horizontal document selector. It keeps the selected file while its content refreshes,
 * and falls back to the first file only if the selected path is no longer present.
 */
@Composable
internal fun MarkdownFileTabsPreview(
    files: List<MarkdownPreviewFile>,
    modifier: Modifier = Modifier,
    initialPath: String = files.firstOrNull()?.path.orEmpty(),
    onCopySource: (MarkdownPreviewFile) -> Unit,
) {
    if (files.isEmpty()) {
        Box(modifier, contentAlignment = Alignment.Center) {
            Text("没有可预览的 Markdown 文件", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        return
    }

    var selectedPath by remember(initialPath) { mutableStateOf(selectMarkdownPreviewFile(files, initialPath)!!.path) }
    val filePaths = files.map(MarkdownPreviewFile::path)
    LaunchedEffect(filePaths) {
        selectedPath = selectMarkdownPreviewFile(files, selectedPath)!!.path
    }
    val selectedFile = selectMarkdownPreviewFile(files, selectedPath)!!
    val selectedIndex = files.indexOf(selectedFile)
    var mode by remember(selectedFile.path) { mutableStateOf(initialMarkdownPreviewMode()) }

    Column(modifier) {
        if (files.size > 1) {
            PrimaryScrollableTabRow(selectedTabIndex = selectedIndex, edgePadding = 14.dp) {
                files.forEachIndexed { index, file ->
                    Tab(
                        selected = index == selectedIndex,
                        onClick = { selectedPath = file.path },
                        text = { Text(file.fileName, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    )
                }
            }
        }
        DocumentPreviewFileHeader(
            fileName = null,
            relativePath = selectedFile.path,
            modifier = Modifier.padding(horizontal = 18.dp, vertical = 8.dp),
        ) {
            MarkdownPreviewToolbarActions(
                mode = mode,
                onModeChange = { mode = it },
                onCopySource = { onCopySource(selectedFile) },
                copyEnabled = selectedFile.content.isNotEmpty(),
            )
        }
        MarkdownDocumentPreview(
            content = selectedFile.content,
            mode = mode,
            modifier = Modifier.weight(1f).fillMaxWidth(),
        )
    }
}

@Composable
private fun MarkdownRenderedContent(content: String, modifier: Modifier) {
    val verticalScroll = rememberScrollState()
    Box(modifier.padding(15.dp).verticalScroll(verticalScroll)) {
        // Markdown tables and code blocks own their horizontal scrolling. Wrapping the whole
        // renderer in another horizontal scroll would measure those children with infinite
        // width and crashes on layouts that activate the renderer's internal overflow path.
        Markdown(
            content = content,
            colors = markdownColor(),
            typography = markdownTypography(
                h1 = MaterialTheme.typography.titleLarge,
                h2 = MaterialTheme.typography.titleMedium,
                h3 = MaterialTheme.typography.titleSmall,
                h4 = MaterialTheme.typography.labelLarge,
                h5 = MaterialTheme.typography.labelLarge,
                h6 = MaterialTheme.typography.labelLarge,
                text = MaterialTheme.typography.bodyMedium,
                paragraph = MaterialTheme.typography.bodyMedium,
                table = MaterialTheme.typography.bodySmall,
                code = MaterialTheme.typography.bodySmall,
                inlineCode = MaterialTheme.typography.bodySmall,
            ),
            modifier = Modifier.fillMaxWidth(),
            retainState = true,
        )
    }
}

@Composable
private fun MarkdownSourceContent(content: String, modifier: Modifier) {
    val verticalScroll = rememberScrollState()
    val horizontalScroll = rememberScrollState()
    SelectionContainer {
        Box(modifier.padding(15.dp).verticalScroll(verticalScroll).horizontalScroll(horizontalScroll)) {
            if (content.isBlank()) {
                Text("（空）", color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                Text(
                    content,
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.bodySmall,
                    softWrap = false,
                )
            }
        }
    }
}
