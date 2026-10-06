@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.border
import androidx.compose.foundation.draganddrop.dragAndDropTarget
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.FileDownload
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draganddrop.DragAndDropEvent
import androidx.compose.ui.draganddrop.DragAndDropTarget
import androidx.compose.ui.draganddrop.awtTransferable
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import io.github.vinceglb.filekit.FileKit
import io.github.vinceglb.filekit.PlatformFile
import io.github.vinceglb.filekit.dialogs.FileKitMode
import io.github.vinceglb.filekit.dialogs.openFilePicker
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable
import java.awt.dnd.DnDConstants
import java.awt.dnd.DropTargetDragEvent
import java.awt.dnd.DropTargetDropEvent
import java.io.File
import java.nio.file.Path

internal enum class MaterialsNameAction { NEW_DIRECTORY, NEW_MARKDOWN, RENAME }

internal data class MaterialsNameRequest(val action: MaterialsNameAction, val directory: String,
    val initialName: String, val relativePath: String? = null)

internal fun interface MaterialsImportFilePicker {
    suspend fun pickFiles(initialDirectory: Path): List<Path>?
}

internal object FileKitMaterialsImportFilePicker : MaterialsImportFilePicker {
    override suspend fun pickFiles(initialDirectory: Path): List<Path>? = FileKit.openFilePicker(
        mode = FileKitMode.Multiple(), directory = PlatformFile(initialDirectory.toFile()),
    )?.map { it.file.toPath() }
}

