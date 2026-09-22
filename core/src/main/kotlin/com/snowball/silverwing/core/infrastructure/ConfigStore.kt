package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.Locale
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.locks.ReentrantLock
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream
import kotlin.io.path.createDirectories
import kotlin.io.path.exists
import kotlin.io.path.name
import kotlin.concurrent.withLock

private const val CONFIG_SHARD_SCHEMA = 2

class UnsupportedConfigVersionException(
    val actualVersion: String?,
) : IllegalStateException(
    "配置 schema 不受支持：${actualVersion ?: "缺少 schema"}；当前分片 schema 为 $CONFIG_SHARD_SCHEMA。silverwing 不读取或改写旧配置。",
)

class DefaultTaskRootInitializationException(
    val taskRoot: Path,
    cause: Throwable,
) : IllegalStateException("无法创建默认任务根目录：$taskRoot；请在设置中选择可写目录", cause)

/** Strict release-versioned configuration repository backed by independent JSON shards. */
interface ConfigurationRepository {
    fun load(): AppConfig
    fun save(config: AppConfig)

    fun update(transform: (AppConfig) -> AppConfig): AppConfig {
        val updated = transform(load())
        save(updated)
        return load()
    }
}

class ConfigStore(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    json: Json = Json {
        prettyPrint = true
        encodeDefaults = true
    },
    private val initializeDefaultTaskRoot: (Path) -> Unit = { it.createDirectories() },
    /** Isolated so the failure rollback path can be verified without faulting the whole filesystem. */
    private val replaceShard: (Path, Path) -> Unit = ::moveAtomically,
) : ConfigurationRepository {
    private val json = Json(json) {
        prettyPrint = true
        encodeDefaults = true
        ignoreUnknownKeys = false
        isLenient = false
    }
    private val processMutationLock = mutationLocks.computeIfAbsent(
        paths.config.toAbsolutePath().normalize().toString().lowercase(Locale.ROOT),
    ) { ReentrantLock() }

    data class Backup(val path: Path, val modifiedAtMillis: Long)
    data class FileSnapshot(
        val path: Path,
        val exists: Boolean,
        val content: String? = null,
        val readError: String? = null,
    )
    data class ImportPreview(
        val source: Path,
        val changes: List<String>,
        val invalidDevelopmentTools: List<DevelopmentToolType>,
    )

    /** True only when every required shard is present. */
    fun exists(): Boolean = SHARD_NAMES.all { Files.isRegularFile(shardPath(it)) }

    /**
     * Reads every present shard verbatim for the read-only desktop preview.
     * It deliberately never parses or repairs the configuration.
     */
    fun fileSnapshot(): FileSnapshot {
        val path = paths.config.toAbsolutePath().normalize()
        if (!Files.exists(path)) return FileSnapshot(path = path, exists = false)
        return runCatching {
            if (!Files.isDirectory(path)) return@runCatching FileSnapshot(
                path = path,
                exists = true,
                content = Files.readString(path),
            )
            val content = Files.list(path).use { entries ->
                entries.filter(Files::isRegularFile)
                    .filter { !isShardStagingFile(it) }
                    .toList()
                    .sortedBy { it.name }
                    .joinToString("\n\n") { file -> "[${file.name}]\n${Files.readString(file)}" }
            }
            FileSnapshot(path = path, exists = true, content = content)
        }.getOrElse { error ->
            FileSnapshot(
                path = path,
                exists = true,
                readError = error.message ?: error::class.simpleName ?: "无法读取配置目录",
            )
        }
    }

    override fun load(): AppConfig {
        if (!hasConfigurationData()) return initializeDefaults()
        return readCurrent().config
    }

    private fun initializeDefaults(): AppConfig = withMutationLock {
        if (hasConfigurationData()) return@withMutationLock load()
        val taskRoot = paths.tasks.toAbsolutePath().normalize()
        try {
            initializeDefaultTaskRoot(taskRoot)
        } catch (error: Exception) {
            writeShards(shardsFor(AppConfig()), SHARD_NAMES)
            throw DefaultTaskRootInitializationException(taskRoot, error)
        }
        val initial = AppConfig(taskRoot = taskRoot.toString())
        writeShards(shardsFor(initial), SHARD_NAMES)
        initial
    }

    override fun save(config: AppConfig) = withMutationLock {
        saveUnlocked(config)
    }

    override fun update(transform: (AppConfig) -> AppConfig): AppConfig = withMutationLock {
        val updated = transform(load())
        saveUnlocked(updated)
        load()
    }

    private fun saveUnlocked(config: AppConfig) {
        val target = shardsFor(config)
        if (!hasConfigurationData()) {
            writeShards(target, SHARD_NAMES)
            return
        }
        val current = readCurrent()
        val changed = changedShardNames(current.shards, target)
        if (changed.isEmpty()) return
        createBackup(current.files)
        writeShards(target, changed)
    }

    /** Explicit ZIP import/restore may replace malformed but complete shard sets. */
    private fun replaceCurrentUnlocked(imported: DecodedConfig): AppConfig {
        if (!hasConfigurationData()) {
            writeShards(imported.shards, SHARD_NAMES)
            return load()
        }
        val currentFiles = readShardFiles()
        val changed = runCatching { changedShardNames(decodeShards(currentFiles).shards, imported.shards) }
            .getOrDefault(SHARD_NAMES)
        if (changed.isEmpty()) return imported.config
        createBackup(currentFiles)
        writeShards(imported.shards, changed)
        return load()
    }

    fun backups(): List<Backup> {
        if (!paths.backups.exists()) return emptyList()
        return Files.list(paths.backups).use { stream ->
            stream.filter { it.name.startsWith(BACKUP_PREFIX) && it.name.endsWith(ZIP_EXTENSION) }
                .map { Backup(it, Files.getLastModifiedTime(it).toMillis()) }
                .sorted(Comparator.comparingLong<Backup> { it.modifiedAtMillis }.reversed())
                .toList()
        }
    }

    fun restore(backup: Path): AppConfig {
        val normalized = backup.toAbsolutePath().normalize()
        require(normalized.parent == paths.backups.toAbsolutePath().normalize()) { "只能恢复 silverwing 配置备份目录中的文件" }
        require(normalized.name.startsWith(BACKUP_PREFIX) && normalized.name.endsWith(ZIP_EXTENSION)) {
            "只能恢复 silverwing 配置 ZIP 备份"
        }
        return withMutationLock {
            replaceCurrentUnlocked(readArchive(normalized))
        }
    }

    /** Exports the complete shard set as one standard ZIP archive. */
    fun exportTo(target: Path): Path {
        val normalized = requireZip(target, "导出目标")
        // Capture all bytes under the same cross-process lock as writes.  ZIP creation
        // deliberately happens afterwards, so a slow export never blocks a save.
        val snapshot = withMutationLock {
            readCurrent().files.mapValues { (_, content) -> content.copyOf() }
        }
        writeZip(normalized, snapshot)
        return normalized
    }

    fun importFrom(source: Path): AppConfig {
        val normalized = requireZip(source, "导入配置")
        return withMutationLock {
            replaceCurrentUnlocked(readArchive(normalized))
        }
    }

    fun previewImport(source: Path): ImportPreview {
        val imported = readArchive(requireZip(source, "导入配置")).config
        val current = if (hasConfigurationData()) decodeCurrentForPreview() else AppConfig()
        val changes = buildList {
            if (current == null) {
                add("当前配置已损坏；确认后将先归档完整分片，再以导入配置替换")
                return@buildList
            }
            if (current.taskRoot != imported.taskRoot) add("任务路径：${current.taskRoot.orEmpty()} → ${imported.taskRoot.orEmpty()}")
            if (current.requirementMaterialsRoot != imported.requirementMaterialsRoot) {
                add("需求资料根路径：${current.requirementMaterialsRoot.orEmpty()} → ${imported.requirementMaterialsRoot.orEmpty()}")
            }
            if (current.requirementMaterialsSubdirectory != imported.requirementMaterialsSubdirectory) {
                add("需求资料子目录：${current.requirementMaterialsSubdirectory.orEmpty()} → ${imported.requirementMaterialsSubdirectory.orEmpty()}")
            }
            if (current.groups.size != imported.groups.size) add("任务组：${current.groups.size} → ${imported.groups.size}")
            if (current.repositories.size != imported.repositories.size) add("仓库：${current.repositories.size} → ${imported.repositories.size}")
            if (current.developmentTools != imported.developmentTools) add("开发工具配置将更新")
            if (current.meegleProjects != imported.meegleProjects) add("飞书项目：${current.meegleProjects.size} → ${imported.meegleProjects.size}")
            if (current.aiRequirementNamingEnabled != imported.aiRequirementNamingEnabled) {
                add("AI 需求命名：${if (current.aiRequirementNamingEnabled) "开启" else "关闭"} → ${if (imported.aiRequirementNamingEnabled) "开启" else "关闭"}")
            }
            if (current.tagEnabled != imported.tagEnabled) {
                add("全局测试Tag：${if (current.tagEnabled) "开启" else "关闭"} → ${if (imported.tagEnabled) "开启" else "关闭"}")
            }
            if (current.tagHistoryMaxGroups != imported.tagHistoryMaxGroups) {
                add("Tag历史保留组数：${current.tagHistoryMaxGroups} → ${imported.tagHistoryMaxGroups}")
            }
            if (isEmpty() && current != imported) add("配置内容存在其他变化")
        }
        val invalidTools = imported.developmentTools.filterNot { runCatching { Files.exists(Path.of(it.path)) }.getOrDefault(false) }
            .map(DevelopmentToolConfig::type)
        return ImportPreview(source.toAbsolutePath().normalize(), changes, invalidTools)
    }

    private fun decodeCurrentForPreview(): AppConfig? = try {
        readCurrent().config
    } catch (error: UnsupportedConfigVersionException) {
        throw error
    } catch (_: SerializationException) {
        null
    } catch (_: IllegalArgumentException) {
        null
    }

    private fun readCurrent(): DecodedConfig = decodeShards(readShardFiles())

    private fun readShardFiles(): Map<String, ByteArray> {
        require(Files.isDirectory(paths.config)) { "配置目录不存在或不是目录：${paths.config}" }
        val entries = Files.list(paths.config).use { stream -> stream.filter { !isShardStagingFile(it) }.toList() }
        val names = entries.map { it.name }.toSet()
        require(names.all { it in SHARD_NAMES }) { "配置目录包含未知文件" }
        require(entries.all(Files::isRegularFile)) { "配置目录只能包含配置分片文件" }
        val missing = SHARD_NAMES.filterNot(names::contains)
        require(missing.isEmpty()) { "配置分片缺失：${missing.joinToString()}" }
        return SHARD_NAMES.associateWith { Files.readAllBytes(shardPath(it)) }
    }

    private fun readArchive(source: Path): DecodedConfig {
        require(Files.isRegularFile(source)) { "配置 ZIP 不存在：$source" }
        val files = linkedMapOf<String, ByteArray>()
        ZipInputStream(Files.newInputStream(source)).use { zip ->
            while (true) {
                val entry = zip.nextEntry ?: break
                val name = entry.name
                require(isSafeZipEntry(name)) { "配置 ZIP 包含不安全条目：$name" }
                require(!entry.isDirectory) { "配置 ZIP 不允许目录条目：$name" }
                require(name in SHARD_NAMES) { "配置 ZIP 包含未知条目：$name" }
                require(files.put(name, zip.readAllBytes()) == null) { "配置 ZIP 包含重复条目：$name" }
                zip.closeEntry()
            }
        }
        val missing = SHARD_NAMES.filterNot(files::containsKey)
        require(missing.isEmpty()) { "配置 ZIP 缺少条目：${missing.joinToString()}" }
        return decodeShards(files)
    }

    private fun decodeShards(files: Map<String, ByteArray>): DecodedConfig {
        val layout = decodeShard<LayoutShard>(LAYOUT_FILE, files.getValue(LAYOUT_FILE))
        val workspace = decodeShard<WorkspaceShard>(WORKSPACE_FILE, files.getValue(WORKSPACE_FILE))
        val services = decodeShard<ServicesShard>(SERVICES_FILE, files.getValue(SERVICES_FILE))
        val tag = decodeShard<TagShard>(TAG_FILE, files.getValue(TAG_FILE))
        val tools = decodeShard<ToolsShard>(TOOLS_FILE, files.getValue(TOOLS_FILE))
        val git = decodeShard<GitShard>(GIT_FILE, files.getValue(GIT_FILE))
        val integrations = decodeShard<IntegrationsShard>(INTEGRATIONS_FILE, files.getValue(INTEGRATIONS_FILE))
        val appearance = decodeShard<AppearanceShard>(APPEARANCE_FILE, files.getValue(APPEARANCE_FILE))
        val servicesByGroup = services.groups.associateBy(GroupServicesShard::groupId)
        require(servicesByGroup.size == services.groups.size) { "services.json 包含重复组 ID" }
        require(servicesByGroup.keys == layout.groups.map(GroupLayoutShard::id).toSet()) {
            "layout.json 与 services.json 的组 ID 不一致"
        }
        val groups = layout.groups.map { group ->
            val serviceGroup = requireNotNull(servicesByGroup[group.id])
            GroupConfig(
                id = group.id,
                name = group.name,
                tagEnabled = group.tagEnabled,
                services = serviceGroup.services,
                defaultBranchPrefix = group.defaultBranchPrefix,
                defaultWorkspaceToolIds = group.defaultWorkspaceToolIds,
            )
        }
        val config = AppConfig(
            schemaVersion = CURRENT_APP_CONFIG_SCHEMA_VERSION,
            taskRoot = workspace.taskRoot,
            repositories = workspace.repositories,
            groups = groups,
            theme = appearance.theme,
            tagEnabled = tag.tagEnabled,
            tagHistoryMaxGroups = tag.tagHistoryMaxGroups,
            terminalExecutable = tools.terminalExecutable,
            developmentTools = tools.developmentTools,
            defaultDevelopmentTool = tools.defaultDevelopmentTool,
            allowTemporaryDevelopmentToolSelection = tools.allowTemporaryDevelopmentToolSelection,
            showTaskDetailGitActionGroup = appearance.showTaskDetailGitActionGroup,
            showTaskDetailPathActionGroup = appearance.showTaskDetailPathActionGroup,
            showWorkspaceGitActionGroup = appearance.showWorkspaceGitActionGroup,
            showWorkspacePathActionGroup = appearance.showWorkspacePathActionGroup,
            showTaskAreaCopyIcons = appearance.showTaskAreaCopyIcons,
            showTaskAreaBranchCopyIcons = appearance.showTaskAreaBranchCopyIcons,
            showTaskAreaRequirementCopyIcons = appearance.showTaskAreaRequirementCopyIcons,
            showTaskAreaProjectNameCopyIcons = appearance.showTaskAreaProjectNameCopyIcons,
            blockedGitWriteBranches = git.blockedGitWriteBranches,
            meegleProjects = integrations.meegleProjects,
            meegleExecutablePath = integrations.meegleExecutablePath,
            gitExecutablePath = git.gitExecutablePath,
            genbuExecutablePath = integrations.genbuExecutablePath,
            genbuExecutableAutoDetected = integrations.genbuExecutableAutoDetected,
            larkExecutablePath = integrations.larkExecutablePath,
            requirementMaterialsRoot = integrations.requirementMaterialsRoot,
            requirementMaterialsSubdirectory = integrations.requirementMaterialsSubdirectory,
            codexPluginMarketplaceSources = integrations.codexPluginMarketplaceSources,
            skillSources = integrations.skillSources,
            aiRequirementNamingEnabled = integrations.aiRequirementNamingEnabled,
        )
        return DecodedConfig(
            config = config,
            shards = Shards(layout, workspace, services, tag, tools, git, integrations, appearance),
            files = files,
        )
    }

    private inline fun <reified T> decodeShard(name: String, bytes: ByteArray): T {
        val element = json.parseToJsonElement(bytes.toString(Charsets.UTF_8))
        val schema = element.jsonObject[SCHEMA_FIELD]?.jsonPrimitive?.intOrNull
        if (schema != CONFIG_SHARD_SCHEMA) throw UnsupportedConfigVersionException(schema?.toString())
        return json.decodeFromJsonElement(element)
    }

    private fun shardsFor(config: AppConfig): Shards {
        if (config.schemaVersion != CURRENT_APP_CONFIG_SCHEMA_VERSION) {
            throw UnsupportedConfigVersionException(config.schemaVersion)
        }
        return Shards(
            layout = LayoutShard(
                groups = config.groups.map {
                    GroupLayoutShard(it.id, it.name, it.tagEnabled, it.defaultBranchPrefix, it.defaultWorkspaceToolIds)
                },
            ),
            workspace = WorkspaceShard(taskRoot = config.taskRoot, repositories = config.repositories),
            services = ServicesShard(groups = config.groups.map { GroupServicesShard(it.id, it.services) }),
            tag = TagShard(tagEnabled = config.tagEnabled, tagHistoryMaxGroups = config.tagHistoryMaxGroups),
            tools = ToolsShard(
                terminalExecutable = config.terminalExecutable,
                developmentTools = config.developmentTools,
                defaultDevelopmentTool = config.defaultDevelopmentTool,
                allowTemporaryDevelopmentToolSelection = config.allowTemporaryDevelopmentToolSelection,
            ),
            git = GitShard(blockedGitWriteBranches = config.blockedGitWriteBranches, gitExecutablePath = config.gitExecutablePath),
            integrations = IntegrationsShard(
                meegleProjects = config.meegleProjects,
                meegleExecutablePath = config.meegleExecutablePath,
                genbuExecutablePath = config.genbuExecutablePath,
                genbuExecutableAutoDetected = config.genbuExecutableAutoDetected,
                larkExecutablePath = config.larkExecutablePath,
                requirementMaterialsRoot = config.requirementMaterialsRoot,
                requirementMaterialsSubdirectory = config.requirementMaterialsSubdirectory,
                codexPluginMarketplaceSources = config.codexPluginMarketplaceSources,
                skillSources = config.skillSources,
                aiRequirementNamingEnabled = config.aiRequirementNamingEnabled,
            ),
            appearance = AppearanceShard(
                theme = config.theme,
                showTaskDetailGitActionGroup = config.showTaskDetailGitActionGroup,
                showTaskDetailPathActionGroup = config.showTaskDetailPathActionGroup,
                showWorkspaceGitActionGroup = config.showWorkspaceGitActionGroup,
                showWorkspacePathActionGroup = config.showWorkspacePathActionGroup,
                showTaskAreaCopyIcons = config.showTaskAreaCopyIcons,
                showTaskAreaBranchCopyIcons = config.showTaskAreaBranchCopyIcons,
                showTaskAreaRequirementCopyIcons = config.showTaskAreaRequirementCopyIcons,
                showTaskAreaProjectNameCopyIcons = config.showTaskAreaProjectNameCopyIcons,
            ),
        )
    }

    private fun changedShardNames(current: Shards, target: Shards): List<String> = buildList {
        if (current.layout != target.layout) add(LAYOUT_FILE)
        if (current.workspace != target.workspace) add(WORKSPACE_FILE)
        if (current.services != target.services) add(SERVICES_FILE)
        if (current.tag != target.tag) add(TAG_FILE)
        if (current.tools != target.tools) add(TOOLS_FILE)
        if (current.git != target.git) add(GIT_FILE)
        if (current.integrations != target.integrations) add(INTEGRATIONS_FILE)
        if (current.appearance != target.appearance) add(APPEARANCE_FILE)
    }

    private fun writeShards(shards: Shards, names: Collection<String>) {
        paths.config.createDirectories()
        val selectedNames = names.distinct()
        val originals = selectedNames.associateWith { name ->
            val target = shardPath(name)
            if (Files.exists(target)) Files.readAllBytes(target) else null
        }
        val temporary = linkedMapOf<String, Path>()
        try {
            selectedNames.forEach { name ->
                temporary[name] = Files.createTempFile(
                    paths.config,
                    "$SHARD_STAGING_PREFIX${name.removeSuffix(".json")}-",
                    SHARD_STAGING_SUFFIX,
                )
            }
            temporary.forEach { (name, path) -> Files.write(path, encodeShard(name, shards)) }
            val replaced = mutableListOf<String>()
            try {
                temporary.forEach { (name, path) ->
                    // Record before the move: a filesystem implementation may move then
                    // report an error, and that target must still be restored.
                    replaced += name
                    replaceShard(path, shardPath(name))
                }
            } catch (failure: Throwable) {
                rollbackShards(originals, replaced, failure)
                throw failure
            }
        } finally {
            temporary.values.forEach { path -> runCatching { Files.deleteIfExists(path) } }
        }
    }

    /** Restores already-attempted targets in reverse order after a replacement failure. */
    private fun rollbackShards(
        originals: Map<String, ByteArray?>,
        replaced: List<String>,
        originalFailure: Throwable,
    ) {
        replaced.asReversed().forEach { name ->
            runCatching {
                val target = shardPath(name)
                val original = originals.getValue(name)
                if (original == null) {
                    Files.deleteIfExists(target)
                } else {
                    val staging = Files.createTempFile(
                        paths.config,
                        "${SHARD_STAGING_PREFIX}rollback-${name.removeSuffix(".json")}-",
                        SHARD_STAGING_SUFFIX,
                    )
                    try {
                        Files.write(staging, original)
                        moveAtomically(staging, target)
                    } finally {
                        Files.deleteIfExists(staging)
                    }
                }
            }.exceptionOrNull()?.let(originalFailure::addSuppressed)
        }
    }

    private fun encodeShard(name: String, shards: Shards): ByteArray = when (name) {
        LAYOUT_FILE -> json.encodeToString(shards.layout)
        WORKSPACE_FILE -> json.encodeToString(shards.workspace)
        SERVICES_FILE -> json.encodeToString(shards.services)
        TAG_FILE -> json.encodeToString(shards.tag)
        TOOLS_FILE -> json.encodeToString(shards.tools)
        GIT_FILE -> json.encodeToString(shards.git)
        INTEGRATIONS_FILE -> json.encodeToString(shards.integrations)
        APPEARANCE_FILE -> json.encodeToString(shards.appearance)
        else -> error("未知配置分片：$name")
    }.toByteArray(Charsets.UTF_8)

    private fun createBackup(files: Map<String, ByteArray>) {
        paths.backups.createDirectories()
        val temporary = Files.createTempFile(paths.backups, "$BACKUP_PREFIX${System.currentTimeMillis()}-", ".zip.tmp")
        val target = temporary.resolveSibling(temporary.fileName.toString().removeSuffix(".tmp"))
        try {
            writeZip(target, files, temporary)
        } finally {
            Files.deleteIfExists(temporary)
        }
        backups().drop(MAX_BACKUPS).forEach { Files.deleteIfExists(it.path) }
    }

    private fun writeZip(target: Path, files: Map<String, ByteArray>, temporary: Path? = null) {
        val normalized = target.toAbsolutePath().normalize()
        normalized.parent?.createDirectories()
        val staging = temporary ?: Files.createTempFile(normalized.parent, ".config-", ".zip.tmp")
        try {
            ZipOutputStream(Files.newOutputStream(staging)).use { zip ->
                SHARD_NAMES.forEach { name ->
                    zip.putNextEntry(ZipEntry(name))
                    zip.write(files.getValue(name))
                    zip.closeEntry()
                }
            }
            moveAtomically(staging, normalized)
        } finally {
            if (temporary == null) Files.deleteIfExists(staging)
        }
    }

    private fun requireZip(path: Path, description: String): Path {
        val normalized = path.toAbsolutePath().normalize()
        require(normalized.name.endsWith(ZIP_EXTENSION, ignoreCase = true)) { "${description}必须是 ZIP 文件" }
        return normalized
    }

    private fun hasConfigurationData(): Boolean = when {
        !Files.exists(paths.config) -> false
        !Files.isDirectory(paths.config) -> true
        else -> Files.list(paths.config).use { entries -> entries.anyMatch { !isShardStagingFile(it) } }
    }

    private fun shardPath(name: String): Path = paths.config.resolve(name)

    private fun <T> withMutationLock(block: () -> T): T = processMutationLock.withLock {
        FileLocking.withExclusiveLock(
            paths.locks.resolve(CONFIG_LOCK_FILE),
            "配置正在被另一个 silverwing 实例修改，请稍后重试",
            block,
        )
    }

    private companion object {
        const val SCHEMA_FIELD = "schema"
        const val LAYOUT_FILE = "layout.json"
        const val WORKSPACE_FILE = "workspace.json"
        const val SERVICES_FILE = "services.json"
        const val TAG_FILE = "tag.json"
        const val TOOLS_FILE = "tools.json"
        const val GIT_FILE = "git.json"
        const val INTEGRATIONS_FILE = "integrations.json"
        const val APPEARANCE_FILE = "appearance.json"
        val SHARD_NAMES = listOf(
            LAYOUT_FILE,
            WORKSPACE_FILE,
            SERVICES_FILE,
            TAG_FILE,
            TOOLS_FILE,
            GIT_FILE,
            INTEGRATIONS_FILE,
            APPEARANCE_FILE,
        )
        const val BACKUP_PREFIX = "silverwing-config-"
        const val ZIP_EXTENSION = ".zip"
        const val MAX_BACKUPS = 10
        const val CONFIG_LOCK_FILE = "config.lock"
        const val SHARD_STAGING_PREFIX = ".silverwing-shard-"
        const val SHARD_STAGING_SUFFIX = ".json.tmp"
        val mutationLocks = ConcurrentHashMap<String, ReentrantLock>()
    }
}

