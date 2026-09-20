package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import java.nio.file.Path
import java.time.Clock
import java.time.Instant

@Serializable
data class RequirementIdentity(
    val space: String,
    val kind: String,
    val workItemId: String,
) {
    init {
        require(space.matches(Regex("[A-Za-z0-9_-]+"))) { "需求空间不合法" }
        require(kind in setOf("userstory", "technical", "bug", "othertask")) { "需求类型不合法" }
        require(workItemId.matches(Regex("\\d+"))) { "需求 ID 不合法" }
    }

    val stableKey: String get() = "$space/$kind/$workItemId"
}

@Serializable
data class RequirementDocumentationManifest(
    val formatVersion: Int = 1,
    val identity: RequirementIdentity,
    /** Title is used in Markdown only; it never controls the directory name. */
    val requirementTitle: String,
    val sprint: RequirementSprintSnapshot,
    val directoryName: String,
    val createdAt: String,
    val updatedAt: String,
)

@Serializable
data class RequirementSprintSnapshot(
    val id: String,
    val label: String,
)

@Serializable
data class IterationDocumentationManifest(
    val formatVersion: Int = 1,
    val sprint: RequirementSprintSnapshot,
    val createdAt: String,
)

@Serializable
data class RequirementDocumentationIndexEntry(
    val identity: RequirementIdentity,
    /** Relative to the configured materials root; never an absolute user path. */
    val requirementDirectory: String,
    val sprint: RequirementSprintSnapshot,
    val updatedAt: String,
)

@Serializable
data class RequirementDocumentationPlan(
    val identity: RequirementIdentity,
    val requirementTitle: String,
    val sprint: RequirementSprintSnapshot,
    /** The configured materials subdirectory (the Agent writeRoot). */
    val documentationDirectory: String,
    /** The Sprint directory immediately containing the requirement directory. */
    val iterationDirectory: String,
    val reusedHistoricalDirectory: Boolean,
)

data class RequirementDocumentationMaterialization(
    val plan: RequirementDocumentationPlan,
    val agentContext: AgentTaskContext,
    val createdRequirementDirectory: Boolean,
)

/**
 * Agent-only process-document materializer for the shared requirement
 * materials hierarchy. Desktop task creation owns the directory itself via
 * [RequirementMaterialsService]; this service only adds process documents in
 * the already-resolved writeRoot during Agent apply.
 */
