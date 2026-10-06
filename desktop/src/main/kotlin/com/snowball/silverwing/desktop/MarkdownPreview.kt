package com.snowball.silverwing.desktop

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.text.selection.LocalTextSelectionColors
import androidx.compose.foundation.text.selection.TextSelectionColors
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.PrimaryScrollableTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.Surface
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.foundation.clickable
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.mikepenz.markdown.model.ImageTransformer
import com.mikepenz.markdown.model.ImageData
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.input.pointer.PointerIcon
import com.mikepenz.markdown.coil3.Coil3ImageTransformerImpl
import com.mikepenz.markdown.compose.Markdown
import com.mikepenz.markdown.compose.components.CurrentComponentsBridge
import com.mikepenz.markdown.compose.components.markdownComponents
import com.mikepenz.markdown.compose.elements.MarkdownCodeBackground
import com.mikepenz.markdown.compose.elements.MarkdownCodeFence
import com.mikepenz.markdown.compose.elements.MarkdownTable
import com.mikepenz.markdown.compose.elements.MarkdownTableHeader
import com.mikepenz.markdown.compose.elements.MarkdownTableRow
import com.mikepenz.markdown.compose.extendedspans.ExtendedSpans
import com.mikepenz.markdown.compose.extendedspans.RoundedCornerSpanPainter
import com.mikepenz.markdown.m3.markdownColor
import com.mikepenz.markdown.m3.markdownTypography
import com.mikepenz.markdown.model.markdownDimens
import com.mikepenz.markdown.model.markdownExtendedSpans
import com.mikepenz.markdown.model.markdownPadding
import com.mikepenz.markdown.model.rememberMarkdownState
import com.mikepenz.markdown.model.State as MarkdownParseState
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.net.URI
import kotlin.math.roundToInt
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first

internal fun markdownSelectionSpans(): ExtendedSpans = ExtendedSpans(
    // 行内代码底色先画在文字后方，避免 SpanStyle 的不透明底色盖住选中高亮。
    // 零圆角和零边距保留现有外观；每个文本块须使用独立实例维护自己的布局。
    RoundedCornerSpanPainter(
        cornerRadius = 0.sp,
        padding = RoundedCornerSpanPainter.TextPaddingValues(),
        topMargin = 0.sp,
        bottomMargin = 0.sp,
    ),
)

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

/** Keep renderer preprocessing separate from the exact source used by copy and source view. */
internal fun markdownPreviewRenderInput(content: String, sourcePath: Path? = null, allowedRoot: Path? = null): String =
    // markdown-jvm's GFM table marker expects LF; a CR left on the separator line
    // prevents an otherwise valid Windows-authored table from becoming a TABLE node.
    markdownWithRelativeImagePaths(content, sourcePath, allowedRoot).replace("\r\n", "\n").replace('\r', '\n')

/** Reusable, stateless Markdown content renderer. */
@Composable
internal fun MarkdownDocumentPreview(
    content: String,
    mode: MarkdownPreviewMode,
    modifier: Modifier = Modifier,
    onCopyCode: ((String) -> Unit)? = null,
    sourcePath: Path? = null,
    allowedRoot: Path? = null,
    onNavigateLocalLink: ((String, String?) -> Unit)? = null,
    headingAnchor: String? = null,
    onImageClick: ((Painter) -> Unit)? = null,
    outlineState: MarkdownOutlineState? = null,
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(sourcePath, content) { DocumentFindState() }
        DocumentFindScope(find, modifier) { Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) { FindDocumentButton() }
            DocumentFindBar(find)
            MarkdownDocumentPreview(content, mode, Modifier.weight(1f).fillMaxWidth(), onCopyCode, sourcePath, allowedRoot,
                onNavigateLocalLink, headingAnchor, onImageClick, outlineState)
        } }
        return
    }
    val scheme = MaterialTheme.colorScheme
    val selection = remember(scheme.primary) {
        TextSelectionColors(handleColor = scheme.primary, backgroundColor = scheme.primary.copy(alpha = 0.22f))
    }
    CompositionLocalProvider(LocalTextSelectionColors provides selection) {
        when (mode) {
            MarkdownPreviewMode.RENDERED -> MarkdownRenderedContent(content, modifier, onCopyCode, sourcePath, allowedRoot, onNavigateLocalLink, headingAnchor, onImageClick, outlineState)
            MarkdownPreviewMode.SOURCE -> MarkdownSourceContent(content, modifier)
        }
    }
}