/** An action captures its destination immediately, so selecting another folder cannot retarget it. */
internal class MaterialsFileManagementState(
    private val root: Path,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val service: MaterialsFileManagementService,
    private val clipboard: MaterialsImageClipboard,
    private val picker: MaterialsImportFilePicker = FileKitMaterialsImportFilePicker,
) {
    var currentDirectory: String = ""
        internal set
    var onCompleted: (MaterialsFileOperationResult.Completed) -> Unit = {}
        internal set
    var onError: (String) -> Unit = {}
        internal set
    var nameRequest by mutableStateOf<MaterialsNameRequest?>(null)
        private set
    var nameError by mutableStateOf<String?>(null)
        private set
    var conflict by mutableStateOf<MaterialsFileConflict?>(null)
        private set
    var busy by mutableStateOf(false)
        private set
    var dragActive by mutableStateOf(false)
        internal set
    private var disposed = false
    private var job: Job? = null
    private val queue = ArrayDeque<() -> MaterialsFileOperationResult>()
    val acceptsInput: Boolean get() = !disposed && !busy && nameRequest == null && conflict == null

    fun createFolder() {
        if (acceptsInput) nameRequest = MaterialsNameRequest(MaterialsNameAction.NEW_DIRECTORY, currentDirectory, "新建文件夹")
    }
    fun createMarkdown() {
        if (acceptsInput) nameRequest = MaterialsNameRequest(MaterialsNameAction.NEW_MARKDOWN, currentDirectory, "新建文档.md")
    }
    fun rename(relativePath: String) {
        if (acceptsInput) nameRequest = MaterialsNameRequest(MaterialsNameAction.RENAME,
            relativePath.substringBeforeLast('/', ""), relativePath.substringAfterLast('/'), relativePath)
    }
    fun dismissName() { if (!busy) { nameRequest = null; nameError = null } }
    fun submitName(name: String) {
        val request = nameRequest ?: return
        nameError = runCatching { validateMaterialsEntryName(name) }.exceptionOrNull()?.message
        if (nameError != null) return
        nameRequest = null
        enqueue {
            when (request.action) {
                MaterialsNameAction.NEW_DIRECTORY -> service.createDirectory(root, request.directory, name)
                MaterialsNameAction.NEW_MARKDOWN -> service.createMarkdown(root, request.directory, name)
                MaterialsNameAction.RENAME -> service.rename(root, request.relativePath!!, name)
            }
        }
    }
    fun importFiles(paths: List<Path>) {
        if (!acceptsInput) return
        val directory = currentDirectory
        paths.distinct().forEach { source -> queue.add { service.importFile(root, directory, source) } }
        pump()
    }
    fun chooseImportFiles() {
        if (!acceptsInput) return
        val directory = currentDirectory
        busy = true
        job = scope.launch {
            try {
                val initial = runInterruptible(ioDispatcher) { service.resolveCurrentDirectory(root, directory) }
                val paths = picker.pickFiles(initial)
                coroutineContext.ensureActive()
                if (!disposed) paths.orEmpty().distinct().forEach { source ->
                    queue.add { service.importFile(root, directory, source) }
                }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (error: Throwable) { reportError(error.message ?: "无法打开文件选择器。") }
            finally { busy = false; pump() }
        }
    }
    fun pasteImage() {
        if (!acceptsInput) return
        val directory = currentDirectory
        enqueue {
            val image = clipboard.readImage() ?: error("剪贴板中没有图片，请先复制截图再粘贴。")
            service.pasteImage(root, directory, image)
        }
    }
    fun onPasteKeyEvent(event: KeyEvent): Boolean {
        if (event.type != KeyEventType.KeyDown || !event.isCtrlPressed || event.key != Key.V || !acceptsInput) return false
        pasteImage()
        return true
    }
    fun resolveConflict(choice: MaterialsConflictChoice, alternateName: String = conflict?.suggestedName.orEmpty()) {
        val pending = conflict ?: return
        if (choice == MaterialsConflictChoice.SAVE_AS) {
            nameError = runCatching { validateMaterialsEntryName(alternateName) }.exceptionOrNull()?.message
            if (nameError != null) return
        }
        conflict = null
        nameError = null
        queue.addFirst { service.resolveConflict(pending, choice, alternateName) }
        pump()
    }
    internal fun reportError(message: String) { if (!disposed) onError(message) }
    private fun enqueue(operation: () -> MaterialsFileOperationResult) { queue.add(operation); pump() }
    private fun pump() {
        if (disposed || busy || conflict != null || queue.isEmpty()) return
        busy = true
        job = scope.launch {
            try {
                while (queue.isNotEmpty() && conflict == null) {
                    val operation = queue.removeFirst()
                    try {
                        val result = runInterruptible(ioDispatcher) { operation() }
                        coroutineContext.ensureActive()
                        if (disposed) return@launch
                        when (result) {
                            is MaterialsFileOperationResult.Completed -> onCompleted(result)
                            is MaterialsFileOperationResult.Conflict -> conflict = result.conflict
                            MaterialsFileOperationResult.Cancelled -> Unit
                        }
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (error: Throwable) { reportError(error.message ?: "文件操作失败，请刷新后重试。") }
                }
            } finally { busy = false }
        }
    }
    internal fun dispose() { disposed = true; job?.cancel(); queue.clear(); dragActive = false }
}

@Composable
internal fun rememberMaterialsFileManagement(
    root: Path,
    currentDirectory: String,
    onCompleted: (MaterialsFileOperationResult.Completed) -> Unit,
    onError: (String) -> Unit,
    ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
    service: MaterialsFileManagementService = remember { MaterialsFileManagementService() },
    clipboard: MaterialsImageClipboard = SystemMaterialsImageClipboard,
    contextKey: Any = root,
    picker: MaterialsImportFilePicker = FileKitMaterialsImportFilePicker,
): MaterialsFileManagementState {
    val scope = rememberCoroutineScope()
    val state = remember(root, contextKey, ioDispatcher, service, clipboard, picker) {
        MaterialsFileManagementState(root, scope, ioDispatcher, service, clipboard, picker)
    }
    SideEffect {
        state.currentDirectory = currentDirectory
        state.onCompleted = onCompleted
        state.onError = onError
    }
    DisposableEffect(state) { onDispose { state.dispose() } }
    return state
}

/** Menu entries are reusable in both the toolbar overflow and folder context menus. */
@Composable
internal fun MaterialsFileManagementMenuItems(state: MaterialsFileManagementState, onDismiss: () -> Unit = {}) {
    DropdownMenuItem(text = { Text("新建文件夹") }, leadingIcon = { Icon(Icons.Outlined.CreateNewFolder, null) },
        enabled = state.acceptsInput, onClick = { onDismiss(); state.createFolder() })
    DropdownMenuItem(text = { Text("新建 Markdown") }, leadingIcon = { Icon(Icons.Outlined.Add, null) },
        enabled = state.acceptsInput, onClick = { onDismiss(); state.createMarkdown() })
    DropdownMenuItem(text = { Text("粘贴截图") }, leadingIcon = { Icon(Icons.Outlined.ContentPaste, null) },
        enabled = state.acceptsInput, onClick = { onDismiss(); state.pasteImage() })
    DropdownMenuItem(text = { Text("导入文件") }, leadingIcon = { Icon(Icons.Outlined.FileDownload, null) },
        enabled = state.acceptsInput, onClick = { onDismiss(); state.chooseImportFiles() })
}

@Composable
internal fun MaterialsFileManagementDialogs(state: MaterialsFileManagementState) {
    state.nameRequest?.let { request ->
        val title = when (request.action) {
            MaterialsNameAction.NEW_DIRECTORY -> "新建文件夹"
            MaterialsNameAction.NEW_MARKDOWN -> "新建 Markdown"
            MaterialsNameAction.RENAME -> "重命名"
        }
        MaterialsEntryNameDialog(title, request.initialName, request.directory, state.nameError,
            if (request.action == MaterialsNameAction.RENAME) "重命名" else "创建", state::submitName, state::dismissName)
    }
    state.conflict?.let { pending ->
        var alternative by remember(pending) { mutableStateOf(pending.suggestedName) }
        AlertDialog(onDismissRequest = { state.resolveConflict(MaterialsConflictChoice.CANCEL) },
            title = { Text("同名项已存在") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("“${pending.request.name}”已存在。输入其他名称另存，或取消本项操作。")
                    if (pending.canReplace) Text("替换会覆盖现有文件内容。", color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall)
                    OutlinedTextField(alternative, { alternative = it }, singleLine = true,
                        label = { Text("另存为") }, modifier = Modifier.fillMaxWidth().testTag("materials-conflict-name"),
                        isError = state.nameError != null,
                        supportingText = state.nameError?.let { message -> ({ Text(message) }) })
                }
            },
            confirmButton = {
                TextButton(onClick = { state.resolveConflict(MaterialsConflictChoice.SAVE_AS, alternative) }) { Text("另存") }
            },
            dismissButton = {
                Row {
                    if (pending.canReplace) TextButton(onClick = { state.resolveConflict(MaterialsConflictChoice.REPLACE) }) { Text("替换") }
                    TextButton(onClick = { state.resolveConflict(MaterialsConflictChoice.CANCEL) }) { Text("取消") }
                }
            })
    }
}

@Composable
private fun MaterialsEntryNameDialog(title: String, initialName: String, directory: String, error: String?,
    confirmLabel: String, onConfirm: (String) -> Unit, onDismiss: () -> Unit) {
    var name by remember(title, initialName, directory) { mutableStateOf(initialName) }
    val focus = remember { FocusRequester() }
    AlertDialog(onDismissRequest = onDismiss, title = { Text(title) },
        text = {
            // The desktop dialog has its own composition. Request focus after its text field
            // attaches, rather than from the outer scene before the dialog exists.
            androidx.compose.runtime.LaunchedEffect(focus) {
                androidx.compose.runtime.withFrameNanos { }
                focus.requestFocus()
            }
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("位置：${directory.ifEmpty { "任务资料根目录" }}", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                OutlinedTextField(name, { name = it }, label = { Text("名称") }, singleLine = true,
                    modifier = Modifier.fillMaxWidth().focusRequester(focus).testTag("materials-entry-name")
                        .onKeyEvent { event ->
                            if (event.type == KeyEventType.KeyDown && event.key == Key.Enter) { onConfirm(name); true } else false
                        }, isError = error != null,
                    supportingText = error?.let { message -> ({ Text(message) }) })
            }
        }, confirmButton = { TextButton(onClick = { onConfirm(name) }) { Text(confirmLabel) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } })
}

/** Reads a native file list only on a drop, and never reports a MOVE operation as successful. */
internal fun materialFilesFromTransferable(transferable: Transferable): List<Path> {
    require(transferable.isDataFlavorSupported(DataFlavor.javaFileListFlavor)) { "请拖入文件。" }
    val payload = transferable.getTransferData(DataFlavor.javaFileListFlavor) as? List<*> ?: error("拖入的文件列表无效。")
    require(payload.all { it is File }) { "拖入的文件列表无效。" }
    return payload.filterIsInstance<File>().map(File::toPath)
}

internal fun Modifier.materialsFileDropTarget(state: MaterialsFileManagementState): Modifier = composed {
    val target = remember(state) {
        object : DragAndDropTarget {
            override fun onEntered(event: DragAndDropEvent) { state.dragActive = state.acceptsInput }
            override fun onExited(event: DragAndDropEvent) { state.dragActive = false }
            override fun onEnded(event: DragAndDropEvent) { state.dragActive = false }
            override fun onDrop(event: DragAndDropEvent): Boolean {
                state.dragActive = false
                if (!state.acceptsInput) return false
                val native = event.nativeEvent as? DropTargetDropEvent ?: return false
                if (native.sourceActions and DnDConstants.ACTION_COPY == 0) return false
                return try {
                    // Compose's desktop host initially accepts the source action. Change it to COPY
                    // before reading data, otherwise Explorer could remove a file after async import.
                    native.acceptDrop(DnDConstants.ACTION_COPY)
                    val paths = materialFilesFromTransferable(event.awtTransferable)
                    if (paths.isEmpty()) false else { state.importFiles(paths); true }
                } catch (error: Exception) { state.reportError(error.message ?: "无法读取拖入的文件。"); false }
            }
        }
    }
    dragAndDropTarget(shouldStartDragAndDrop = { event ->
        state.acceptsInput && runCatching {
            val native = event.nativeEvent as? DropTargetDragEvent
            native != null && native.sourceActions and DnDConstants.ACTION_COPY != 0 &&
                event.awtTransferable.isDataFlavorSupported(DataFlavor.javaFileListFlavor)
        }.getOrDefault(false)
    }, target = target)
}

/** Wrap the directory pane so Ctrl+V does not intercept editable preview/search text fields. */
@Composable
internal fun MaterialsFileManagementDropZone(state: MaterialsFileManagementState, modifier: Modifier = Modifier,
    content: @Composable () -> Unit) {
    Box(modifier.materialsFileDropTarget(state).onKeyEvent(state::onPasteKeyEvent)
        .then(if (state.dragActive) Modifier.border(1.dp, MaterialTheme.colorScheme.primary) else Modifier)) {
        content()
        if (state.dragActive) Surface(Modifier.align(Alignment.BottomCenter).padding(8.dp).widthIn(max = 360.dp),
            color = MaterialTheme.colorScheme.primaryContainer) {
            Text("复制文件到 ${state.currentDirectory.ifEmpty { "任务资料根目录" }}", Modifier.padding(10.dp),
                color = MaterialTheme.colorScheme.onPrimaryContainer, style = MaterialTheme.typography.bodySmall)
        }
    }
}