private fun isShardStagingFile(path: Path): Boolean =
    path.name.startsWith(".silverwing-shard-") && path.name.endsWith(".json.tmp")

private fun moveAtomically(source: Path, target: Path) {
    try {
        Files.move(source, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
    } catch (_: AtomicMoveNotSupportedException) {
        Files.move(source, target, StandardCopyOption.REPLACE_EXISTING)
    }
}

private fun isSafeZipEntry(name: String): Boolean =
    name.isNotBlank() &&
        !name.startsWith('/') &&
        !name.startsWith('\\') &&
        !name.contains('\\') &&
        !name.contains(':') &&
        name.split('/').all { it != "." && it != ".." } &&
        !Path.of(name).isAbsolute

@Serializable
private data class LayoutShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val groups: List<GroupLayoutShard> = listOf(GroupLayoutShard(DEFAULT_GROUP_ID, DEFAULT_GROUP_NAME)),
)

@Serializable
private data class GroupLayoutShard(
    val id: String,
    val name: String,
    val tagEnabled: Boolean = true,
    val defaultBranchPrefix: String = "",
    val defaultWorkspaceToolIds: List<String> = emptyList(),
)

@Serializable
private data class WorkspaceShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val taskRoot: String? = null,
    val repositories: List<RepositoryConfig> = emptyList(),
)

