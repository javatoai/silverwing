package com.snowball.silverwing.core

import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import kotlin.io.path.createDirectories

/** Writes the self-contained handoff required by Agent CLI tasks. */
object HandoffDocumentWriter {
    const val DIRECTORY_NAME = ".workspace"
    const val FILE_NAME = "HANDOFF.md"

    fun write(taskDirectory: Path, suppliedMarkdown: String?): Path {
        val directory = taskDirectory.resolve(DIRECTORY_NAME)
        directory.createDirectories()
        val target = directory.resolve(FILE_NAME)
        val content = safeMarkdown(suppliedMarkdown)
        val temporary = Files.createTempFile(directory, ".${FILE_NAME}-", ".tmp")
        Files.writeString(temporary, content.trimEnd() + "\n", StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE)
        try {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
        return target
    }

    /** No conversation/session identifier is stored; the document stands alone. */
    fun template(): String = """
        # 任务交接

        ## 目标

        - 待补充。

        ## 范围与边界

        - 待补充。

        ## 已验证事实

        - 待补充。

        ## 风险与未决项

        - 待补充。

        ## 建议下一步

        - 待补充。
    """.trimIndent()

    /** Safe both for HANDOFF.md and for the persisted Agent operation audit record. */
    fun safeMarkdown(suppliedMarkdown: String?): String = sanitize(
        suppliedMarkdown?.trim().takeUnless { it.isNullOrBlank() } ?: template(),
    )

    private fun sanitize(content: String): String = content
        .replace(secretAssignment, "\$1[REDACTED]")
        .replace(openAiStyleToken, "[REDACTED]")

    private val secretAssignment = Regex(
        """(?im)^([ \t]*(?:[-*][ \t]*)?["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|authorization|proxy-authorization|cookie|set-cookie)["']?[ \t]*[:=][ \t]*)[^\r\n]+""",
    )
    private val openAiStyleToken = Regex("\\b(?:(?:sk|rk)[_-]|ghp_)[A-Za-z0-9_-]{12,}\\b")
}
