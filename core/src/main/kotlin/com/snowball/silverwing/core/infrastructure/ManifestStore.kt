package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

data class ManifestScanResult(
    val current: List<Pair<Path, TaskManifest>>,
    val unsupportedDirectories: List<Path>,
    val failures: Map<Path, String> = emptyMap(),
    val unsupportedReasons: Map<Path, String> = emptyMap(),
)

interface TaskManifestRepository {
    fun save(taskDirectory: Path, manifest: TaskManifest)
    fun load(taskDirectory: Path): TaskManifest
    fun scan(taskRoot: Path): ManifestScanResult
}

class ManifestStore(
    private val json: Json = Json {
        prettyPrint = true
        encodeDefaults = true
    },
) : TaskManifestRepository {
    override fun save(taskDirectory: Path, manifest: TaskManifest) {
        require(SchemaVersionCompatibility.isCompatible(manifest.schemaVersion, CURRENT_TASK_MANIFEST_SCHEMA_VERSION)) {
            "不能写入任务 JSON 版本 ${manifest.schemaVersion}，当前版本为 $CURRENT_TASK_MANIFEST_SCHEMA_VERSION"
        }
        taskDirectory.createDirectories()
        val target = taskDirectory.resolve(FILE_NAME)
        val temporary = Files.createTempFile(taskDirectory, ".$FILE_NAME-", ".tmp")
        Files.writeString(
            temporary,
            json.encodeToString(manifest.copy(schemaVersion = CURRENT_TASK_MANIFEST_SCHEMA_VERSION)),
        )
        try {
            Files.move(
                temporary,
                target,
                StandardCopyOption.REPLACE_EXISTING,
                StandardCopyOption.ATOMIC_MOVE,
            )
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(temporary, target, StandardCopyOption.REPLACE_EXISTING)
        }
    }

    override fun load(taskDirectory: Path): TaskManifest {
        val content = Files.readString(taskDirectory.resolve(FILE_NAME))
        val version = json.parseToJsonElement(content)
            .jsonObject["schemaVersion"]
            ?.jsonPrimitive
            ?.contentOrNull
        require(SchemaVersionCompatibility.isCompatible(version, CURRENT_TASK_MANIFEST_SCHEMA_VERSION)) {
            "任务 JSON 版本不受支持：${version ?: "缺少 schemaVersion"}，当前版本为 " +
                CURRENT_TASK_MANIFEST_SCHEMA_VERSION
        }
        return json.decodeFromJsonElement(json.parseToJsonElement(content))
    }

    fun list(taskRoot: Path): List<Pair<Path, TaskManifest>> = scan(taskRoot).current

    /** Unsupported manifests remain byte-for-byte untouched and are reported rather than imported. */
    override fun scan(taskRoot: Path): ManifestScanResult {
        if (!taskRoot.exists()) return ManifestScanResult(emptyList(), emptyList())
        return Files.list(taskRoot).use { children ->
            val directories = children
                .filter { it.resolve(FILE_NAME).exists() }
                .toList()
            val current = mutableListOf<Pair<Path, TaskManifest>>()
            val unsupported = mutableListOf<Path>()
            val failures = linkedMapOf<Path, String>()
            val unsupportedReasons = linkedMapOf<Path, String>()
            directories.forEach { directory ->
                runCatching {
                    val content = Files.readString(directory.resolve(FILE_NAME))
                    val version = json.parseToJsonElement(content)
                        .jsonObject["schemaVersion"]
                        ?.jsonPrimitive
            ?.contentOrNull
                    if (SchemaVersionCompatibility.isCompatible(version, CURRENT_TASK_MANIFEST_SCHEMA_VERSION)) {
                        current.add(directory to json.decodeFromJsonElement(json.parseToJsonElement(content)))
                    } else {
                        unsupported.add(directory)
                        unsupportedReasons[directory] = "任务 JSON 版本不受支持：${version ?: "缺少 schemaVersion"}，当前版本为 $CURRENT_TASK_MANIFEST_SCHEMA_VERSION"
                    }
                }.onFailure { error ->
                    failures[directory] = error.message ?: error::class.simpleName ?: "读取失败"
                }
            }
            ManifestScanResult(current, unsupported, failures, unsupportedReasons)
        }
    }

    companion object {
        const val FILE_NAME = "silverwing.json"
    }
}
