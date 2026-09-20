package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.LinkOption.NOFOLLOW_LINKS
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardCopyOption
import java.nio.file.attribute.BasicFileAttributes
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.Comparator
import java.util.UUID

/** One plugin listed by Codex from a configured Marketplace snapshot. */
@Serializable
data class CodexPluginCatalogItem(
    val name: String,
    val description: String = "",
    val version: String? = null,
    val installed: Boolean = false,
    val bundledSkills: List<String> = emptyList(),
)

/** Last successful state for one SilverWing-managed Marketplace source. */
@Serializable
data class CodexPluginMarketplaceSnapshot(
    val sourceId: String,
    val marketplaceName: String? = null,
    /** Last directory reported by Codex for this Marketplace snapshot, when available. */
    val cacheDirectory: String? = null,
    val plugins: List<CodexPluginCatalogItem> = emptyList(),
    val updatedAt: String? = null,
    val error: String? = null,
)

/** One parsed standalone Skill found in a cloned source repository. */
@Serializable
data class ExternalSkillCatalogItem(
    val name: String,
    val description: String,
    /** Relative to the cloned source root, retained for a clear UI explanation. */
    val sourcePath: String,
)

/** Last successful state for one SilverWing-managed external Skill source. */
@Serializable
data class ExternalSkillSourceSnapshot(
    val sourceId: String,
    val skills: List<ExternalSkillCatalogItem> = emptyList(),
    val updatedAt: String? = null,
    val error: String? = null,
)

data class CodexExtensionsSnapshot(
    val marketplaces: Map<String, CodexPluginMarketplaceSnapshot> = emptyMap(),
    val skillSources: Map<String, ExternalSkillSourceSnapshot> = emptyMap(),
)

/** Ownership is deliberately explicit so SilverWing never removes someone else's installation by accident. */
enum class CodexExtensionOwnership {
    NOT_INSTALLED,
    MANAGED,
    EXTERNAL,
}

class CodexExtensionTakeoverRequiredException(message: String) : IllegalStateException(message)

/**
 * Local, user-scoped manager for Codex Marketplace and standalone Skill sources.
 *
 * Its state lives below [ApplicationPaths.codex], rather than in the exported
 * configuration shards: source definitions are portable; machine-local Codex
 * registrations, checked-out repositories, ownership records and backups are not.
 */
