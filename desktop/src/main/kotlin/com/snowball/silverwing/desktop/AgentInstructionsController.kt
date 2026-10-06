package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AgentConflictResolution
import com.snowball.silverwing.core.AgentDocumentPropagationService
import com.snowball.silverwing.core.AgentDocumentService
import com.snowball.silverwing.core.AgentDocumentLayout
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.AgentFileChange
import com.snowball.silverwing.core.AgentFileMonitor
import com.snowball.silverwing.core.AgentInstructionScope
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.AgentTaskTemplateStore
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.SilverWingTime
import com.snowball.silverwing.core.ModuleDisplayNaming
import com.snowball.silverwing.core.ModuleBaseOverride
import com.snowball.silverwing.core.TaskServiceSelection
import com.snowball.silverwing.core.TaskModuleSelection
import com.snowball.silverwing.core.RemoteBranchRef
import com.snowball.silverwing.core.RepositoryConfig
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TaskApplicationService
import com.snowball.silverwing.core.TaskBranchNaming
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskNaming
import com.snowball.silverwing.core.WorkspaceLayout
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.toInfo
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.nio.file.Path
import java.time.Instant
import java.util.UUID

data class AgentInstructionsUiState(
    val revision: Long,
    val conflict: AgentFileChange.Conflict?,
    val templates: List<AgentTaskTemplate>,
)

/** 页面重新创建和异步读取都共享同一份任务草稿；内容只在明确接受磁盘版本时被丢弃。 */
internal class TaskNotesDraft {
    var notes by mutableStateOf("")
        private set
    var loaded by mutableStateOf(false)
        private set
    var loading by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var dirty by mutableStateOf(false)
        private set
    var contentRevision by mutableStateOf(0L)
        private set
    private var request = 0L
    val ready: Boolean get() = loaded || dirty

    internal data class ReadToken(val request: Long, val contentRevision: Long)
    fun beginRead(): ReadToken {
        loading = true; error = null
        return ReadToken(++request, contentRevision)
    }
    fun completeRead(token: ReadToken, content: String) {
        if (token.request != request) return
        if (!dirty && token.contentRevision == contentRevision) replaceContent(content)
        loaded = true; loading = false; error = null
    }
    fun failRead(token: ReadToken, failure: Throwable) {
        if (token.request != request) return
        loading = false; error = failure.message ?: failure::class.simpleName ?: "无法读取需求说明"
    }
    fun cancelRead(token: ReadToken) { if (token.request == request) loading = false }
    fun edit(content: String) { replaceContent(content); dirty = true }
    fun markSaved(savedRevision: Long) { if (contentRevision == savedRevision) dirty = false }
    fun acceptDisk(content: String, force: Boolean = false) {
        if (dirty && !force) return
        // 同内容的磁盘决策也应让此前已开始的读取失效。
        request++; loading = false; error = null; loaded = true
        replaceContent(content); dirty = false
    }
    private fun replaceContent(content: String) {
        if (notes != content) { notes = content; contentRevision++ }
    }
}