@Serializable
private data class ServicesShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val groups: List<GroupServicesShard> = listOf(GroupServicesShard(DEFAULT_GROUP_ID)),
)

@Serializable
private data class GroupServicesShard(
    val groupId: String,
    val services: List<GroupServiceConfig> = emptyList(),
)

@Serializable
private data class TagShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val tagEnabled: Boolean = true,
    val tagHistoryMaxGroups: Int = DEFAULT_TAG_HISTORY_MAX_GROUPS,
)

@Serializable
private data class ToolsShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val terminalExecutable: String? = null,
    val developmentTools: List<DevelopmentToolConfig> = emptyList(),
    val defaultDevelopmentTool: DevelopmentToolType = DevelopmentToolType.INTELLIJ_IDEA,
    val allowTemporaryDevelopmentToolSelection: Boolean = false,
)

@Serializable
private data class GitShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val blockedGitWriteBranches: List<String> = listOf("master", "main"),
    val gitExecutablePath: String? = null,
)

@Serializable
private data class IntegrationsShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    /** Schema 2 requires an explicit value; schema 1 configurations are rejected before decode. */
    val aiRequirementNamingEnabled: Boolean,
    val meegleProjects: List<MeegleProjectConfig> = emptyList(),
    val meegleExecutablePath: String? = null,
    val genbuExecutablePath: String? = null,
    val genbuExecutableAutoDetected: Boolean = false,
    val larkExecutablePath: String? = null,
    val requirementMaterialsRoot: String? = null,
    val requirementMaterialsSubdirectory: String? = null,
    val codexPluginMarketplaceSources: List<CodexPluginMarketplaceSource> = emptyList(),
    val skillSources: List<SkillSource> = emptyList(),
)

