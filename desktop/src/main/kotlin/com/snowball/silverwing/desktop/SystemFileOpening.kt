package com.snowball.silverwing.desktop

import com.sun.jna.Native
import com.sun.jna.Pointer
import com.sun.jna.Structure
import com.sun.jna.WString
import com.sun.jna.platform.win32.Kernel32
import com.sun.jna.platform.win32.Ole32
import com.sun.jna.platform.win32.WinDef.HWND
import com.sun.jna.ptr.IntByReference
import com.sun.jna.win32.StdCallLibrary
import com.sun.jna.win32.W32APIOptions
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.withContext
import kotlinx.coroutines.runInterruptible
import java.awt.Desktop
import java.nio.file.Files
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

data class DefaultFileApplication(val displayName: String? = null, val associationMissing: Boolean = false)

sealed interface FileOpenResult {
    data object Submitted : FileOpenResult
    data object Cancelled : FileOpenResult
    data class Failed(val error: Throwable) : FileOpenResult
}

/** 文件打开和网页跳转分开；测试可替换此边界，不启动用户的软件。 */
interface SystemFileOpening : AutoCloseable {
    val chooserAvailable: Boolean
    suspend fun defaultApplication(path: Path): DefaultFileApplication
    suspend fun open(path: Path): FileOpenResult
    suspend fun chooseApplication(path: Path): FileOpenResult
    override fun close() {}
}

class PlatformSystemFileOpening : SystemFileOpening {
    private val windows = System.getProperty("os.name").startsWith("Windows", ignoreCase = true)
    override val chooserAvailable: Boolean get() = windows
    // Shell 扩展可能依赖 STA；独立线程串行调用，不能占用 Compose 或共享 IO 线程。
    private val sta = if (windows) Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "silverwing-file-open-sta").apply { isDaemon = true }
    }.asCoroutineDispatcher() else null

    override suspend fun defaultApplication(path: Path): DefaultFileApplication = if (windows) onSta {
        val extension = path.fileName?.toString()?.substringAfterLast('.', "")?.takeIf(String::isNotBlank)
            ?: return@onSta DefaultFileApplication(associationMissing = true)
        val association = queryAssociation(".$extension", 20) // ASSOCSTR_PROGID：系统当前默认关联。
        val name = queryAssociation(association.value ?: ".$extension", 4).value // FRIENDLYAPPNAME
        // Windows 对未关联类型也可能返回 Unknown / “Pick an app”，查询成功不代表有默认软件。
        val missing = association.value.equals("Unknown", ignoreCase = true) || association.error in setOf(NO_ASSOCIATION, FILE_NOT_FOUND)
        DefaultFileApplication(if (missing) null else name?.takeIf(String::isNotBlank), missing)
    } else DefaultFileApplication()

    override suspend fun open(path: Path): FileOpenResult = perform(path) { absolute ->
        if (windows) onSta {
            val info = ShellExecutionInfo().apply {
                fMask = 0x00000400 or 0x00000100 // FLAG_NO_UI / NOASYNC：错误由本程序统一显示。
                lpVerb = WString("open")
                lpFile = WString(absolute.toString())
                nShow = 1
            }
            if (FileShellApi.instance.ShellExecuteExW(info)) FileOpenResult.Submitted else {
                when (val error = Kernel32.INSTANCE.GetLastError()) {
                    NO_ASSOCIATION -> chooseOnSta(absolute)
                    CANCELLED -> FileOpenResult.Cancelled
                    else -> FileOpenResult.Failed(IllegalStateException("系统无法打开文件（错误 $error），请尝试其他应用。"))
                }
            }
        } else runInterruptible(Dispatchers.IO) {
            if (Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.OPEN)) {
                Desktop.getDesktop().open(absolute.toFile())
            } else {
                runFileOpenProcess(platformFileOpenCommand(System.getProperty("os.name"), absolute))
            }
            FileOpenResult.Submitted
        }
    }

    override suspend fun chooseApplication(path: Path): FileOpenResult = perform(path) { absolute ->
        if (windows) onSta { chooseOnSta(absolute) }
        else FileOpenResult.Failed(IllegalStateException("当前系统不支持应用选择窗口。"))
    }

    private suspend fun perform(path: Path, block: suspend (Path) -> FileOpenResult): FileOpenResult = try {
        val absolute = withContext(Dispatchers.IO) {
            path.toAbsolutePath().normalize().also {
                require(Files.isRegularFile(it, NOFOLLOW_LINKS) && !Files.isSymbolicLink(it)) { "文件不存在或不是普通文件：$it" }
            }
        }
        block(absolute)
    } catch (cancelled: kotlinx.coroutines.CancellationException) {
        throw cancelled
    } catch (error: Exception) {
        FileOpenResult.Failed(error)
    }

    private suspend fun <T> onSta(block: () -> T): T = withContext(requireNotNull(sta)) {
        val initialized = Ole32.INSTANCE.CoInitializeEx(null, Ole32.COINIT_APARTMENTTHREADED or Ole32.COINIT_DISABLE_OLE1DDE).toInt()
        check(initialized >= 0) { "无法初始化系统文件打开服务。" }
        try { block() } finally { Ole32.INSTANCE.CoUninitialize() }
    }

    private fun chooseOnSta(path: Path): FileOpenResult {
        val info = OpenAsInfo().apply { pcszFile = WString(path.toString()); oaifInFlags = 0x4 } // OAIF_EXEC，仅打开本次文件。
        val result = FileShellApi.instance.SHOpenWithDialog(null, info)
        return when {
            result == 0 -> FileOpenResult.Submitted
            result == hresult(CANCELLED) -> FileOpenResult.Cancelled
            else -> FileOpenResult.Failed(IllegalStateException("无法选择打开应用（错误 ${result.toUInt().toString(16)}）。"))
        }
    }

    override fun close() { sta?.close() }

    private companion object {
        const val NO_ASSOCIATION = 1155
        const val FILE_NOT_FOUND = 2
        const val CANCELLED = 1223
        fun hresult(error: Int) = 0x80070000.toInt() or error
    }
}

