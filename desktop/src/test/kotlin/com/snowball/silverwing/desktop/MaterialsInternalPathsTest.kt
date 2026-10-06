package com.snowball.silverwing.desktop

import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.*

class MaterialsInternalPathsTest {
    @TempDir lateinit var root: Path
    @Test fun `in progress imports and renames never appear as materials but ordinary dotfiles do`() = runBlocking {
        val import = ".silverwing-import-12345678-1234-1234-1234-123456789abc.tmp"
        val rename = ".silverwing-rename-12345678-1234-1234-1234-123456789abc"
        Files.writeString(root.resolve(import), "unfinished needle")
        Files.createDirectory(root.resolve(rename))
        Files.writeString(root.resolve(rename).resolve("partial.md"), "unfinished needle")
        Files.writeString(root.resolve(".ordinary.txt"), "normal needle")
        Files.writeString(root.resolve(".silverwing-import-manual.txt"), "normal needle")
        val expected = setOf(".ordinary.txt", ".silverwing-import-manual.txt")
        assertEquals(expected, RequirementMaterialsMarkdownService().list(root).files.map { it.relativePath }.toSet())
        assertEquals(expected, captureMaterialsLiveRefreshSnapshot(root).files.keys)
        assertEquals(expected, MaterialsFullTextSearchService().search(root, "needle").hits.map { it.relativePath }.toSet())
        assertFalse(isMaterialsStagingName(".silverwing-rename-not-a-uuid"))
    }
}
