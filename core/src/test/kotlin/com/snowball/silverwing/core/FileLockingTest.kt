package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import org.junit.jupiter.api.io.TempDir

class FileLockingTest {
    @TempDir
    lateinit var temporary: Path

    @Test
    fun `lock identity follows a directory symlink`() {
        val real = Files.createDirectories(temporary.resolve("real"))
        val alias = temporary.resolve("alias")
        DirectoryAliasTestSupport.create(temporary, alias, real)

        assertEquals(FileLocking.stablePathHash(real), FileLocking.stablePathHash(alias))
        assertEquals(
            FileLocking.stablePathHash(real.resolve("missing-child")),
            FileLocking.stablePathHash(alias.resolve("missing-child")),
        )
    }

    @Test
    fun `lock identity stays stable when a previously missing path is created`() {
        val target = temporary.resolve("pending").resolve("child")
        val beforeCreation = FileLocking.stablePathHash(target)

        Files.createDirectories(target)

        assertEquals(beforeCreation, FileLocking.stablePathHash(target))
    }
}
