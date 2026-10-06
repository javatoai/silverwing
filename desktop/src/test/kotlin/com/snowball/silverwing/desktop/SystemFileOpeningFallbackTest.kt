package com.snowball.silverwing.desktop

import com.sun.jna.WString
import com.sun.jna.ptr.IntByReference
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.cancelAndJoin
import org.junit.jupiter.api.io.TempDir
import java.io.InputStream
import java.io.OutputStream
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class SystemFileOpeningFallbackTest {
    @TempDir lateinit var root: Path
    private class TrackedInput : InputStream() {
        var closed = false
        override fun read(): Int = error("Discarded launcher output must never be read")
        override fun close() { closed = true }
    }
    private class TrackedOutput : OutputStream() {
        var closed = false
        override fun write(value: Int) = Unit
        override fun close() { closed = true }
    }
    private class FakeProcess(val complete: Boolean = true, val code: Int = 0, val blocking: Boolean = false) : Process() {
        val output = TrackedOutput()
        val input = TrackedInput()
        val error = TrackedInput()
        val entered = CountDownLatch(1)
        var alive = !complete || blocking
        var destroyed = false
        var timeoutMillis = 0L
        override fun getOutputStream(): OutputStream = output
        override fun getInputStream(): InputStream = input
        override fun getErrorStream(): InputStream = error
        override fun isAlive(): Boolean = alive
        override fun waitFor(): Int = kotlin.error("An unbounded wait is forbidden")
        override fun waitFor(timeout: Long, unit: TimeUnit): Boolean {
            timeoutMillis = unit.toMillis(timeout)
            entered.countDown()
            if (blocking) CountDownLatch(1).await()
            return complete
        }
        override fun exitValue(): Int = code
        override fun destroy() { destroyed = true; alive = false }
        override fun destroyForcibly(): Process { destroy(); return this }
    }

    @Test fun `platform launcher keeps a complete Unicode path as one argument and discards both output pipes`() {
        val path = root.resolve("中文 空格 & ' (1) #.docx")
        assertEquals(listOf("open", path.toAbsolutePath().toString()), platformFileOpenCommand("Mac OS X", path))
        val command = platformFileOpenCommand("Linux", path)
        assertEquals(listOf("xdg-open", path.toAbsolutePath().toString()), command)
        val process = FakeProcess()
        runFileOpenProcess(command, startProcess = { builder ->
            assertEquals(command, builder.command())
            assertEquals(ProcessBuilder.Redirect.DISCARD, builder.redirectOutput())
            assertEquals(ProcessBuilder.Redirect.DISCARD, builder.redirectError())
            process
        })
        assertEquals(15_000L, process.timeoutMillis)
        assertTrue(process.output.closed && process.input.closed && process.error.closed)
    }

    @Test fun `launcher timeout and failure remain failures and release every process resource`() {
        for (process in listOf(FakeProcess(complete = false), FakeProcess(code = 1))) {
            assertFailsWith<IllegalStateException> { runFileOpenProcess(listOf("fake-open"), 25) { process } }
            assertEquals(25L, process.timeoutMillis)
            assertTrue(process.output.closed && process.input.closed && process.error.closed)
            if (!process.complete) assertTrue(process.destroyed)
        }
    }

    @Test fun `cancelling an interruptible launcher wait destroys only the launcher and closes its streams`() = runBlocking {
        val process = FakeProcess(blocking = true)
        val pending = launch(Dispatchers.Default) { runInterruptible { runFileOpenProcess(listOf("fake-open")) { process } } }
        assertTrue(process.entered.await(5, TimeUnit.SECONDS))
        pending.cancelAndJoin()
        assertTrue(process.destroyed)
        assertTrue(process.output.closed && process.input.closed && process.error.closed)
    }

    @Test fun `association lookup retries bounded UTF-16 buffers after the default app grows between queries`() {
        var calls = 0
        val name = "WPS 中文应用"
        val api = object : AssociationApi {
            override fun AssocQueryStringW(flags: Int, kind: Int, association: WString, extra: WString?, output: CharArray?, length: IntByReference): Int {
                calls++
                assertEquals(0x120, flags)
                assertEquals(".docx", association.toString())
                if (output == null) { length.value = 3; return 1 }
                if (output.size < name.length + 1) { length.value = name.length + 1; return 0x80004003.toInt() }
                name.toCharArray().copyInto(output)
                output[name.length] = '\u0000'
                length.value = name.length + 1
                return 0
            }
        }
        assertEquals(name, queryAssociation(".docx", 4, api).value)
        assertEquals(3, calls)
    }

    @Test fun `association lookup refuses oversized buffers before allocation`() {
        var calls = 0
        val api = object : AssociationApi {
            override fun AssocQueryStringW(flags: Int, kind: Int, association: WString, extra: WString?, output: CharArray?, length: IntByReference): Int {
                calls++
                length.value = Int.MAX_VALUE
                return 1
            }
        }
        assertEquals(null, queryAssociation(".txt", 4, api).value)
        assertEquals(1, calls)
    }
}