class CodexExtensionsService(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val gitExecutable: () -> String = { "git" },
    private val codexExecutable: () -> String = { "codex" },
    private val userHome: () -> Path = { Path.of(System.getProperty("user.home")) },
    private val clock: Clock = Clock.systemUTC(),
) {
    private val stateLock = Any()
    private val json = Json {
        prettyPrint = true
        encodeDefaults = true
        ignoreUnknownKeys = true
    }
    private var inMemoryState: ExtensionsRuntimeState? = null

    /** Reads cached status only; this method never invokes Codex or Git. */
    fun snapshot(
        pluginSources: List<CodexPluginMarketplaceSource>,
        skillSources: List<SkillSource>,
    ): CodexExtensionsSnapshot = synchronized(stateLock) {
        val state = loadState()
        CodexExtensionsSnapshot(
            marketplaces = pluginSources.associate { source ->
                source.id to (state.marketplaces[source.id] ?: CodexPluginMarketplaceSnapshot(source.id))
            },
            skillSources = skillSources.associate { source ->
                source.id to (state.skillSources[source.id] ?: ExternalSkillSourceSnapshot(source.id))
            },
        )
    }

    /** Registers then loads one Marketplace. Failures become a retryable cached status. */
    fun addMarketplace(source: CodexPluginMarketplaceSource): CodexPluginMarketplaceSnapshot = synchronized(stateLock) {
        refreshMarketplaceLocked(source, registerWhenMissing = true, upgrade = false)
    }

    /** Updates one already-configured Marketplace, or registers it if its local mapping was lost. */
    fun refreshMarketplace(source: CodexPluginMarketplaceSource): CodexPluginMarketplaceSnapshot = synchronized(stateLock) {
        refreshMarketplaceLocked(source, registerWhenMissing = true, upgrade = true)
    }

    /** Removes only the Marketplace source. Installed plugins remain installed until explicitly removed. */
    fun removeMarketplace(source: CodexPluginMarketplaceSource) = synchronized(stateLock) {
        val state = loadState()
        val marketplace = state.marketplaces[source.id]?.marketplaceName
        if (!marketplace.isNullOrBlank()) {
            runCodex("plugin", "marketplace", "remove", marketplace, "--json")
        }
        persist(
            state.copy(
                marketplaces = state.marketplaces - source.id,
                managedPlugins = state.managedPlugins.filterNot { it.sourceId == source.id },
            ),
        )
    }

    fun pluginOwnership(sourceId: String, plugin: CodexPluginCatalogItem): CodexExtensionOwnership = synchronized(stateLock) {
        val managed = loadState().managedPlugins.any { it.sourceId == sourceId && it.pluginName == plugin.name }
        when {
            managed -> CodexExtensionOwnership.MANAGED
            plugin.installed -> CodexExtensionOwnership.EXTERNAL
            else -> CodexExtensionOwnership.NOT_INSTALLED
        }
    }

    /** Installs a plugin, or records an explicit takeover of an existing installation. */
    fun installPlugin(
        source: CodexPluginMarketplaceSource,
        pluginName: String,
        takeOver: Boolean = false,
    ): CodexPluginMarketplaceSnapshot = synchronized(stateLock) {
        require(pluginName.isNotBlank()) { "插件名称不能为空" }
        val state = loadState()
        val status = requireNotNull(state.marketplaces[source.id]) { "请先加载插件来源“${source.name}”" }
        val marketplace = requireNotNull(status.marketplaceName) { "插件来源“${source.name}”尚未成功注册到 Codex" }
        val plugin = status.plugins.firstOrNull { it.name == pluginName }
            ?: throw IllegalArgumentException("插件来源“${source.name}”中找不到 $pluginName")
        val ownership = pluginOwnership(source.id, plugin)
        if (ownership == CodexExtensionOwnership.EXTERNAL && !takeOver) {
            throw CodexExtensionTakeoverRequiredException("$pluginName 已由外部方式安装；确认接管后才能由 SilverWing 卸载它")
        }
        if (ownership == CodexExtensionOwnership.NOT_INSTALLED) {
            runCodex("plugin", "add", pluginName, "--marketplace", marketplace, "--json")
        }
        val updatedState = loadState()
        val updatedStatus = updatedState.marketplaces[source.id] ?: status
        val changed = updatedStatus.copy(
            plugins = updatedStatus.plugins.map {
                if (it.name == pluginName) it.copy(installed = true) else it
            },
            error = null,
        )
        persist(
            updatedState.copy(
                marketplaces = updatedState.marketplaces + (source.id to changed),
                managedPlugins = updatedState.managedPlugins
                    .filterNot { it.sourceId == source.id && it.pluginName == pluginName } +
                    ManagedPluginInstallation(source.id, pluginName, now()),
            ),
        )
        changed
    }

    /** Removes only a plugin that SilverWing previously installed or explicitly took over. */
    fun uninstallPlugin(source: CodexPluginMarketplaceSource, pluginName: String): CodexPluginMarketplaceSnapshot = synchronized(stateLock) {
        val state = loadState()
        val status = requireNotNull(state.marketplaces[source.id]) { "请先加载插件来源“${source.name}”" }
        val marketplace = requireNotNull(status.marketplaceName) { "插件来源“${source.name}”尚未成功注册到 Codex" }
        require(state.managedPlugins.any { it.sourceId == source.id && it.pluginName == pluginName }) {
            "$pluginName 不是 SilverWing 管理的插件；请先确认接管"
        }
        runCodex("plugin", "remove", pluginName, "--marketplace", marketplace, "--json")
        val changed = status.copy(
            plugins = status.plugins.map { if (it.name == pluginName) it.copy(installed = false) else it },
            error = null,
        )
        persist(
            state.copy(
                marketplaces = state.marketplaces + (source.id to changed),
                managedPlugins = state.managedPlugins.filterNot { it.sourceId == source.id && it.pluginName == pluginName },
            ),
        )
        changed
    }

    /** Clones/fetches a source to the app cache and discovers standalone Skills without executing repository code. */
    fun addSkillSource(source: SkillSource): ExternalSkillSourceSnapshot = refreshSkillSource(source)

    fun refreshSkillSource(source: SkillSource): ExternalSkillSourceSnapshot = synchronized(stateLock) {
        val old = loadState().skillSources[source.id] ?: ExternalSkillSourceSnapshot(source.id)
        try {
            val checkout = ensureSkillCheckout(source)
            val skills = discoverSkills(source, checkout)
            val status = ExternalSkillSourceSnapshot(source.id, skills, now(), null)
            val state = loadState()
            persist(state.copy(skillSources = state.skillSources + (source.id to status)))
            status
        } catch (error: Throwable) {
            val status = old.copy(error = detail(error))
            val state = loadState()
            persist(state.copy(skillSources = state.skillSources + (source.id to status)))
            status
        }
    }

    /** Removes the private checkout/cache only. Installed Skills and their ownership records are retained. */
    fun removeSkillSource(source: SkillSource) = synchronized(stateLock) {
        val checkout = skillCheckout(source)
        deleteTreeWithin(paths.codex.resolve(SKILL_SOURCES_DIRECTORY), checkout)
        val state = loadState()
        persist(state.copy(skillSources = state.skillSources - source.id))
    }

    fun skillOwnership(sourceId: String, skillName: String): CodexExtensionOwnership = synchronized(stateLock) {
        val destination = skillDestination(skillName)
        if (!Files.exists(destination, NOFOLLOW_LINKS)) return CodexExtensionOwnership.NOT_INSTALLED
        return if (loadState().managedSkills.any { it.sourceId == sourceId && it.skillName == skillName }) {
            CodexExtensionOwnership.MANAGED
        } else {
            CodexExtensionOwnership.EXTERNAL
        }
    }

    /** Copies an entire discovered Skill to the user Skill root after an explicit takeover when needed. */
    fun installSkill(source: SkillSource, skillName: String, takeOver: Boolean = false): ExternalSkillSourceSnapshot = synchronized(stateLock) {
        val state = loadState()
        val status = requireNotNull(state.skillSources[source.id]) { "请先加载 Skill 来源“${source.name}”" }
        val skill = status.skills.firstOrNull { it.name == skillName }
            ?: throw IllegalArgumentException("Skill 来源“${source.name}”中找不到 $skillName")
        val ownership = skillOwnership(source.id, skillName)
        if (ownership == CodexExtensionOwnership.EXTERNAL && !takeOver) {
            throw CodexExtensionTakeoverRequiredException("$skillName 已由外部方式安装；确认接管后才能覆盖它")
        }
        val checkout = skillCheckout(source)
        val sourceDirectory = safeChild(checkout, skill.sourcePath, "Skill 来源路径")
        validateSkillDirectory(sourceDirectory, skillName)
        installSkillDirectory(sourceDirectory, skillName)
        val updatedState = loadState()
        persist(
            updatedState.copy(
                managedSkills = updatedState.managedSkills
                    .filterNot { it.skillName == skillName } +
                    ManagedSkillInstallation(source.id, skillName, now()),
            ),
        )
        loadState().skillSources[source.id] ?: status
    }

    /** Uninstalls exactly one managed Skill, preserving a recoverable backup if it still exists. */
    fun uninstallSkill(source: SkillSource, skillName: String): ExternalSkillSourceSnapshot = synchronized(stateLock) {
        val state = loadState()
        require(state.managedSkills.any { it.sourceId == source.id && it.skillName == skillName }) {
            "$skillName 不是 SilverWing 管理的 Skill"
        }
        val destination = skillDestination(skillName)
        if (Files.exists(destination, NOFOLLOW_LINKS)) {
            backupSkillDestination(destination, skillName)
            deleteExactSkillDirectory(destination)
        }
        persist(
            state.copy(
                managedSkills = state.managedSkills.filterNot { it.sourceId == source.id && it.skillName == skillName },
            ),
        )
        return loadState().skillSources[source.id] ?: ExternalSkillSourceSnapshot(source.id)
    }

    private fun refreshMarketplaceLocked(
        source: CodexPluginMarketplaceSource,
        registerWhenMissing: Boolean,
        upgrade: Boolean,
    ): CodexPluginMarketplaceSnapshot {
        val state = loadState()
        val old = state.marketplaces[source.id] ?: CodexPluginMarketplaceSnapshot(source.id)
        return try {
            val marketplaceName = old.marketplaceName ?: if (registerWhenMissing) registerMarketplace(source) else null
            require(!marketplaceName.isNullOrBlank()) { "无法识别 Codex Marketplace 名称" }
            if (upgrade) runCodex("plugin", "marketplace", "upgrade", marketplaceName, "--json")
            val plugins = listMarketplacePlugins(marketplaceName)
            val cacheDirectory = listMarketplaces().firstOrNull { it.name == marketplaceName }?.cacheDirectory ?: old.cacheDirectory
            val status = old.copy(
                marketplaceName = marketplaceName,
                cacheDirectory = cacheDirectory,
                plugins = plugins,
                updatedAt = now(),
                error = null,
            )
            persist(loadState().copy(marketplaces = loadState().marketplaces + (source.id to status)))
            status
        } catch (error: Throwable) {
            val status = old.copy(error = detail(error))
            persist(state.copy(marketplaces = state.marketplaces + (source.id to status)))
            status
        }
    }

    private fun registerMarketplace(source: CodexPluginMarketplaceSource): String {
        val before = listMarketplaces()
        val command = buildList {
            addAll(listOf("plugin", "marketplace", "add", source.repositoryUrl))
            source.ref?.let { addAll(listOf("--ref", it)) }
            if (source.marketplaceDirectory != ".") addAll(listOf("--sparse", source.marketplaceDirectory))
            add("--json")
        }
        val added = runCodex(*command.toTypedArray())
        val after = listMarketplaces()
        val normalizedSource = normalizeRemoteForMatch(source.repositoryUrl)
        val matching = after.firstOrNull { record ->
            record.source?.let(::normalizeRemoteForMatch) == normalizedSource
        }?.name
        val addedNames = after.map(CodexMarketplaceRecord::name).toSet() - before.map(CodexMarketplaceRecord::name).toSet()
        return matching ?: addedNames.singleOrNull() ?: parseMarketplaceList(added.stdout)
            .firstOrNull { it.name.isNotBlank() }?.name
            ?: error("Codex 已执行 Marketplace 添加，但未返回可识别的 Marketplace 名称")
    }

    private fun listMarketplaces(): List<CodexMarketplaceRecord> = parseMarketplaceList(
        runCodex("plugin", "marketplace", "list", "--json").stdout,
    )

    private fun listMarketplacePlugins(marketplaceName: String): List<CodexPluginCatalogItem> = parseCodexPluginList(
        runCodex("plugin", "list", "--marketplace", marketplaceName, "--available", "--json").stdout,
    )

    private fun ensureSkillCheckout(source: SkillSource): Path {
        val root = paths.codex.resolve(SKILL_SOURCES_DIRECTORY)
        val checkout = skillCheckout(source)
        Files.createDirectories(root)
        if (!Files.isDirectory(checkout.resolve(".git"), NOFOLLOW_LINKS)) {
            if (Files.exists(checkout, NOFOLLOW_LINKS)) deleteTreeWithin(root, checkout)
            val clone = buildList {
                add(gitExecutable())
                addAll(listOf("-c", "core.symlinks=false", "clone", "--depth", "1"))
                source.ref?.let { addAll(listOf("--branch", it)) }
                add(source.repositoryUrl)
                add(checkout.toString())
            }
            runGit(clone)
        } else if (source.ref.isNullOrBlank()) {
            runGit(listOf(gitExecutable(), "-C", checkout.toString(), "pull", "--ff-only"))
        } else {
            runGit(listOf(gitExecutable(), "-C", checkout.toString(), "fetch", "--depth", "1", "origin", source.ref))
            runGit(listOf(gitExecutable(), "-C", checkout.toString(), "checkout", "--force", "FETCH_HEAD"))
        }
        return checkout
    }

    private fun discoverSkills(source: SkillSource, checkout: Path): List<ExternalSkillCatalogItem> {
        val roots = source.skillRoot?.let { listOf(safeChild(checkout, it, "Skill 目录")) }
            ?: listOf(checkout.resolve(".agents").resolve("skills"), checkout.resolve("skills"))
        val discovered = roots.filter { Files.isDirectory(it, NOFOLLOW_LINKS) && !Files.isSymbolicLink(it) }
            .flatMap { root -> discoverSkillDirectories(root).map { directory -> parseSkill(directory, checkout) } }
            .sortedBy(ExternalSkillCatalogItem::name)
        require(discovered.map(ExternalSkillCatalogItem::name).distinct().size == discovered.size) {
            "Skill 来源“${source.name}”包含重复的 Skill 名称"
        }
        return discovered
    }

    private fun discoverSkillDirectories(root: Path): List<Path> {
        if (Files.isRegularFile(root.resolve(SKILL_FILE), NOFOLLOW_LINKS)) return listOf(root)
        return Files.list(root).use { entries ->
            entries.filter { directory ->
                Files.isDirectory(directory, NOFOLLOW_LINKS) &&
                    !Files.isSymbolicLink(directory) &&
                    Files.isRegularFile(directory.resolve(SKILL_FILE), NOFOLLOW_LINKS)
            }.toList()
        }
    }

    private fun parseSkill(directory: Path, checkout: Path): ExternalSkillCatalogItem {
        validateSkillDirectory(directory)
        val content = readSkillFile(directory)
        val frontMatter = SKILL_FRONT_MATTER.find(content)?.groupValues?.get(1)
            ?: throw IllegalArgumentException("Skill 缺少 YAML frontmatter：$directory")
        val name = frontMatterValue(frontMatter, "name")
            ?: throw IllegalArgumentException("Skill 缺少 name：$directory")
        val description = frontMatterValue(frontMatter, "description")
            ?: throw IllegalArgumentException("Skill 缺少 description：$directory")
        require(name.matches(SKILL_NAME_PATTERN)) { "Skill name 不合法：$name" }
        require(description.isNotBlank()) { "Skill description 不能为空：$name" }
        val sourcePath = checkout.relativize(directory).toString().replace('\\', '/').ifBlank { "." }
        return ExternalSkillCatalogItem(name, description, sourcePath)
    }

    private fun frontMatterValue(frontMatter: String, key: String): String? =
        Regex("(?m)^${Regex.escape(key)}:\\s*(.+?)\\s*$")
            .find(frontMatter)
            ?.groupValues
            ?.get(1)
            ?.trim()
            ?.removeSurrounding("\"")
            ?.removeSurrounding("'")
            ?.takeIf(String::isNotBlank)

    private fun validateSkillDirectory(directory: Path, expectedName: String? = null) {
        require(Files.isDirectory(directory, NOFOLLOW_LINKS) && !Files.isSymbolicLink(directory)) {
            "Skill 目录不存在或不是普通目录：$directory"
        }
        require(Files.isRegularFile(directory.resolve(SKILL_FILE), NOFOLLOW_LINKS)) { "Skill 缺少 SKILL.md：$directory" }
        visitSkillTree(directory) { _, _ -> }
        expectedName?.let { require(parseSkillName(directory) == it) { "Skill 名称不匹配：$directory" } }
    }

    private fun parseSkillName(directory: Path): String {
        val content = readSkillFile(directory)
        val frontMatter = SKILL_FRONT_MATTER.find(content)?.groupValues?.get(1)
            ?: throw IllegalArgumentException("Skill 缺少 YAML frontmatter：$directory")
        return frontMatterValue(frontMatter, "name") ?: throw IllegalArgumentException("Skill 缺少 name：$directory")
    }

    private fun readSkillFile(directory: Path): String {
        val skillFile = directory.resolve(SKILL_FILE)
        require(Files.size(skillFile) <= MAX_SKILL_FILE_BYTES) { "Skill 文件过大，无法安全读取：$directory" }
        return Files.readString(skillFile)
    }

    private fun installSkillDirectory(source: Path, skillName: String) {
        val destination = skillDestination(skillName)
        val parent = requireNotNull(destination.parent)
        Files.createDirectories(parent)
        require(Files.isDirectory(parent, NOFOLLOW_LINKS) && !Files.isSymbolicLink(parent)) {
            "Skill 安装目录必须是普通目录：$parent"
        }
        val backup = if (Files.exists(destination, NOFOLLOW_LINKS)) backupSkillDestination(destination, skillName) else null
        val staging = parent.resolve(".$skillName-silverwing-staging-${UUID.randomUUID()}")
        try {
            copyDirectory(source, staging)
            validateSkillDirectory(staging, skillName)
            if (Files.exists(destination, NOFOLLOW_LINKS)) deleteExactSkillDirectory(destination)
            moveDirectory(staging, destination)
        } catch (error: Throwable) {
            runCatching { deleteExactSkillDirectory(staging) }
            if (backup != null && !Files.exists(destination, NOFOLLOW_LINKS)) {
                runCatching { copyBackup(backup, destination) }
            }
            throw error
        }
    }

    private fun backupSkillDestination(destination: Path, skillName: String): Path {
        val backupRoot = paths.codex.resolve(SKILL_BACKUPS_DIRECTORY)
        Files.createDirectories(backupRoot)
        val backup = backupRoot.resolve("$skillName-${Instant.now(clock).toEpochMilli()}-${UUID.randomUUID()}")
        if (Files.isSymbolicLink(destination) || !Files.isDirectory(destination, NOFOLLOW_LINKS)) {
            Files.copy(destination, backup, StandardCopyOption.REPLACE_EXISTING, LinkOption.NOFOLLOW_LINKS)
        } else {
            copyDirectoryTree(destination, backup)
        }
        return backup
    }

    private fun copyDirectory(source: Path, target: Path) {
        validateSkillDirectory(source)
        copySkillDirectoryTree(source, target)
    }

    /** Copies a validated Skill but omits Git metadata when the repository root itself is the Skill. */
    private fun copySkillDirectoryTree(source: Path, target: Path) {
        require(Files.isDirectory(source, NOFOLLOW_LINKS) && !Files.isSymbolicLink(source)) { "源目录不存在或不是普通目录：$source" }
        require(!Files.exists(target, NOFOLLOW_LINKS)) { "目标已存在：$target" }
        val normalizedTarget = target.toAbsolutePath().normalize()
        visitSkillTree(source) { entry, directory ->
            val relative = source.relativize(entry)
            val destination = target.resolve(relative.toString()).normalize()
            require(destination.toAbsolutePath().normalize().startsWith(normalizedTarget)) { "Skill 路径越界：$entry" }
            if (directory) {
                Files.createDirectories(destination)
            } else {
                Files.createDirectories(requireNotNull(destination.parent))
                Files.copy(entry, destination, StandardCopyOption.REPLACE_EXISTING)
            }
        }
    }

    private fun visitSkillTree(root: Path, visit: (Path, Boolean) -> Unit) {
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun preVisitDirectory(directory: Path, attributes: BasicFileAttributes): FileVisitResult {
                if (directory != root && directory.fileName.toString() == ".git") return FileVisitResult.SKIP_SUBTREE
                require(!Files.isSymbolicLink(directory)) { "Skill 不允许包含符号链接：$directory" }
                visit(directory, true)
                return FileVisitResult.CONTINUE
            }

            override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                require(!Files.isSymbolicLink(file)) { "Skill 不允许包含符号链接：$file" }
                visit(file, false)
                return FileVisitResult.CONTINUE
            }
        })
    }

    /** Copies an arbitrary ordinary directory without following links; used for recoverable external backups. */
    private fun copyDirectoryTree(source: Path, target: Path) {
        require(Files.isDirectory(source, NOFOLLOW_LINKS) && !Files.isSymbolicLink(source)) { "源目录不存在或不是普通目录：$source" }
        require(!Files.exists(target, NOFOLLOW_LINKS)) { "目标已存在：$target" }
        val normalizedTarget = target.toAbsolutePath().normalize()
        Files.walk(source).use { entries ->
            entries.forEach { entry ->
                require(!Files.isSymbolicLink(entry)) { "不允许复制符号链接：$entry" }
                val relative = source.relativize(entry)
                val destination = target.resolve(relative.toString()).normalize()
                require(destination.toAbsolutePath().normalize().startsWith(normalizedTarget)) { "Skill 路径越界：$entry" }
                if (Files.isDirectory(entry, NOFOLLOW_LINKS)) {
                    Files.createDirectories(destination)
                } else {
                    Files.createDirectories(requireNotNull(destination.parent))
                    Files.copy(entry, destination, StandardCopyOption.REPLACE_EXISTING)
                }
            }
        }
    }

    private fun copyBackup(source: Path, target: Path) {
        require(!Files.exists(target, NOFOLLOW_LINKS)) { "目标已存在：$target" }
        if (Files.isSymbolicLink(source) || !Files.isDirectory(source, NOFOLLOW_LINKS)) {
            Files.copy(source, target, StandardCopyOption.REPLACE_EXISTING, LinkOption.NOFOLLOW_LINKS)
        } else {
            copyDirectoryTree(source, target)
        }
    }

    private fun moveDirectory(source: Path, target: Path) {
        try {
            Files.move(source, target, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
            Files.move(source, target)
        }
    }

    private fun deleteExactSkillDirectory(path: Path) {
        val normalizedRoot = userHome().resolve(".agents").resolve("skills").toAbsolutePath().normalize()
        if (Files.exists(normalizedRoot, NOFOLLOW_LINKS)) {
            require(Files.isDirectory(normalizedRoot, NOFOLLOW_LINKS) && !Files.isSymbolicLink(normalizedRoot)) {
                "Skill 安装目录必须是普通目录：$normalizedRoot"
            }
        }
        val normalized = path.toAbsolutePath().normalize()
        require(normalized.parent == normalizedRoot) { "只能删除用户 Skill 根目录下的单个 Skill：$path" }
        deleteTree(path)
    }

    private fun deleteTreeWithin(root: Path, target: Path) {
        val normalizedRoot = root.toAbsolutePath().normalize()
        val normalizedTarget = target.toAbsolutePath().normalize()
        require(normalizedTarget.startsWith(normalizedRoot) && normalizedTarget != normalizedRoot) { "删除目标超出 SilverWing 缓存目录：$target" }
        deleteTree(target)
    }

    private fun deleteTree(path: Path) {
        if (!Files.exists(path, NOFOLLOW_LINKS)) return
        if (Files.isSymbolicLink(path) || !Files.isDirectory(path, NOFOLLOW_LINKS)) {
            Files.deleteIfExists(path)
            return
        }
        Files.walk(path).use { entries ->
            entries.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists)
        }
    }

    private fun skillCheckout(source: SkillSource): Path {
        val root = paths.codex.resolve(SKILL_SOURCES_DIRECTORY).toAbsolutePath().normalize()
        val checkout = root.resolve(source.id).normalize()
        require(checkout.startsWith(root) && checkout != root) { "Skill 缓存路径不安全" }
        return checkout
    }

    private fun skillDestination(skillName: String): Path {
        require(skillName.matches(SKILL_NAME_PATTERN)) { "Skill 名称不合法：$skillName" }
        val normalizedRoot = userHome().resolve(".agents").resolve("skills").toAbsolutePath().normalize()
        val destination = normalizedRoot.resolve(skillName).normalize()
        require(destination.parent == normalizedRoot) { "Skill 安装路径不安全：$skillName" }
        return destination
    }

    private fun safeChild(root: Path, relative: String, label: String): Path {
        validateExtensionRelativePath(relative, label)
        val resolved = root.resolve(relative).normalize()
        require(resolved.startsWith(root.toAbsolutePath().normalize())) { "$label 超出仓库范围" }
        return resolved
    }

    private fun runCodex(vararg arguments: String): CommandResult {
        val command = listOf(codexExecutable()) + arguments
        val result = runner.run(command, timeout = CODEX_TIMEOUT)
        if (!result.succeeded) throw CodexExtensionCommandException("Codex 命令失败：${command.joinToString(" ")}", result)
        return result
    }

    private fun runGit(command: List<String>): CommandResult {
        val result = runner.run(command, timeout = GIT_TIMEOUT)
        if (!result.succeeded) throw CodexExtensionCommandException("Git 命令失败：${command.drop(1).joinToString(" ")}", result)
        return result
    }

    private fun loadState(): ExtensionsRuntimeState {
        inMemoryState?.let { return it }
        val loaded = runCatching {
            val path = statePath()
            if (!Files.isRegularFile(path, NOFOLLOW_LINKS)) ExtensionsRuntimeState()
            else json.decodeFromString<ExtensionsRuntimeState>(Files.readString(path))
        }.getOrDefault(ExtensionsRuntimeState())
        inMemoryState = loaded
        return loaded
    }

    private fun persist(state: ExtensionsRuntimeState) {
        val path = statePath()
        Files.createDirectories(requireNotNull(path.parent))
        val temporary = Files.createTempFile(requireNotNull(path.parent), ".extensions-", ".json.tmp")
        try {
            Files.writeString(temporary, json.encodeToString(state))
            try {
                Files.move(temporary, path, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
            } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
                Files.move(temporary, path, StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
        inMemoryState = state
    }

    private fun statePath(): Path = paths.codex.resolve(STATE_FILE)

    private fun now(): String = SilverWingTime.format(Instant.now(clock))

    private fun detail(error: Throwable): String = when (error) {
        is CodexExtensionCommandException -> error.result.stderr.ifBlank { error.result.stdout }
            .trim()
            .ifBlank { error.message ?: "命令执行失败" }
            .take(MAX_ERROR_LENGTH)
        else -> (error.message ?: error::class.simpleName ?: "未知错误").take(MAX_ERROR_LENGTH)
    }

    private companion object {
        const val STATE_FILE = "extensions-state.json"
        const val SKILL_SOURCES_DIRECTORY = "skill-sources"
        const val SKILL_BACKUPS_DIRECTORY = "skill-backups"
        const val SKILL_FILE = "SKILL.md"
        const val MAX_SKILL_FILE_BYTES = 512 * 1024
        const val MAX_ERROR_LENGTH = 4_000
        val CODEX_TIMEOUT: Duration = Duration.ofMinutes(2)
        val GIT_TIMEOUT: Duration = Duration.ofMinutes(5)
        val SKILL_NAME_PATTERN = Regex("[A-Za-z0-9][A-Za-z0-9._-]{0,63}")
        val SKILL_FRONT_MATTER = Regex("(?s)\\A---\\R(.*?)\\R---(?:\\R|$)")
    }
}

class CodexExtensionCommandException(
    message: String,
    val result: CommandResult,
) : RuntimeException("$message\n${result.stderr.ifBlank { result.stdout }}")

@Serializable
private data class ExtensionsRuntimeState(
    val schema: Int = 1,
    val marketplaces: Map<String, CodexPluginMarketplaceSnapshot> = emptyMap(),
    val skillSources: Map<String, ExternalSkillSourceSnapshot> = emptyMap(),
    val managedPlugins: List<ManagedPluginInstallation> = emptyList(),
    val managedSkills: List<ManagedSkillInstallation> = emptyList(),
)

@Serializable
private data class ManagedPluginInstallation(
    val sourceId: String,
    val pluginName: String,
    val managedAt: String,
)

@Serializable
private data class ManagedSkillInstallation(
    val sourceId: String,
    val skillName: String,
    val managedAt: String,
)

internal data class CodexMarketplaceRecord(
    val name: String,
    val source: String? = null,
    val cacheDirectory: String? = null,
)

/** Parses Codex CLI Marketplace JSON while remaining tolerant of additive CLI fields. */
internal fun parseMarketplaceList(output: String): List<CodexMarketplaceRecord> {
    val root = parseCodexJson(output)
    val objects = namedObjectArray(root, "marketplaces", "items", "sources") ?: root.asObjectList()
    return objects.mapNotNull { item ->
        val name = item.string("name", "marketplace", "id") ?: return@mapNotNull null
        val source = item.string("source", "url", "repositoryUrl") ?: item.objectValue("source")?.string("url", "repositoryUrl", "path")
        val cacheDirectory = item.string("cacheDirectory", "cache_path", "cachePath", "resolvedPath", "root", "directory", "path")
        CodexMarketplaceRecord(name, source, cacheDirectory)
    }.distinctBy(CodexMarketplaceRecord::name)
}

/** Parses `codex plugin list --available --json`; unknown fields are intentionally ignored. */
fun parseCodexPluginList(output: String): List<CodexPluginCatalogItem> {
    val root = parseCodexJson(output)
    val objects = namedObjectArray(root, "plugins", "items") ?: root.asObjectList()
    return objects.mapNotNull { item ->
        val nested = item.objectValue("plugin") ?: item
        val name = nested.string("name", "id") ?: return@mapNotNull null
        val description = nested.string("description", "summary") ?: nested.objectValue("interface")?.string("description", "shortDescription").orEmpty()
        val version = nested.string("version", "latestVersion")
        val installed = nested.boolean("installed", "isInstalled") ?: when (nested.string("status")?.lowercase()) {
            "installed", "enabled" -> true
            else -> nested.boolean("enabled") ?: false
        }
        val bundledSkills = nested.arrayValue("skills")?.mapNotNull { element ->
            when (element) {
                is JsonPrimitive -> element.contentOrNull
                is JsonObject -> element.string("name", "id")
                else -> null
            }
        }.orEmpty()
        CodexPluginCatalogItem(name, description, version, installed, bundledSkills.distinct())
    }.distinctBy(CodexPluginCatalogItem::name).sortedBy(CodexPluginCatalogItem::name)
}

private fun parseCodexJson(output: String): JsonElement = try {
    Json.parseToJsonElement(output.trim())
} catch (error: Throwable) {
    throw IllegalArgumentException("Codex 未返回可识别的 JSON", error)
}

private fun namedObjectArray(root: JsonElement, vararg names: String): List<JsonObject>? {
    val objectRoot = root as? JsonObject ?: return null
    return names.firstNotNullOfOrNull { name -> objectRoot[name]?.asObjectList() }
}

private fun JsonElement.asObjectList(): List<JsonObject> = when (this) {
    is JsonArray -> mapNotNull { it as? JsonObject }
    is JsonObject -> listOf(this)
    else -> emptyList()
}

private fun JsonObject.string(vararg names: String): String? = names.firstNotNullOfOrNull { name ->
    (this[name] as? JsonPrimitive)?.contentOrNull?.takeIf(String::isNotBlank)
}

private fun JsonObject.boolean(vararg names: String): Boolean? = names.firstNotNullOfOrNull { name ->
    (this[name] as? JsonPrimitive)?.booleanOrNull
}

private fun JsonObject.objectValue(name: String): JsonObject? = this[name] as? JsonObject

private fun JsonObject.arrayValue(name: String): JsonArray? = this[name] as? JsonArray

private fun normalizeRemoteForMatch(value: String): String = value.trim().removeSuffix("/").removeSuffix(".git").lowercase()
