package com.snowball.silverwing.desktop

import com.sun.jna.Native
import com.sun.jna.Pointer
import com.sun.jna.WString
import com.sun.jna.platform.win32.COM.COMUtils
import com.sun.jna.platform.win32.COM.COMException
import com.sun.jna.platform.win32.COM.Unknown
import com.sun.jna.platform.win32.Guid
import com.sun.jna.platform.win32.Ole32
import com.sun.jna.platform.win32.WinNT.HRESULT
import com.sun.jna.ptr.IntByReference
import com.sun.jna.ptr.PointerByReference
import com.sun.jna.win32.StdCallLibrary
import com.sun.jna.win32.W32APIOptions
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.withContext
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import java.awt.Desktop
import java.nio.file.Path
import java.util.concurrent.Executors

data class FileTrashResult(val path: Path, val error: String? = null, val cancelled: Boolean = false)
interface SystemFileTrash : AutoCloseable {
    suspend fun moveToTrash(paths: List<Path>): List<FileTrashResult>
    override fun close() {}
}

/** 只请求回收，失败不得退化为永久删除。原生 Shell 调用在独立 STA 线程执行。 */
class PlatformSystemFileTrash : SystemFileTrash {
    private val dispatcher = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "silverwing-trash-sta").apply { isDaemon = true } }.asCoroutineDispatcher()
    override suspend fun moveToTrash(paths: List<Path>): List<FileTrashResult> = withContext(dispatcher) {
        collectFileTrashResults(paths) { path ->
            val absolute = path.toAbsolutePath().normalize()
            if (System.getProperty("os.name").startsWith("Windows", true)) recycleWindows(absolute)
            else {
                check(Desktop.isDesktopSupported() && Desktop.getDesktop().isSupported(Desktop.Action.MOVE_TO_TRASH)) { "当前系统不支持回收站删除" }
                check(Desktop.getDesktop().moveToTrash(absolute.toFile())) { "无法移入系统回收站" }
                true
            }
        }
    }

    private fun recycleWindows(path: Path): Boolean {
        COMUtils.checkRC(Ole32.INSTANCE.CoInitializeEx(null, Ole32.COINIT_APARTMENTTHREADED))
        try {
            val pointer = PointerByReference()
            COMUtils.checkRC(Ole32.INSTANCE.CoCreateInstance(Guid.CLSID("{3AD05575-8857-4850-9277-11B85BDB8E09}"), null,
                1, Guid.IID("{947AAB5F-0A5C-4C13-B4D6-4BF7836FC9F8}"), pointer))
            val operation = RecycleOperation(pointer.value)
            try {
                // RECYCLEONDELETE、EARLYFAILURE、NOERRORUI、SILENT、NOCONFIRMATION；不遍历目录交接点。
                operation.call(5, 0x00080000 or 0x00100000 or 0x0400 or 0x0004 or 0x0010)
                val itemPointer = PointerByReference()
                COMUtils.checkRC(TrashShell.instance.SHCreateItemFromParsingName(WString(path.toAbsolutePath().toString()), null,
                    Guid.IID("{43826D1E-E718-42EE-BC55-A1E261C37BFE}"), itemPointer))
                val item = Unknown(itemPointer.value)
                try {
                    operation.call(18, item.pointer, null)
                    val performed = operation.result(21)
                    val aborted = IntByReference()
                    operation.call(22, aborted)
                    return windowsTrashOperationCompleted(performed.toInt(), aborted.value != 0)
                } finally { item.Release() }
            } finally { operation.Release() }
        } finally { Ole32.INSTANCE.CoUninitialize() }
    }

    override fun close() { dispatcher.close() }
}

private class RecycleOperation(pointer: Pointer) : Unknown(pointer) {
    fun result(index: Int, vararg arguments: Any?): HRESULT =
        _invokeNativeObject(index, arrayOf(pointer, *arguments), HRESULT::class.java) as HRESULT
    fun call(index: Int, vararg arguments: Any?) {
        COMUtils.checkRC(result(index, *arguments))
    }
}

/** A native cancellation stops the remaining batch; coroutine cancellation must never be converted to a result. */
internal suspend fun collectFileTrashResults(paths: List<Path>, recycle: (Path) -> Boolean): List<FileTrashResult> {
    val results = ArrayList<FileTrashResult>(paths.size)
    for ((index, path) in paths.withIndex()) {
        currentCoroutineContext().ensureActive()
        val result = try {
            FileTrashResult(path, cancelled = !recycle(path))
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (failure: Exception) {
            if ((failure as? COMException)?.hresult?.toInt()?.let(::isWindowsTrashCancellation) == true) {
                FileTrashResult(path, cancelled = true)
            } else FileTrashResult(path, failure.message?.takeIf(String::isNotBlank) ?: "无法移入系统回收站（${failure.javaClass.simpleName}）")
        }
        currentCoroutineContext().ensureActive()
        results += result
        if (result.cancelled) {
            paths.drop(index + 1).forEach { results += FileTrashResult(it, cancelled = true) }
            break
        }
    }
    return results
}

internal fun windowsTrashOperationCompleted(result: Int, aborted: Boolean): Boolean {
    if (isWindowsTrashCancellation(result)) return false
    if (result < 0) throw COMException("无法移入系统回收站（HRESULT ${result.toUInt().toString(16)}）", HRESULT(result))
    return !aborted
}

private fun isWindowsTrashCancellation(result: Int): Boolean =
    result == (0x80070000.toInt() or 1223) || result == (0x80070000.toInt() or 995) || result == 0x80270000.toInt()

private interface TrashShell : StdCallLibrary {
    fun SHCreateItemFromParsingName(path: WString, binding: Pointer?, iid: Guid.IID, item: PointerByReference): HRESULT
    companion object { val instance: TrashShell by lazy { Native.load("shell32", TrashShell::class.java, W32APIOptions.UNICODE_OPTIONS) } }
}

internal fun distinctMaterialTargets(paths: List<Path>): List<Path> = paths.map { it.toAbsolutePath().normalize() }.distinct().let { selected ->
    selected.filter { item -> selected.none { parent -> parent != item && item.startsWith(parent) } }
}
