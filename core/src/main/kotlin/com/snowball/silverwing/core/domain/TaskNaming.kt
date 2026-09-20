package com.snowball.silverwing.core

import java.nio.charset.StandardCharsets
import java.security.MessageDigest
import java.text.Normalizer
import java.util.Locale

object TaskNaming {
    const val MAX_DIRECTORY_NAME_LENGTH = 80
    private val invalidDirectoryCharacters = Regex("""[<>:"/\\\\|?*\p{Cc}]""")

    /**
     * Validates a task name used verbatim as its future task directory.
     *
     * Existing configuration identifiers still use [directoryName], which sanitises values for
     * backwards-safe identifiers. New task directories instead fail explicitly so the user never
     * receives a silently altered folder name.
     */
    fun directoryNameValidationError(value: String): String? {
        val normalized = Normalizer.normalize(value, Normalizer.Form.NFKC)
        if (normalized.isBlank()) return "任务名称 / 文件夹名称不能为空"
        if (normalized != normalized.trim()) return "任务名称 / 文件夹名称不能以空白开头或结尾"
        if (normalized.endsWith('.')) return "任务名称 / 文件夹名称不能以点结尾"
        if (invalidDirectoryCharacters.containsMatchIn(normalized)) {
            return "任务名称 / 文件夹名称不能包含 < > : \" / \\ | ? * 等非法字符"
        }
        if (isWindowsReservedName(normalized)) return "任务名称 / 文件夹名称不能使用 Windows 保留名"
        if (normalized.length > MAX_DIRECTORY_NAME_LENGTH) {
            return "任务名称 / 文件夹名称不能超过 $MAX_DIRECTORY_NAME_LENGTH 个字符"
        }
        return null
    }

    /** Returns the normalised, unmodified task-directory name or rejects an unsafe value. */
    fun requireValidDirectoryName(value: String): String {
        val error = directoryNameValidationError(value)
        require(error == null) { error ?: "任务名称 / 文件夹名称不合法" }
        return Normalizer.normalize(value, Normalizer.Form.NFKC)
    }

    /** Creates a stable safe identifier for configuration records, not a user task directory. */
    fun directoryName(folderName: String): String {
        require(folderName.isNotBlank()) { "文件夹名不能为空" }
        val normalized = Normalizer.normalize(folderName.trim(), Normalizer.Form.NFKC)
        val safe = normalized
            .replace(invalidDirectoryCharacters, "-")
            .replace(Regex("""\s+"""), "-")
            .replace(Regex("""-+"""), "-")
            .trim(' ', '.', '-')
            .ifBlank { "task" }
        val reservedSafe = if (isWindowsReservedName(safe)) "_$safe" else safe
        if (reservedSafe.length <= MAX_DIRECTORY_NAME_LENGTH) return reservedSafe
        val suffix = shortHash(folderName)
        return "${reservedSafe.take(MAX_DIRECTORY_NAME_LENGTH - suffix.length - 1)}-$suffix"
    }

    private fun isWindowsReservedName(value: String): Boolean {
        val stem = value.substringBefore('.').uppercase()
        return stem in setOf(
            "CON", "PRN", "AUX", "NUL",
            "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
            "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
        )
    }

    private fun shortHash(value: String): String =
        MessageDigest.getInstance("SHA-256")
            .digest(value.toByteArray(StandardCharsets.UTF_8))
            .take(4)
            .joinToString("") { "%02x".format(Locale.ROOT, it) }
}