internal fun platformFileOpenCommand(osName: String, path: Path): List<String> =
    listOf(if (osName.startsWith("Mac", true)) "open" else "xdg-open", path.toAbsolutePath().normalize().toString())

/** Discard launcher diagnostics so a noisy association cannot fill a pipe and stall waitFor. */
internal fun runFileOpenProcess(
    command: List<String>,
    timeoutMillis: Long = 15_000,
    startProcess: (ProcessBuilder) -> Process = { it.start() },
) {
    val process = startProcess(ProcessBuilder(command).redirectOutput(ProcessBuilder.Redirect.DISCARD)
        .redirectError(ProcessBuilder.Redirect.DISCARD))
    try {
        process.outputStream.close()
        check(process.waitFor(timeoutMillis, TimeUnit.MILLISECONDS)) { "系统打开文件超时，请重试。" }
        check(process.exitValue() == 0) { "系统无法打开文件，请检查默认应用。" }
    } finally {
        try { if (process.isAlive) process.destroyForcibly() }
        finally {
            runCatching { process.outputStream.close() }
            runCatching { process.inputStream.close() }
            runCatching { process.errorStream.close() }
        }
    }
}

internal data class AssociationString(val value: String?, val error: Int? = null)
internal fun queryAssociation(association: String, kind: Int, api: AssociationApi = AssociationApi.instance): AssociationString {
    val length = IntByReference()
    val first = api.AssocQueryStringW(0x120, kind, WString(association), null, null, length) // NOTRUNCATE | NOFIXUPS
    if (first < 0) return AssociationString(null, first and 0xFFFF)
    repeat(3) {
        if (length.value !in 1..32768) return AssociationString(null)
        val buffer = CharArray(length.value)
        val result = api.AssocQueryStringW(0x120, kind, WString(association), null, buffer, length)
        if (result == 0) return AssociationString(buffer.takeWhile { it != '\u0000' }.joinToString(""))
        // A user can change the default association between the size and value queries.
        if (result != 0x80004003.toInt() || length.value <= buffer.size) return AssociationString(null, result and 0xFFFF)
    }
    return AssociationString(null)
}

@Structure.FieldOrder("pcszFile", "pcszClass", "oaifInFlags")
internal class OpenAsInfo : Structure() {
    @JvmField var pcszFile: WString? = null
    @JvmField var pcszClass: WString? = null
    @JvmField var oaifInFlags: Int = 0
}

internal interface FileShellApi : StdCallLibrary {
    fun ShellExecuteExW(info: ShellExecutionInfo): Boolean
    fun SHOpenWithDialog(parent: HWND?, info: OpenAsInfo): Int
    companion object { val instance: FileShellApi by lazy { Native.load("shell32", FileShellApi::class.java, W32APIOptions.UNICODE_OPTIONS) } }
}

/** 显式使用 UTF-16 字段，不能把默认窄字符串结构传给 ShellExecuteExW。 */
@Structure.FieldOrder("cbSize", "fMask", "hwnd", "lpVerb", "lpFile", "lpParameters", "lpDirectory", "nShow", "hInstApp", "lpIDList", "lpClass", "hkeyClass", "dwHotKey", "hMonitor", "hProcess")
internal class ShellExecutionInfo : Structure() {
    @JvmField var cbSize: Int = 0
    @JvmField var fMask: Int = 0
    @JvmField var hwnd: Pointer? = null
    @JvmField var lpVerb: WString? = null
    @JvmField var lpFile: WString? = null
    @JvmField var lpParameters: WString? = null
    @JvmField var lpDirectory: WString? = null
    @JvmField var nShow: Int = 1
    @JvmField var hInstApp: Pointer? = null
    @JvmField var lpIDList: Pointer? = null
    @JvmField var lpClass: WString? = null
    @JvmField var hkeyClass: Pointer? = null
    @JvmField var dwHotKey: Int = 0
    @JvmField var hMonitor: Pointer? = null
    @JvmField var hProcess: Pointer? = null
    init { cbSize = size() }
}

internal interface AssociationApi : StdCallLibrary {
    fun AssocQueryStringW(flags: Int, kind: Int, association: WString, extra: WString?, output: CharArray?, length: IntByReference): Int
    companion object { val instance: AssociationApi by lazy { Native.load("shlwapi", AssociationApi::class.java, W32APIOptions.UNICODE_OPTIONS) } }
}
