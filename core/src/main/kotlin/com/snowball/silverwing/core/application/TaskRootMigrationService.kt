package com.snowball.silverwing.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.FileVisitResult
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.BasicFileAttributes
import java.time.Clock
import java.time.Instant
import java.util.UUID
import kotlin.io.path.createDirectories
import kotlin.io.path.exists

enum class TaskRootMigrationMode {
    DIRECT_SWITCH,
    SAME_FILE_STORE,
    CROSS_FILE_STORE,
}

data class TaskRootMigrationPreview(
    val sourceRoot: Path?,
    val targetRoot: Path,
    val mode: TaskRootMigrationMode,
    val taskCount: Int,
    val workspaceCount: Int,
    val totalBytes: Long,
    val blockers: List<String>,
) {
    val canMigrate: Boolean get() = blockers.isEmpty()
}

data class TaskRootMigrationResult(
    val config: AppConfig,
    val migratedTasks: Int,
    val cleanupFailures: List<Path> = emptyList(),
)

enum class TaskRootMigrationPhase {
    PREPARING,
    TRANSFERRING_AND_VERIFYING,
    UPDATING_CONFIG,
    CLEANING_SOURCE,
    COMPLETED,
}

data class TaskRootMigrationProgress(
    val phase: TaskRootMigrationPhase,
    val completedTasks: Int,
    val totalTasks: Int,
    val currentTask: String? = null,
)

/**
 * Moves an entire silverwing task root without losing Git worktree registration or dirty files.
 * The persisted task-root setting is switched only after every target task has been verified.
 */
