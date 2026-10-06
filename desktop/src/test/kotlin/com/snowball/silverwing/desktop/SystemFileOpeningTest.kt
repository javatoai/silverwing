package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.DesktopIntegration
import com.snowball.silverwing.core.CommandRunner
import com.snowball.silverwing.core.CommandResult
import com.sun.jna.Native
import com.sun.jna.WString
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.launch
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.CancellationException
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import kotlin.test.*

class SystemFileOpeningTest {
    @TempDir lateinit var root: Path

    @Test fun `submitted cancelled and failed results report once and allow retry`() = runBlocking {
        val fake = object : SystemFileOpening {
            override val chooserAvailable = true
            var result: FileOpenResult = FileOpenResult.Submitted
            val calls = mutableListOf<Pair<Path, Boolean>>()
            override suspend fun defaultApplication(path: Path) = DefaultFileApplication("WPS")
            override suspend fun open(path: Path): FileOpenResult { calls += path to false; return result }
            override suspend fun chooseApplication(path: Path): FileOpenResult { calls += path to true; return result }
        }
        val statuses = mutableListOf<String>()
        val errors = mutableListOf<Throwable>()
        val actions = DesktopActions(DesktopIntegration(), { AppConfig() }, {}, statuses::add, errors::add, fake)
        val path = root.resolve("中文 空格 & ' (1).docx")
        assertEquals("WPS", actions.defaultFileApplication(path).displayName)
        actions.openFile(path)
        fake.result = FileOpenResult.Cancelled
        actions.openFile(path, true)
        assertEquals(1, statuses.size)
        assertTrue(errors.isEmpty())
        fake.result = FileOpenResult.Failed(IllegalStateException("launch failure"))
        actions.openFile(path)
        assertEquals(1, errors.size)
        fake.result = FileOpenResult.Submitted
        actions.openFile(path)
        assertEquals(2, statuses.size)
        assertEquals(listOf(path to false, path to true, path to false, path to false), fake.calls)
    }

    @Test fun `missing file fails before handing path to the native shell`() = runBlocking {
        PlatformSystemFileOpening().use { opening ->
            assertIs<FileOpenResult.Failed>(opening.open(root.resolve("missing.docx")))
            assertIs<FileOpenResult.Failed>(opening.chooseApplication(root.resolve("missing.xlsx")))
        }
        Unit
    }

    @Test fun `Windows default app lookup uses current associations without launching apps`() = runBlocking {
        assumeTrue(System.getProperty("os.name").startsWith("Windows", true))
        PlatformSystemFileOpening().use { opening ->
            assertTrue(opening.chooserAvailable)
            // 不硬编码用户必须安装 WPS；只确认 UTF-16 查询和 STA 入口可以实际执行。
            for (extension in listOf("docx", "xlsx", "pdf")) {
                val app = opening.defaultApplication(root.resolve("关联检测.$extension"))
                println("Default application .$extension: ${app.displayName ?: "unknown"}; missing=${app.associationMissing}")
                assertFalse(app.displayName?.contains('\u0000') == true)
            }
            assertTrue(opening.defaultApplication(root.resolve("无扩展名")).associationMissing)
            val unknown = opening.defaultApplication(root.resolve("未知.silverwing_preview_unregistered_1234"))
            println("Unknown extension association: $unknown")
            val size = com.sun.jna.ptr.IntByReference()
            val result = AssociationApi.instance.AssocQueryStringW(0, 20, WString(".silverwing_preview_unregistered_1234"), null, null, size)
            val buffer = CharArray(size.value.coerceIn(1, 32768))
            AssociationApi.instance.AssocQueryStringW(0, 20, WString(".silverwing_preview_unregistered_1234"), null, buffer, size)
            println("Unknown association query: HRESULT=${result.toUInt().toString(16)}; ProgID=${buffer.takeWhile { it != '\u0000' }.joinToString("")}")
            assertTrue(unknown.associationMissing)
        }
    }

    @Test fun `Unicode shell structure preserves Chinese spaces and shell metacharacters`() {
        val path = "C:\\资料\\中文 空格 & ' (1).docx"
        val info = ShellExecutionInfo().apply { lpVerb = WString("open"); lpFile = WString(path) }
        info.write()
        assertEquals(if (Native.POINTER_SIZE == 8) 112 else 60, info.cbSize)
        assertEquals(path, info.pointer.getPointer(8L + 2 * Native.POINTER_SIZE).getWideString(0))
    }

