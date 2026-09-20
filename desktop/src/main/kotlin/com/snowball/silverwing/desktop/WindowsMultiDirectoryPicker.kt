package com.snowball.silverwing.desktop

import com.sun.jna.Native
import com.sun.jna.Pointer
import com.sun.jna.WString
import com.sun.jna.platform.win32.COM.COMUtils
import com.sun.jna.platform.win32.COM.Unknown
import com.sun.jna.platform.win32.Guid
import com.sun.jna.platform.win32.Ole32
import com.sun.jna.platform.win32.WTypes
import com.sun.jna.platform.win32.Win32Exception
import com.sun.jna.platform.win32.WinError
import com.sun.jna.platform.win32.WinNT
import com.sun.jna.ptr.IntByReference
import com.sun.jna.ptr.PointerByReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/** Windows Shell IFileOpenDialog configured for native multi-folder selection. */
internal object WindowsMultiDirectoryPicker {
    private const val FOS_PICKFOLDERS = 0x20
    private const val FOS_FORCEFILESYSTEM = 0x40
    private const val FOS_ALLOWMULTISELECT = 0x200
    private const val SIGDN_FILESYSPATH = 0x80058000L
    private val clsid = Guid.CLSID("{DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7}")
    private val iid = Guid.IID("{d57c7288-d4ad-4768-be02-9d969532d960}")
    private val shellItemIid = Guid.IID("{43826d1e-e718-42ee-bc55-a1e261c37bfe}")

    suspend fun pick(initialPath: String?): List<String>? = withContext(Dispatchers.IO) {
        var dialog: FileOpenDialog? = null
        var comInitialized = false
        try {
            val initialized = Ole32.INSTANCE.CoInitializeEx(
                null,
                Ole32.COINIT_APARTMENTTHREADED or Ole32.COINIT_DISABLE_OLE1DDE,
            )
            verify(initialized, "初始化 Windows 文件选择器失败")
            comInitialized = true

            val dialogPointer = PointerByReference()
            verify(
                Ole32.INSTANCE.CoCreateInstance(
                    clsid,
                    null,
                    WTypes.CLSCTX_ALL,
                    iid,
                    dialogPointer,
                ),
                "创建 Windows 文件选择器失败",
            )
            dialog = FileOpenDialog(dialogPointer.value)
            val options = IntByReference()
            verify(dialog.getOptions(options), "读取文件选择器选项失败")
            verify(
                dialog.setOptions(options.value or FOS_PICKFOLDERS or FOS_FORCEFILESYSTEM or FOS_ALLOWMULTISELECT),
                "配置多目录选择失败",
            )
            verify(dialog.setTitle(WString("选择一个或多个 Git 仓库目录")), "设置文件选择器标题失败")
            initialPath?.trim()?.takeIf(String::isNotEmpty)?.let { dialog.setInitialFolder(it) }

            val shown = dialog.show()
            if (shown == Win32Exception(WinError.ERROR_CANCELLED).hr) return@withContext null
            verify(shown, "打开多目录选择器失败")
            dialog.results()
        } finally {
            dialog?.Release()
            if (comInitialized) Ole32.INSTANCE.CoUninitialize()
        }
    }

    private fun FileOpenDialog.setInitialFolder(path: String) {
        if (!File(path).isDirectory) return
        val pointer = PointerByReference()
        val result = Shell32Ex.INSTANCE.SHCreateItemFromParsingName(
            WString(path),
            null,
            Guid.REFIID(shellItemIid),
            pointer,
        )
        if (COMUtils.FAILED(result)) return
        val item = ShellItem(pointer.value)
        try {
            verify(setFolder(item.pointerValue), "设置初始目录失败")
        } finally {
            item.Release()
        }
    }

    private fun FileOpenDialog.results(): List<String> {
        val arrayPointer = PointerByReference()
        verify(getResults(arrayPointer), "读取所选目录失败")
        val items = ShellItemArray(arrayPointer.value)
        try {
            val count = IntByReference()
            verify(items.getCount(count), "读取所选目录数量失败")
            return (0 until count.value).map { index ->
                val itemPointer = PointerByReference()
                verify(items.getItemAt(index, itemPointer), "读取所选目录失败")
                val item = ShellItem(itemPointer.value)
                try {
                    val namePointer = PointerByReference()
                    verify(item.getDisplayName(SIGDN_FILESYSPATH, namePointer), "读取目录路径失败")
                    try {
                        namePointer.value.getWideString(0)
                    } finally {
                        Ole32.INSTANCE.CoTaskMemFree(namePointer.value)
                    }
                } finally {
                    item.Release()
                }
            }
        } finally {
            items.Release()
        }
    }

    private fun verify(result: WinNT.HRESULT, message: String): WinNT.HRESULT {
        if (COMUtils.FAILED(result)) throw IllegalStateException(message, Win32Exception(result))
        return result
    }

    private class FileOpenDialog(pointer: Pointer?) : Unknown(pointer) {
        fun show(): WinNT.HRESULT = invoke(3, arrayOf(pointerValue, null))
        fun setOptions(options: Int): WinNT.HRESULT = invoke(9, arrayOf(pointerValue, options))
        fun getOptions(options: IntByReference): WinNT.HRESULT = invoke(10, arrayOf(pointerValue, options))
        fun setFolder(folder: Pointer?): WinNT.HRESULT = invoke(12, arrayOf(pointerValue, folder))
        fun setTitle(title: WString): WinNT.HRESULT = invoke(17, arrayOf(pointerValue, title))
        fun getResults(result: PointerByReference): WinNT.HRESULT = invoke(27, arrayOf(pointerValue, result))
        val pointerValue: Pointer? get() = pointer
        private fun invoke(index: Int, args: Array<Any?>): WinNT.HRESULT =
            _invokeNativeObject(index, args, WinNT.HRESULT::class.java) as WinNT.HRESULT
    }

    private class ShellItemArray(pointer: Pointer?) : Unknown(pointer) {
        fun getCount(count: IntByReference): WinNT.HRESULT = invoke(7, arrayOf(pointerValue, count))
        fun getItemAt(index: Int, item: PointerByReference): WinNT.HRESULT = invoke(8, arrayOf(pointerValue, index, item))
        private val pointerValue: Pointer? get() = pointer
        private fun invoke(index: Int, args: Array<Any?>): WinNT.HRESULT =
            _invokeNativeObject(index, args, WinNT.HRESULT::class.java) as WinNT.HRESULT
    }

    private class ShellItem(pointer: Pointer?) : Unknown(pointer) {
        fun getDisplayName(kind: Long, result: PointerByReference): WinNT.HRESULT =
            _invokeNativeObject(5, arrayOf(pointer, kind, result), WinNT.HRESULT::class.java) as WinNT.HRESULT
        val pointerValue: Pointer? get() = pointer
    }

    private interface Shell32Ex : com.sun.jna.win32.StdCallLibrary {
        fun SHCreateItemFromParsingName(
            path: WString,
            bindingContext: Pointer?,
            iid: Guid.REFIID,
            item: PointerByReference,
        ): WinNT.HRESULT

        companion object {
            val INSTANCE: Shell32Ex = Native.load("shell32", Shell32Ex::class.java)
        }
    }
}
