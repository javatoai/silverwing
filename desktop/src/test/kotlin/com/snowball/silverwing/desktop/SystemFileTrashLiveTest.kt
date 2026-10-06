package com.snowball.silverwing.desktop

import java.nio.file.Files
import java.nio.file.Path
import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.condition.EnabledOnOs
import org.junit.jupiter.api.condition.OS
import org.junit.jupiter.api.io.TempDir
import kotlin.test.*

/** 仅操作测试创建的临时资料，验证中文路径和文件夹实际进入 Windows 回收站。 */
@EnabledOnOs(OS.WINDOWS)
class SystemFileTrashLiveTest {
    @TempDir lateinit var root: Path
    @Test fun `native recycling handles Chinese paths and folders without permanently deleting`() = runBlocking {
        val file = Files.writeString(root.resolve("资料 中文 & #.txt"), "临时资料")
        val folder = Files.createDirectory(root.resolve("资料 文件夹"))
        Files.writeString(folder.resolve("内部文档.txt"), "临时资料")
        PlatformSystemFileTrash().use { trash ->
            val result = trash.moveToTrash(listOf(file, folder))
            assertTrue(result.all { it.error == null }, result.toString())
            assertFalse(Files.exists(file))
            assertFalse(Files.exists(folder))
        }
    }
}