/** Owns three-level AGENTS.md editing, monitoring, propagation and conflict resolution. */
class AgentInstructionsController internal constructor(
    private val session: AppSessionStore,
    private val paths: ApplicationPaths,
    private val documents: AgentDocumentService,
    private val propagation: AgentDocumentPropagationService,
    private val tasks: TaskApplicationService,
    private val monitor: AgentFileMonitor,
    private val templateStore: AgentTaskTemplateStore = AgentTaskTemplateStore(paths),
    private val operations: OperationRunner,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val taskDirectory: (TaskManifest) -> Path,
    private val desktopActions: DesktopActions,
    private val isBusy: () -> Boolean,
    private val showError: (Throwable) -> Unit,
    private val showStatus: (String) -> Unit,
) {
    private var revision by mutableStateOf(0L)
    private var conflict by mutableStateOf<AgentFileChange.Conflict?>(null)
    private val pendingConflicts = linkedMapOf<Path, AgentFileChange.Conflict>()
    private val taskNotesDrafts = mutableMapOf<Path, TaskNotesDraft>()
    private val taskNotesManifests = mutableMapOf<Path, TaskManifest>()
    private var templates by mutableStateOf(runCatching { templateStore.list() }.getOrDefault(emptyList()))
    val state: AgentInstructionsUiState get() = AgentInstructionsUiState(revision, conflict, templates)

    fun readGlobal(): String = read(paths.globalAgents)
    fun readGroup(groupId: String): String = read(paths.groupAgents(groupId))

    /** Reads the authoritative global document without blocking Compose's dispatcher. */
    suspend fun readGlobalAsync(): String = runInterruptible(ioDispatcher) {
        monitor.track(paths.globalAgents).content
    }

    /** Reads the authoritative group document without blocking Compose's dispatcher. */
    suspend fun readGroupAsync(groupId: String): String = runInterruptible(ioDispatcher) {
        monitor.track(paths.groupAgents(groupId)).content
    }

    fun markGlobalEdited(content: String) = monitor.markLocalEdit(paths.globalAgents, content)
    fun markGroupEdited(groupId: String, content: String) = monitor.markLocalEdit(paths.groupAgents(groupId), content)

    fun saveGlobal(content: String): Boolean = operations.run("正在保存全局 AGENTS.md…", "全局 AGENTS.md 已保存", block = {
        monitor.save(paths.globalAgents, content)
        requirePropagationSucceeded(propagation.propagate(session.config, AgentInstructionScope.Global).failures)
    })

    fun saveGroup(groupId: String, content: String): Boolean = operations.run("正在保存组 AGENTS.md…", "组 AGENTS.md 已保存", block = {
        monitor.save(paths.groupAgents(groupId), content)
        requirePropagationSucceeded(propagation.propagate(session.config, AgentInstructionScope.Group(groupId)).failures)
    })

    fun saveTemplate(id: String?, name: String, content: String): Boolean =
        operations.run("正在保存模板…", "模板已保存", block = {
            val trimmedName = name.trim()
            val trimmedContent = content.trim()
            val now = SilverWingTime.format(Instant.now())
            templateStore.update { current ->
                if (id == null || current.none { it.id == id }) {
                    current + AgentTaskTemplate(UUID.randomUUID().toString(), trimmedName, trimmedContent, now)
                } else {
                    current.map { if (it.id == id) it.copy(name = trimmedName, content = trimmedContent, updatedAt = now) else it }
                }
            }
        }, onSuccess = { refreshTemplates() })

    fun deleteTemplate(id: String): Boolean =
        operations.run("正在删除模板…", "模板已删除", block = {
            templateStore.update { current -> current.filterNot { it.id == id } }
        }, onSuccess = { refreshTemplates() })

    private fun refreshTemplates() {
        templates = runCatching { templateStore.list() }.getOrDefault(templates)
        revision++
    }

    internal fun notesDraftFor(task: TaskManifest): TaskNotesDraft {
        val path = taskNotesPath(task)
        taskNotesManifests[path] = task
        return taskNotesDrafts.getOrPut(path) { TaskNotesDraft() }
    }

    private fun trackedTaskNotes(path: Path, content: String): String =
        taskNotesManifests[path.toAbsolutePath().normalize()]?.let { documents.taskNotesFromDocument(it, content) }
            ?: content.trimEnd('\r', '\n')

    private fun taskNotesPath(task: TaskManifest): Path =
        documents.taskNotesFile(taskDirectory(task), task).toAbsolutePath().normalize()

    fun readTaskNotes(task: TaskManifest): String {
        notesDraftFor(task).takeIf { it.dirty }?.let { return it.notes }
        return runCatching {
            val content = monitor.track(documents.taskNotesFile(taskDirectory(task), task)).content
            if (content.isNotBlank()) documents.taskNotesFromDocument(task, content) else ""
        }.getOrElse { showError(it); "" }
    }

    /** Reads and parses task-level notes without blocking Compose's dispatcher. */
    suspend fun readTaskNotesAsync(task: TaskManifest): String {
        notesDraftFor(task).takeIf { it.dirty }?.let { return it.notes }
        val path = taskNotesPath(task)
        return runInterruptible(ioDispatcher) {
            val content = monitor.track(path).content
            if (content.isNotBlank()) documents.taskNotesFromDocument(task, content) else ""
        }
    }

    internal suspend fun loadTaskNotesDraftAsync(task: TaskManifest) {
        val draft = notesDraftFor(task)
        val token = draft.beginRead()
        try { draft.completeRead(token, readTaskNotesAsync(task)) }
        catch (cancelled: CancellationException) { draft.cancelRead(token); throw cancelled }
        catch (failure: Throwable) { draft.failRead(token, failure) }
    }

    fun saveTaskNotes(task: TaskManifest, notes: String): Boolean {
        val draft = notesDraftFor(task)
        draft.edit(notes)
        val savedRevision = draft.contentRevision
        val directory = taskDirectory(task)
        val path = documents.taskNotesFile(directory, task)
        val config = session.config
        val retainDraft = {
            draft.edit(draft.notes)
            monitor.markLocalEdit(path, documents.replaceTaskNotesDocument(task, "", draft.notes))
        }
        return operations.run("正在保存需求说明…", "需求说明已保存", block = {
            val current = monitor.snapshot(path)?.content ?: monitor.track(path).content
            monitor.save(path, documents.replaceTaskNotesDocument(task, current, notes))
            tasks.saveTaskNotes(config, directory, notes)
            monitor.checkNow()
        }, onFailure = { failure ->
            // 规则文件可能已写入但系统文件生成失败；此时草稿仍然属于用户。
            runCatching(retainDraft)
                .onFailure { if (it !== failure) failure.addSuppressed(it) }
        }, onCancelled = {
            runCatching(retainDraft).onFailure(showError)
        }, onSuccess = {
            draft.markSaved(savedRevision)
            if (draft.dirty) runCatching { monitor.markLocalEdit(path, documents.replaceTaskNotesDocument(task, "", draft.notes)) }.onFailure(showError)
        })
    }

    fun markTaskNotesEdited(task: TaskManifest, notes: String) {
        notesDraftFor(task).edit(notes)
        val path = documents.taskNotesFile(taskDirectory(task), task)
        runCatching {
            val current = monitor.snapshot(path)?.content ?: monitor.track(path).content
            monitor.markLocalEdit(path, documents.replaceTaskNotesDocument(task, current, notes))
        }.onFailure(showError)
    }

    fun revealGlobal() = runCatching { desktopActions.reveal(documents.ensureGlobalFile()).getOrThrow() }.onFailure(showError)
    fun revealGroup(groupId: String) = runCatching { desktopActions.reveal(documents.ensureGroupFile(groupId)).getOrThrow() }.onFailure(showError)
    fun onWindowFocused() { if (!isBusy()) monitor.checkNow() }

    fun resolveConflict(resolution: AgentConflictResolution): Boolean {
        val current = conflict ?: return false
        val normalizedPath = current.path.toAbsolutePath().normalize()
        val resolvingDraftRevision = taskNotesDrafts[normalizedPath]?.contentRevision
        val task = session.tasks.firstOrNull {
            documents.taskNotesFile(taskDirectory(it), it).toAbsolutePath().normalize() == current.path.toAbsolutePath().normalize()
        }
        val config = session.config
        val directory = task?.let(taskDirectory)
        return operations.run("正在处理 Agent 文件冲突…", "Agent 文件冲突已处理", block = {
            if (resolution == AgentConflictResolution.USE_LOCAL && task != null) {
                monitor.resolve(current.path, resolution, expectedDiskContent = current.diskContent) { localContent ->
                    val notes = documents.taskNotesFromDocument(task, localContent)
                    tasks.saveTaskNotes(config, requireNotNull(directory), notes)
                }
            } else {
                monitor.resolve(current.path, resolution, expectedDiskContent = current.diskContent)
            }
        }, onSuccess = {
            val normalized = current.path.toAbsolutePath().normalize()
            if (pendingConflicts[normalized] === current) pendingConflicts.remove(normalized)
            conflict = pendingConflicts.values.firstOrNull()
            revision++
            taskNotesDrafts[normalized]?.let { draft ->
                if (resolution == AgentConflictResolution.USE_DISK) {
                    if (resolvingDraftRevision == draft.contentRevision || resolvingDraftRevision == null && !draft.dirty) {
                        monitor.snapshot(normalized)?.let { draft.acceptDisk(trackedTaskNotes(normalized, it.content), force = true) }
                    } else taskNotesManifests[normalized]?.let { manifest ->
                        runCatching { monitor.markLocalEdit(normalized, documents.replaceTaskNotesDocument(manifest, "", draft.notes)) }.onFailure(showError)
                    }
                } else {
                    resolvingDraftRevision?.let(draft::markSaved)
                    if (draft.dirty) taskNotesManifests[normalized]?.let { manifest ->
                        runCatching { monitor.markLocalEdit(normalized, documents.replaceTaskNotesDocument(manifest, "", draft.notes)) }.onFailure(showError)
                    }
                }
            }
            if (resolution == AgentConflictResolution.USE_LOCAL && task == null) synchronize(current.path)
        })
    }

    fun handleFileChange(change: AgentFileChange) {
        when (change) {
            is AgentFileChange.Conflict -> {
                pendingConflicts[change.path.toAbsolutePath().normalize()] = change
                conflict = pendingConflicts.values.firstOrNull()
            }
            is AgentFileChange.Reloaded -> {
                taskNotesDrafts[change.path.toAbsolutePath().normalize()]?.acceptDisk(trackedTaskNotes(change.path, change.content))
                revision++; synchronize(change.path)
            }
        }
    }

    /** Renders exactly what [saveTaskNotes] would write, without touching disk. */
    fun previewTask(task: TaskManifest, notes: String): AgentDocumentPreview =
        documents.renderPreview(taskDirectory(task), task, session.config.repositories.map(RepositoryConfig::toInfo), notes)

    /** Renders a task preview away from Compose's dispatcher. */
    suspend fun previewTaskAsync(task: TaskManifest, notes: String): AgentDocumentPreview {
        val directory = taskDirectory(task)
        val repositories = session.config.repositories.map(RepositoryConfig::toInfo)
        return runInterruptible(ioDispatcher) {
            documents.renderPreview(directory, task, repositories, notes)
        }
    }

    fun preview(
        folderName: String,
        branch: String,
        groupId: String,
        serviceIds: Set<String>,
        requirementLink: String,
        notes: String,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        requirementMaterials: RequirementMaterialsDirectory = RequirementMaterialsDirectory(),
    ): AgentDocumentPreview {
        val config = session.config
        val root = config.taskRoot?.let(Path::of) ?: paths.temp
        val normalizedName = folderName.ifBlank { "任务名称" }
        val normalizedBranch = branch.trim().ifBlank { "feature/example" }
        val directoryName = runCatching { TaskNaming.requireValidDirectoryName(normalizedName) }.getOrDefault("任务名称")
        val now = SilverWingTime.format(Instant.now())
        val directory = root.resolve(directoryName)
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        val selectionsByService = serviceSelections.associateBy(TaskServiceSelection::serviceId)
        val workspaces = config.group(groupId).services.filter { it.id in serviceIds }.flatMap { service ->
            val repository = repositories[service.repositoryId] ?: return@flatMap emptyList()
            val selectedModules = selectionsByService[service.id]?.modules ?: service.modules.map { configured ->
                TaskModuleSelection.configured(
                    service,
                    configured,
                    if (service.modules.size == 1) normalizedBranch else "$normalizedBranch-${configured.name}",
                )
            }
            selectedModules.map { selectedModule ->
                val baseRef = selectedModule.baseRef
                val module = selectedModule.toConfig()
                val defaultTarget = if (service.modules.size == 1) normalizedBranch else "$normalizedBranch-${module.name}"
                val target = selectedModule.targetBranch ?: if (module.strategy == WorkspaceStrategy.STANDARD_WORKTREE) defaultTarget else ""
                val branch = if (module.strategy == WorkspaceStrategy.INDEPENDENT_CLONE && target.isBlank()) {
                    RemoteBranchRef.parse(baseRef).branch
                } else target
                ServiceWorkspace(
                    repository.id, service.displayName, repository.rootPath,
                    directory.resolve(WorkspaceLayout.moduleDirectoryName(service, module)).toString(),
                    service.developmentTool, branch, groupServiceId = service.id, moduleId = module.id,
                    moduleName = ModuleDisplayNaming.resolve(module.name, service.displayName, baseRef, service.modules.size),
                    strategy = module.strategy, originUrl = repository.originUrl, baseRef = baseRef,
                    moduleSource = selectedModule.source, targetBranch = target.ifBlank { null }, tagEnabled = module.tagEnabled,
                    tagMode = module.tagMode, tagTargetRef = service.effectiveTagTargetRef(module), tagMessagePrefix = module.tagMessagePrefix,
                )
            }
        }
        val manifest = TaskManifest(
            folderName = normalizedName,
            taskDirectoryName = directoryName,
            featureBranch = normalizedBranch,
            requirementLink = requirementLink.trim(),
            createdAt = now,
            updatedAt = now,
            requirementMaterials = requirementMaterials,
            services = workspaces,
            groupId = groupId,
            agentDocumentLayout = AgentDocumentLayout.REFERENCED_V2,
        )
        return documents.renderPreview(directory, manifest, config.repositories.map(RepositoryConfig::toInfo), notes)
    }

    /** Builds a create-task preview away from Compose's dispatcher. */
    suspend fun previewAsync(
        folderName: String,
        branch: String,
        groupId: String,
        serviceIds: Set<String>,
        requirementLink: String,
        notes: String,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        requirementMaterials: RequirementMaterialsDirectory = RequirementMaterialsDirectory(),
    ): AgentDocumentPreview = runInterruptible(ioDispatcher) {
        preview(folderName, branch, groupId, serviceIds, requirementLink, notes, serviceSelections, requirementMaterials)
    }

    private fun read(path: Path): String = runCatching { monitor.track(path).content }.getOrElse { showError(it); "" }

    private fun synchronize(path: Path) {
        val normalized = path.toAbsolutePath().normalize()
        val instructionScope = when {
            normalized == paths.globalAgents.toAbsolutePath().normalize() -> AgentInstructionScope.Global
            else -> session.config.groups.firstOrNull { paths.groupAgents(it.id).toAbsolutePath().normalize() == normalized }
                ?.let { AgentInstructionScope.Group(it.id) } ?: return
        }
        scope.launch {
            val result = withContext(ioDispatcher) {
                runCatching { requirePropagationSucceeded(propagation.propagate(session.config, instructionScope).failures) }
            }
            result.onSuccess { showStatus("Agent 文件已从磁盘同步") }.onFailure(showError)
        }
    }

    private fun requirePropagationSucceeded(failures: Map<Path, String>) {
        if (failures.isEmpty()) return
        error("部分任务 AGENTS.md 同步失败：" + failures.entries.joinToString { (path, reason) -> "${path.fileName}（$reason）" })
    }
}