@Serializable
private data class AppearanceShard(
    val schema: Int = CONFIG_SHARD_SCHEMA,
    val theme: ThemePreference = ThemePreference.SYSTEM,
    val showTaskDetailGitActionGroup: Boolean = false,
    val showTaskDetailPathActionGroup: Boolean = false,
    val showWorkspaceGitActionGroup: Boolean = false,
    val showWorkspacePathActionGroup: Boolean = false,
    val showTaskAreaCopyIcons: Boolean = true,
    val showTaskAreaBranchCopyIcons: Boolean = true,
    val showTaskAreaRequirementCopyIcons: Boolean = true,
    val showTaskAreaProjectNameCopyIcons: Boolean = true,
)

private data class Shards(
    val layout: LayoutShard,
    val workspace: WorkspaceShard,
    val services: ServicesShard,
    val tag: TagShard,
    val tools: ToolsShard,
    val git: GitShard,
    val integrations: IntegrationsShard,
    val appearance: AppearanceShard,
)

private data class DecodedConfig(
    val config: AppConfig,
    val shards: Shards,
    val files: Map<String, ByteArray> = emptyMap(),
)

/** Agent planning accepts no legacy configuration and has no write path. */
class AgentCompatibleConfigurationRepository(
    paths: ApplicationPaths = ApplicationPaths.systemDefault(),
) : ConfigurationRepository {
    private val delegate = ConfigStore(paths = paths)

    override fun load(): AppConfig = delegate.load()

    override fun save(config: AppConfig): Nothing =
        throw UnsupportedOperationException("Agent CLI 不会写入 silverwing 配置")
}
