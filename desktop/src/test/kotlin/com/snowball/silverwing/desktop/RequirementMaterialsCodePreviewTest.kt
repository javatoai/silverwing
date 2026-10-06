@file:OptIn(
    androidx.compose.ui.ExperimentalComposeUiApi::class,
    androidx.compose.ui.InternalComposeUiApi::class,
    kotlinx.coroutines.ExperimentalCoroutinesApi::class,
)

package com.snowball.silverwing.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.pointer.PointerButton
import androidx.compose.ui.input.pointer.PointerButtons
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.asAwtTransferable
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.use
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.DevelopmentToolAutoDetectionResult
import com.snowball.silverwing.core.DevelopmentToolStartupDetection
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.RequirementMaterialsStatus
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.WorkspaceFileLanguage
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.io.TempDir
import java.nio.charset.StandardCharsets.UTF_8
import java.nio.file.Files
import java.nio.file.Path
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.StringSelection
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class RequirementMaterialsCodePreviewTest {
    @TempDir lateinit var root: Path
    private val io = StandardTestDispatcher()

    @BeforeEach fun setMainDispatcher() { Dispatchers.setMain(io) }
    @AfterEach fun resetMainDispatcher() { Dispatchers.resetMain() }

    @Test
    fun `Python multiline strings retain hashes and do not swallow keywords after comments`() {
        val doubleQuoted = "\"\"\"说明\r\n# still a string\r\nreturn is text\"\"\""
        val singleQuoted = "'''second\n# still a string\nif is text'''"
        val escaped = "\"escaped \\\" # literal\""
        val source = "value = $doubleQuoted\r\nother = $singleQuoted\nlabel = $escaped\n# real comment\ndef greet():\n\treturn True\n"
        val tokens = workspaceDocumentSyntaxTokens(WorkspaceFileLanguage.PYTHON, source)

        assertToken(source, tokens, doubleQuoted, WorkspaceSyntaxTokenKind.STRING)
        assertToken(source, tokens, singleQuoted, WorkspaceSyntaxTokenKind.STRING)
        assertToken(source, tokens, escaped, WorkspaceSyntaxTokenKind.STRING)
        assertEquals(listOf("# real comment"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.COMMENT))
        assertEquals(listOf("def", "return", "True"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.KEYWORD))
        assertValidRanges(source, tokens)
    }

    @Test
    fun `Shell variables positional parameters and comments have independent ranges`() {
        val source = "#!/bin/sh\nif [ ${'$'}1 = ${'$'}{name} ]; then\n  echo ${'$'}HOME ${'$'}? ${'$'}@ ${'$'}# ${'$'}*\n  echo \"# literal\" # real comment\nfi\n"
        val tokens = workspaceDocumentSyntaxTokens(WorkspaceFileLanguage.SHELL, source)

        assertEquals(listOf("${'$'}1", "${'$'}{name}", "${'$'}HOME", "${'$'}?", "${'$'}@", "${'$'}#", "${'$'}*"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.PROPERTY_KEY))
        assertEquals(listOf("#!/bin/sh", "# real comment"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.COMMENT))
        assertToken(source, tokens, "\"# literal\"", WorkspaceSyntaxTokenKind.STRING)
        assertEquals(listOf("if", "then", "fi"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.KEYWORD))
        assertValidRanges(source, tokens)
    }

    @Test
    fun `Shell single quoted backslash and noncomment hashes keep subsequent keywords visible`() {
        val quotedBackslash = "'\\'"
        val source = "echo $quotedBackslash; if true; then echo ok; fi\necho foo#bar\necho \\#literal\necho foo # comment\nfor x in ${'$'}@; do echo ${'$'}x; done"
        val tokens = workspaceDocumentSyntaxTokens(WorkspaceFileLanguage.SHELL, source)

        assertToken(source, tokens, quotedBackslash, WorkspaceSyntaxTokenKind.STRING)
        assertEquals(listOf("# comment"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.COMMENT))
        assertEquals(listOf("if", "then", "fi", "for", "in", "do", "done"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.KEYWORD))
        assertValidRanges(source, tokens)
    }

    @Test
    fun `PowerShell block comments end before the following case insensitive keyword`() {
        val block = "<# comment\nif ${'$'}hidden # also comment\n#>"
        val source = "$block\nFUNCTION Invoke-Test {\n  ${'$'}value = \"escaped `\" # literal\"\n  # line comment\n  RETURN ${'$'}value\n}\n"
        val tokens = workspaceDocumentSyntaxTokens(WorkspaceFileLanguage.POWERSHELL, source)

        assertToken(source, tokens, block, WorkspaceSyntaxTokenKind.COMMENT)
        assertEquals(listOf(block, "# line comment"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.COMMENT))
        assertEquals(listOf("FUNCTION", "RETURN"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.KEYWORD))
        assertEquals(listOf("${'$'}value", "${'$'}value"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.PROPERTY_KEY))
        assertToken(source, tokens, "\"escaped `\" # literal\"", WorkspaceSyntaxTokenKind.STRING)
        assertValidRanges(source, tokens)
    }

    @Test
    fun `Batch comments stop at each line and environment and delayed variables are highlighted`() {
        val source = "@echo off\r\nREM %ignored% comment\r\n  :: !ignored! comment\r\nSET name=%USERNAME%\r\nECHO %1\r\nECHO !name!\r\nIF defined name EXIT 0\r\n"
        val tokens = workspaceDocumentSyntaxTokens(WorkspaceFileLanguage.BATCH, source)

        assertEquals(listOf("REM %ignored% comment\r", ":: !ignored! comment\r"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.COMMENT))
        assertEquals(listOf("%USERNAME%", "%1", "!name!"), tokenText(source, tokens, WorkspaceSyntaxTokenKind.PROPERTY_KEY))
        assertTrue(tokenText(source, tokens, WorkspaceSyntaxTokenKind.KEYWORD).containsAll(listOf("SET", "ECHO", "IF", "defined", "EXIT")))
        assertToken(source, tokens, "0", WorkspaceSyntaxTokenKind.NUMBER)
        assertValidRanges(source, tokens)
    }

    @Test
    fun `materials browser highlights Python and Shell preserves source and supports find in both themes and widths`() {
        val materials = Files.createDirectories(root.resolve("materials"))
        val python = "def greet():\r\n\treturn \"中文 # literal 🐍\"\r\n# comment\r\nmessage = \"${"long text ".repeat(30)}\"\r\n"
        val shell = "#!/bin/sh\nif [ ${'$'}1 = ${'$'}{name} ]; then\n  echo \"中文 # literal\" # comment\nfi\n"
        Files.writeString(materials.resolve("example.py"), python, UTF_8)
        Files.writeString(materials.resolve("example.sh"), shell, UTF_8)

        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            app().use { app ->
                val find = DocumentFindState()
                val state = MaterialsBrowserState().apply {
                    selectedPath.value = "example.py"
                    directoryCollapsed.value = true
                }
                ImageComposeScene(width, 640, coroutineContext = Dispatchers.Unconfined) {
                    SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                        Surface {
                            DocumentFindScope(find, Modifier.fillMaxSize()) {
                                RequirementMaterialsBrowser(app, task(materials), browserState = state, ioDispatcher = io)
                            }
                        }
                    }
                }.use { scene ->
                    scene.await { scene.textValue(python) != null && scene.labelOrNull("查看PY源码") != null }
                    val displayed = assertNotNull(scene.textValue(python))
                    assertContentEquals(python.toByteArray(UTF_8), displayed.text.toByteArray(UTF_8))
                    assertDistinctSyntaxColors(displayed, "def", "\"中文", "# comment")
                    assertEquals(python, find.blocks.values.single().text)
                    val layout = scene.layout(scene.textNode(python))
                    assertEquals(python.count { it == '\n' } + 1, layout.lineCount, "Code should retain source lines at width=$width")
                    assertTrue(layout.getLineRight(3) > width, "Long code must overflow horizontally rather than wrap")
                    scene.capture("python-${if (dark) "dark" else "light"}-$width.png")

                    scene.clickAt(Offset(width / 2f, 480f))
                    assertTrue(scene.sendKeyEvent(KeyEvent(Key.F, KeyEventType.KeyDown, isCtrlPressed = true)))
                    scene.await { find.open && scene.editable() != null }
                    assertTrue(scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("# literal")))
                    scene.await { find.hits.size == 1 }
                    assertEquals(python.indexOf("# literal"), find.current!!.range.first)
                    assertDistinctSyntaxColors(assertNotNull(scene.textValue(python)), "def", "\"中文", "# comment")
                    scene.click(scene.label("关闭查找 (Esc)"))
                    scene.await { !find.open }

                    scene.click(scene.label("查看PY源码"))
                    scene.await { scene.labelOrNull("查看PY预览") != null && scene.textValue(python)?.spanStyles?.isEmpty() == true }
                    assertEquals(python, find.blocks.values.single().text)
                    assertContentEquals(Files.readAllBytes(materials.resolve("example.py")), scene.textValue(python)!!.text.toByteArray(UTF_8))
                    scene.click(scene.label("查看PY预览"))
                    scene.await { scene.textValue(python)?.spanStyles?.isNotEmpty() == true }

                    state.selectedPath.value = "example.sh"
                    scene.await { scene.textValue(shell) != null && scene.labelOrNull("查看SH源码") != null }
                    val shellDisplayed = assertNotNull(scene.textValue(shell))
                    assertDistinctSyntaxColors(shellDisplayed, "if", "\"中文", "# comment")
                    assertEquals(shell, find.blocks.values.single().text)
                    assertContentEquals(Files.readAllBytes(materials.resolve("example.sh")), shellDisplayed.text.toByteArray(UTF_8))
                    scene.capture("shell-${if (dark) "dark" else "light"}-$width.png")
                    assertTrue(app.statusMessage.orEmpty().let { !it.contains("无法预览") && !it.contains("无法读取") })
                }
            }
        }
    }

    @Test
    fun `highlighted code supports real drag selection and keyboard copy without changing the system clipboard`() {
        val source = "return \"copy # 中文\""
        val clipboard = RecordingClipboard()
        val find = DocumentFindState()
        ImageComposeScene(600, 260, coroutineContext = Dispatchers.Unconfined) {
            SilverWingTheme(ThemePreference.LIGHT) {
                CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard) {
                    Surface {
                        DocumentFindScope(find, Modifier.fillMaxSize()) {
                            RequirementMaterialsTextPreview(source, "py", MarkdownPreviewMode.RENDERED, {}, Modifier.fillMaxSize())
                        }
                    }
                }
            }
        }.use { scene ->
            scene.await { scene.textValue(source) != null }
            val node = scene.textNode(source)
            val layout = scene.layout(node)
            val start = node.boundsInRoot.topLeft + layout.getBoundingBox(0).let { Offset(it.left + 1, it.center.y) }
            val end = node.boundsInRoot.topLeft + layout.getBoundingBox(source.lastIndex).let { Offset(it.right - 1, it.center.y) }
            scene.sendPointerEvent(PointerEventType.Move, start, timeMillis = 1)
            scene.sendPointerEvent(PointerEventType.Press, start, timeMillis = 2,
                buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
            scene.sendPointerEvent(PointerEventType.Move, end, timeMillis = 100, buttons = PointerButtons(isPrimaryPressed = true))
            scene.sendPointerEvent(PointerEventType.Release, end, timeMillis = 110, button = PointerButton.Primary)
            repeat(4) { scene.render(System.nanoTime()).close() }
            scene.sendKeyEvent(KeyEvent(Key.C, KeyEventType.KeyDown, isCtrlPressed = true))
            scene.await { clipboard.capturedText?.text == source }
            assertEquals(source, clipboard.capturedText!!.text)
            assertEquals(1, clipboard.modernWrites, "SelectionContainer must copy into the injected modern Clipboard")
            scene.capture("python-selection-copy.png")
        }
    }

    @Test
    fun `Markdown Python and Shell fences retain syntax colors find text and copy payload`() {
        val python = "def greet():\n    return \"中文 # literal\"\n# python comment"
        val shell = "if [ ${'$'}1 = ${'$'}{name} ]; then\n  echo \"中文 # literal\" # shell comment\nfi"
        val markdown = "# 脚本示例\n\n```python\n$python\n```\n\n```shell\n$shell\n```"
        for (dark in listOf(false, true)) for (width in listOf(360, 900)) {
            val find = DocumentFindState()
            val clipboard = RecordingClipboard()
            ImageComposeScene(width, 760, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    CompositionLocalProvider(LocalClipboardManager provides clipboard, LocalClipboard provides clipboard) {
                        Surface {
                            DocumentFindScope(find, Modifier.fillMaxSize()) {
                                MarkdownDocumentPreview(markdown, Modifier.fillMaxSize())
                            }
                        }
                    }
                }
            }.use { scene ->
                scene.await { scene.textValue(python) != null && scene.textValue(shell) != null }
                assertDistinctSyntaxColors(assertNotNull(scene.textValue(python)), "def", "\"中文", "# python")
                assertDistinctSyntaxColors(assertNotNull(scene.textValue(shell)), "if", "\"中文", "# shell")
                assertTrue(find.blocks.values.any { it.text == python })
                assertTrue(find.blocks.values.any { it.text == shell })
                assertTrue(find.blocks.values.none { it.text.contains("```") })
                val copy = scene.nodes().filter {
                    it.config.getOrNull(SemanticsActions.OnClick) != null &&
                        it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains("复制代码") == true
                }
                assertEquals(2, copy.size)
                scene.click(copy.first())
                scene.await { clipboard.capturedText?.text == python }
                assertContentEquals(python.toByteArray(UTF_8), clipboard.capturedText!!.text.toByteArray(UTF_8))
                scene.click(scene.label("查找正文 (Ctrl+F)"))
                scene.await { find.open && scene.editable() != null }
                scene.editable()!!.config[SemanticsActions.SetText].action!!.invoke(AnnotatedString("# literal"))
                scene.await { find.hits.size == 2 }
                assertDistinctSyntaxColors(assertNotNull(scene.textValue(python)), "def", "\"中文", "# python")
                val previous = find.current
                assertTrue(scene.sendKeyEvent(KeyEvent(Key.F3, KeyEventType.KeyDown)))
                scene.await { find.current != previous }
                scene.capture("markdown-fences-${if (dark) "dark" else "light"}-$width.png")
            }
        }
    }

    @Test
    fun `task detail no longer exposes work data or creates an ai data directory`() {
        val tasks = Files.createDirectories(root.resolve("tasks"))
        val directory = Files.createDirectories(tasks.resolve("语法预览"))
        val task = task(root).copy(requirementMaterials = RequirementMaterialsDirectory())
        for (dark in listOf(false, true)) app().use { app ->
            ImageComposeScene(900, 640, coroutineContext = Dispatchers.Unconfined) {
                SilverWingTheme(if (dark) ThemePreference.DARK else ThemePreference.LIGHT) {
                    Surface { TaskDetail(app, task, Modifier.fillMaxSize()) }
                }
            }.use { scene ->
                scene.await { scene.labelOrNull("更多操作") != null }
                assertFalse(scene.nodes().any { node ->
                    node.config.getOrNull(SemanticsProperties.Text)?.any { it.text.contains("工作数据") } == true ||
                        node.config.getOrNull(SemanticsProperties.ContentDescription)?.any { it.contains("工作数据") } == true
                })
                assertFalse(Files.exists(directory.resolve("ai-data")))
            }
        }
    }

    private fun app(): DesktopApplication {
        val paths = ApplicationPaths(Files.createTempDirectory(root, "app-"))
        val tasks = Files.createDirectories(root.resolve("tasks"))
        val store = ConfigStore(paths).also { it.save(AppConfig(taskRoot = tasks.toString(), aiRequirementNamingEnabled = false)) }
        return DesktopApplication(paths = paths, configStore = store, ioDispatcher = io,
            developmentToolStartupDetection = DevelopmentToolStartupDetection { DevelopmentToolAutoDetectionResult(it, emptySet()) },
            systemFileOpening = object : SystemFileOpening {
                override val chooserAvailable = false
                override suspend fun defaultApplication(path: Path) = DefaultFileApplication("Fixture reader")
                override suspend fun open(path: Path): FileOpenResult = error("Preview must not open an external application")
                override suspend fun chooseApplication(path: Path): FileOpenResult = error("Preview must not launch an application chooser")
            })
    }

    private fun task(materials: Path) = TaskManifest(folderName = "语法预览", taskDirectoryName = "语法预览",
        featureBranch = "feature/syntax-preview", createdAt = "now", updatedAt = "now", services = emptyList(),
        requirementMaterials = RequirementMaterialsDirectory(RequirementMaterialsStatus.READY, materials.toString()))

    private class RecordingClipboard : ClipboardManager, Clipboard {
        var capturedText: AnnotatedString? = null
        private var capturedEntry: ClipEntry? = null
        var modernWrites = 0
            private set
        // A private in-memory AWT clipboard supports any native clipboard queries without
        // reading or writing the user's Toolkit.systemClipboard.
        override val nativeClipboard = java.awt.datatransfer.Clipboard("materials-preview-test")
        override fun setText(annotatedString: AnnotatedString) { capturedText = annotatedString }
        override fun getText(): AnnotatedString? = capturedText
        override suspend fun getClipEntry(): ClipEntry? = capturedEntry ?: capturedText?.let { ClipEntry(StringSelection(it.text)) }
        override suspend fun setClipEntry(clipEntry: ClipEntry?) {
            modernWrites++
            capturedEntry = clipEntry
            val transferable = clipEntry?.asAwtTransferable
            capturedText = (transferable?.getTransferData(DataFlavor.stringFlavor) as? String)?.let(::AnnotatedString)
            if (transferable != null) nativeClipboard.setContents(transferable, null)
        }
    }

    private fun tokenText(source: String, tokens: List<WorkspaceSyntaxToken>, kind: WorkspaceSyntaxTokenKind) =
        tokens.filter { it.kind == kind }.map { source.substring(it.start, it.end) }

    private fun assertToken(source: String, tokens: List<WorkspaceSyntaxToken>, value: String, kind: WorkspaceSyntaxTokenKind) {
        assertTrue(tokens.any { it.kind == kind && source.substring(it.start, it.end) == value }, "Missing $kind token: $value; $tokens")
    }

    private fun assertValidRanges(source: String, tokens: List<WorkspaceSyntaxToken>) {
        tokens.forEach { assertTrue(it.start in source.indices && it.end in (it.start + 1)..source.length) }
        tokens.zipWithNext().forEach { (first, second) -> assertTrue(first.end <= second.start, "Highlight ranges must not overlap: $first, $second") }
    }

    private fun assertDistinctSyntaxColors(text: AnnotatedString, keyword: String, string: String, comment: String) {
        fun colorAt(needle: String): Color {
            val start = text.text.indexOf(needle)
            assertTrue(start >= 0, "Missing syntax sample: $needle")
            return assertNotNull(text.spanStyles.lastOrNull { it.start <= start && it.end > start && it.item.color != Color.Unspecified }, "No color for $needle").item.color
        }
        val keywordColor = colorAt(keyword)
        val stringColor = colorAt(string)
        val commentColor = colorAt(comment)
        assertNotEquals(keywordColor, stringColor)
        assertNotEquals(keywordColor, commentColor)
        assertNotEquals(stringColor, commentColor)
    }

    private fun ImageComposeScene.nodes(): List<SemanticsNode> {
        fun walk(node: SemanticsNode): List<SemanticsNode> = listOf(node) + node.children.flatMap(::walk)
        return semanticsOwners.flatMap { walk(it.rootSemanticsNode) }
    }

    private fun ImageComposeScene.textNode(value: String) = nodes().first {
        it.config.getOrNull(SemanticsProperties.Text)?.any { text -> text.text == value } == true
    }
    private fun ImageComposeScene.textValue(value: String) = nodes().asSequence().mapNotNull { it.config.getOrNull(SemanticsProperties.Text) }
        .flatMap { it.asSequence() }.firstOrNull { it.text == value }
    private fun ImageComposeScene.editable() = nodes().firstOrNull { it.config.getOrNull(SemanticsProperties.EditableText) != null }
    private fun ImageComposeScene.labelOrNull(value: String) = nodes().firstOrNull {
        it.config.getOrNull(SemanticsActions.OnClick) != null && it.config.getOrNull(SemanticsProperties.ContentDescription)?.contains(value) == true
    }
    private fun ImageComposeScene.label(value: String) = assertNotNull(labelOrNull(value), "Missing action: $value")
    private fun ImageComposeScene.layout(node: SemanticsNode): TextLayoutResult {
        val layouts = mutableListOf<TextLayoutResult>()
        assertTrue(node.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(layouts))
        return layouts.single()
    }
    private fun ImageComposeScene.await(condition: () -> Boolean) {
        val until = System.nanoTime() + 8_000_000_000L
        do {
            io.scheduler.runCurrent()
            Snapshot.sendApplyNotifications()
            repeat(3) { render(System.nanoTime()).close() }
            if (condition()) return
            Thread.sleep(10)
        } while (System.nanoTime() < until)
        assertTrue(condition(), nodes().joinToString { it.config.toString() })
    }
    private fun ImageComposeScene.click(node: SemanticsNode) = clickAt(node.boundsInRoot.center)
    private fun ImageComposeScene.clickAt(point: Offset) {
        sendPointerEvent(PointerEventType.Move, point)
        sendPointerEvent(PointerEventType.Press, point, buttons = PointerButtons(isPrimaryPressed = true), button = PointerButton.Primary)
        sendPointerEvent(PointerEventType.Release, point, button = PointerButton.Primary)
        repeat(3) { render(System.nanoTime()).close() }
    }
    private fun ImageComposeScene.capture(name: String) {
        val file = Path.of("build/reports/materials-code", name)
        Files.createDirectories(file.parent)
        render(System.nanoTime()).use { image ->
            image.encodeToData(org.jetbrains.skia.EncodedImageFormat.PNG)!!.use { Files.write(file, it.bytes) }
        }
    }
}