/** Reusable read-only source viewer for scripts and other plain-text documents. */
@Composable
internal fun PlainTextDocumentPreview(
    content: String,
    modifier: Modifier = Modifier,
    onCopySource: (() -> Unit)? = null,
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(content) { DocumentFindState() }
        DocumentFindScope(find, modifier) { Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) { FindDocumentButton() }
            DocumentFindBar(find)
            PlainTextDocumentPreview(content, Modifier.weight(1f).fillMaxWidth(), onCopySource)
        } }
        return
    }
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
    sourcePath: Path? = null,
    onCopyPath: ((Path) -> Unit)? = null,
    onCopyFile: ((Path) -> Unit)? = null,
    onCopyCode: ((String) -> Unit)? = null,
    onImageClick: ((Painter) -> Unit)? = null,
) {
    if (LocalDocumentFind.current == null) {
        val find = remember(documentKey) { DocumentFindState() }
        DocumentFindScope(find, modifier) { MarkdownDocumentPreview(content, Modifier.fillMaxSize(), documentKey,
            onCopySource, sourcePath, onCopyPath, onCopyFile, onCopyCode, onImageClick) }
        return
    }
    val localMode = remember(documentKey) { mutableStateOf(initialMarkdownPreviewMode()) }
    val modeState = LocalMaterialsReadingState.current?.mode ?: localMode
    val outline = rememberMarkdownOutlineState(content, documentKey, sourcePath)
    Column(modifier) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.End,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FindDocumentButton()
            MarkdownPreviewToolbarActions(
                mode = modeState.value,
                onModeChange = { modeState.value = it },
                onCopySource = onCopySource,
                sourcePath = sourcePath,
                onCopyPath = onCopyPath,
                onCopyFile = onCopyFile,
                copyEnabled = content.isNotEmpty(),
                outlineState = outline,
            )
        }
        LocalDocumentFind.current?.let { DocumentFindBar(it) }
        MarkdownDocumentPreview(
            content = content,
            mode = modeState.value,
            modifier = Modifier.weight(1f).fillMaxWidth(),
            onCopyCode = onCopyCode,
            onImageClick = onImageClick,
            sourcePath = sourcePath,
            outlineState = outline,
        )
    }
}

/** Compact, reusable action that switches between rendered Markdown and raw source. */
@Composable
internal fun MarkdownPreviewModeToggle(
    mode: MarkdownPreviewMode,
    onModeChange: (MarkdownPreviewMode) -> Unit,
    modifier: Modifier = Modifier,
    fileTypeLabel: String = "Markdown",
) {
    val presentation = markdownPreviewTogglePresentation(mode)
    val actionLabel = if (fileTypeLabel == "Markdown") presentation.label
        else if (mode == MarkdownPreviewMode.RENDERED) "查看${fileTypeLabel}源码" else "查看${fileTypeLabel}预览"
    ActionIconButton(
        label = actionLabel,
        onClick = { onModeChange(presentation.targetMode) },
        modifier = modifier,
    ) {
        when (presentation.icon) {
            MarkdownPreviewToggleIcon.CODE -> Icon(Icons.Outlined.Code, actionLabel, Modifier.size(16.dp))
            MarkdownPreviewToggleIcon.VISIBILITY -> Icon(Icons.Outlined.Visibility, actionLabel, Modifier.size(16.dp))
        }
    }
}

