package com.snowball.silverwing.desktop

import com.sun.jna.Callback
import com.sun.jna.CallbackReference
import com.sun.jna.Function
import com.sun.jna.Memory
import com.sun.jna.NativeLibrary
import com.sun.jna.Pointer
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

internal data class MacNativePanelConfiguration(
    val canChooseFiles: Boolean,
    val canChooseDirectories: Boolean,
    val allowsMultipleSelection: Boolean,
)

/** macOS NSOpenPanel configured for native multi-folder selection. */
internal object MacMultiDirectoryPicker {
    suspend fun pick(initialPath: String?): List<String>? = withContext(Dispatchers.IO) {
        try {
            MacObjectiveC.runOnMainThread {
                MacObjectiveC.pickDirectories(initialPath)
            }
        } catch (error: Throwable) {
            if (error is CancellationException) throw error
            throw IllegalStateException("打开 macOS 多目录选择器失败", error)
        }
    }

    /** Creates and configures the real AppKit panel without showing it, for macOS CI smoke tests. */
    fun inspectNativePanelConfiguration(): MacNativePanelConfiguration? {
        if (!isMacOs(System.getProperty("os.name"))) return null
        return MacObjectiveC.runOnMainThread { MacObjectiveC.inspectPanelConfiguration() }
    }
}

/**
 * Small Objective-C runtime bridge kept local to the desktop module. The bridge is loaded lazily so
 * Windows and Linux never attempt to load macOS libraries.
 */
private object MacObjectiveC {
    private val objc by lazy { NativeLibrary.getInstance("objc") }
    private val foundation by lazy {
        NativeLibrary.getInstance("/System/Library/Frameworks/Foundation.framework/Foundation")
    }
    private val appKit by lazy {
        NativeLibrary.getInstance("/System/Library/Frameworks/AppKit.framework/AppKit")
    }
    private val dispatch by lazy {
        runCatching { NativeLibrary.getInstance("/usr/lib/system/libdispatch.dylib") }
            .getOrElse { NativeLibrary.getInstance("libSystem.B.dylib") }
    }
    private val objcGetClass by lazy { objc.getFunction("objc_getClass") }
    private val selRegisterName by lazy { objc.getFunction("sel_registerName") }
    private val objcMsgSend by lazy { objc.getFunction("objc_msgSend") }
    private val dispatchMainQueue by lazy {
        runCatching { dispatch.getGlobalVariableAddress("dispatch_main_q") }
            .getOrElse { dispatch.getGlobalVariableAddress("_dispatch_main_q") }
    }
    private val dispatchSyncF by lazy { dispatch.getFunction("dispatch_sync_f") }

    fun pickDirectories(initialPath: String?): List<String>? {
        loadMacFrameworks()
        val pool = sendPointer(classPointer("NSAutoreleasePool"), selector("new"))
            ?: error("无法创建 macOS 自动释放池")
        return try {
            val panel = sendPointer(classPointer("NSOpenPanel"), selector("openPanel"))
                ?: error("无法创建 macOS 文件选择器")
            configurePanel(panel)
            sendString(panel, selector("setMessage:"), "选择一个或多个 Git 仓库目录")

            initialPath
                ?.trim()
                ?.takeIf { it.isNotEmpty() }
                ?.let(::File)
                ?.takeIf(File::isDirectory)
                ?.let { directory ->
                    val url = fileUrl(directory)
                    if (url != null) sendVoid(panel, selector("setDirectoryURL:"), url)
                }

            val response = sendLong(panel, selector("runModal"))
            if (response != NS_MODAL_RESPONSE_OK) null else selectedPaths(panel)
        } finally {
            sendVoid(pool, selector("drain"))
        }
    }

    fun inspectPanelConfiguration(): MacNativePanelConfiguration {
        loadMacFrameworks()
        val pool = sendPointer(classPointer("NSAutoreleasePool"), selector("new"))
            ?: error("无法创建 macOS 自动释放池")
        return try {
            val panel = sendPointer(classPointer("NSOpenPanel"), selector("openPanel"))
                ?: error("无法创建 macOS 文件选择器")
            configurePanel(panel)
            MacNativePanelConfiguration(
                canChooseFiles = sendInt(panel, selector("canChooseFiles")) != 0,
                canChooseDirectories = sendInt(panel, selector("canChooseDirectories")) != 0,
                allowsMultipleSelection = sendInt(panel, selector("allowsMultipleSelection")) != 0,
            )
        } finally {
            sendVoid(pool, selector("drain"))
        }
    }