    @Test fun `opening remains guarded if another file pane submits while native chooser is pending`() = runBlocking {
        val gate = CompletableDeferred<FileOpenResult>()
        val fake = object : SystemFileOpening {
            override val chooserAvailable = true
            var calls = 0
            override suspend fun defaultApplication(path: Path) = DefaultFileApplication()
            override suspend fun open(path: Path): FileOpenResult { calls++; return gate.await() }
            override suspend fun chooseApplication(path: Path): FileOpenResult { calls++; return gate.await() }
        }
        val actions = DesktopActions(DesktopIntegration(), { AppConfig() }, {}, {}, {}, fake)
        val pending = launch(start = CoroutineStart.UNDISPATCHED) { actions.openFile(root.resolve("first.docx"), true) }
        assertTrue(actions.fileOpenBusy)
        assertEquals(FileOpenResult.Cancelled, actions.openFile(root.resolve("second.xlsx")))
        assertEquals(1, fake.calls)
        gate.complete(FileOpenResult.Cancelled)
        pending.join()
        assertFalse(actions.fileOpenBusy)
    }

    @Test fun `cancelling a pending open releases the busy guard without error or submitted status`() = runBlocking {
        val gate = CompletableDeferred<FileOpenResult>()
        var wait = true
        val opening = object : SystemFileOpening {
            override val chooserAvailable = true
            override suspend fun defaultApplication(path: Path) = DefaultFileApplication()
            override suspend fun open(path: Path) = if (wait) {
                try { gate.await() } catch (_: CancellationException) { FileOpenResult.Submitted }
            } else FileOpenResult.Submitted
            override suspend fun chooseApplication(path: Path) = open(path)
        }
        val errors = mutableListOf<Throwable>()
        val statuses = mutableListOf<String>()
        val actions = DesktopActions(DesktopIntegration(), { AppConfig() }, {}, statuses::add, errors::add, opening)
        val pending = launch(start = CoroutineStart.UNDISPATCHED) { actions.openFile(root.resolve("中文 取消.txt")) }
        assertTrue(actions.fileOpenBusy)
        pending.cancelAndJoin()
        assertFalse(actions.fileOpenBusy)
        assertTrue(errors.isEmpty())
        assertTrue(statuses.isEmpty())
        wait = false
        assertEquals(FileOpenResult.Submitted, actions.openFile(root.resolve("重试.txt")))
        assertEquals(1, statuses.size)
    }

    @Test fun `an opening adapter that throws reports once and restores the guard for retry`() = runBlocking {
        var failing = true
        val opening = object : SystemFileOpening {
            override val chooserAvailable = true
            override suspend fun defaultApplication(path: Path) = DefaultFileApplication()
            override suspend fun open(path: Path): FileOpenResult {
                if (failing) throw IllegalStateException("native open failed")
                return FileOpenResult.Submitted
            }
            override suspend fun chooseApplication(path: Path) = open(path)
        }
        val errors = mutableListOf<Throwable>()
        val actions = DesktopActions(DesktopIntegration(), { AppConfig() }, {}, {}, errors::add, opening)
        assertIs<FileOpenResult.Failed>(actions.openFile(root.resolve("失败.txt")))
        assertEquals(1, errors.size)
        assertFalse(actions.fileOpenBusy)
        failing = false
        assertEquals(FileOpenResult.Submitted, actions.openFile(root.resolve("重试.txt")))
        assertEquals(1, errors.size)
    }

    @Test fun `Windows reveal passes the complete file path as one explorer argument`() = runBlocking {
        assumeTrue(System.getProperty("os.name").startsWith("Windows", true))
        val calls = mutableListOf<List<String>>()
        val runner = object : CommandRunner {
            override fun run(command: List<String>, workingDirectory: Path?, timeout: Duration, environment: Map<String, String>): CommandResult {
                calls += command
                return CommandResult(0, "", "")
            }
        }
        val opening = object : SystemFileOpening {
            override val chooserAvailable = false
            override suspend fun defaultApplication(path: Path) = DefaultFileApplication()
            override suspend fun open(path: Path) = FileOpenResult.Submitted
            override suspend fun chooseApplication(path: Path) = FileOpenResult.Cancelled
        }
        val actions = DesktopActions(DesktopIntegration(runner), { AppConfig() }, {}, {}, { throw it }, opening)
        val path = root.resolve("中文 空格 & ' (1).docx")
        actions.revealFile(path)
        assertEquals(listOf(listOf("explorer.exe", "/select,${path.toAbsolutePath()}")), calls)
    }
}