class RequirementDocumentationService(
    private val iterations: RequirementIterationProvider = MeegleRequirementIterationProvider(),
    private val metadata: RequirementMetadataProvider = MeegleRequirementMetadataProvider(),
    private val clock: Clock = Clock.systemUTC(),
    private val materials: RequirementMaterialsService = RequirementMaterialsService(),
    private val files: RequirementDocumentationFileStore = RequirementDocumentationFileStore(),
) {
    /**
     * [directoryFolderName] is intentionally separate from [requestedTitle].
     * The former is the task folder supplied by the caller; the latter is only
     * the title shown in process Markdown. The default preserves the old API.
     */
    fun plan(
        config: AppConfig,
        requirementLink: String,
        requestedTitle: String?,
        directoryFolderName: String? = requestedTitle,
    ): RequirementDocumentationPlan {
        val root = materials.requireMaterialsRoot(config.requirementMaterialsRoot)
        val subdirectory = materials.requireMaterialsSubdirectory(config.requirementMaterialsSubdirectory)
        val identity = materials.parseRequirementIdentity(requirementLink).getOrElse { error ->
            throw IllegalArgumentException("需求链接不是支持的飞书项目工作项链接", error)
        }

        // Directory matching, identity validation and locking are owned by the
        // materials service. An existing desktop directory is a complete reuse
        // decision; planning must not call Meegle just to rediscover its Sprint.
        return materials.withMaterialsLock(root) {
            val existingContext = materials.resolveExistingRequirementDirectory(
                root = root,
                id = identity.workItemId,
                subdirectory = subdirectory,
                requirementInput = requirementLink,
                readSprint = { path -> if (files.isRegularFile(path)) files.readIteration(path).sprint else null },
            )
            existingContext?.let { context ->
                val historical = findHistorical(root, identity, subdirectory)
                require(historical.size <= 1) {
                    "本地需求资料目录发现多个历史目录，无法安全复用：${historical.joinToString { it.context.writeRoot.toString() }}"
                }
                historical.singleOrNull()?.let { return@withMaterialsLock planFromExisting(it) }

                val sprint = context.sprint
                val title = requestedTitle?.trim()?.takeIf(String::isNotBlank)
                    ?: directoryTitle(context.requirementDirectory, identity.workItemId)
                return@withMaterialsLock RequirementDocumentationPlan(
                    identity = identity,
                    requirementTitle = title,
                    sprint = sprint,
                    documentationDirectory = context.writeRoot.toString(),
                    iterationDirectory = context.iterationDirectory.toString(),
                    reusedHistoricalDirectory = true,
                )
            }

            val projectKey = config.meegleProjects
                .firstOrNull { it.simpleName.equals(identity.space, ignoreCase = true) }
                ?.projectKey
                ?: materials.resolveRequirementProjectKey(requirementLink)
                ?: throw IllegalStateException("需求空间 ${identity.space} 未配置 Meegle project key")
            val sprint = resolveRequirementSprint(requirementLink, projectKey)
            val title = resolveTitle(requirementLink, projectKey, requestedTitle)
            val requirementDirectory = materialsDirectory(root, sprint.label, identity.workItemId, directoryFolderName)
            val context = materials.validatePlannedDirectory(
                root = root,
                requirementDirectory = requirementDirectory,
                writeRoot = requirementDirectory.resolve(subdirectory),
                iterationDirectory = requirementDirectory.parent
                    ?: throw IllegalArgumentException("需求目录缺少 Sprint 目录"),
                id = identity.workItemId,
                subdirectory = subdirectory,
                sprint = sprint,
            )
            RequirementDocumentationPlan(
                identity = identity,
                requirementTitle = title,
                sprint = sprint,
                documentationDirectory = context.writeRoot.toString(),
                iterationDirectory = context.iterationDirectory.toString(),
                reusedHistoricalDirectory = false,
            )
        }
    }

    fun materialize(config: AppConfig, plan: RequirementDocumentationPlan): RequirementDocumentationMaterialization {
        val root = materials.requireMaterialsRoot(config.requirementMaterialsRoot)
        val subdirectory = materials.requireMaterialsSubdirectory(config.requirementMaterialsSubdirectory)
        val expectedContext = materials.validatePlannedDirectory(
            root = root,
            requirementDirectory = Path.of(plan.documentationDirectory).parent
                ?: throw IllegalArgumentException("计划中的需求资料写入目录缺少需求目录"),
            writeRoot = Path.of(plan.documentationDirectory),
            iterationDirectory = Path.of(plan.iterationDirectory),
            id = plan.identity.workItemId,
            subdirectory = subdirectory,
            sprint = plan.sprint,
        )

        return materials.withMaterialsLock(root) {
            // Re-scan while holding the same lock used by desktop creation. A
            // plan is intentionally only a snapshot: a desktop task (or a
            // second Agent invocation) may have created the requirement
            // directory between plan and apply. Never create the planned
            // directory as a second copy in that case.
            val existingContext = materials.resolveExistingRequirementDirectory(
                root = root,
                id = plan.identity.workItemId,
                subdirectory = subdirectory,
                requirementInput = "https://project.feishu.cn/${plan.identity.space}/${plan.identity.kind}/detail/${plan.identity.workItemId}",
                readSprint = { path -> if (files.isRegularFile(path)) files.readIteration(path).sprint else null },
            )
            val current = findHistorical(root, plan.identity, subdirectory)
            require(current.size <= 1) {
                "本地需求资料目录发现多个历史目录，无法安全复用：${current.joinToString { it.context.requirementDirectory.toString() }}"
            }
            current.singleOrNull()?.let { existing ->
                return@withMaterialsLock materialization(existing, created = false)
            }

            // A desktop-created materials directory has no process manifest;
            // it is safe to add Agent files after validating its contents. If
            // it differs from the plan, derive Sprint/title from that existing
            // path rather than using stale plan metadata.
            val context = existingContext ?: expectedContext
            val requirementDirectory = context.requirementDirectory
            val writeRoot = context.writeRoot
            val iterationDirectory = context.iterationDirectory
            val title = if (existingContext != null) {
                plan.requirementTitle.takeIf(String::isNotBlank) ?: directoryTitle(requirementDirectory, plan.identity.workItemId)
            } else {
                plan.requirementTitle
            }
            val requirementDirectoryAlreadyExists = files.exists(requirementDirectory)
            if (requirementDirectoryAlreadyExists) {
                require(files.isDirectory(requirementDirectory)) {
                    "需求目录不是目录：$requirementDirectory"
                }
                require(!files.isSymbolicLink(requirementDirectory)) { "需求目录不能是符号链接：$requirementDirectory" }
            }
            val sprint = ensureIteration(iterationDirectory, context.sprint)
            materials.ensureMaterialsDirectory(root, requirementDirectory)
            materials.ensureMaterialsDirectory(root, writeRoot)

            val now = SilverWingTime.format(Instant.now(clock))
            val manifest = RequirementDocumentationManifest(
                identity = plan.identity,
                requirementTitle = title,
                sprint = sprint,
                directoryName = requirementDirectory.fileName?.toString().orEmpty(),
                createdAt = now,
                updatedAt = now,
            )
            files.write(writeRoot.resolve(REQUIREMENT_MANIFEST), files.encodeRequirement(manifest))
            val overview = writeRoot.resolve(REQUIREMENT_OVERVIEW)
            if (!files.exists(overview)) {
                files.write(overview, renderRequirementOverview(manifest))
            }
            appendIterationOverview(iterationDirectory, manifest, subdirectory)
            files.updateIndex(root, requirementDirectory, manifest)
            materialization(
                HistoricalRequirement(
                    RequirementMaterialsDirectoryContext(requirementDirectory, writeRoot, iterationDirectory, sprint),
                    manifest,
                ),
                created = !requirementDirectoryAlreadyExists,
            )
        }
    }

    private fun planFromExisting(existing: HistoricalRequirement): RequirementDocumentationPlan =
        RequirementDocumentationPlan(
            identity = existing.manifest.identity,
            requirementTitle = existing.manifest.requirementTitle,
            sprint = existing.manifest.sprint,
            documentationDirectory = existing.context.writeRoot.toString(),
            iterationDirectory = existing.context.iterationDirectory.toString(),
            reusedHistoricalDirectory = true,
        )

    private fun materialization(
        existing: HistoricalRequirement,
        created: Boolean,
    ): RequirementDocumentationMaterialization {
        val agentContext = AgentTaskContext(
            documentationDirectory = existing.context.writeRoot.toString(),
            iterationLabel = existing.manifest.sprint.label,
        )
        return RequirementDocumentationMaterialization(
            plan = RequirementDocumentationPlan(
                identity = existing.manifest.identity,
                requirementTitle = existing.manifest.requirementTitle,
                sprint = existing.manifest.sprint,
                documentationDirectory = existing.context.writeRoot.toString(),
                iterationDirectory = existing.context.iterationDirectory.toString(),
                reusedHistoricalDirectory = !created,
            ),
            agentContext = agentContext,
            createdRequirementDirectory = created,
        )
    }

    private fun resolveTitle(requirementLink: String, projectKey: String, requestedTitle: String?): String =
        requestedTitle?.trim()?.takeIf(String::isNotBlank)
            ?: metadata.fetch(requirementLink, projectKey)?.title?.trim()?.takeIf(String::isNotBlank)
            ?: throw IllegalArgumentException("需要提供需求中文简写，或确保本地 Meegle 能读取需求标题")

    private fun resolveRequirementSprint(requirementLink: String, projectKey: String): RequirementSprintSnapshot {
        return materials.resolveMaterialsSprint(iterations.resolve(requirementLink, projectKey))
    }

    private fun materialsDirectory(root: Path, sprintLabel: String, id: String, folderName: String?): Path {
        return materials.buildRequirementDirectory(root, sprintLabel, id, folderName)
    }

    private fun ensureIteration(directory: Path, sprint: RequirementSprintSnapshot): RequirementSprintSnapshot {
        val manifestPath = directory.resolve(ITERATION_MANIFEST)
        if (files.exists(directory)) {
            require(files.isDirectory(directory)) { "迭代路径不是目录：$directory" }
            require(!files.isSymbolicLink(directory)) { "迭代目录不能是符号链接：$directory" }
            if (files.isRegularFile(manifestPath)) {
                val existing = files.readIteration(manifestPath)
                require(existing.sprint.label == sprint.label &&
                    (existing.sprint.id.isBlank() || sprint.id.isBlank() || existing.sprint.id == sprint.id)) {
                    "迭代目录与当前 Sprint 不匹配，已停止写入：$directory"
                }
                // Desktop reuse only knows the label. Enrich its marker when a
                // later requirement resolves the ID, without losing a known ID.
                val mergedSprint = if (existing.sprint.id.isBlank()) sprint else existing.sprint
                if (mergedSprint != existing.sprint) {
                    files.write(manifestPath, files.encodeIteration(existing.copy(sprint = mergedSprint)))
                }
                return mergedSprint
            }
            // Desktop materials directories predate the Agent process marker;
            // bootstrap it without replacing any human-owned overview.
            require(!files.exists(manifestPath)) { "迭代 manifest 不是普通文件：$manifestPath" }
            val iteration = IterationDocumentationManifest(sprint = sprint, createdAt = SilverWingTime.format(Instant.now(clock)))
            files.write(manifestPath, files.encodeIteration(iteration))
            val overview = directory.resolve(ITERATION_OVERVIEW)
            if (!files.exists(overview)) {
                files.write(
                    overview,
                    "# ${sprint.label} 迭代任务总览\n\n" +
                        "本目录由 silverwing Agent CLI 创建；每个需求的过程文档位于其独立子目录。\n",
                )
            }
            return sprint
        }
        val parent = directory.parent ?: throw IllegalArgumentException("迭代目录缺少父目录：$directory")
        materials.ensureMaterialsDirectory(parent, directory)
        val iteration = IterationDocumentationManifest(sprint = sprint, createdAt = SilverWingTime.format(Instant.now(clock)))
        files.write(manifestPath, files.encodeIteration(iteration))
        files.write(
            directory.resolve(ITERATION_OVERVIEW),
            "# ${sprint.label} 迭代任务总览\n\n" +
                "本目录由 silverwing Agent CLI 创建；每个需求的过程文档位于其独立子目录。\n",
        )
        return sprint
    }

    private fun appendIterationOverview(directory: Path, manifest: RequirementDocumentationManifest, subdirectory: String) {
        val overview = directory.resolve(ITERATION_OVERVIEW)
        val marker = "<!-- SILVERWING:REQUIREMENT:${manifest.identity.stableKey} -->"
        val current = files.read(overview)
        if (current.contains(marker)) return
        val line = buildString {
            appendLine()
            appendLine(marker)
            appendLine("- [${manifest.identity.workItemId}-${manifest.requirementTitle}](${manifest.directoryName}/$subdirectory/$REQUIREMENT_OVERVIEW)")
        }
        files.write(overview, current.trimEnd() + "\n" + line)
    }

    private fun renderRequirementOverview(manifest: RequirementDocumentationManifest): String = buildString {
        appendLine("# ${manifest.identity.workItemId}-${manifest.requirementTitle}")
        appendLine()
        appendLine("- 需求：`${manifest.identity.stableKey}`")
        appendLine("- Sprint：`${manifest.sprint.label}`")
        appendLine("- 创建时间：${manifest.createdAt}")
        appendLine()
        appendLine("## 过程文档")
        appendLine()
        appendLine("过程文档、研发辅助资料、SQL 与脚本统一写入本需求的资料子目录。代码仓库自身的 README、ADR、API 文档仍留在对应 Worktree。")
    }

    private fun directoryTitle(directory: Path, requirementId: String): String {
        val name = directory.fileName?.toString().orEmpty()
        return name.removePrefix("$requirementId-").ifBlank { requirementId }
    }

    private fun findHistorical(root: Path, identity: RequirementIdentity, subdirectory: String): List<HistoricalRequirement> {
        val readSprint: (Path) -> RequirementSprintSnapshot? = { path ->
            if (files.isRegularFile(path)) files.readIteration(path).sprint else null
        }
        return materials.findRequirementManifestPaths(root, identity.workItemId)
            .mapNotNull { path ->
                val context = materials.contextForRequirementManifest(
                    root = root,
                    manifestPath = path,
                    id = identity.workItemId,
                    subdirectory = subdirectory,
                    readSprint = readSprint,
                ) ?: return@mapNotNull null
                val manifest = files.readRequirement(path)
                if (manifest.identity != identity) return@mapNotNull null
                HistoricalRequirement(context, manifest)
            }
            .distinctBy { it.context.requirementDirectory.toAbsolutePath().normalize().toString() }
    }


    private data class HistoricalRequirement(
        val context: RequirementMaterialsDirectoryContext,
        val manifest: RequirementDocumentationManifest,
    )

    private companion object {
        const val ITERATION_MANIFEST = ".silverwing-iteration.json"
        const val ITERATION_OVERVIEW = "00-迭代任务总览.md"
        const val REQUIREMENT_OVERVIEW = "00-需求总览.md"
        const val REQUIREMENT_MANIFEST = ".silverwing-requirement.json"
    }
}
