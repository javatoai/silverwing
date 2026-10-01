package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.PlainTooltip
import androidx.compose.material3.TooltipAnchorPosition
import androidx.compose.material3.TooltipBox
import androidx.compose.material3.TooltipDefaults
import androidx.compose.material3.rememberTooltipState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.onPointerEvent
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isCtrlPressed as isPointerCtrlPressed
import androidx.compose.ui.input.pointer.isShiftPressed as isPointerShiftPressed
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.LocalSkillFileEntry
import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.runInterruptible
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.awt.Cursor
import kotlin.math.roundToInt

private sealed interface RequirementMaterialsFilesState {
    data object Loading : RequirementMaterialsFilesState
    data class Loaded(val catalog: RequirementMaterialsMarkdownCatalog) : RequirementMaterialsFilesState
    data class Failed(val message: String) : RequirementMaterialsFilesState
}

private sealed interface RequirementMaterialsDocumentState {
    data object Empty : RequirementMaterialsDocumentState
    data object Loading : RequirementMaterialsDocumentState
    data class Loaded(val relativePath: String, val content: String) : RequirementMaterialsDocumentState
    data class Failed(val relativePath: String, val message: String) : RequirementMaterialsDocumentState
}

private const val DEFAULT_MATERIALS_DIRECTORY_WIDTH_DP = 240f
private const val MIN_MATERIALS_DIRECTORY_WIDTH_DP = 180f
private const val MAX_MATERIALS_DIRECTORY_WIDTH_DP = 420f
private const val MIN_MATERIALS_PREVIEW_WIDTH_DP = 360f
private const val MATERIALS_RESIZE_HANDLE_WIDTH_DP = 8f
private const val MIN_MATERIALS_SPLIT_WIDTH_DP = MIN_MATERIALS_DIRECTORY_WIDTH_DP +
    MATERIALS_RESIZE_HANDLE_WIDTH_DP + MIN_MATERIALS_PREVIEW_WIDTH_DP

/** Clamp only the displayed width; a smaller window must not replace the saved preference. */
internal fun resolveMaterialsDirectoryWidth(preferredWidthDp: Float, availableWidthDp: Float): Float =
    preferredWidthDp.coerceIn(
        MIN_MATERIALS_DIRECTORY_WIDTH_DP,
        (availableWidthDp - MATERIALS_RESIZE_HANDLE_WIDTH_DP - MIN_MATERIALS_PREVIEW_WIDTH_DP)
            .coerceIn(MIN_MATERIALS_DIRECTORY_WIDTH_DP, MAX_MATERIALS_DIRECTORY_WIDTH_DP),
    )

internal fun selectMaterialEntryKeys(
    selected: Set<String>,
    visibleKeys: List<String>,
    anchor: String?,
    key: String,
    ctrl: Boolean,
    shift: Boolean,
): Set<String> = when {
    shift && anchor in visibleKeys -> {
        val from = visibleKeys.indexOf(anchor)
        val to = visibleKeys.indexOf(key)
        val range = visibleKeys.subList(minOf(from, to), maxOf(from, to) + 1).toSet()
        if (ctrl) selected + range else range
    }
    ctrl -> if (key in selected) selected - key else selected + key
    else -> setOf(key)
}

internal fun initialExpandedMaterialsDirectories(directories: List<String>, enabled: Boolean): Set<String> =
    if (enabled) directories.filter { path -> path.count { it == '/' } < 2 }.toSet() else emptySet()