    fun <T> runOnMainThread(block: () -> T): T {
        loadMacFrameworks()
        if (isMainThread()) return block()

        var outcome: Result<T>? = null
        val callback = object : DispatchFunction {
            override fun invoke(context: Pointer?) {
                outcome = runCatching(block)
            }
        }
        dispatchSyncF.invokeVoid(arrayOf(mainQueue(), null, CallbackReference.getFunctionPointer(callback)))
        return checkNotNull(outcome) { "macOS 主线程选择器未返回结果" }.getOrThrow()
    }

    private fun selectedPaths(panel: Pointer): List<String> {
        val urls = sendPointer(panel, selector("URLs")) ?: return emptyList()
        val count = sendLong(urls, selector("count")).coerceAtLeast(0L).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
        return (0 until count).mapNotNull { index ->
            val url = sendPointer(urls, selector("objectAtIndex:"), index.toLong()) ?: return@mapNotNull null
            val path = sendPointer(url, selector("path")) ?: return@mapNotNull null
            val utf8 = sendPointer(path, selector("UTF8String")) ?: return@mapNotNull null
            utf8.getString(0, "UTF-8")
        }
    }

    private fun configurePanel(panel: Pointer) {
        sendVoid(panel, selector("setCanChooseFiles:"), 0)
        sendVoid(panel, selector("setCanChooseDirectories:"), 1)
        sendVoid(panel, selector("setAllowsMultipleSelection:"), 1)
    }

    private fun fileUrl(directory: File): Pointer? {
        val path = nsString(directory.absolutePath) ?: return null
        return sendPointer(classPointer("NSURL"), selector("fileURLWithPath:"), path)
    }

    private fun nsString(value: String): Pointer? {
        val utf8 = cString(value)
        return sendPointer(classPointer("NSString"), selector("stringWithUTF8String:"), utf8)
    }

    private fun isMainThread(): Boolean =
        sendInt(classPointer("NSThread"), selector("isMainThread")) != 0

    private fun mainQueue(): Pointer =
        dispatchMainQueue

    private fun classPointer(name: String): Pointer = invokePointer(
        objcGetClass,
        arrayOf(cString(name)),
    ) ?: error("找不到 macOS 类：$name")

    private fun selector(name: String): Pointer = invokePointer(
        selRegisterName,
        arrayOf(cString(name)),
    ) ?: error("找不到 macOS 选择器：$name")

    private fun sendPointer(receiver: Pointer, selector: Pointer, vararg args: Any?): Pointer? =
        invokePointer(objcMsgSend, arrayOf(receiver, selector, *args))

    private fun sendVoid(receiver: Pointer, selector: Pointer, vararg args: Any?) {
        objcMsgSend.invokeVoid(arrayOf(receiver, selector, *args))
    }

    private fun sendInt(receiver: Pointer, selector: Pointer, vararg args: Any?): Int =
        objcMsgSend.invokeInt(arrayOf(receiver, selector, *args))

    private fun sendLong(receiver: Pointer, selector: Pointer, vararg args: Any?): Long =
        objcMsgSend.invokeLong(arrayOf(receiver, selector, *args))

    private fun sendString(receiver: Pointer, selector: Pointer, value: String) {
        nsString(value)?.let { sendVoid(receiver, selector, it) }
    }

    private fun loadMacFrameworks() {
        foundation
        appKit
    }

    private fun invokePointer(function: Function, args: Array<Any?>): Pointer? =
        function.invokePointer(args)

    private fun cString(value: String): Memory = Memory(value.toByteArray(Charsets.UTF_8).size.toLong() + 1).also {
        it.setString(0, value, "UTF-8")
    }

    private interface DispatchFunction : Callback {
        fun invoke(context: Pointer?)
    }

    private const val NS_MODAL_RESPONSE_OK = 1L
}
