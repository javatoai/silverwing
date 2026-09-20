package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.nio.file.Files
import java.nio.file.Path

/**
 * Filesystem boundary for the requirement documentation use case.
 *
 * The application service deliberately deals in plans and documents, not
 * Files APIs or JSON files.  Keeping this seam small also makes it possible to
 * test the application orchestration without teaching it how atomic writes,
 * links and JSONL indexes work.
 */
class RequirementDocumentationFileStore(
    private val manifestJson: Json = Json { prettyPrint = true; encodeDefaults = true; ignoreUnknownKeys = false },
    private val indexJson: Json = Json { encodeDefaults = true; ignoreUnknownKeys = false },
) {
    fun exists(path: Path): Boolean = Files.exists(path)

    fun isDirectory(path: Path): Boolean = Files.isDirectory(path)

    fun isRegularFile(path: Path): Boolean = Files.isRegularFile(path)

    fun isSymbolicLink(path: Path): Boolean = Files.isSymbolicLink(path)

    fun walk(root: Path): List<Path> = Files.walk(root).use { it.toList() }

    fun read(path: Path): String = Files.readString(path)

    fun write(path: Path, content: String) = AtomicFileWriter.write(path, content)

    fun readRequirement(path: Path): RequirementDocumentationManifest = runCatching {
        manifestJson.decodeFromString<RequirementDocumentationManifest>(read(path))
    }.getOrElse { throw IllegalStateException("需求资料 manifest 损坏：$path", it) }

    /** Legacy desktop manifests may contain only the requirement identity. */
    fun readRequirementIdentity(path: Path): RequirementIdentity = runCatching {
        val identity = (manifestJson.parseToJsonElement(read(path)) as? JsonObject)
            ?.get("identity") as? JsonObject
            ?: throw IllegalArgumentException("manifest 缺少需求身份")
        RequirementIdentity(
            space = identity.stringValue("space"),
            kind = identity.stringValue("kind"),
            workItemId = identity.stringValue("workItemId"),
        )
    }.getOrElse { throw IllegalStateException("需求资料 manifest 身份损坏：$path", it) }

    fun readIteration(path: Path): IterationDocumentationManifest = runCatching {
        manifestJson.decodeFromString<IterationDocumentationManifest>(read(path))
    }.getOrElse { throw IllegalStateException("迭代 manifest 损坏：$path", it) }

    fun readIndex(root: Path): List<RequirementDocumentationIndexEntry> {
        val index = root.resolve(INDEX_FILE)
        if (!exists(index)) return emptyList()
        return read(index).lineSequence()
            .filter(String::isNotBlank)
            .mapIndexed { indexNumber, line ->
                runCatching { indexJson.decodeFromString<RequirementDocumentationIndexEntry>(line) }
                    .getOrElse { throw IllegalStateException("需求资料索引第 ${indexNumber + 1} 行损坏", it) }
            }
            .toList()
    }

    fun updateIndex(root: Path, directory: Path, manifest: RequirementDocumentationManifest) {
        val relative = root.relativize(directory).toString()
        val next = (readIndex(root).filterNot { it.identity == manifest.identity } + RequirementDocumentationIndexEntry(
            identity = manifest.identity,
            requirementDirectory = relative,
            sprint = manifest.sprint,
            updatedAt = manifest.updatedAt,
        )).sortedBy { it.identity.stableKey }
        write(root.resolve(INDEX_FILE), next.joinToString("\n") { indexJson.encodeToString(it) } + "\n")
    }

    fun encodeRequirement(manifest: RequirementDocumentationManifest): String = manifestJson.encodeToString(manifest)

    fun encodeIteration(manifest: IterationDocumentationManifest): String = manifestJson.encodeToString(manifest)

    private companion object {
        const val INDEX_FILE = ".silverwing-requirement-index.jsonl"
    }

    private fun JsonObject.stringValue(name: String): String =
        this[name]?.jsonPrimitive?.content?.trim()?.takeIf(String::isNotEmpty)
            ?: throw IllegalArgumentException("manifest 需求身份缺少字段：$name")
}