class TaskRootMigrationService(
    private val configStore: ConfigurationRepository = ConfigStore(),
    private val manifests: ManifestStore = ManifestStore(),
    private val git: GitClient = GitClient(),
    private val agentDocuments: AgentDocuments = AgentDocumentService(),
    private val taskLock: TaskOperationLock = FileTaskOperationLock(),
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(paths),
    private val clock: Clock = Clock.systemUTC(),
    private val sameFileStore: (Path, Path) -> Boolean = ::onSameFileStore,
    private val moveTaskDirectory: (Path, Path) -> Unit = ::moveDirectory,
    private val copyTaskDirectory: (Path, Path) -> Unit = ::copyDirectoryTree,
    private val deleteTaskDirectory: (Path) -> Unit = ::deleteDirectoryTree,
    private val repairWorktree: (Path, Path) -> Unit = git::repairWorktree,
) {
    fun preview(config: AppConfig, requestedTarget: Path): TaskRootMigrationPreview =
        buildPlan(config, requestedTarget).preview

    fun migrate(
        config: AppConfig,
        requestedTarget: Path,
        onProgress: (TaskRootMigrationProgress) -> Unit = {},
    ): TaskRootMigrationResult {
        val initialPlan = buildPlan(config, requestedTarget)
        require(initialPlan.preview.blockers.isEmpty()) {
            initialPlan.preview.blockers.joinToString("；")
        }
        val sourceRoot = initialPlan.preview.sourceRoot
        onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.PREPARING, 0, initialPlan.tasks.size))
        if (sourceRoot == null || initialPlan.tasks.isEmpty()) {
            val target = initialPlan.preview.targetRoot
            target.createDirectories()
            onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.UPDATING_CONFIG, 0, 0))
            val updated = updateConfiguredRoot(config.taskRoot, target)
            onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.COMPLETED, 0, 0))
            return TaskRootMigrationResult(updated, migratedTasks = 0)
        }
        return taskLock.withLock(sourceRoot) {
            withTaskLocks(initialPlan.tasks.map(PlannedTask::sourceDirectory)) {
                migrateLocked(config, requestedTarget, onProgress)
            }
        }
    }

    /** Resolves a journal left by an interrupted process before tasks are scanned. */
    fun recoverInterruptedMigration(config: AppConfig): List<String> {
        if (!journalPath.exists()) return emptyList()
        val journal = json.decodeFromString<MigrationJournal>(Files.readString(journalPath))
        val sourceRoot = validateJournal(journal)
        val targetRoot = Path.of(journal.targetRoot).toAbsolutePath().normalize()
        val configuredRoot = config.taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize()
        return taskLock.withLock(sourceRoot) {
            withTaskLocks(journal.tasks.map { Path.of(it.sourceDirectory) }) {
                if (configuredRoot == targetRoot || journal.phase == MigrationPhase.CONFIG_UPDATED) {
                    val failures = cleanupSources(journal)
                    if (failures.isEmpty()) Files.deleteIfExists(journalPath)
                    failures.map { "旧任务目录待清理：$it" }
                } else {
                    val failures = rollback(journal, config)
                    if (failures.isEmpty()) Files.deleteIfExists(journalPath)
                    failures.map { "任务目录迁移回滚待处理：$it" }
                }
            }
        }
    }

    private fun migrateLocked(
        config: AppConfig,
        requestedTarget: Path,
        onProgress: (TaskRootMigrationProgress) -> Unit,
    ): TaskRootMigrationResult {
        val plan = buildPlan(config, requestedTarget)
        require(plan.preview.blockers.isEmpty()) { plan.preview.blockers.joinToString("；") }
        require(plan.preview.sourceRoot != null && plan.tasks.isNotEmpty()) { "没有需要迁移的任务" }
        val journal = plan.toJournal()
        writeJournal(journal)
        plan.preview.targetRoot.createDirectories()
        var configUpdateAttempted = false
        var configUpdated = false
        var cleanupCompleted = false
        try {
            plan.tasks.forEachIndexed { index, task ->
                onProgress(
                    TaskRootMigrationProgress(
                        TaskRootMigrationPhase.TRANSFERRING_AND_VERIFYING,
                        index,
                        plan.tasks.size,
                        task.sourceDirectory.fileName.toString(),
                    ),
                )
                transferAndVerifyTask(config, plan.preview.mode, task, journal.id)
            }
            onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.UPDATING_CONFIG, plan.tasks.size, plan.tasks.size))
            configUpdateAttempted = true
            val updated = updateConfiguredRoot(config.taskRoot, plan.preview.targetRoot)
            // The configuration update is the transaction commit point. From here
            // on, the target is authoritative and recovery must only finish source
            // cleanup; moving the target back would leave config and tasks split.
            configUpdated = true
            writeJournal(journal.copy(phase = MigrationPhase.CONFIG_UPDATED))
            onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.CLEANING_SOURCE, plan.tasks.size, plan.tasks.size))
            val cleanupFailures = cleanupSources(journal)
            if (cleanupFailures.isEmpty()) {
                Files.deleteIfExists(journalPath)
                cleanupCompleted = true
            }
            onProgress(TaskRootMigrationProgress(TaskRootMigrationPhase.COMPLETED, plan.tasks.size, plan.tasks.size))
            return TaskRootMigrationResult(updated, plan.tasks.size, cleanupFailures)
        } catch (error: Throwable) {
            // Some repository implementations may report an error after their
            // physical save (for example while reloading the saved document).
            // Re-read the setting before deciding whether rollback is legal.
            val configuredRoot = runCatching {
                configStore.load().taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize() == plan.preview.targetRoot
            }
            val commitObserved = configUpdated || configuredRoot.getOrDefault(false)
            if (commitObserved || (configUpdateAttempted && configuredRoot.isFailure)) {
                val message = when {
                    cleanupCompleted -> "任务目录迁移已完成，但完成通知失败"
                    !commitObserved -> "任务目录迁移已尝试提交，但无法确认提交状态，目录与迁移日志已保留供恢复"
                    else -> "任务目录迁移已提交，旧目录待清理"
                }
                // Keep the migration journal for startup recovery whenever source
                // cleanup is incomplete. Its phase may still be PREPARED if
                // persisting CONFIG_UPDATED failed, but the committed taskRoot is
                // enough to select cleanup recovery.
                throw IllegalStateException(message, error)
            }
            val rollbackFailures = rollback(journal, config)
            if (rollbackFailures.isEmpty()) Files.deleteIfExists(journalPath)
            throw IllegalStateException(
                if (rollbackFailures.isEmpty()) "任务目录迁移失败，已恢复原目录" else "任务目录迁移失败且自动回滚未完成",
                error,
            )
        }
    }

    private fun transferAndVerifyTask(
        config: AppConfig,
        mode: TaskRootMigrationMode,
        task: PlannedTask,
        migrationId: String,
    ) {
        val clones = task.workspaces.filter { it.workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }
        clones.forEach { planned ->
            IndependentCloneWorkspaceSafety.requireOwned(planned.sourcePath, cloneOwnership(task.sourceDirectory, planned.workspace))
        }
        val snapshots = task.workspaces.associateWith { planned -> snapshot(planned.workspace, planned.sourcePath) }
        when (mode) {
            TaskRootMigrationMode.SAME_FILE_STORE -> {
                moveTaskDirectory(task.sourceDirectory, task.targetDirectory)
                writeOwnershipMarker(task.targetDirectory, migrationId)
            }
            TaskRootMigrationMode.CROSS_FILE_STORE -> {
                task.targetDirectory.createDirectories()
                writeOwnershipMarker(task.targetDirectory, migrationId)
                copyTaskDirectory(task.sourceDirectory, task.targetDirectory)
            }
            TaskRootMigrationMode.DIRECT_SWITCH -> error("有任务时不能直接切换目录")
        }
        clones.forEach { planned ->
            IndependentCloneWorkspaceSafety.reassignOwnership(
                planned.targetPath,
                cloneOwnership(task.sourceDirectory, planned.workspace),
                cloneOwnership(task.targetDirectory, planned.workspace),
            )
        }
        task.workspaces.filter { it.workspace.strategy == WorkspaceStrategy.STANDARD_WORKTREE }.forEach { planned ->
            repairRegisteredWorktree(Path.of(planned.workspace.repositoryPath), planned.targetPath)
        }
        val migrated = task.manifest.copy(
            updatedAt = SilverWingTime.format(Instant.now(clock)),
            services = task.workspaces.map { planned ->
                planned.workspace.copy(
                    worktreePath = planned.targetPath.toString(),
                    repositoryPath = if (planned.workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                        planned.targetPath.toString()
                    } else {
                        planned.workspace.repositoryPath
                    },
                )
            },
        )
        manifests.save(task.targetDirectory, migrated)
        agentDocuments.writeTaskDocument(
            task.targetDirectory,
            migrated,
            config.repositories.map(RepositoryConfig::toInfo),
        )
        task.workspaces.forEach { planned -> verify(planned.workspace, planned.targetPath, snapshots.getValue(planned)) }
    }

    private fun updateConfiguredRoot(expectedSource: String?, target: Path): AppConfig = configStore.update { current ->
        val actual = current.taskRoot?.let(Path::of)?.toAbsolutePath()?.normalize()
        val expected = expectedSource?.let(Path::of)?.toAbsolutePath()?.normalize()
        require(actual == expected) { "任务根目录在迁移期间已被其他操作修改" }
        current.copy(taskRoot = target.toAbsolutePath().normalize().toString())
    }

    private fun buildPlan(config: AppConfig, requestedTarget: Path): MigrationPlan {
        val target = requestedTarget.toAbsolutePath().normalize()
        val source = config.taskRoot?.takeIf(String::isNotBlank)?.let(Path::of)?.toAbsolutePath()?.normalize()
        val blockers = mutableListOf<String>()
        if (source == target) blockers += "新旧任务根目录不能相同"
        if (source != null && (source.startsWith(target) || target.startsWith(source))) {
            blockers += "新旧任务根目录不能互相包含"
        }
        if (Files.exists(target) && !Files.isDirectory(target)) blockers += "目标路径不是目录：$target"
        if (Files.isDirectory(target) && Files.list(target).use { it.findAny().isPresent }) {
            blockers += "目标任务根目录必须为空"
        }
        val targetWriteLocation = if (Files.exists(target)) target else nearestExistingParent(target)
        if (!Files.isDirectory(targetWriteLocation)) blockers += "目标任务根目录的上级路径不是目录：$targetWriteLocation"
        if (!Files.isWritable(targetWriteLocation)) blockers += "目标任务根目录不可写：$target"

        val scan = if (source != null && Files.isDirectory(source)) manifests.scan(source) else ManifestScanResult(emptyList(), emptyList())
        scan.unsupportedDirectories.forEach { directory ->
            blockers += scan.unsupportedReasons[directory] ?: "任务清单版本不受支持：$directory"
        }
        scan.failures.forEach { (directory, reason) -> blockers += "任务清单读取失败：$directory：$reason" }
        if (source != null && scan.current.isNotEmpty()) {
            val recognized = (scan.current.map { it.first } +
                scan.unsupportedDirectories + scan.failures.keys)
                .map { it.toAbsolutePath().normalize() }
                .toSet()
            Files.list(source).use { children ->
                children
                    .filter { it.toAbsolutePath().normalize() !in recognized }
                    .forEach { blockers += "任务根目录包含无法识别的内容：$it" }
            }
        }

        val tasks = scan.current.mapNotNull { (directory, manifest) ->
            val normalizedTask = directory.toAbsolutePath().normalize()
            if (source == null || normalizedTask.parent != source) {
                blockers += "任务必须是任务根目录的直接子目录：$normalizedTask"
                return@mapNotNull null
            }
            if (normalizedTask.fileName.toString() != manifest.taskDirectoryName) {
                blockers += "任务目录与清单名称不一致：$normalizedTask"
                return@mapNotNull null
            }
            if (Files.exists(normalizedTask.resolve(MIGRATION_MARKER_FILE))) {
                blockers += "任务目录包含未处理的迁移标记：$normalizedTask"
                return@mapNotNull null
            }
            val targetTask = target.resolve(manifest.taskDirectoryName).normalize()
            if (targetTask.parent != target) {
                blockers += "任务目录名不安全：${manifest.taskDirectoryName}"
                return@mapNotNull null
            }
            if (Files.exists(targetTask)) blockers += "目标任务目录已存在：$targetTask"
            val workspaces = manifest.services.mapNotNull { workspace ->
                val sourcePath = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
                if (sourcePath == normalizedTask || !sourcePath.startsWith(normalizedTask)) {
                    blockers += "工作区不在任务目录内：$sourcePath"
                    return@mapNotNull null
                }
                if (!Files.isDirectory(sourcePath)) {
                    blockers += "工作区不存在：$sourcePath"
                    return@mapNotNull null
                }
                if (workspace.strategy == WorkspaceStrategy.INDEPENDENT_CLONE) {
                    val ownershipCheck = runCatching {
                        IndependentCloneWorkspaceSafety.requireOwned(sourcePath, cloneOwnership(normalizedTask, workspace))
                    }
                    if (ownershipCheck.isFailure) {
                        blockers += ownershipCheck.exceptionOrNull()?.message ?: "无法验证独立克隆所有权：$sourcePath"
                        return@mapNotNull null
                    }
                }
                val relative = normalizedTask.relativize(sourcePath)
                PlannedWorkspace(workspace, sourcePath, targetTask.resolve(relative).normalize(), relative.toString())
            }
            if (workspaces.size != manifest.services.size) return@mapNotNull null
            PlannedTask(normalizedTask, targetTask, manifest, workspaces)
        }
        val totalBytes = tasks.sumOf { directorySize(it.sourceDirectory) }
        val mode = when {
            source == null || tasks.isEmpty() -> TaskRootMigrationMode.DIRECT_SWITCH
            sameFileStore(source, nearestExistingParent(target)) -> TaskRootMigrationMode.SAME_FILE_STORE
            else -> TaskRootMigrationMode.CROSS_FILE_STORE
        }
        if (mode == TaskRootMigrationMode.CROSS_FILE_STORE) {
            val usable = runCatching { Files.getFileStore(nearestExistingParent(target)).usableSpace }.getOrNull()
            if (usable != null && usable < totalBytes) blockers += "目标磁盘空间不足"
        }
        return MigrationPlan(
            TaskRootMigrationPreview(source, target, mode, tasks.size, tasks.sumOf { it.workspaces.size }, totalBytes, blockers.distinct()),
            tasks,
        )
    }

    private fun snapshot(workspace: ServiceWorkspace, path: Path): WorkspaceSnapshot = WorkspaceSnapshot(
        head = git.resolve(path, "HEAD"),
        branch = git.currentBranch(path),
        status = git.readOnly(path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all").stdout,
        commonDirectory = git.commonDirectory(path),
        originUrl = git.remoteUrl(path),
    )

    private fun verify(workspace: ServiceWorkspace, target: Path, expected: WorkspaceSnapshot) {
        require(sameFile(git.topLevel(target), target)) { "迁移后的路径不是 Git 顶层目录：$target" }
        require(git.resolve(target, "HEAD") == expected.head) { "迁移后 HEAD 发生变化：$target" }
        require(git.currentBranch(target) == expected.branch) { "迁移后分支发生变化：$target" }
        require(
            git.readOnly(target, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all").stdout == expected.status,
        ) { "迁移后工作区文件状态发生变化：$target" }
        when (workspace.strategy) {
            WorkspaceStrategy.STANDARD_WORKTREE -> require(sameFile(git.commonDirectory(target), expected.commonDirectory)) {
                "迁移后 Worktree 仓库身份发生变化：$target"
            }
            WorkspaceStrategy.INDEPENDENT_CLONE -> require(git.remoteUrl(target) == expected.originUrl) {
                "迁移后独立克隆 origin 发生变化：$target"
            }
        }
    }

    private fun sameFile(left: Path, right: Path): Boolean =
        runCatching { Files.isSameFile(left, right) }.getOrDefault(false)

    private fun cloneOwnership(taskDirectory: Path, workspace: ServiceWorkspace): String =
        IndependentCloneWorkspaceSafety.ownership(
            taskDirectory, workspace.repositoryId, workspace.groupServiceId, workspace.moduleId,
        )

    private fun restoreCloneOwnership(task: JournalTask, source: Path, target: Path) {
        task.originalManifest.services.filter { it.strategy == WorkspaceStrategy.INDEPENDENT_CLONE }.forEach { workspace ->
            val clone = Path.of(workspace.worktreePath).toAbsolutePath().normalize()
            require(clone != source && clone.startsWith(source)) { "迁移日志中的独立克隆路径越界：$clone" }
            val originalOwnership = cloneOwnership(source, workspace)
            // A copy leaves the source unchanged; a move may have failed before or after rebinding.
            if (runCatching { IndependentCloneWorkspaceSafety.requireOwned(clone, originalOwnership) }.isFailure) {
                IndependentCloneWorkspaceSafety.reassignOwnership(clone, cloneOwnership(target, workspace), originalOwnership)
            }
        }
    }

    private fun repairRegisteredWorktree(repository: Path, worktree: Path) {
        repositoryLock.withLock(git.commonDirectory(repository)) {
            repairWorktree(repository, worktree)
        }
    }

    private fun rollback(journal: MigrationJournal, config: AppConfig): List<Path> {
        val failures = mutableListOf<Path>()
        journal.tasks.asReversed().forEach { task ->
            val source = Path.of(task.sourceDirectory)
            val target = Path.of(task.targetDirectory)
            runCatching {
                when {
                    target.exists() && !source.exists() -> {
                        requireOwnedTarget(target, journal.id)
                        moveTaskDirectory(target, source)
                        Files.deleteIfExists(source.resolve(MIGRATION_MARKER_FILE))
                    }
                    target.exists() && source.exists() -> {
                        requireOwnedTarget(target, journal.id)
                        task.workspaces.filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }.forEach { workspace ->
                            repairRegisteredWorktree(Path.of(workspace.repositoryPath), source.resolve(workspace.relativePath))
                        }
                        deleteTaskDirectory(target)
                    }
                }
                if (source.exists()) {
                    restoreCloneOwnership(task, source, target)
                    task.workspaces.filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }.forEach { workspace ->
                        repairRegisteredWorktree(Path.of(workspace.repositoryPath), source.resolve(workspace.relativePath))
                    }
                    manifests.save(source, task.originalManifest)
                    agentDocuments.writeTaskDocument(
                        source,
                        task.originalManifest,
                        config.repositories.map(RepositoryConfig::toInfo),
                    )
                }
            }.onFailure { failures.add(source) }
        }
        return failures
    }

    private fun cleanupSources(journal: MigrationJournal): List<Path> {
        val failures = mutableListOf<Path>()
        journal.tasks.forEach { task ->
            val marker = Path.of(task.targetDirectory).resolve(MIGRATION_MARKER_FILE)
            if (marker.exists()) {
                runCatching {
                    require(Files.readString(marker) == journal.id) { "目标目录迁移标记不属于当前事务：${marker.parent}" }
                    Files.delete(marker)
                }.onFailure { failures.add(marker) }
            }
        }
        if (failures.isNotEmpty()) return failures
        journal.tasks.forEach { task ->
            val source = Path.of(task.sourceDirectory)
            if (source.exists()) runCatching { deleteTaskDirectory(source) }.onFailure { failures.add(source) }
        }
        val root = Path.of(journal.sourceRoot)
        if (Files.isDirectory(root)) {
            runCatching {
                if (Files.list(root).use { it.findAny().isEmpty }) Files.deleteIfExists(root)
            }.onFailure { failures.add(root) }
        }
        return failures
    }

    private fun MigrationPlan.toJournal(): MigrationJournal = MigrationJournal(
        id = UUID.randomUUID().toString(),
        sourceRoot = requireNotNull(preview.sourceRoot).toString(),
        targetRoot = preview.targetRoot.toString(),
        mode = preview.mode,
        tasks = tasks.map { task ->
            JournalTask(
                sourceDirectory = task.sourceDirectory.toString(),
                targetDirectory = task.targetDirectory.toString(),
                originalManifest = task.manifest,
                workspaces = task.workspaces.map { workspace ->
                    JournalWorkspace(
                        strategy = workspace.workspace.strategy,
                        repositoryPath = workspace.workspace.repositoryPath,
                        relativePath = workspace.relativePath,
                    )
                },
            )
        },
    )

    private fun writeJournal(journal: MigrationJournal) {
        AtomicFileWriter.write(journalPath, json.encodeToString(journal))
    }

    private fun validateJournal(journal: MigrationJournal): Path {
        require(journal.version == 1) { "不支持的任务目录迁移日志版本：${journal.version}" }
        require(runCatching { UUID.fromString(journal.id) }.isSuccess) { "任务目录迁移日志 ID 无效" }
        val sourceRoot = Path.of(journal.sourceRoot).toAbsolutePath().normalize()
        val targetRoot = Path.of(journal.targetRoot).toAbsolutePath().normalize()
        require(sourceRoot != targetRoot && !sourceRoot.startsWith(targetRoot) && !targetRoot.startsWith(sourceRoot)) {
            "任务目录迁移日志中的源目录和目标目录不安全"
        }
        journal.tasks.forEach { task ->
            val source = Path.of(task.sourceDirectory).toAbsolutePath().normalize()
            val target = Path.of(task.targetDirectory).toAbsolutePath().normalize()
            require(source.parent == sourceRoot && target.parent == targetRoot) { "任务目录迁移日志包含越界任务路径" }
            require(source.fileName.toString() == task.originalManifest.taskDirectoryName) { "任务目录迁移日志中的源任务名称不一致" }
            require(target.fileName.toString() == task.originalManifest.taskDirectoryName) { "任务目录迁移日志中的目标任务名称不一致" }
            task.workspaces.forEach { workspace ->
                val relative = Path.of(workspace.relativePath)
                require(!relative.isAbsolute && relative.nameCount > 0 && !relative.startsWith("..")) {
                    "任务目录迁移日志包含不安全的工作区相对路径"
                }
                require(source.resolve(relative).normalize().startsWith(source)) { "任务目录迁移日志中的源工作区路径越界" }
                require(target.resolve(relative).normalize().startsWith(target)) { "任务目录迁移日志中的目标工作区路径越界" }
            }
        }
        return sourceRoot
    }

    private fun requireOwnedTarget(target: Path, migrationId: String) {
        val marker = target.resolve(MIGRATION_MARKER_FILE)
        require(marker.exists() && Files.readString(marker) == migrationId) {
            "无法确认目标副本属于当前迁移，已保留目录：$target"
        }
    }

    private fun writeOwnershipMarker(target: Path, migrationId: String) {
        Files.writeString(
            target.resolve(MIGRATION_MARKER_FILE),
            migrationId,
            StandardOpenOption.CREATE_NEW,
        )
    }

    private fun <T> withTaskLocks(taskDirectories: List<Path>, block: () -> T): T {
        fun nested(index: Int): T = if (index == taskDirectories.size) block() else {
            taskLock.withLock(taskDirectories[index]) { nested(index + 1) }
        }
        return nested(0)
    }

    private val journalPath: Path get() = paths.home.resolve("migrations").resolve("task-root.json")

    private data class MigrationPlan(val preview: TaskRootMigrationPreview, val tasks: List<PlannedTask>)
    private data class PlannedTask(
        val sourceDirectory: Path,
        val targetDirectory: Path,
        val manifest: TaskManifest,
        val workspaces: List<PlannedWorkspace>,
    )
    private data class PlannedWorkspace(
        val workspace: ServiceWorkspace,
        val sourcePath: Path,
        val targetPath: Path,
        val relativePath: String,
    )
    private data class WorkspaceSnapshot(
        val head: String,
        val branch: String?,
        val status: String,
        val commonDirectory: Path,
        val originUrl: String?,
    )

    @Serializable
    private data class MigrationJournal(
        val id: String,
        val version: Int = 1,
        val sourceRoot: String,
        val targetRoot: String,
        val mode: TaskRootMigrationMode,
        val phase: MigrationPhase = MigrationPhase.PREPARED,
        val tasks: List<JournalTask>,
    )

    @Serializable
    private data class JournalTask(
        val sourceDirectory: String,
        val targetDirectory: String,
        val originalManifest: TaskManifest,
        val workspaces: List<JournalWorkspace>,
    )

    @Serializable
    private data class JournalWorkspace(
        val strategy: WorkspaceStrategy,
        val repositoryPath: String,
        val relativePath: String,
    )

    @Serializable
    private enum class MigrationPhase { PREPARED, CONFIG_UPDATED }

    companion object {
        private const val MIGRATION_MARKER_FILE = ".silverwing-task-root-migration"
        private val json = Json { prettyPrint = true; encodeDefaults = true }

        private fun nearestExistingParent(path: Path): Path {
            var current: Path? = path
            while (current != null && !Files.exists(current)) current = current.parent
            return requireNotNull(current) { "找不到目标目录所在的文件系统：$path" }
        }

        private fun onSameFileStore(source: Path, targetParent: Path): Boolean =
            Files.getFileStore(source) == Files.getFileStore(targetParent)

        private fun moveDirectory(source: Path, target: Path) {
            target.parent.createDirectories()
            try {
                Files.move(source, target, StandardCopyOption.ATOMIC_MOVE)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(source, target)
            }
        }

        private fun copyDirectoryTree(source: Path, target: Path) {
            require(Files.isDirectory(target)) { "目标迁移目录不存在：$target" }
            require(
                Files.list(target).use { children ->
                    children.allMatch { it.fileName.toString() == MIGRATION_MARKER_FILE }
                },
            ) { "目标迁移目录包含非事务文件：$target" }
            Files.walkFileTree(source, object : SimpleFileVisitor<Path>() {
                override fun preVisitDirectory(directory: Path, attributes: BasicFileAttributes): FileVisitResult {
                    Files.createDirectories(target.resolve(source.relativize(directory)))
                    return FileVisitResult.CONTINUE
                }

                override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                    Files.copy(
                        file,
                        target.resolve(source.relativize(file)),
                        LinkOption.NOFOLLOW_LINKS,
                        StandardCopyOption.COPY_ATTRIBUTES,
                    )
                    return FileVisitResult.CONTINUE
                }

                override fun postVisitDirectory(directory: Path, error: java.io.IOException?): FileVisitResult {
                    if (error != null) throw error
                    Files.setLastModifiedTime(
                        target.resolve(source.relativize(directory)),
                        Files.getLastModifiedTime(directory, LinkOption.NOFOLLOW_LINKS),
                    )
                    return FileVisitResult.CONTINUE
                }
            })
        }

        private fun deleteDirectoryTree(root: Path) {
            Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
                override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                    deleteWritable(file)
                    return FileVisitResult.CONTINUE
                }

                override fun postVisitDirectory(directory: Path, error: java.io.IOException?): FileVisitResult {
                    if (error != null) throw error
                    deleteWritable(directory)
                    return FileVisitResult.CONTINUE
                }
            })
        }

        private fun deleteWritable(path: Path) {
            try {
                Files.deleteIfExists(path)
            } catch (denied: java.nio.file.AccessDeniedException) {
                path.toFile().setWritable(true)
                try {
                    Files.deleteIfExists(path)
                } catch (retry: Throwable) {
                    retry.addSuppressed(denied)
                    throw retry
                }
            }
        }

        private fun directorySize(root: Path): Long {
            var size = 0L
            Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
                override fun visitFile(file: Path, attributes: BasicFileAttributes): FileVisitResult {
                    if (attributes.isRegularFile) size += attributes.size()
                    return FileVisitResult.CONTINUE
                }
            })
            return size
        }
    }
}
