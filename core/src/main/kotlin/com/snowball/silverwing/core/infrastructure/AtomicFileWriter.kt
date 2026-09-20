package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import kotlin.io.path.createDirectories

/** Small infrastructure seam for replacing generated documents safely. */
internal object AtomicFileWriter {
    fun write(target: Path, content: String) {
        target.parent.createDirectories()
        val temporary = Files.createTempFile(target.parent, ".${target.fileName}-", ".tmp")
        try {
            Files.writeString(temporary, content, StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE)
            try {
                Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
    }
}
