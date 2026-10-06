package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.DesktopIntegration
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.ServiceWorkspace
import java.awt.Toolkit
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable
import java.awt.datatransfer.UnsupportedFlavorException
import java.awt.datatransfer.StringSelection
import java.io.File
import java.nio.file.Path
import java.nio.file.Files
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Mutex
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue

/**
 * The sole adapter for clipboard and operating-system actions. UI and feature
 * controllers receive this small boundary instead of calling AWT or the shell.
 */
class DesktopActions internal constructor(
    private val integration: DesktopIntegration,
    private val config: () -> AppConfig,
    private val onSettingsRequired: () -> Unit,
    private val onStatus: (String) -> Unit,
    private val onError: (Throwable) -> Unit,
    private val fileOpening: SystemFileOpening = PlatformSystemFileOpening(),
) {
    var fileOpenBusy by mutableStateOf(false)
        private set
    private val fileOpenMutex = Mutex()
    val fileApplicationChooserAvailable: Boolean get() = fileOpening.chooserAvailable
    suspend fun defaultFileApplication(path: Path): DefaultFileApplication = fileOpening.defaultApplication(path)
    suspend fun openFile(path: Path, chooseApplication: Boolean = false): FileOpenResult {
        currentCoroutineContext().ensureActive()
        if (!fileOpenMutex.tryLock()) return FileOpenResult.Cancelled
        fileOpenBusy = true
        try {
            val result = try {
                if (chooseApplication) fileOpening.chooseApplication(path) else fileOpening.open(path)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                FileOpenResult.Failed(failure)
            }
            currentCoroutineContext().ensureActive()
            when (result) {
                FileOpenResult.Submitted -> onStatus("已交给系统打开")
                FileOpenResult.Cancelled -> Unit
                is FileOpenResult.Failed -> onError(result.error)
            }
            return result
        } finally { fileOpenBusy = false; fileOpenMutex.unlock() }
    }
    suspend fun revealFile(path: Path) {
        val result = withContext(Dispatchers.IO) {
            try { Result.success(integration.reveal(path)) }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { Result.failure(failure) }
        }
        result.onFailure(onError)
    }
    fun close() = fileOpening.close()

    fun openWorkspace(workspace: ServiceWorkspace, type: DevelopmentToolType = workspace.developmentTool) {
        val path = config().developmentTools.firstOrNull { it.type == type }?.path
        if (path.isNullOrBlank()) {
            onSettingsRequired()
            onError(IllegalStateException("请先在设置中配置 ${type.displayName} 路径"))
            return
        }
        attempt { integration.openDevelopmentTool(Path.of(workspace.worktreePath), type, path) }
    }

    fun reveal(path: Path) = attempt { integration.reveal(path) }
    fun openDirectory(path: Path) = attempt { integration.openDirectory(path) }
    fun terminal(path: Path) = attempt { integration.openTerminal(path, config().terminalExecutable) }
    /** Copies the command path exactly as it is resolved by the CLI settings. */
    fun copyCliCommandPath(command: String) = attempt {
        require(command.isNotBlank()) { "CLI 命令路径不能为空" }
        integration.copyText(command)
    }.onSuccess { onStatus("命令路径已复制") }

    /**
     * Opens the system terminal, changes to the CLI's directory, and runs the
     * CLI without adding any arguments.  This intentionally bypasses the
     * user-configured terminal because the system-terminal adapter can pass a
     * resolved command path safely.
     */
    fun runCliInTerminal(command: String) = attempt {
        require(command.isNotBlank()) { "CLI 命令路径不能为空" }
        integration.openCliInSystemTerminal(command)
    }.onSuccess { onStatus("CLI 终端已打开") }
    fun openUrl(url: String) = attempt { integration.openUrl(url) }

    fun copy(text: String, message: String = "已复制") = attempt {
        Toolkit.getDefaultToolkit().systemClipboard.setContents(StringSelection(text), null)
    }.onSuccess { onStatus(message) }

    /** Copies existing files or folders as a native file-list clipboard payload. */
    fun copyFiles(paths: List<Path>) = attempt {
        require(paths.isNotEmpty()) { "请先选择文件或文件夹" }
        val normalized = paths.map { it.toAbsolutePath().normalize() }.distinct()
        normalized.forEach { path ->
            require(!Files.isSymbolicLink(path) && (Files.isRegularFile(path) || Files.isDirectory(path))) {
                "文件或文件夹不存在：$path"
            }
        }
        val selected = withoutCopiedDescendants(normalized)
        Toolkit.getDefaultToolkit().systemClipboard.setContents(FileListTransferable(selected.map(Path::toFile)), null)
    }.onSuccess { onStatus("文件引用已复制") }

    fun copyFile(path: Path) = copyFiles(listOf(path))

    private fun attempt(block: () -> Unit): Result<Unit> = runCatching(block).onFailure(onError)
}

internal fun withoutCopiedDescendants(paths: List<Path>): List<Path> = paths.distinct().filter { path ->
    paths.none { parent -> parent != path && Files.isDirectory(parent) && path.startsWith(parent) }
}

private class FileListTransferable(private val files: List<File>) : Transferable {
    override fun getTransferDataFlavors(): Array<DataFlavor> = arrayOf(DataFlavor.javaFileListFlavor)

    override fun isDataFlavorSupported(flavor: DataFlavor): Boolean = flavor == DataFlavor.javaFileListFlavor

    override fun getTransferData(flavor: DataFlavor): Any {
        if (!isDataFlavorSupported(flavor)) throw UnsupportedFlavorException(flavor)
        return files
    }
}

internal fun temporaryDevelopmentToolSelectionEnabled(config: AppConfig): Boolean =
    config.allowTemporaryDevelopmentToolSelection

internal val DevelopmentToolType.displayName: String
    get() = when (this) {
        DevelopmentToolType.INTELLIJ_IDEA -> "IntelliJ IDEA"
        DevelopmentToolType.WEBSTORM -> "WebStorm"
        DevelopmentToolType.PYCHARM -> "PyCharm"
        DevelopmentToolType.VISUAL_STUDIO_CODE -> "Visual Studio Code"
        DevelopmentToolType.ANDROID_STUDIO -> "Android Studio"
        DevelopmentToolType.DEVECO_STUDIO -> "DevEco Studio"
    }
