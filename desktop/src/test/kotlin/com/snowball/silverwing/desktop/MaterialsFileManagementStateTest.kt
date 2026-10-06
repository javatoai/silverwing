@file:OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)

package com.snowball.silverwing.desktop

import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.Transferable
import java.awt.datatransfer.UnsupportedFlavorException
import java.awt.image.BufferedImage
import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class MaterialsFileManagementStateTest {
    @TempDir lateinit var temporary: Path
    private val image = BufferedImage(2, 2, BufferedImage.TYPE_INT_ARGB)

    @Test fun `clipboard is read only after explicit paste yields and cancelled actions never read it`() = runTest {
        val root = Files.createDirectory(temporary.resolve("root"))
        var clipboardReads = 0
        val results = mutableListOf<MaterialsFileOperationResult.Completed>()
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { clipboardReads++; image })
        state.onCompleted = { results += it }
        assertEquals(0, clipboardReads)
        state.pasteImage()
        assertTrue(state.busy)
        assertEquals(0, clipboardReads, "The pointer/key handler must not synchronously read the native clipboard")
        state.dispose()
        runCurrent()
        assertEquals(0, clipboardReads)
        assertTrue(results.isEmpty())
        Files.list(root).use { assertEquals(0L, it.count()) }

        val active = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { clipboardReads++; image })
        active.onCompleted = { results += it }
        active.pasteImage()
        runCurrent()
        assertEquals(1, clipboardReads)
        assertEquals(1, results.size)
        assertTrue(Files.isRegularFile(root.resolve(results.single().relativePath)))
        assertFalse(active.busy)
        active.dispose()
    }

    @Test fun `missing clipboard image reports a recoverable error without creating anything`() = runTest {
        val root = Files.createDirectory(temporary.resolve("empty-clipboard"))
        val errors = mutableListOf<String>()
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { null })
        state.onError = { errors += it }
        state.pasteImage()
        runCurrent()
        assertTrue(errors.single().contains("剪贴板中没有图片"))
        assertTrue(state.acceptsInput)
        Files.list(root).use { assertEquals(0L, it.count()) }
        state.dispose()
    }

    @Test fun `name dialogs capture their destination and invalid names stay editable`() = runTest {
        val root = Files.createDirectory(temporary.resolve("captured"))
        Files.createDirectory(root.resolve("first"))
        Files.createDirectory(root.resolve("second"))
        val results = mutableListOf<MaterialsFileOperationResult.Completed>()
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { error("Clipboard must not be used") })
        state.currentDirectory = "first"
        state.onCompleted = { results += it }
        state.createMarkdown()
        state.currentDirectory = "second"
        state.submitName("../outside")
        assertNotNull(state.nameError)
        assertNotNull(state.nameRequest)
        assertFalse(state.busy)
        state.submitName(" 中文 说明")
        assertNull(state.nameRequest)
        assertTrue(state.busy)
        runCurrent()
        assertEquals("first/ 中文 说明.md", results.single().relativePath)
        assertTrue(Files.isRegularFile(root.resolve("first/ 中文 说明.md")))
        assertFalse(Files.exists(root.resolve("second/ 中文 说明.md")))
        state.dispose()
    }

    @Test fun `serial imports stop for each conflict and resume in the originally selected folder`() = runTest {
        val root = Files.createDirectory(temporary.resolve("queue"))
        Files.createDirectory(root.resolve("first"))
        Files.createDirectory(root.resolve("second"))
        Files.writeString(root.resolve("first/one.txt"), "keep")
        val first = Files.writeString(temporary.resolve("one.txt"), "one")
        val second = Files.writeString(temporary.resolve("two.txt"), "two")
        val results = mutableListOf<MaterialsFileOperationResult.Completed>()
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { null })
        state.currentDirectory = "first"
        state.onCompleted = { results += it }
        state.importFiles(listOf(first, second, second))
        state.currentDirectory = "second"
        runCurrent()
        assertNotNull(state.conflict)
        assertFalse(state.acceptsInput)
        assertFalse(Files.exists(root.resolve("first/two.txt")))
        assertTrue(results.isEmpty())
        state.pasteImage() // Blocked input cannot queue an unexpected native clipboard read.
        state.resolveConflict(MaterialsConflictChoice.CANCEL)
        runCurrent()
        assertNull(state.conflict)
        assertEquals(listOf("first/two.txt"), results.map { it.relativePath })
        assertEquals("keep", Files.readString(root.resolve("first/one.txt")))
        assertEquals("two", Files.readString(root.resolve("first/two.txt")))
        assertFalse(Files.exists(root.resolve("second/two.txt")))
        assertTrue(Files.isRegularFile(first))
        assertTrue(Files.isRegularFile(second))
        state.dispose()
    }

    @Test fun `native picker captures directory and cancelling it is silent`() = runTest {
        val root = Files.createDirectory(temporary.resolve("picker"))
        Files.createDirectory(root.resolve("first"))
        Files.createDirectory(root.resolve("second"))
        val selected = Files.writeString(temporary.resolve("selected.txt"), "picked")
        val chosen = CompletableDeferred<List<Path>?>()
        val initialDirectories = mutableListOf<Path>()
        val results = mutableListOf<MaterialsFileOperationResult.Completed>()
        val errors = mutableListOf<String>()
        val picker = MaterialsImportFilePicker { initial -> initialDirectories.add(initial); chosen.await() }
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { null }, picker)
        state.currentDirectory = "first"
        state.onCompleted = { results += it }
        state.onError = { errors += it }
        state.chooseImportFiles()
        state.currentDirectory = "second"
        runCurrent()
        assertEquals(listOf(root.resolve("first")), initialDirectories)
        assertTrue(state.busy)
        chosen.complete(listOf(selected))
        runCurrent()
        assertEquals("first/selected.txt", results.single().relativePath)
        assertFalse(Files.exists(root.resolve("second/selected.txt")))
        state.dispose()

        val cancelled = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { null }, MaterialsImportFilePicker { null })
        cancelled.onError = { errors += it }
        cancelled.chooseImportFiles()
        runCurrent()
        assertFalse(cancelled.busy)
        assertTrue(errors.isEmpty())
        cancelled.dispose()
    }

    @Test fun `disposing pending picker prevents stale-root filesystem work and callbacks`() = runTest {
        val root = Files.createDirectory(temporary.resolve("disposed"))
        val selected = Files.writeString(temporary.resolve("selected.txt"), "picked")
        val chosen = CompletableDeferred<List<Path>?>()
        val state = MaterialsFileManagementState(root, this, StandardTestDispatcher(testScheduler),
            MaterialsFileManagementService(), MaterialsImageClipboard { null }, MaterialsImportFilePicker { chosen.await() })
        state.onCompleted = { error("Disposed state cannot send stale callbacks") }
        state.onError = { error("Disposed state cannot send stale errors") }
        state.chooseImportFiles()
        runCurrent()
        state.dispose()
        chosen.complete(listOf(selected))
        runCurrent()
        assertFalse(Files.exists(root.resolve("selected.txt")))
        assertFalse(state.acceptsInput)
    }

    @Test fun `native file payload retains Chinese and shell-looking names without executing them`() {
        val path = temporary.resolve("中文 空格 `\u0024(特殊).md")
        val transferable = object : Transferable {
            override fun getTransferDataFlavors() = arrayOf(DataFlavor.javaFileListFlavor)
            override fun isDataFlavorSupported(flavor: DataFlavor) = flavor == DataFlavor.javaFileListFlavor
            override fun getTransferData(flavor: DataFlavor): Any {
                if (!isDataFlavorSupported(flavor)) throw UnsupportedFlavorException(flavor)
                return listOf(path.toFile())
            }
        }
        assertEquals(listOf(path), materialFilesFromTransferable(transferable))
        val text = java.awt.datatransfer.StringSelection(path.toString())
        assertFailsWith<IllegalArgumentException> { materialFilesFromTransferable(text) }
    }
}
