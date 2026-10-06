package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.time.Instant

interface RequirementAiNamingCache {
    fun read(identity: String, model: String): RequirementAiNamingSuggestion?
    fun write(identity: String, model: String, suggestion: RequirementAiNamingSuggestion)
}

/** 缓存不属于配置分片。损坏或写入失败仅影响缓存，不阻止任务创建。 */
class LocalRequirementAiNamingCache(
    file: Path,
    private val onFailure: (Throwable) -> Unit = {},
) : RequirementAiNamingCache {
    private val file = file.toAbsolutePath().normalize()
    private val json = Json { prettyPrint = true; encodeDefaults = true }
    private var failureReported = false
    private val entries by lazy {
        runCatching {
            if (!Files.exists(file)) emptyMap() else {
                require(Files.size(file) <= 10 * 1024 * 1024) { "命名缓存文件过大" }
                val saved = json.decodeFromString<CacheFile>(Files.readString(file))
                require(saved.version == 1) { "命名缓存版本不受支持" }
                saved.entries
            }
        }.onFailure(::report).getOrDefault(emptyMap()).toMutableMap()
    }

    @Synchronized override fun read(identity: String, model: String): RequirementAiNamingSuggestion? =
        (entries[entryKey(identity, model)] ?: entries[identity])
            ?.takeIf { it.model == model && it.rulesVersion == RULES_VERSION }?.let {
            runCatching { RequirementAiNamingRules.requireValid(RequirementAiNamingSuggestion(it.folderName, it.branchSuffix)) }.getOrNull()
        }

    @Synchronized override fun write(identity: String, model: String, suggestion: RequirementAiNamingSuggestion) {
        val value = RequirementAiNamingRules.requireValid(suggestion)
        entries[entryKey(identity, model)] = CacheEntry(value.folderName, value.branchSuffix, model, RULES_VERSION, Instant.now().toString())
        runCatching {
            Files.createDirectories(file.parent)
            val temporary = Files.createTempFile(file.parent, "ai-naming-", ".tmp")
            try {
                Files.writeString(temporary, json.encodeToString(CacheFile(entries = entries.toMap())))
                try { Files.move(temporary, file, ATOMIC_MOVE, REPLACE_EXISTING) }
                catch (_: AtomicMoveNotSupportedException) { Files.move(temporary, file, REPLACE_EXISTING) }
            } finally { Files.deleteIfExists(temporary) }
        }.onFailure(::report)
    }

    // Keep version-1 entries readable while retaining separate results for each model.
    private fun entryKey(identity: String, model: String): String = json.encodeToString(listOf(identity, model))

    private fun report(error: Throwable) { if (!failureReported) { failureReported = true; runCatching { onFailure(error) } } }
    private companion object { const val RULES_VERSION = 2 }
}

@Serializable private data class CacheFile(val version: Int = 1, val entries: Map<String, CacheEntry> = emptyMap())
@Serializable private data class CacheEntry(
    val folderName: String, val branchSuffix: String, val model: String, val rulesVersion: Int, val generatedAt: String,
)
