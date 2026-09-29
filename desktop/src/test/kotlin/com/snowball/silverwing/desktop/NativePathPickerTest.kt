package com.snowball.silverwing.desktop

import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertEquals

class NativePathPickerTest {
    @Test
    fun `cancel retains current values and selections replace them`() = runBlocking {
        val cancelled = PathSelectionCoordinator(FakeNativePathPicker())
        assertEquals("C:/tasks", cancelled.directory("C:/tasks"))
        assertEquals("C:/idea.exe", cancelled.file("C:/idea.exe"))
        assertEquals("C:/idea.exe", cancelled.application("C:/idea.exe"))
        assertEquals(emptyList(), cancelled.directories("C:/tasks"))

        val selected = PathSelectionCoordinator(
            FakeNativePathPicker("D:/tasks", "D:/tools/idea.exe", listOf("D:/repo-a", "D:/repo-b")),
        )
        assertEquals("D:/tasks", selected.directory("C:/tasks"))
        assertEquals("D:/tools/idea.exe", selected.file("C:/idea.exe"))
        assertEquals(listOf("D:/repo-a", "D:/repo-b"), selected.directories("C:/tasks"))
    }

    @Test
    fun `application picker accepts arbitrary executable files on macOS`() {
        assertEquals(emptyList<String>(), applicationPickerExtensions())
    }

    @Test
    fun `terminal picker accepts macOS application bundles`() {
        assertEquals(true, terminalUsesApplicationPicker("Mac OS X"))
        assertEquals(false, terminalUsesApplicationPicker("Windows 11"))
        assertEquals(false, terminalUsesApplicationPicker("Linux"))
    }

    @Test
    fun `directory picker routes macOS and Windows to native multi-select implementations`() = runBlocking {
        val macPaths = pickDirectoriesForOs(
            osName = "Mac OS X",
            initialPath = "/workspace",
            windowsPicker = { error("Windows picker must not be called") },
            macPicker = { initial ->
                assertEquals("/workspace", initial)
                listOf("/workspace/repo-a", "/workspace/repo-b")
            },
            singlePicker = { error("single picker must not be called") },
        )
        assertEquals(listOf("/workspace/repo-a", "/workspace/repo-b"), macPaths)

        val windowsPaths = pickDirectoriesForOs(
            osName = "Windows 11",
            initialPath = "C:/workspace",
            windowsPicker = { initial ->
                assertEquals("C:/workspace", initial)
                listOf("C:/workspace/repo-a", "C:/workspace/repo-b")
            },
            macPicker = { error("macOS picker must not be called") },
            singlePicker = { error("single picker must not be called") },
        )
        assertEquals(listOf("C:/workspace/repo-a", "C:/workspace/repo-b"), windowsPaths)

        val linuxPaths = pickDirectoriesForOs(
            osName = "Linux",
            initialPath = "/workspace",
            windowsPicker = { error("Windows picker must not be called") },
            macPicker = { error("macOS picker must not be called") },
            singlePicker = { initial ->
                assertEquals("/workspace", initial)
                listOf("/workspace/repo-a")
            },
        )
        assertEquals(listOf("/workspace/repo-a"), linuxPaths)
    }

    @Test
    fun `macOS picker errors remain visible to the caller`() = runBlocking {
        assertFailsWith<IllegalStateException> {
            pickDirectoriesForOs(
                osName = "Mac OS X",
                initialPath = null,
                windowsPicker = { error("Windows picker must not be called") },
                macPicker = { error("native panel unavailable") },
                singlePicker = { error("single picker must not be called") },
            )
        }
    }

    @Test
    fun `macOS native panel is configured for multi-directory selection`() {
        if (!isMacOs(System.getProperty("os.name"))) return

        assertEquals(
            MacNativePanelConfiguration(
                canChooseFiles = false,
                canChooseDirectories = true,
                allowsMultipleSelection = true,
            ),
            MacMultiDirectoryPicker.inspectNativePanelConfiguration(),
        )
    }
}

private class FakeNativePathPicker(
    private val directory: String? = null,
    private val file: String? = null,
    private val directories: List<String>? = null,
) : NativePathPicker {
    override suspend fun pickDirectory(initialPath: String?): String? = directory
    override suspend fun pickDirectories(initialPath: String?): List<String>? = directories
    override suspend fun pickFile(initialPath: String?, extensions: List<String>): String? = file
    override suspend fun pickApplication(initialPath: String?): String? = file
}