/** Shared compact actions for Markdown viewers with caller-owned mode and clipboard behavior. */
@Composable
internal fun MarkdownPreviewToolbarActions(
    mode: MarkdownPreviewMode,
    onModeChange: (MarkdownPreviewMode) -> Unit,
    onCopySource: (() -> Unit)? = null,
    sourcePath: Path? = null,
    onCopyPath: ((Path) -> Unit)? = null,
    onCopyFile: ((Path) -> Unit)? = null,
    copyEnabled: Boolean = true,
    modifier: Modifier = Modifier,
    fileTypeLabel: String = "Markdown",
    outlineState: MarkdownOutlineState? = null,
) {
    Row(
        modifier,
        horizontalArrangement = Arrangement.spacedBy(2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        outlineState?.let { outline ->
            MarkdownOutlineButton(outline, { onModeChange(MarkdownPreviewMode.RENDERED) }, Modifier.size(30.dp))
        }
        MarkdownPreviewModeToggle(mode, onModeChange, Modifier.size(30.dp), fileTypeLabel)
        onCopySource?.let { copy ->
            val existingFile = sourcePath?.takeIf { Files.isRegularFile(it, NOFOLLOW_LINKS) && !Files.isSymbolicLink(it) }
            var expanded by remember(sourcePath) { mutableStateOf(false) }
            Box {
                ActionIconButton(
                    label = "复制…",
                    onClick = { expanded = true },
                    modifier = Modifier.size(30.dp),
                ) {
                    Icon(Icons.Outlined.ContentCopy, "复制…", Modifier.size(16.dp))
                }
                SilverWingDropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                    DropdownMenuItem(
                        text = { Text("复制${fileTypeLabel}源码") },
                        leadingIcon = { Icon(Icons.Outlined.ContentCopy, null) },
                        enabled = copyEnabled,
                        onClick = {
                            expanded = false
                            copy()
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(if (existingFile != null) "复制文件路径" else "复制文件路径（无本地文件）") },
                        leadingIcon = { Icon(Icons.Outlined.FolderOpen, null) },
                        enabled = existingFile != null && onCopyPath != null,
                        onClick = {
                            expanded = false
                            existingFile?.let { onCopyPath?.invoke(it) }
                        },
                    )
                    DropdownMenuItem(
                        text = { Text(if (existingFile != null) "复制文件引用" else "复制文件引用（无本地文件）") },
                        leadingIcon = { Icon(Icons.Outlined.Description, null) },
                        enabled = existingFile != null && onCopyFile != null,
                        onClick = {
                            expanded = false
                            existingFile?.let { onCopyFile?.invoke(it) }
                        },
                    )
                }
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
    val outline = rememberMarkdownOutlineState(selectedFile.content, selectedFile.path)

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
                outlineState = outline,
            )
        }
        MarkdownDocumentPreview(
            content = selectedFile.content,
            mode = mode,
            modifier = Modifier.weight(1f).fillMaxWidth(),
            outlineState = outline,
        )
    }
}

