package com.snowball.silverwing.desktop

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.SecondaryTabRow
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.snowball.silverwing.core.LocalSkillCatalogItem
import com.snowball.silverwing.core.LocalSkillFileEntry
import java.nio.file.Path

/** Primary browser for Skills installed in the current user's standard directory. */
@Composable
internal fun LocalSkillsScreen(controller: DesktopApplication, localSkills: LocalSkillsController = controller.localSkillsController) {
    val catalogState = localSkills.catalogState
    val catalog = (catalogState as? LocalSkillCatalogLoadState.Loaded)?.catalog
    var selectedDirectoryName by remember { mutableStateOf<String?>(null) }
    var fileSelection by remember { mutableStateOf<LocalSkillFileSelection?>(null) }
    var compactPane by remember { mutableStateOf(LocalSkillsCompactPane.SKILLS) }
    var uninstallTarget by remember { mutableStateOf<LocalSkillCatalogItem?>(null) }
    var expandedDirectoriesBySkill by remember { mutableStateOf<Map<String, Set<String>>>(emptyMap()) }
    val selected = catalog?.skills?.firstOrNull { it.directoryName == selectedDirectoryName }
        ?: catalog?.skills?.firstOrNull()
    val filesCatalog = (localSkills.filesState as? LocalSkillFilesState.Loaded)?.catalog
        ?.takeIf { it.directoryName == selected?.directoryName }
    val visibleFilesState = when (val current = localSkills.filesState) {
        is LocalSkillFilesState.Loaded -> current.takeIf { it.catalog.directoryName == selected?.directoryName }
            ?: LocalSkillFilesState.Empty
        is LocalSkillFilesState.Failed -> current.takeIf { it.directoryName == selected?.directoryName }
            ?: LocalSkillFilesState.Empty
        else -> current
    }
    val selectedFile = filesCatalog?.files?.firstOrNull {
        fileSelection?.directoryName == selected?.directoryName && it.relativePath == fileSelection?.relativePath
    }
        ?: filesCatalog?.files?.firstOrNull()
    val uninstalling = localSkills.uninstallState is LocalSkillUninstallState.Removing
    val expandedDirectories = selected?.let { expandedDirectoriesBySkill[it.directoryName] } ?: emptySet()

    fun toggleDirectory(path: String) {
        val skill = selected ?: return
        val current = expandedDirectoriesBySkill[skill.directoryName] ?: emptySet()
        val next = if (path in current) current - path else current + path
        expandedDirectoriesBySkill = expandedDirectoriesBySkill + (skill.directoryName to next)
    }

    LaunchedEffect(Unit) { localSkills.refresh() }
    LaunchedEffect(catalog?.skills) {
        val loaded = catalog ?: return@LaunchedEffect
        if (loaded.skills.isEmpty()) fileSelection = null
        if (loaded.skills.none { it.directoryName == selectedDirectoryName }) {
            selectedDirectoryName = loaded.skills.firstOrNull()?.directoryName
        }
    }
    LaunchedEffect(selected?.directoryName) {
        val skill = selected
        if (skill == null) {
            if (catalog != null) fileSelection = null
            return@LaunchedEffect
        }
        if (fileSelection?.directoryName != skill.directoryName) fileSelection = LocalSkillFileSelection(skill.directoryName, null)
        localSkills.loadFiles(skill)
    }
    LaunchedEffect(selected?.directoryName, filesCatalog?.files) {
        val loaded = filesCatalog ?: return@LaunchedEffect
        val previous = fileSelection?.takeIf { it.directoryName == loaded.directoryName }?.relativePath
        fileSelection = LocalSkillFileSelection(loaded.directoryName,
            previous?.takeIf { path -> loaded.files.any { it.relativePath == path } } ?: loaded.files.firstOrNull()?.relativePath)
    }
    LaunchedEffect(selected?.directoryName, selectedFile?.relativePath) {
        if (selected != null && selectedFile != null) localSkills.preview(selected, selectedFile)
    }

    BoxWithConstraints(
        Modifier.fillMaxSize().padding(start = MAIN_CONTENT_START_PADDING_DP.dp, end = 28.dp, bottom = 28.dp),
    ) {
        val compact = maxWidth < 960.dp
        Column(
            Modifier.fillMaxSize(),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(
                        "本机 Skills${catalog?.skills?.size?.let { "（$it）" }.orEmpty()}",
                        style = MaterialTheme.typography.titleLarge,
                    )
                    Text(
                        catalog?.root ?: "~/.agents/skills",
                        style = MaterialTheme.typography.labelSmall,
                        fontFamily = FontFamily.Monospace,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                ActionIconButton(
                    label = selected?.let { "卸载 ${it.name}" } ?: "请选择要卸载的 Skill",
                    onClick = { selected?.let { uninstallTarget = it } },
                    modifier = Modifier.size(30.dp),
                    enabled = selected != null && !uninstalling && catalogState is LocalSkillCatalogLoadState.Loaded,
                    loading = uninstalling,
                ) {
                    Icon(
                        Icons.Outlined.Delete,
                        "卸载 Skill",
                        Modifier.size(16.dp),
                        tint = MaterialTheme.colorScheme.error,
                    )
                }
                ActionIconButton(
                    label = "刷新本机 Skill",
                    onClick = {
                        expandedDirectoriesBySkill = emptyMap()
                        localSkills.refresh()
                    },
                    modifier = Modifier.size(30.dp),
                    enabled = !uninstalling,
                    loading = catalogState is LocalSkillCatalogLoadState.Loading,
                ) {
                    Icon(Icons.Outlined.Refresh, "刷新本机 Skill", Modifier.size(16.dp))
                }
            }
            when (val uninstall = localSkills.uninstallState) {
                LocalSkillUninstallState.Idle -> Unit
                is LocalSkillUninstallState.Removing -> Text(
                    "正在卸载 ${uninstall.directoryName}…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                is LocalSkillUninstallState.Succeeded -> Text(
                    "${uninstall.directoryName} 已卸载，恢复备份已保留在 SilverWing 本机目录。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.primary,
                )
                is LocalSkillUninstallState.Failed -> Text(
                    "卸载 ${uninstall.directoryName} 失败：${uninstall.message}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (compact) {
                SecondaryTabRow(selectedTabIndex = compactPane.ordinal) {
                    LocalSkillsCompactPane.entries.forEach { pane ->
                        Tab(selected = compactPane == pane, onClick = { compactPane = pane },
                            text = { Text(pane.label) })
                    }
                }
            }
            when (catalogState) {
                LocalSkillCatalogLoadState.Idle,
                LocalSkillCatalogLoadState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                is LocalSkillCatalogLoadState.Failed -> Text(
                    catalogState.message,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )
                is LocalSkillCatalogLoadState.Loaded -> {
                    if (catalogState.catalog.skills.isEmpty()) {
                        OutlinedCard(Modifier.fillMaxWidth()) {
                            Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Text("尚未发现本机 Skill", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                                Text(
                                    "请确认 ${catalogState.catalog.root} 下的直接子目录包含 SKILL.md。",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    } else {
                        LocalSkillsContent(
                            controller = controller,
                            skills = catalogState.catalog.skills,
                            skillRootPath = catalogState.catalog.root,
                            selected = selected,
                            onSelected = {
                                if (selectedDirectoryName != it.directoryName) fileSelection = LocalSkillFileSelection(it.directoryName, null)
                                selectedDirectoryName = it.directoryName
                                compactPane = LocalSkillsCompactPane.DIRECTORY
                            },
                            filesState = visibleFilesState,
                            expandedDirectories = expandedDirectories,
                            onToggleDirectory = ::toggleDirectory,
                            selectedFile = selectedFile,
                            onSelectedFile = { file ->
                                selected?.let { fileSelection = LocalSkillFileSelection(it.directoryName, file.relativePath) }
                                compactPane = LocalSkillsCompactPane.DOCUMENT
                            },
                            previewState = localSkills.previewState,
                            paneWidthSession = localSkills.paneWidthSession,
                            compactPane = compactPane.takeIf { compact },
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            }
        }
    }
    uninstallTarget?.let { skill ->
        ConfirmDialog(
            title = "卸载 Skill？",
            message = "将从本机 Skill 目录删除“${skill.name}”。SilverWing 会先创建可恢复备份；不会删除任何 Skill 来源配置。",
            confirmLabel = "卸载",
            destructive = true,
            onDismiss = { uninstallTarget = null },
            onConfirm = {
                uninstallTarget = null
                localSkills.uninstall(skill)
            },
        )
    }
}

/** 只保存选择标识；文件路径不能跨 Skill 套用，读取中的空状态也不是删除结果。 */
private data class LocalSkillFileSelection(val directoryName: String, val relativePath: String?)

private enum class LocalSkillsCompactPane(val label: String) {
    SKILLS("Skills"), DIRECTORY("目录"), DOCUMENT("文档"),
}

@Composable
private fun LocalSkillsContent(
    controller: DesktopApplication,
    skills: List<LocalSkillCatalogItem>,
    skillRootPath: String,
    selected: LocalSkillCatalogItem?,
    onSelected: (LocalSkillCatalogItem) -> Unit,
    filesState: LocalSkillFilesState,
    expandedDirectories: Set<String>,
    onToggleDirectory: (String) -> Unit,
    selectedFile: LocalSkillFileEntry?,
    onSelectedFile: (LocalSkillFileEntry) -> Unit,
    previewState: LocalSkillFilePreviewState,
    paneWidthSession: LocalSkillsPaneWidthSession,
    compactPane: LocalSkillsCompactPane?,
    modifier: Modifier = Modifier,
) {
    val skillList: @Composable (Modifier) -> Unit = { paneModifier ->
        LocalSkillListPane(skills, selected, onSelected, paneModifier)
    }
    val directory: @Composable (Modifier) -> Unit = { paneModifier ->
        LocalSkillDirectoryPane(filesState, expandedDirectories, onToggleDirectory, selectedFile, onSelectedFile, paneModifier)
    }
    val document: @Composable (Modifier) -> Unit = { paneModifier ->
        LocalSkillDocumentPane(controller, skillRootPath, selected, filesState, selectedFile,
            onSelectedFile, previewState, compactPane != null, paneModifier)
    }
    if (compactPane != null) {
        // Compose only the active panel; navigation and selection live outside this branch.
        when (compactPane) {
            LocalSkillsCompactPane.SKILLS -> skillList(modifier.fillMaxSize())
            LocalSkillsCompactPane.DIRECTORY -> directory(modifier.fillMaxSize())
            LocalSkillsCompactPane.DOCUMENT -> document(modifier.fillMaxSize())
        }
        return
    }
    BoxWithConstraints(modifier.fillMaxSize()) {
        val availableWidthDp = maxWidth.value
        val layout = resolveLocalSkillsPaneLayout(availableWidthDp, paneWidthSession.preferences)
        Row(Modifier.fillMaxSize()) {
            skillList(Modifier.width(layout.skillListWidthDp.dp).fillMaxHeight())
            LocalSkillsPaneResizeHandle(
                label = "拖动调整 Skill 列表和目录宽度",
                onResizeStart = { paneWidthSession.preferences = layout.toPreferences() },
                onResize = { dragAmountDp ->
                    val current = resolveLocalSkillsPaneLayout(availableWidthDp, paneWidthSession.preferences)
                    paneWidthSession.preferences = resizeLocalSkillsPaneLayout(
                        availableWidthDp, current, LocalSkillsPaneBoundary.SKILL_LIST_AND_DIRECTORY, dragAmountDp,
                    ).toPreferences()
                },
            )
            directory(Modifier.width(layout.directoryWidthDp.dp).fillMaxHeight())
            LocalSkillsPaneResizeHandle(
                label = "拖动调整目录和文件预览宽度",
                onResizeStart = { paneWidthSession.preferences = layout.toPreferences() },
                onResize = { dragAmountDp ->
                    val current = resolveLocalSkillsPaneLayout(availableWidthDp, paneWidthSession.preferences)
                    paneWidthSession.preferences = resizeLocalSkillsPaneLayout(
                        availableWidthDp, current, LocalSkillsPaneBoundary.DIRECTORY_AND_PREVIEW, dragAmountDp,
                    ).toPreferences()
                },
            )
            document(Modifier.width(layout.previewWidthDp.dp).fillMaxHeight())
        }
    }
}

@Composable
private fun LocalSkillListPane(
    skills: List<LocalSkillCatalogItem>,
    selected: LocalSkillCatalogItem?,
    onSelected: (LocalSkillCatalogItem) -> Unit,
    modifier: Modifier,
) {
    OutlinedCard(modifier) {
        LazyColumn(Modifier.fillMaxSize().padding(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            items(skills, key = LocalSkillCatalogItem::directoryName) { skill ->
                val isSelected = selected?.directoryName == skill.directoryName
                Column(
                    Modifier.fillMaxWidth()
                        .background(if (isSelected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                        .clickable { onSelected(skill) }.padding(10.dp),
                    verticalArrangement = Arrangement.spacedBy(3.dp),
                ) {
                    Text(skill.name, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.SemiBold,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(skill.description, style = MaterialTheme.typography.bodySmall,
                        maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

@Composable
private fun LocalSkillDirectoryPane(
    filesState: LocalSkillFilesState,
    expandedDirectories: Set<String>,
    onToggleDirectory: (String) -> Unit,
    selectedFile: LocalSkillFileEntry?,
    onSelectedFile: (LocalSkillFileEntry) -> Unit,
    modifier: Modifier,
) {
    OutlinedCard(modifier) {
        Column(Modifier.fillMaxSize().padding(8.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text("目录", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            when (filesState) {
                LocalSkillFilesState.Empty,
                LocalSkillFilesState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                is LocalSkillFilesState.Failed -> Text(filesState.message, color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall)
                is LocalSkillFilesState.Loaded -> {
                    if (filesState.catalog.files.isEmpty()) {
                        Text("目录中没有可预览文件", color = MaterialTheme.colorScheme.onSurfaceVariant,
                            style = MaterialTheme.typography.bodySmall)
                    } else {
                        LocalSkillDirectoryTree(filesState.catalog.directoryName, filesState.catalog.files,
                            expandedDirectories, selectedFile, onToggleDirectory, onSelectedFile,
                            Modifier.weight(1f).fillMaxWidth())
                    }
                    if (filesState.catalog.truncated) {
                        Text("仅显示前 2,000 个文件", style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
    }
}

@Composable
private fun LocalSkillDocumentPane(
    controller: DesktopApplication,
    skillRootPath: String,
    selected: LocalSkillCatalogItem?,
    filesState: LocalSkillFilesState,
    selectedFile: LocalSkillFileEntry?,
    onSelectedFile: (LocalSkillFileEntry) -> Unit,
    previewState: LocalSkillFilePreviewState,
    compact: Boolean,
    modifier: Modifier,
) {
    OutlinedCard(modifier) {
        Column(Modifier.fillMaxSize().padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            selected?.let { skill ->
                selectedFile?.let { file ->
                    val previewContent = (previewState as? LocalSkillFilePreviewState.Loaded)
                        ?.takeIf { it.directoryName == skill.directoryName && it.relativePath == file.relativePath }?.content
                    val sourcePath = Path.of(skillRootPath).resolve(skill.directoryName).resolve(file.relativePath).normalize()
                    var mode by remember(skill.directoryName, file.relativePath) { mutableStateOf(initialMarkdownPreviewMode()) }
                    val outline = rememberMarkdownOutlineState(previewContent.orEmpty(), sourcePath,
                        sourcePath, Path.of(skillRootPath).resolve(skill.directoryName))
                    val actions: @Composable () -> Unit = {
                        if (file.markdown) {
                            MarkdownPreviewToolbarActions(
                                mode = mode, onModeChange = { mode = it },
                                onCopySource = { previewContent?.let { controller.copyText(it, "Markdown 源码已复制") } },
                                sourcePath = sourcePath,
                                onCopyPath = { controller.copyText(it.toAbsolutePath().toString(), "文件路径已复制") },
                                onCopyFile = controller::copyFile, copyEnabled = !previewContent.isNullOrEmpty(),
                                outlineState = outline,
                            )
                        } else {
                            ActionIconButton(label = "复制文件内容",
                                onClick = { previewContent?.let { controller.copyText(it, "文件内容已复制") } },
                                modifier = Modifier.size(30.dp), enabled = !previewContent.isNullOrEmpty()) {
                                Icon(Icons.Outlined.ContentCopy, "复制文件内容", Modifier.size(16.dp))
                            }
                        }
                    }
                    DocumentPreviewFileHeader(fileName = file.relativePath.substringAfterLast('/'),
                        relativePath = "${skill.directoryName}/${file.relativePath}") {
                        if (!compact) actions()
                    }
                    if (compact) Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) { actions() }
                    HorizontalDivider()
                    when (previewState) {
                        LocalSkillFilePreviewState.Empty,
                        LocalSkillFilePreviewState.Loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
                        is LocalSkillFilePreviewState.Failed -> {
                            if (previewState.directoryName == skill.directoryName && previewState.relativePath == file.relativePath) {
                                Text(previewState.message, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                            } else LinearProgressIndicator(Modifier.fillMaxWidth())
                        }
                        is LocalSkillFilePreviewState.Loaded -> {
                            if (previewState.directoryName == skill.directoryName && previewState.relativePath == file.relativePath) {
                                if (file.markdown) {
                                    MarkdownDocumentPreview(
                                        content = previewState.content, mode = mode, modifier = Modifier.weight(1f).fillMaxWidth(),
                                        onCopyCode = { controller.copyText(it, "代码已复制") }, sourcePath = sourcePath,
                                        allowedRoot = Path.of(skillRootPath).resolve(skill.directoryName),
                                        outlineState = outline,
                                        onNavigateLocalLink = { relativePath, _ ->
                                            (filesState as? LocalSkillFilesState.Loaded)?.catalog?.files
                                                ?.firstOrNull { it.relativePath == relativePath && it.markdown }?.let(onSelectedFile)
                                        },
                                    )
                                } else PlainTextDocumentPreview(previewState.content, Modifier.weight(1f).fillMaxWidth())
                            } else LinearProgressIndicator(Modifier.fillMaxWidth())
                        }
                    }
                } ?: Text("请选择一个文件", color = MaterialTheme.colorScheme.onSurfaceVariant)
            } ?: Text("请选择一个 Skill", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun LocalSkillDirectoryTree(
    skillDirectoryName: String,
    files: List<LocalSkillFileEntry>,
    expandedDirectories: Set<String>,
    selectedFile: LocalSkillFileEntry?,
    onToggleDirectory: (String) -> Unit,
    onSelectedFile: (LocalSkillFileEntry) -> Unit,
    modifier: Modifier = Modifier,
) {
    val tree = remember(skillDirectoryName, files) { buildLocalSkillFileTree(skillDirectoryName, files) }
    val rows = remember(tree, expandedDirectories) { visibleLocalSkillFileTreeRows(tree, expandedDirectories) }
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        items(rows, key = LocalSkillFileTreeRow::key) { row ->
            val node = row.node
            val file = (node as? LocalSkillFileTreeNode.File)?.entry
            val directory = node as? LocalSkillFileTreeNode.Directory
            val rootDirectory = directory?.relativePath?.isEmpty() == true
            val selected = file != null && file.relativePath == selectedFile?.relativePath
            val clickModifier = when {
                file != null -> Modifier.clickable { onSelectedFile(file) }
                directory != null && !rootDirectory -> Modifier.clickable { onToggleDirectory(directory.relativePath) }
                else -> Modifier
            }
            Row(
                Modifier.fillMaxWidth()
                    .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                    .then(clickModifier)
                    .padding(start = (8 + row.depth * 16).dp, end = 8.dp, top = 6.dp, bottom = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                when {
                    directory != null && !rootDirectory -> Icon(
                        if (row.expanded) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight,
                        if (row.expanded) "收起目录 ${directory.name}" else "展开目录 ${directory.name}",
                        Modifier.size(16.dp),
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
                        if (file.markdown) Icons.Outlined.Description else Icons.AutoMirrored.Outlined.InsertDriveFile,
                        null,
                        Modifier.size(16.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.width(6.dp))
                Text(
                    directory?.name ?: file?.relativePath?.substringAfterLast('/').orEmpty(),
                    fontFamily = FontFamily.Monospace,
                    fontWeight = if (rootDirectory || selected) FontWeight.SemiBold else FontWeight.Normal,
                    style = MaterialTheme.typography.labelMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}
