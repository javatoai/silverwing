package com.snowball.silverwing.desktop

import com.sun.jna.platform.win32.COM.COMException
import com.sun.jna.platform.win32.WinNT.HRESULT
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class SystemFileTrashTest {
    @TempDir lateinit var root: Path

    @Test fun `batch retains every path and distinguishes success failure and native cancellation`() = runBlocking {
        val paths = listOf("资料 中文 & #.txt", "失败.docx", "取消.pdf", "不应调用.xlsx").map(root::resolve)
        val calls = mutableListOf<Path>()
        val result = collectFileTrashResults(paths) { path ->
            calls.add(path)
            when (path) {
                paths[1] -> throw IllegalStateException()
                paths[2] -> false
                else -> true
            }
        }
        assertEquals(paths, result.map { it.path })
        assertEquals(paths.take(3), calls)
        assertEquals(FileTrashResult(paths[0]), result[0])
        assertNotNull(result[1].error, "An exception without a message must remain a failure")
        assertFalse(result[1].cancelled)
        assertTrue(result.drop(2).all { it.cancelled && it.error == null })
    }

    @Test fun `coroutine cancellation is propagated and never starts remaining recycle operations`() = runBlocking {
        val calls = mutableListOf<Path>()
        val paths = listOf(root.resolve("first.txt"), root.resolve("second.txt"))
        assertFailsWith<CancellationException> {
            collectFileTrashResults(paths) { path -> calls.add(path); throw CancellationException("cancelled") }
        }
        assertEquals(listOf(paths.first()), calls)
    }

    @Test fun `Windows user cancelled HRESULTs do not become errors while storage failures remain failures`() {
        assertTrue(windowsTrashOperationCompleted(0, false))
        assertFalse(windowsTrashOperationCompleted(0, true))
        assertFalse(windowsTrashOperationCompleted(0x800704C7.toInt(), false))
        assertFalse(windowsTrashOperationCompleted(0x80270000.toInt(), true))
        assertFailsWith<COMException> { windowsTrashOperationCompleted(0x80270036.toInt(), true) }
        assertFailsWith<COMException> { windowsTrashOperationCompleted(0x80070005.toInt(), false) }
    }

    @Test fun `cancellation from an earlier native shell call stops the batch without an error`() = runBlocking {
        val paths = listOf(root.resolve("取消.txt"), root.resolve("保留.txt"))
        var calls = 0
        val results = collectFileTrashResults(paths) {
            calls++
            throw COMException("user cancelled", HRESULT(0x80270000.toInt()))
        }
        assertEquals(1, calls)
        assertTrue(results.all { it.cancelled && it.error == null })
    }
}