@Composable
private fun MarkdownRenderedContent(
    content: String,
    modifier: Modifier,
    onCopyCode: ((String) -> Unit)?,
    sourcePath: Path?,
    allowedRoot: Path?,
    onNavigateLocalLink: ((String, String?) -> Unit)?,
    headingAnchor: String?,
    onImageClick: ((Painter) -> Unit)?,
    outlineState: MarkdownOutlineState?,
) {
    val imageClick by rememberUpdatedState(onImageClick)
    val zoomableImages = remember {
        object : ImageTransformer by Coil3ImageTransformerImpl {
            @Composable override fun transform(link: String): ImageData? {
                val data = Coil3ImageTransformerImpl.transform(link) ?: return null
                return if (imageClick == null || !data.painter.intrinsicSize.width.isFinite() || data.painter.intrinsicSize.width <= 0f) data else data.copy(modifier = data.modifier
                    .pointerHoverIcon(PointerIcon.Hand).clickable(onClickLabel = "放大图片") { imageClick?.invoke(data.painter) })
            }
        }
    }
    val scheme = MaterialTheme.colorScheme
    val syntaxPalette = workspacePreviewPalette()
    val bodyStyle = MaterialTheme.typography.bodyLarge.copy(fontSize = 15.sp, lineHeight = 24.sp)
    val frontMatter = remember(content) { splitLeadingMarkdownFrontMatter(content) }
    val renderedContent = remember(content, sourcePath, allowedRoot) {
        markdownPreviewRenderInput(frontMatter?.remainingContent ?: content, sourcePath, allowedRoot)
    }
    val markdownState = rememberMarkdownState(renderedContent, retainState = true)
    val parsed by markdownState.state.collectAsState()
    val renderReady = (parsed as? MarkdownParseState.Success)?.content == renderedContent
    val verticalScroll = rememberMaterialsScrollState("markdown-rendered", ready = renderReady)
    val clipboardManager = LocalClipboardManager.current
    val outline = outlineState ?: rememberMarkdownOutlineState(content, sourcePath = sourcePath, allowedRoot = allowedRoot)
    val headingAnchors = remember(outline.entries) { outline.entries.associate { it.startOffset to it.anchor } }
    val headingOffsets = remember(content) { mutableStateMapOf<String, Int>() }
    var viewportTop by remember(content) { mutableStateOf(0f) }
    LaunchedEffect(headingAnchor, content) {
        if (headingAnchor != null && outline.navigationRequest == null) {
            val offset = snapshotFlow { headingOffsets[headingAnchor] }.filterNotNull().first()
            // Saved positions restore after measurement; an explicit navigation wins afterward.
            repeat(2) { withFrameNanos { } }
            verticalScroll.animateScrollTo(offset.coerceAtLeast(0))
        }
    }
    val navigation = outline.navigationRequest
    LaunchedEffect(outline, navigation, content) {
        if (navigation != null) {
            val offset = snapshotFlow { headingOffsets[navigation.anchor] }.filterNotNull().first()
            repeat(2) { withFrameNanos { } }
            verticalScroll.animateScrollTo(offset.coerceAtLeast(0))
            outline.finishNavigation(navigation)
        }
    }
    fun recordHeading(startOffset: Int, y: Float) {
        if (!renderReady) return
        headingAnchors[startOffset]?.let { slug ->
            headingOffsets[slug] = (y - viewportTop + verticalScroll.value).roundToInt()
        }
    }
    val defaultUriHandler = LocalUriHandler.current
    val uriHandler = remember(defaultUriHandler, sourcePath, allowedRoot, onNavigateLocalLink) {
        object : UriHandler {
            override fun openUri(uri: String) {
                val local = resolveLocalMarkdownDestination(sourcePath, allowedRoot, uri)
                if (local != null && (local.first.toString().endsWith(".md", ignoreCase = true) || local.first.toString().endsWith(".markdown", ignoreCase = true))) {
                    val relative = allowedRoot!!.toAbsolutePath().normalize().relativize(local.first).joinToString("/")
                    onNavigateLocalLink?.invoke(relative, local.second)
                } else if (runCatching { URI(uri).scheme?.lowercase() in setOf("http", "https", "mailto") }.getOrDefault(false)) {
                    defaultUriHandler.openUri(uri)
                }
            }
        }
    }
    CompositionLocalProvider(LocalUriHandler provides uriHandler) {
    SelectionContainer {
        BoxWithConstraints(modifier) {
            val pagePadding = if (maxWidth < 600.dp) 16.dp else 28.dp
            val pageWidth = minOf(maxWidth, 820.dp)
            // Markdown tables and code blocks own their horizontal scrolling. Wrapping the whole
            // renderer in another horizontal scroll would measure those children with infinite
            // width and crashes on layouts that activate the renderer's internal overflow path.
            Box(Modifier.fillMaxSize().onGloballyPositioned { viewportTop = it.positionInRoot().y }.verticalScroll(verticalScroll), contentAlignment = Alignment.TopCenter) {
                Column(Modifier.width(pageWidth).padding(horizontal = pagePadding, vertical = 20.dp)) {
                    frontMatter?.let { metadata ->
                        HorizontalDivider(color = scheme.outlineVariant)
                        Column(Modifier.fillMaxWidth().padding(vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            val properties = remember(metadata.lines) { parseFrontMatterProperties(metadata.lines) }
                            if (properties == null) metadata.lines.forEachIndexed { index, line ->
                                SearchableText(line, style = bodyStyle, softWrap = true, sourceOrder = Long.MIN_VALUE + index)
                            }
                            else properties.forEachIndexed { index, property ->
                                val propertyOrder = Long.MIN_VALUE + (index.toLong() shl 32)
                                Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), verticalAlignment = Alignment.Top) {
                                    SearchableText(property.name, modifier = Modifier.widthIn(min = 120.dp).padding(end = 12.dp), style = bodyStyle.copy(fontSize = 13.sp), color = scheme.onSurfaceVariant, sourceOrder = propertyOrder)
                                    if (property.name == "tags") FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                        property.values.forEachIndexed { valueIndex, value ->
                                            Surface(shape = RoundedCornerShape(14.dp), color = scheme.secondaryContainer) {
                                                SearchableText(value, Modifier.padding(horizontal = 8.dp, vertical = 3.dp), style = bodyStyle.copy(fontSize = 12.sp), color = scheme.onSecondaryContainer, sourceOrder = propertyOrder + valueIndex + 1)
                                            }
                                        }
                                    } else SearchableText(property.values.joinToString(", "), style = bodyStyle, softWrap = true, sourceOrder = propertyOrder + 1)
                                }
                            }
                        }
                        HorizontalDivider(Modifier.padding(bottom = 12.dp), color = scheme.outlineVariant)
                    }
                    if (frontMatter == null || frontMatter.remainingContent.isNotBlank()) {
            Markdown(
                markdownState = markdownState,
                extendedSpans = markdownExtendedSpans { remember { markdownSelectionSpans() } },
                colors = markdownColor(
                    text = scheme.onSurface,
                    codeBackground = scheme.surfaceVariant,
                    inlineCodeBackground = scheme.surfaceVariant,
                    dividerColor = scheme.outlineVariant,
                    tableBackground = scheme.surfaceVariant.copy(alpha = 0.7f),
                ),
                typography = markdownTypography(
                    h1 = bodyStyle.copy(fontSize = 27.sp, lineHeight = 36.sp, fontWeight = FontWeight.Bold),
                    h2 = bodyStyle.copy(fontSize = 22.sp, lineHeight = 30.sp, fontWeight = FontWeight.Bold),
                    h3 = bodyStyle.copy(fontSize = 18.sp, lineHeight = 26.sp, fontWeight = FontWeight.SemiBold),
                    h4 = bodyStyle.copy(fontSize = 16.sp, lineHeight = 24.sp, fontWeight = FontWeight.SemiBold),
                    h5 = bodyStyle.copy(fontWeight = FontWeight.SemiBold),
                    h6 = bodyStyle.copy(fontSize = 14.sp, lineHeight = 22.sp, fontWeight = FontWeight.SemiBold),
                    text = bodyStyle,
                    paragraph = bodyStyle,
                    ordered = bodyStyle,
                    bullet = bodyStyle,
                    list = bodyStyle,
                    quote = bodyStyle.copy(color = scheme.outline),
                    code = bodyStyle.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp, lineHeight = 20.sp),
                    inlineCode = bodyStyle.copy(fontFamily = FontFamily.Monospace, fontSize = 13.sp),
                    table = bodyStyle.copy(fontSize = 13.sp, lineHeight = 20.sp),
                    textLink = TextLinkStyles(style = SpanStyle(color = scheme.primary, textDecoration = TextDecoration.Underline)),
                ),
                padding = markdownPadding(
                    block = 4.dp,
                    list = 4.dp,
                    listItemTop = 2.dp,
                    listItemBottom = 2.dp,
                    listIndent = 16.dp,
                    codeBlock = PaddingValues(horizontal = 14.dp, vertical = 12.dp),
                    blockQuote = PaddingValues(horizontal = 16.dp, vertical = 4.dp),
                ),
                dimens = markdownDimens(
                    codeBackgroundCornerSize = 8.dp,
                    blockQuoteThickness = 2.dp,
                    tableCellWidth = 160.dp,
                    tableCellPadding = 10.dp,
                    tableCornerSize = 8.dp,
                ),
                components = markdownComponents(
                    paragraph = { model -> SearchableMarkdownText(model.content, model.node, model.typography.paragraph) },
                    text = { model -> SearchableMarkdownText(model.content, model.node, model.typography.text) },
                    codeBlock = { model -> SearchableMarkdownText(model.content, model.node, model.typography.code) },
                    codeFence = { model ->
                        MarkdownCodeFence(model.content, model.node, model.typography.code) { code, language, style ->
                            val highlightedCode = remember(code, language, syntaxPalette) {
                                workspaceHighlightedText(code, syntaxLanguageFromLabel(language), syntaxPalette)
                            }
                            MarkdownCodeBackground(
                                color = scheme.surfaceVariant,
                                shape = RoundedCornerShape(8.dp),
                                modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
                                language = language,
                                code = code,
                            ) {
                                Column {
                                    Row(
                                        Modifier.fillMaxWidth().padding(start = 12.dp, end = 6.dp, top = 5.dp, bottom = 3.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Text(
                                            language?.uppercase() ?: "CODE",
                                            modifier = Modifier.weight(1f),
                                            style = MaterialTheme.typography.labelSmall.copy(fontFamily = FontFamily.Monospace),
                                            color = scheme.onSurfaceVariant,
                                        )
                                        ActionIconButton(
                                            label = "复制代码",
                                            onClick = {
                                                if (onCopyCode != null) onCopyCode(code)
                                                else clipboardManager.setText(AnnotatedString(code))
                                            },
                                            modifier = Modifier.size(28.dp),
                                            enabled = code.isNotEmpty(),
                                        ) {
                                            Icon(Icons.Outlined.ContentCopy, "复制代码", Modifier.size(15.dp))
                                        }
                                    }
                                    HorizontalDivider(color = scheme.outlineVariant.copy(alpha = 0.5f))
                                    SearchableText(
                                        highlightedCode,
                                        style = style.copy(fontFamily = FontFamily.Monospace),
                                        modifier = Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 12.dp),
                                        softWrap = true,
                                        sourceOrder = model.node.startOffset.toLong(),
                                    )
                                }
                            }
                        }
                    },
                    heading1 = { model -> MarkdownSectionHeading(18.dp, { y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h1, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    heading2 = { model -> MarkdownSectionHeading(14.dp, { y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h2, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    heading3 = { model -> MarkdownAnchorHeading({ y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h3, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    heading4 = { model -> MarkdownAnchorHeading({ y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h4, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    heading5 = { model -> MarkdownAnchorHeading({ y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h5, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    heading6 = { model -> MarkdownAnchorHeading({ y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h6, org.intellij.markdown.MarkdownTokenTypes.ATX_CONTENT) } },
                    setextHeading1 = { model -> MarkdownSectionHeading(18.dp, { y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h1, org.intellij.markdown.MarkdownTokenTypes.SETEXT_CONTENT) } },
                    setextHeading2 = { model -> MarkdownSectionHeading(14.dp, { y -> recordHeading(model.node.startOffset, y) }, headingAnchors[model.node.startOffset]) { SearchableMarkdownText(model.content, model.node, model.typography.h2, org.intellij.markdown.MarkdownTokenTypes.SETEXT_CONTENT) } },
                    table = { model ->
                        val raw = model.content.substring(model.node.startOffset, model.node.endOffset)
                        val selectable = remember(raw) { parsePreviewMarkdownTable(raw) }
                        if (selectable != null) SelectableMarkdownTable(
                            table = selectable,
                            markdownContent = model.content,
                            node = model.node,
                            style = model.typography.table,
                            onCopy = { clipboardManager.setText(AnnotatedString(it)) },
                        )
                        else MarkdownTable(
                            content = model.content,
                            node = model.node,
                            style = model.typography.table,
                            headerBlock = { text, header, width, style ->
                                MarkdownTableHeader(text, header, width, style, maxLines = 2)
                            },
                            rowBlock = { text, row, width, style ->
                                MarkdownTableRow(text, row, width, style, maxLines = 2)
                            },
                        )
                    },
                ),
                imageTransformer = zoomableImages,
                modifier = Modifier.fillMaxWidth(),
            )
                    }
                }
            }
        }
    }
    }
}

internal data class MarkdownFrontMatterPreview(val lines: List<String>, val remainingContent: String)

internal data class FrontMatterProperty(val name: String, val values: List<String>)

internal fun parseFrontMatterProperties(lines: List<String>): List<FrontMatterProperty>? {
    val result = mutableListOf<FrontMatterProperty>()
    var index = 0
    while (index < lines.size) {
        val line = lines[index]
        if (line.isBlank()) { index++; continue }
        val match = Regex("^([A-Za-z_][A-Za-z0-9_-]*):(?:\\s*(.*))?$").matchEntire(line) ?: return null
        val name = match.groupValues[1]
        val scalar = match.groupValues[2].trim()
        if (scalar.isNotEmpty()) {
            if (scalar.startsWith("|") || scalar.startsWith(">") || scalar.startsWith("{")) return null
            val values = if (name == "tags" && scalar.startsWith('[') && scalar.endsWith(']'))
                scalar.removeSurrounding("[", "]").split(',').map { it.trim().trim('"', '\'') }
            else listOf(scalar.trim('"', '\''))
            result += FrontMatterProperty(name, values)
            index++
        } else {
            index++
            val items = mutableListOf<String>()
            while (index < lines.size && Regex("^\\s*-\\s+").containsMatchIn(lines[index])) {
                items += lines[index].replaceFirst(Regex("^\\s*-\\s+"), "").trim().trim('"', '\'')
                index++
            }
            if (items.isEmpty()) return null
            result += FrontMatterProperty(name, items)
        }
    }
    return result
}

internal fun splitLeadingMarkdownFrontMatter(content: String): MarkdownFrontMatterPreview? {
    val match = FRONT_MATTER_PREVIEW_REGEX.find(content) ?: return null
    val body = match.groups[1]?.value ?: return null
    val lines = body.split('\n').map { it.removeSuffix("\r") }
    return MarkdownFrontMatterPreview(lines, content.substring(match.range.last + 1))
}

private val FRONT_MATTER_PREVIEW_REGEX = Regex("\\A---[ \\t]*\\r?\\n(.*?)(?:\\r?\\n---[ \\t]*(?:\\r?\\n|\\z))", RegexOption.DOT_MATCHES_ALL)

private val MARKDOWN_IMAGE_LINK_REGEX = Regex("""!\[[^\]]*]\((<[^>]+>|[^)\s]+)(?:\s+(?:"[^"]*"|'[^']*'))?\)""")

internal fun meegleRichTextImageUrls(content: String): List<String> = MARKDOWN_IMAGE_LINK_REGEX.findAll(content)
    .mapNotNull { match -> match.groupValues[1].removeSurrounding("<", ">").takeIf(::isFeishuProjectImageUrl) }
    .distinct()
    .toList()

internal fun markdownWithLocalImagePaths(content: String, localImages: Map<String, Path>): String =
    MARKDOWN_IMAGE_LINK_REGEX.replace(content) { match ->
        val destination = match.groups[1] ?: return@replace match.value
        val originalToken = destination.value
        val sourceUrl = originalToken.removeSurrounding("<", ">")
        val localPath = localImages[sourceUrl] ?: return@replace match.value
        val replacement = localPath.toUri().toASCIIString().let {
            if (originalToken.startsWith('<')) "<$it>" else it
        }
        val start = destination.range.first - match.range.first
        val endExclusive = destination.range.last - match.range.first + 1
        match.value.replaceRange(start, endExclusive, replacement)
    }

/** A link or image may only resolve to an existing ordinary file below the document root. */
internal fun resolveLocalMarkdownDestination(sourcePath: Path?, rootPath: Path?, target: String): Pair<Path, String?>? {
    if (sourcePath == null || rootPath == null) return null
    return runCatching {
        val uri = URI(target.replace(" ", "%20"))
        if (uri.isAbsolute || uri.rawAuthority != null || target.startsWith('/') || target.startsWith('\\')) return null
        val relative = uri.path.orEmpty()
        if (relative.isBlank() && uri.fragment == null) return null
        val root = rootPath.toRealPath()
        val path = if (relative.isBlank()) sourcePath.toAbsolutePath().normalize()
            else sourcePath.toAbsolutePath().normalize().parent.resolve(relative).normalize()
        if (!path.startsWith(root) || !Files.isRegularFile(path, NOFOLLOW_LINKS)) return null
        var cursor = root
        for (segment in root.relativize(path)) {
            cursor = cursor.resolve(segment)
            if (Files.isSymbolicLink(cursor)) return null
        }
        if (!path.toRealPath().startsWith(root)) return null
        path to uri.fragment
    }.getOrNull()
}

internal fun markdownWithRelativeImagePaths(content: String, sourcePath: Path?, rootPath: Path?): String =
    if (sourcePath == null || rootPath == null) content else MARKDOWN_IMAGE_LINK_REGEX.replace(content) { match ->
        val destination = match.groups[1] ?: return@replace match.value
        val target = destination.value.removeSurrounding("<", ">")
        if (runCatching { URI(target).scheme?.lowercase() in setOf("http", "https") }.getOrDefault(false)) return@replace match.value
        val replacement = resolveLocalMarkdownDestination(sourcePath, rootPath, target)?.first?.toUri()?.toASCIIString()
            ?: "about:blank"
        match.value.replaceRange(
            destination.range.first - match.range.first,
            destination.range.last - match.range.first + 1,
            replacement,
        )
    }

private fun isFeishuProjectImageUrl(value: String): Boolean = runCatching {
    val uri = URI(value)
    uri.scheme.equals("https", ignoreCase = true) &&
        uri.host.equals("project.feishu.cn", ignoreCase = true) &&
        uri.rawUserInfo == null &&
        uri.rawPath.startsWith("/goapi/v5/platform/file/stream/download/")
}.getOrDefault(false)

internal fun markdownHeadingSlug(raw: String): String = raw.lines().firstOrNull().orEmpty()
    .trim().trimStart('#').trim().lowercase()
    .replace(Regex("\\[([^]]+)]\\([^)]*\\)"), "$1")
    .replace(Regex("[^\\p{L}\\p{N}_ -]"), "")
    .trim().replace(Regex("[\\s-]+"), "-")

@Composable
private fun MarkdownSectionHeading(topPadding: Dp, onPosition: (Float) -> Unit, anchor: String?, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().semantics { heading(); anchor?.let { testTag = "markdown-heading-$it" } }
        .onGloballyPositioned { onPosition(it.positionInRoot().y) }.padding(top = topPadding, bottom = 4.dp)) {
        content()
        HorizontalDivider(Modifier.padding(top = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
    }
}

@Composable
private fun MarkdownAnchorHeading(onPosition: (Float) -> Unit, anchor: String?, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxWidth().semantics { heading(); anchor?.let { testTag = "markdown-heading-$it" } }
        .onGloballyPositioned { onPosition(it.positionInRoot().y) }) { content() }
}

@Composable
private fun MarkdownSourceContent(content: String, modifier: Modifier) {
    val verticalScroll = rememberMaterialsScrollState("source-vertical")
    val horizontalScroll = rememberMaterialsScrollState("source-horizontal")
    SelectionContainer {
        Box(modifier.padding(15.dp).verticalScroll(verticalScroll).horizontalScroll(horizontalScroll)) {
            if (content.isBlank()) {
                Text("（空）", color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                SearchableText(
                    content,
                    style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                    softWrap = false,
                )
            }
        }
    }
}