/** Read-only browser for Markdown documents under a task's persisted requirement-materials root. */
@Composable
internal fun RequirementMaterialsBrowser(
    controller: DesktopApplication,
    task: TaskManifest,
    modifier: Modifier = Modifier,
    onClose: () -> Unit,
) {
    val rootValue = task.requirementMaterials.writeRoot
    val rootPath = remember(task.folderName, rootValue) {
        rootValue?.takeIf(String::isNotBlank)?.let { runCatching { Path.of(it) }.getOrNull() }
    }
    val service = remember { RequirementMaterialsMarkdownService() }
    var refreshAttempt by remember(task.folderName, rootValue) { mutableStateOf(0) }
    var previewAttempt by remember(task.folderName, rootValue) { mutableStateOf(0) }
    var filesState by remember(task.folderName, rootValue) { mutableStateOf<RequirementMaterialsFilesState>(RequirementMaterialsFilesState.Loading) }
    var documentState by remember(task.folderName, rootValue) { mutableStateOf<RequirementMaterialsDocumentState>(RequirementMaterialsDocumentState.Empty) }
    var selectedRelativePath by remember(task.folderName, rootValue) { mutableStateOf<String?>(null) }
    var pendingAnchor by remember(task.folderName, rootValue) { mutableStateOf<String?>(null) }
    var selectedEntryKeys by remember(task.folderName, rootValue) { mutableStateOf<Set<String>>(emptySet()) }
    var selectionAnchor by remember(task.folderName, rootValue) { mutableStateOf<String?>(null) }
    var expandedDirectories by remember(task.folderName, rootValue) { mutableStateOf<Set<String>>(emptySet()) }
    var expansionInitialized by remember(task.folderName, rootValue) { mutableStateOf(false) }
    var compactShowingPreview by remember(task.folderName) { mutableStateOf(false) }
    val preferredDirectoryWidth = remember {
        mutableFloatStateOf(
            WindowPreferences.load().materialsDirectoryPaneWidth?.toFloat() ?: DEFAULT_MATERIALS_DIRECTORY_WIDTH_DP,
        )
    }

    LaunchedEffect(task.folderName, rootValue, refreshAttempt) {
        filesState = RequirementMaterialsFilesState.Loading
        val root = rootPath
        if (root == null) {
            filesState = RequirementMaterialsFilesState.Failed("需求资料目录路径无效或为空。")
        } else {
            try {
                val catalog = runInterruptible(Dispatchers.IO) { service.list(root) }
                filesState = RequirementMaterialsFilesState.Loaded(catalog)
                if (!expansionInitialized) {
                    expandedDirectories = initialExpandedMaterialsDirectories(
                        catalog.directories,
                        WindowPreferences.load().expandRequirementMaterialsSecondLevelFolders,
                    )
                    expansionInitialized = true
                }
                selectedRelativePath = selectedRelativePath
                    ?.takeIf { selected -> catalog.files.any { it.relativePath == selected } }
                    ?: catalog.files.firstOrNull()?.relativePath
                val validKeys = catalog.files.map { "file:${it.relativePath}" }.toSet() +
                    catalog.directories.map { "directory:$it" }
                selectedEntryKeys = selectedEntryKeys.intersect(validKeys)
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                filesState = RequirementMaterialsFilesState.Failed(
                    error.message ?: "无法读取需求资料目录。",
                )
            }
        }
    }

    val catalog = (filesState as? RequirementMaterialsFilesState.Loaded)?.catalog
    val selectedFile = catalog?.files?.firstOrNull { it.relativePath == selectedRelativePath }
    LaunchedEffect(task.folderName, rootPath, selectedFile?.relativePath, previewAttempt, catalog) {
        val selected = selectedFile
        val root = catalog?.root
        if (selected == null || root == null) {
            documentState = RequirementMaterialsDocumentState.Empty
        } else {
            documentState = RequirementMaterialsDocumentState.Loading
            try {
                val content = runInterruptible(Dispatchers.IO) { service.read(root, selected.relativePath) }
                documentState = RequirementMaterialsDocumentState.Loaded(selected.relativePath, content)
            } catch (cancelled: kotlinx.coroutines.CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                documentState = RequirementMaterialsDocumentState.Failed(
                    selected.relativePath,
                    error.message ?: "无法读取文件。",
                )
            }
        }
    }

    val treeEntries = remember(catalog?.files) {
        catalog?.files.orEmpty().map { file ->
            LocalSkillFileEntry(
                relativePath = file.relativePath,
                sizeBytes = file.sizeBytes,
                markdown = file.markdown,
            )
        }
    }
    val fileTree = remember(treeEntries, catalog?.directories) {
        buildLocalSkillFileTree("需求资料", treeEntries, catalog?.directories.orEmpty())
    }
    val visibleRows = remember(fileTree, expandedDirectories) {
        visibleLocalSkillFileTreeRows(fileTree, expandedDirectories).drop(1)
    }

    fun navigateMarkdown(relativePath: String, anchor: String?) {
        val current = catalog ?: return
        val file = current.files.firstOrNull { it.relativePath == relativePath && it.markdown } ?: return
        selectedRelativePath = file.relativePath
        selectedEntryKeys = setOf("file:${file.relativePath}")
        selectionAnchor = "file:${file.relativePath}"
        var ancestor = file.relativePath.substringBeforeLast('/', "")
        while (ancestor.isNotEmpty()) {
            expandedDirectories = expandedDirectories + ancestor
            ancestor = ancestor.substringBeforeLast('/', "")
        }
        pendingAnchor = anchor
    }

    fun selectEntry(row: LocalSkillFileTreeRow, ctrl: Boolean, shift: Boolean, compact: Boolean) {
        val key = row.key
        val visibleKeys = visibleRows.map(LocalSkillFileTreeRow::key)
        selectedEntryKeys = selectMaterialEntryKeys(selectedEntryKeys, visibleKeys, selectionAnchor, key, ctrl, shift)
        if (!shift) selectionAnchor = key
        val file = (row.node as? LocalSkillFileTreeNode.File)?.entry
        if (file != null) {
            pendingAnchor = null
            selectedRelativePath = file.relativePath
            if (compact) compactShowingPreview = true
        }
    }

    fun copySelectedEntries() {
        val current = catalog ?: return
        runCatching {
            selectedEntryKeys.sorted().map { key ->
                service.resolveCopyItem(current.root, key.substringAfter(':'))
            }
        }.onSuccess { paths -> if (paths.isNotEmpty()) controller.copyFiles(paths) }
            .onFailure(controller::showError)
    }

    BoxWithConstraints(modifier.fillMaxSize().padding(14.dp)) {
        val availableWidth = maxWidth.value
        val compact = availableWidth < MIN_MATERIALS_SPLIT_WIDTH_DP
        val displayedDirectoryWidth = resolveMaterialsDirectoryWidth(
            preferredDirectoryWidth.floatValue,
            availableWidth,
        ).dp

        if (compact && compactShowingPreview && selectedFile != null) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    TextButton(onClick = { compactShowingPreview = false }) { Text("返回文档列表") }
                    Spacer(Modifier.weight(1f))
                    ActionIconButton("返回任务详情", onClose, Modifier.size(30.dp)) {
                        Icon(Icons.AutoMirrored.Outlined.ArrowBack, null, Modifier.size(18.dp))
                    }
                }
                RequirementMaterialsDocumentPane(
                    controller = controller,
                    root = catalog.root,
                    selectedFile = selectedFile,
                    state = documentState,
                    onRetry = { previewAttempt += 1 },
                    onNavigateMarkdown = ::navigateMarkdown,
                    pendingAnchor = pendingAnchor,
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                )
            }
        } else {
            val directoryModifier = if (compact) Modifier.fillMaxSize() else Modifier.width(displayedDirectoryWidth).fillMaxHeight()
            Row(Modifier.fillMaxSize()) {
                RequirementMaterialsDirectoryPane(
                    state = filesState,
                    files = treeEntries,
                    rows = visibleRows,
                    expandedDirectories = expandedDirectories,
                    selectedEntryKeys = selectedEntryKeys,
                    onToggleDirectory = { path ->
                        expandedDirectories = if (path in expandedDirectories) expandedDirectories - path else expandedDirectories + path
                    },
                    onSelectEntry = { row, ctrl, shift -> selectEntry(row, ctrl, shift, compact) },
                    onCopySelected = ::copySelectedEntries,
                    onRefresh = { refreshAttempt += 1 },
                    onClose = onClose,
                    modifier = directoryModifier,
                )
                if (!compact) {
                    MaterialsDirectoryResizeHandle(
                        onResizeStart = { preferredDirectoryWidth.floatValue = displayedDirectoryWidth.value },
                        onResize = { dragAmount ->
                            preferredDirectoryWidth.floatValue = resolveMaterialsDirectoryWidth(
                                preferredDirectoryWidth.floatValue + dragAmount,
                                availableWidth,
                            )
                        },
                        onResizeEnd = {
                            WindowPreferences.saveMaterialsDirectoryPaneWidth(preferredDirectoryWidth.floatValue.roundToInt())
                        },
                    )
                    when (val current = filesState) {
                        RequirementMaterialsFilesState.Loading -> BrowserLoadingCard(Modifier.weight(1f).fillMaxHeight())
                        is RequirementMaterialsFilesState.Failed -> BrowserErrorCard(
                            message = current.message,
                            onRetry = { refreshAttempt += 1 },
                            modifier = Modifier.weight(1f).fillMaxHeight(),
                        )
                        is RequirementMaterialsFilesState.Loaded -> {
                            if (current.catalog.files.isEmpty()) {
                                BrowserEmptyCard(
                                    title = "目录中没有可预览文件",
                                    detail = "支持 .md、.json、.csv 和 .txt 文件，文件夹仍可在左侧选择并复制。",
                                    modifier = Modifier.weight(1f).fillMaxHeight(),
                                )
                            } else {
                                RequirementMaterialsDocumentPane(
                                    controller = controller,
                                    root = current.catalog.root,
                                    selectedFile = selectedFile,
                                    state = documentState,
                                    onRetry = { previewAttempt += 1 },
                                    onNavigateMarkdown = ::navigateMarkdown,
                                    pendingAnchor = pendingAnchor,
                                    modifier = Modifier.weight(1f).fillMaxHeight(),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
@OptIn(ExperimentalMaterial3Api::class)
private fun MaterialsDirectoryResizeHandle(
    onResizeStart: () -> Unit,
    onResize: (Float) -> Unit,
    onResizeEnd: () -> Unit,
) {
    val currentOnResizeStart by rememberUpdatedState(onResizeStart)
    val currentOnResize by rememberUpdatedState(onResize)
    val currentOnResizeEnd by rememberUpdatedState(onResizeEnd)
    val density = LocalDensity.current
    TooltipBox(
        positionProvider = TooltipDefaults.rememberTooltipPositionProvider(TooltipAnchorPosition.Above),
        tooltip = { PlainTooltip { Text("拖动调整目录宽度") } },
        state = rememberTooltipState(),
    ) {
        Box(
            Modifier
                .width(MATERIALS_RESIZE_HANDLE_WIDTH_DP.dp)
                .fillMaxHeight()
                .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR)))
                .pointerInput(density) {
                    detectHorizontalDragGestures(
                        onDragStart = { currentOnResizeStart() },
                        onHorizontalDrag = { change, dragAmount ->
                            change.consume()
                            currentOnResize(with(density) { dragAmount.toDp().value })
                        },
                        onDragEnd = currentOnResizeEnd,
                    )
                },
            contentAlignment = Alignment.Center,
        ) {
            Box(
                Modifier.fillMaxHeight().padding(vertical = 16.dp).width(2.dp)
                    .background(MaterialTheme.colorScheme.outlineVariant),
            )
        }
    }
}

@Composable
@OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)
private fun RequirementMaterialsDirectoryPane(
    state: RequirementMaterialsFilesState,
    files: List<LocalSkillFileEntry>,
    rows: List<LocalSkillFileTreeRow>,
    expandedDirectories: Set<String>,
    selectedEntryKeys: Set<String>,
    onToggleDirectory: (String) -> Unit,
    onSelectEntry: (LocalSkillFileTreeRow, Boolean, Boolean) -> Unit,
    onCopySelected: () -> Unit,
    onRefresh: () -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val focusRequester = remember { FocusRequester() }
    OutlinedCard(modifier) {
        Column(Modifier.fillMaxSize().padding(10.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (state is RequirementMaterialsFilesState.Loaded) "文档（${files.size}）" else "文档",
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                )
                ActionIconButton(
                    label = "刷新需求资料目录",
                    onClick = onRefresh,
                    modifier = Modifier.size(30.dp),
                    loading = state is RequirementMaterialsFilesState.Loading,
                ) {
                    Icon(Icons.Outlined.Refresh, null, Modifier.size(16.dp))
                }
                ActionIconButton("返回任务详情", onClose, Modifier.size(30.dp)) {
                    Icon(Icons.AutoMirrored.Outlined.ArrowBack, null, Modifier.size(18.dp))
                }
            }
            when (state) {
                RequirementMaterialsFilesState.Loading -> {
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text("正在读取目录…", style = MaterialTheme.typography.bodySmall)
                }
                is RequirementMaterialsFilesState.Failed -> {
                    Text(state.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = onRefresh) { Text("重试") }
                }
                is RequirementMaterialsFilesState.Loaded -> if (rows.isEmpty()) {
                    Text("目录为空", style = MaterialTheme.typography.bodySmall)
                } else LazyColumn(
                    Modifier.weight(1f).fillMaxWidth()
                        .focusRequester(focusRequester)
                        .onPreviewKeyEvent { event ->
                            if (event.type == KeyEventType.KeyDown && event.isCtrlPressed && event.key == Key.C) {
                                onCopySelected()
                                true
                            } else false
                        }.focusable(),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                items(rows, key = LocalSkillFileTreeRow::key) { row ->
                    val node = row.node
                    val file = (node as? LocalSkillFileTreeNode.File)?.entry
                    val directory = node as? LocalSkillFileTreeNode.Directory
                    val selected = row.key in selectedEntryKeys
                    var ctrlPressed by remember(row.key) { mutableStateOf(false) }
                    var shiftPressed by remember(row.key) { mutableStateOf(false) }
                    Row(
                        Modifier.fillMaxWidth()
                            .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                            .onPointerEvent(PointerEventType.Press) { event ->
                                ctrlPressed = event.keyboardModifiers.isPointerCtrlPressed
                                shiftPressed = event.keyboardModifiers.isPointerShiftPressed
                            }
                            .clickable {
                                focusRequester.requestFocus()
                                onSelectEntry(row, ctrlPressed, shiftPressed)
                                if (directory != null && !ctrlPressed && !shiftPressed) onToggleDirectory(directory.relativePath)
                            }
                            .padding(start = ((row.depth - 1).coerceAtLeast(0) * 16).dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        when {
                            directory != null -> Icon(
                                if (row.expanded) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight,
                                if (row.expanded) "收起目录 ${directory.name}" else "展开目录 ${directory.name}",
                                Modifier.size(16.dp).clickable {
                                    focusRequester.requestFocus()
                                    onToggleDirectory(directory.relativePath)
                                },
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            else -> Spacer(Modifier.width(16.dp))
                        }
                        Spacer(Modifier.width(4.dp))
                        when {
                            directory != null -> Icon(
                                if (row.expanded) Icons.Outlined.FolderOpen else Icons.Outlined.Folder,
                                null,
                                Modifier.size(16.dp),
                                tint = MaterialTheme.colorScheme.primary,
                            )
                            file != null -> Icon(
                                Icons.Outlined.Description,
                                null,
                                Modifier.size(16.dp),
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        Spacer(Modifier.width(6.dp))
                        Text(
                            directory?.name ?: file?.relativePath?.substringAfterLast('/').orEmpty(),
                            modifier = Modifier.weight(1f),
                            fontFamily = FontFamily.Monospace,
                            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                            style = MaterialTheme.typography.labelMedium,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
                }
            }
        }
    }
}

@Composable
private fun RequirementMaterialsDocumentPane(
    controller: DesktopApplication,
    root: Path,
    selectedFile: RequirementMaterialsMarkdownFile?,
    state: RequirementMaterialsDocumentState,
    onRetry: () -> Unit,
    onNavigateMarkdown: (String, String?) -> Unit = { _, _ -> },
    pendingAnchor: String? = null,
    modifier: Modifier = Modifier,
) {
    OutlinedCard(modifier) {
        val loaded = (state as? RequirementMaterialsDocumentState.Loaded)
            ?.takeIf { it.relativePath == selectedFile?.relativePath }
        val sourcePath = selectedFile?.let { file ->
            root.resolve(file.relativePath)
                .takeIf { Files.isRegularFile(it, LinkOption.NOFOLLOW_LINKS) && !Files.isSymbolicLink(it) }
        }
        var mode by remember(root, selectedFile?.relativePath) { mutableStateOf(initialMarkdownPreviewMode()) }
        Column(Modifier.fillMaxSize().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (selectedFile != null) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f).padding(end = 8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(
                            selectedFile.fileName,
                            style = MaterialTheme.typography.bodyMedium,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.SemiBold,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            selectedFile.relativePath,
                            style = MaterialTheme.typography.labelSmall,
                            fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    MarkdownPreviewToolbarActions(
                        mode = mode,
                        onModeChange = { mode = it },
                        fileTypeLabel = if (selectedFile.markdown) "Markdown" else selectedFile.extension.uppercase(),
                        onCopySource = { loaded?.content?.let { content ->
                            controller.copyText(content, "文件原文已复制")
                        } },
                        copyEnabled = !loaded?.content.isNullOrEmpty(),
                        sourcePath = sourcePath,
                        onCopyPath = { path -> controller.copyText(path.toAbsolutePath().toString(), "文档路径已复制") },
                        onCopyFile = controller::copyFile,
                    )
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
            when {
                selectedFile == null -> BrowserEmptyCard(
                    title = "选择一个文件",
                    detail = "从左侧目录选择 .md、.json、.csv 或 .txt 文件。",
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                )
                state is RequirementMaterialsDocumentState.Loading || loaded == null && state !is RequirementMaterialsDocumentState.Failed -> {
                    Column(
                        Modifier.weight(1f).fillMaxWidth().padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        LinearProgressIndicator(Modifier.fillMaxWidth())
                        Text("正在读取文件…", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                state is RequirementMaterialsDocumentState.Failed && state.relativePath == selectedFile.relativePath -> {
                    BrowserErrorCard(
                        message = state.message,
                        onRetry = onRetry,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                    )
                }
                loaded != null -> if (selectedFile.markdown) {
                    MarkdownDocumentPreview(
                        content = loaded.content,
                        mode = mode,
                        modifier = Modifier.weight(1f).fillMaxWidth(),
                        sourcePath = sourcePath,
                        allowedRoot = root,
                        onNavigateLocalLink = onNavigateMarkdown,
                        headingAnchor = pendingAnchor,
                    )
                } else RequirementMaterialsTextPreview(
                    content = loaded.content,
                    extension = selectedFile.extension,
                    mode = mode,
                    onCopyFragment = { controller.copyText(it, "表格片段已复制") },
                    modifier = Modifier.weight(1f).fillMaxWidth(),
                )
            }
        }
    }
}

@Composable
private fun BrowserLoadingCard(modifier: Modifier = Modifier) {
    OutlinedCard(modifier) {
        Column(Modifier.fillMaxSize().padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            LinearProgressIndicator(Modifier.fillMaxWidth())
            Text("正在读取需求资料目录…", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun BrowserEmptyCard(
    title: String,
    detail: String,
    modifier: Modifier = Modifier,
) {
    OutlinedCard(modifier) {
        Column(
            Modifier.fillMaxSize().padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun BrowserErrorCard(
    message: String,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    OutlinedCard(modifier) {
        Column(
            Modifier.fillMaxSize().padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text("无法预览需求资料", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            TextButton(onClick = onRetry) { Text("重试") }
        }
    }
}
