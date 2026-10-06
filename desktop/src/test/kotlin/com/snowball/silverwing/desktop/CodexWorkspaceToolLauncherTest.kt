package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.TaskWorkspaceContext
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.net.URI
import java.net.URLDecoder
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class CodexWorkspaceToolLauncherTest {
    @TempDir lateinit var temporary: Path

    @Test fun `opens a bound thread and rejects arbitrary URI content`() {
        val opened = mutableListOf<URI>()
        val launcher = CodexWorkspaceToolLauncher(ExternalUriOpener { opened += it })
        val id = "01a0fd65-1c49-7952-9725-369dfb7527b1"
        launcher.openThread(id)
        assertEquals("codex://threads/$id", opened.single().toString())
        kotlin.test.assertFailsWith<IllegalArgumentException> { launcher.openThread("new?path=other") }
        assertEquals(1, opened.size)
    }

    @Test
    fun `opens encoded task directory through the current Codex new-thread deep link`() {
        val opened = mutableListOf<URI>()
        val launcher = CodexWorkspaceToolLauncher(ExternalUriOpener { opened += it })
        val taskDirectory = Files.createDirectories(temporary.resolve("研发任务 PAY 1024+& #"))

        launcher.open(
            TaskWorkspaceContext(
                taskName = "支付任务",
                taskDirectory = taskDirectory,
                agentsFile = taskDirectory.resolve("AGENTS.md"),
                workspaces = emptyList(),
            ),
        )

        val uri = opened.single()
        val encodedPath = uri.rawQuery.substringAfter("path=")
        assertEquals("codex", uri.scheme)
        assertEquals("threads", uri.host)
        assertEquals("/new", uri.path)
        assertEquals(
            taskDirectory.toAbsolutePath().normalize().toString(),
            URLDecoder.decode(encodedPath, StandardCharsets.UTF_8),
        )
        assertContains(encodedPath, "%20")
        assertContains(encodedPath, "%2B")
        assertContains(encodedPath, "%26")
        assertContains(encodedPath, "%23")
        assertFalse(encodedPath.contains("+"))
    }

    @Test
    fun `macOS dispatches the Codex URI through Launch Services instead of browser browse`() {
        val commands = mutableListOf<List<String>>()
        val uri = URI.create("codex://threads/new?path=%2FUsers%2Fme%2Ftasks%2FPAY%201024")

        SystemExternalUriOpener(
            osName = "Mac OS X",
            processLauncher = DetachedProcessLauncher(commands::add),
        ).open(uri)

        assertEquals(listOf("open", uri.toASCIIString()), commands.single())
    }

    @Test
    fun `Windows dispatches the Codex URI through the registered protocol handler`() {
        val commands = mutableListOf<List<String>>()
        val uri = URI.create("codex://threads/new?path=C%3A%5Ctasks%5CPAY%201024")

        SystemExternalUriOpener(
            osName = "Windows 11",
            processLauncher = DetachedProcessLauncher(commands::add),
        ).open(uri)

        assertEquals(
            listOf("rundll32", "url.dll,FileProtocolHandler", uri.toASCIIString()),
            commands.single(),
        )
    }
}
