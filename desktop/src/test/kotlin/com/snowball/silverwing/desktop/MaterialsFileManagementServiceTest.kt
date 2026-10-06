package com.snowball.silverwing.desktop

import java.awt.image.BufferedImage
import java.io.ByteArrayInputStream
import java.nio.file.Files
import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import javax.imageio.ImageIO
import org.junit.jupiter.api.Assumptions.assumeTrue
import org.junit.jupiter.api.io.TempDir
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class MaterialsFileManagementServiceTest {
    @TempDir lateinit var temporary: Path
    private val service = MaterialsFileManagementService()
    private fun root(name: String = "资料 根目录"): Path = Files.createDirectory(temporary.resolve(name))

    @Test fun `create and rename preserve Chinese spaces and directory contents`() {
        val root = root()
        val created = assertIs<MaterialsFileOperationResult.Completed>(service.createDirectory(root, "", " 中文 文件夹"))
        assertEquals(" 中文 文件夹", created.relativePath)
        assertTrue(created.isDirectory)
        val markdown = assertIs<MaterialsFileOperationResult.Completed>(service.createMarkdown(root, created.relativePath, "支付 说明"))
        assertEquals(" 中文 文件夹/支付 说明.md", markdown.relativePath)
        assertEquals("", Files.readString(root.resolve(markdown.relativePath)))
        Files.writeString(root.resolve(markdown.relativePath), "原资料")
        val renamed = assertIs<MaterialsFileOperationResult.Completed>(service.rename(root, created.relativePath, "新 文件夹"))
        assertEquals(created.relativePath, renamed.previousRelativePath)
        assertTrue(renamed.isDirectory)
        assertFalse(Files.exists(root.resolve(created.relativePath)))
        assertEquals("原资料", Files.readString(root.resolve("新 文件夹/支付 说明.md")))
        val fileRename = assertIs<MaterialsFileOperationResult.Completed>(service.rename(root, "新 文件夹/支付 说明.md", "不同 名称.md"))
        assertEquals("新 文件夹/不同 名称.md", fileRename.relativePath)
        assertEquals("新 文件夹/支付 说明.md", fileRename.previousRelativePath)
    }

    @Test fun `file import conflict cancels saves as or explicitly replaces without changing source`() {
        val root = root()
        val destination = Files.writeString(root.resolve("同名 文件.txt"), "existing")
        val source = Files.writeString(temporary.resolve("同名 文件.txt"), "imported")
        val conflict = assertIs<MaterialsFileOperationResult.Conflict>(service.importFile(root, "", source)).conflict
        assertTrue(conflict.canReplace)
        assertEquals("同名 文件 (1).txt", conflict.suggestedName)
        assertEquals("existing", Files.readString(destination))
        assertEquals(MaterialsFileOperationResult.Cancelled, service.resolveConflict(conflict, MaterialsConflictChoice.CANCEL))
        assertEquals("existing", Files.readString(destination))
        val separate = assertIs<MaterialsFileOperationResult.Completed>(service.resolveConflict(conflict, MaterialsConflictChoice.SAVE_AS))
        assertEquals("同名 文件 (1).txt", separate.relativePath)
        assertEquals("imported", Files.readString(root.resolve(separate.relativePath)))
        assertEquals("existing", Files.readString(destination))
        assertIs<MaterialsFileOperationResult.Completed>(service.resolveConflict(conflict, MaterialsConflictChoice.REPLACE))
        assertEquals("imported", Files.readString(destination))
        assertEquals("imported", Files.readString(source))
        Files.list(root).use { entries -> assertFalse(entries.anyMatch { it.fileName.toString().startsWith(".silverwing-import-") }) }
    }

    @Test fun `rename and directory conflicts never silently merge or replace`() {
        val root = root()
        val first = Files.writeString(root.resolve("first.md"), "first")
        val second = Files.writeString(root.resolve("second.md"), "second")
        val conflict = assertIs<MaterialsFileOperationResult.Conflict>(service.rename(root, "first.md", "second.md")).conflict
        assertFalse(conflict.canReplace)
        assertFailsWith<IllegalArgumentException> { service.resolveConflict(conflict, MaterialsConflictChoice.REPLACE) }
        assertEquals("first", Files.readString(first))
        assertEquals("second", Files.readString(second))
        val renamed = assertIs<MaterialsFileOperationResult.Completed>(service.resolveConflict(conflict, MaterialsConflictChoice.SAVE_AS, "third.md"))
        assertEquals("first.md", renamed.previousRelativePath)
        assertEquals("first", Files.readString(root.resolve("third.md")))
        Files.createDirectory(root.resolve("folder"))
        val folder = assertIs<MaterialsFileOperationResult.Conflict>(service.createDirectory(root, "", "folder")).conflict
        assertFalse(folder.canReplace)
        assertFailsWith<IllegalArgumentException> { service.resolveConflict(folder, MaterialsConflictChoice.REPLACE) }
        assertIs<MaterialsFileOperationResult.Completed>(service.resolveConflict(folder, MaterialsConflictChoice.SAVE_AS))
        assertTrue(Files.isDirectory(root.resolve("folder (1)")))
    }

    @Test fun `changed conflict target blocks replacement and leaves both files intact`() {
        val root = root()
        val target = Files.writeString(root.resolve("file.txt"), "old")
        val source = Files.writeString(temporary.resolve("file.txt"), "incoming")
        val pending = assertIs<MaterialsFileOperationResult.Conflict>(service.importFile(root, "", source)).conflict
        Files.writeString(target, "changed while confirmation was open")
        assertFailsWith<IllegalArgumentException> { service.resolveConflict(pending, MaterialsConflictChoice.REPLACE) }
        assertEquals("changed while confirmation was open", Files.readString(target))
        assertEquals("incoming", Files.readString(source))
    }

    @Test fun `replacement checks target again after writing staging and cleans up on failure`() {
        val root = root()
        val target = Files.writeString(root.resolve("file.txt"), "old")
        val source = Files.writeString(temporary.resolve("file.txt"), "incoming")
        val racing = object : MaterialsFileSystem by NioMaterialsFileSystem {
            override fun copyNew(source: Path, destination: Path) {
                NioMaterialsFileSystem.copyNew(source, destination)
                if (destination.fileName.toString().startsWith(".silverwing-import-")) Files.writeString(target, "concurrent edit")
            }
        }
        val guarded = MaterialsFileManagementService(racing)
        val pending = assertIs<MaterialsFileOperationResult.Conflict>(guarded.importFile(root, "", source)).conflict
        assertFailsWith<IllegalArgumentException> { guarded.resolveConflict(pending, MaterialsConflictChoice.REPLACE) }
        assertEquals("concurrent edit", Files.readString(target))
        Files.list(root).use { entries -> assertEquals(listOf("file.txt"), entries.map { it.fileName.toString() }.toList()) }
    }

    @Test fun `new file appearing between check and copy is never truncated`() {
        val root = root()
        val source = Files.writeString(temporary.resolve("racing.txt"), "incoming")
        val racing = object : MaterialsFileSystem by NioMaterialsFileSystem {
            override fun copyNew(source: Path, destination: Path) {
                Files.writeString(destination, "concurrent file")
                NioMaterialsFileSystem.copyNew(source, destination)
            }
        }
        assertFailsWith<java.nio.file.FileAlreadyExistsException> { MaterialsFileManagementService(racing).importFile(root, "", source) }
        assertEquals("concurrent file", Files.readString(root.resolve("racing.txt")))
    }

    @Test fun `invalid names parent paths root rename and git boundaries are rejected`() {
        val root = root()
        for (name in listOf("", " ", ".", "..", ".git", ".GiT", "../escape", "a/b", "a\\b", "a:b", "C:\\escape", "name.", "name ", "CON", "nul.txt", "a\u0000b")) {
            assertFailsWith<IllegalArgumentException>(name) { service.createDirectory(root, "", name) }
        }
        for (directory in listOf("../", ".", "/absolute", "a/../b", "C:/outside", "a//b")) {
            assertFailsWith<IllegalArgumentException>(directory) { service.createMarkdown(root, directory, "safe.md") }
        }
        assertFailsWith<IllegalArgumentException> { service.rename(root, ".", "renamed") }
        assertFailsWith<IllegalArgumentException> { service.rename(root, "../", "renamed") }
        Files.createDirectory(root.resolve(".git"))
        Files.writeString(root.resolve(".git/config"), "untouched")
        assertFailsWith<IllegalArgumentException> { service.createMarkdown(root, ".git", "file") }
        assertFailsWith<IllegalArgumentException> { service.rename(root, ".git/config", "changed") }
        assertFailsWith<IllegalArgumentException> { service.importFile(root, "", root.resolve(".git/config")) }
        assertEquals("untouched", Files.readString(root.resolve(".git/config")))
        assertFalse(Files.exists(temporary.resolve("escape")))
    }

    @Test fun `folders containing git internals cannot be renamed`() {
        val root = root()
        Files.createDirectories(root.resolve("nested/.git"))
        assertFailsWith<IllegalArgumentException> { service.rename(root, "nested", "changed") }
        assertTrue(Files.isDirectory(root.resolve("nested/.git")))
        assertFalse(Files.exists(root.resolve("changed")))
    }

    @Test fun `directory junction aliases are rejected even when their destination stays in the root`() {
        val root = root()
        val junction = Files.createDirectory(root.resolve("junction"))
        val real = Files.createDirectory(root.resolve("real"))
        val source = Files.writeString(junction.resolve("source.txt"), "alias")
        val redirected = object : MaterialsFileSystem by NioMaterialsFileSystem {
            override fun realPath(path: Path): Path = when {
                path == junction -> real
                path.startsWith(junction) -> real.resolve(junction.relativize(path))
                else -> NioMaterialsFileSystem.realPath(path)
            }
        }
        val guarded = MaterialsFileManagementService(redirected)
        assertFailsWith<IllegalArgumentException> { guarded.createMarkdown(root, "junction", "blocked") }
        assertFailsWith<IllegalArgumentException> { guarded.rename(root, "junction", "changed") }
        assertFailsWith<IllegalArgumentException> { guarded.importFile(root, "", source) }
        assertFailsWith<IllegalArgumentException> { guarded.createDirectory(junction, "", "blocked") }
        assertFalse(Files.exists(real.resolve("blocked.md")))
        assertEquals("alias", Files.readString(source))
    }

    @Test fun `symlink roots parents targets and nested rename entries are rejected`() {
        val root = root()
        val outside = Files.createDirectory(temporary.resolve("outside"))
        val outsideFile = Files.writeString(outside.resolve("source.txt"), "outside")
        val link = root.resolve("linked")
        val made = runCatching { Files.createSymbolicLink(link, outside) }.isSuccess
        assumeTrue(made, "This Windows environment does not permit temporary symbolic links")
        assertFailsWith<IllegalArgumentException> { service.createMarkdown(root, "linked", "bad") }
        assertFailsWith<IllegalArgumentException> { service.rename(root, "linked/source.txt", "bad.txt") }
        assertFailsWith<IllegalArgumentException> { service.importFile(root, "", link.resolve("source.txt")) }
        assertFailsWith<IllegalArgumentException> { service.createDirectory(link, "", "bad") }
        val alias = Files.createSymbolicLink(root.resolve("alias.txt"), outsideFile)
        val source = Files.writeString(temporary.resolve("alias.txt"), "incoming")
        assertFailsWith<IllegalArgumentException> { service.importFile(root, "", source) }
        assertFailsWith<IllegalArgumentException> { service.rename(root, "alias.txt", "renamed.txt") }
        val folder = Files.createDirectory(root.resolve("contains-link"))
        Files.createSymbolicLink(folder.resolve("link.txt"), outsideFile)
        assertFailsWith<IllegalArgumentException> { service.rename(root, "contains-link", "renamed-folder") }
        assertTrue(Files.isSymbolicLink(alias))
        assertEquals("outside", Files.readString(outsideFile))
        assertFalse(Files.exists(outside.resolve("bad.md")))
    }

    @Test fun `image paste writes real png and second screenshot waits for a conflict decision`() {
        val root = root()
        Files.createDirectory(root.resolve("截图 目录"))
        val clock = Clock.fixed(Instant.parse("2026-10-03T08:09:10Z"), ZoneOffset.UTC)
        val images = MaterialsFileManagementService(clock = clock)
        val image = BufferedImage(3, 2, BufferedImage.TYPE_INT_ARGB).apply { setRGB(1, 1, 0xFF336699.toInt()) }
        val first = assertIs<MaterialsFileOperationResult.Completed>(images.pasteImage(root, "截图 目录", image))
        assertEquals("截图 目录/截图-20261003-080910.png", first.relativePath)
        val decoded = assertNotNull(ImageIO.read(ByteArrayInputStream(Files.readAllBytes(root.resolve(first.relativePath)))))
        assertEquals(3, decoded.width)
        assertEquals(2, decoded.height)
        assertEquals(image.getRGB(1, 1), decoded.getRGB(1, 1))
        val pending = assertIs<MaterialsFileOperationResult.Conflict>(images.pasteImage(root, "截图 目录", image)).conflict
        assertTrue(pending.canReplace)
        val second = assertIs<MaterialsFileOperationResult.Completed>(images.resolveConflict(pending, MaterialsConflictChoice.SAVE_AS))
        assertEquals("截图 目录/截图-20261003-080910 (1).png", second.relativePath)
        assertTrue(Files.isRegularFile(root.resolve(first.relativePath)))
    }

    @Test fun `markdown extension is preserved and renaming to self retains the file`() {
        assertEquals("name.md", markdownFileName("name"))
        assertEquals("Name.MD", markdownFileName("Name.MD"))
        assertEquals("name.markdown", markdownFileName("name.markdown"))
        val root = root()
        val file = Files.writeString(root.resolve("same.md"), "contents")
        val renamed = assertIs<MaterialsFileOperationResult.Completed>(service.rename(root, "same.md", "same.md"))
        assertEquals("same.md", renamed.relativePath)
        assertEquals("contents", Files.readString(file))
        if (System.getProperty("os.name").startsWith("Windows", true)) {
            assertIs<MaterialsFileOperationResult.Completed>(service.rename(root, "same.md", "Same.MD"))
            Files.list(root).use { entries -> assertEquals(listOf("Same.MD"), entries.map { it.fileName.toString() }.toList()) }
        }
    }
}
