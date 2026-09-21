package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AgentDocumentService
import com.snowball.silverwing.core.AgentDocumentPreview
import com.snowball.silverwing.core.AgentConflictResolution
import com.snowball.silverwing.core.AgentFileChange
import com.snowball.silverwing.core.AgentFileMonitor
import com.snowball.silverwing.core.AgentDocumentPropagationService
import com.snowball.silverwing.core.AgentTaskTemplate
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.ApplicationPaths
import com.snowball.silverwing.core.BatchRepositoryAddResult
import com.snowball.silverwing.core.BranchReuseKey
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.CodexExtensionsApplicationService
import com.snowball.silverwing.core.CodexExtensionsService
import com.snowball.silverwing.core.CURRENT_PRODUCT_VERSION
import com.snowball.silverwing.core.DeleteRisk
import com.snowball.silverwing.core.DesktopIntegration
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.DevelopmentToolAutoDetectionService
import com.snowball.silverwing.core.DevelopmentToolStartupDetection
import com.snowball.silverwing.core.ConfiguredGitExecutable
import com.snowball.silverwing.core.ConfiguredGenbuExecutable
import com.snowball.silverwing.core.ConfiguredMeegleExecutable
import com.snowball.silverwing.core.GitBranchReferenceValidator
import com.snowball.silverwing.core.GitClient
import com.snowball.silverwing.core.GitCommandSource
import com.snowball.silverwing.core.GitWorkspaceLifecycle
import com.snowball.silverwing.core.CommandRunner
import com.snowball.silverwing.core.ProcessCommandRunner
import com.snowball.silverwing.core.MeegleCommandSource
import com.snowball.silverwing.core.MeegleRequirementMetadataProvider
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.MeegleProjectCatalog
import com.snowball.silverwing.core.CliMeegleProjectCatalog
import com.snowball.silverwing.core.MeegleCliService
import com.snowball.silverwing.core.ProcessMeegleCliService
import com.snowball.silverwing.core.LocalGitEnvironmentInspector
import com.snowball.silverwing.core.DiagnosticsExporter
import com.snowball.silverwing.core.ApplicationEvent
import com.snowball.silverwing.core.ApplicationErrorLogReader
import com.snowball.silverwing.core.EventSink
import com.snowball.silverwing.core.FeishuWorkItemLink
import com.snowball.silverwing.core.JsonlEventSink
import com.snowball.silverwing.core.error
import com.snowball.silverwing.core.MeegleRequirementLinkSource
import com.snowball.silverwing.core.RequirementLinkFailureLog
import com.snowball.silverwing.core.GitRepositoryInspector
import com.snowball.silverwing.core.GroupConfigurationService
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.GitRemoteBranchCatalog
import com.snowball.silverwing.core.GitRepositoryRemoteCatalog
import com.snowball.silverwing.core.ManifestStore
import com.snowball.silverwing.core.ModuleBaseOverride
import com.snowball.silverwing.core.TaskServiceSelection
import com.snowball.silverwing.core.RepositoryConfig
import com.snowball.silverwing.core.RepositoryInspector
import com.snowball.silverwing.core.RepositoryOperationLock
import com.snowball.silverwing.core.RemoteBranchCatalog
import com.snowball.silverwing.core.RemoteGitBranchCatalog
import com.snowball.silverwing.core.RepositoryRemoteCatalog
import com.snowball.silverwing.core.RequirementMetadataProvider
import com.snowball.silverwing.core.RequirementMetadata
import com.snowball.silverwing.core.RequirementMaterialsDirectory
import com.snowball.silverwing.core.RequirementMaterialsService
import com.snowball.silverwing.core.ServiceWorkspace
import com.snowball.silverwing.core.TagBuildService
import com.snowball.silverwing.core.GitTagDeliveryAdapter
import com.snowball.silverwing.core.GenbuTagProbeService
import com.snowball.silverwing.core.ProcessGenbuTagStatusService
import com.snowball.silverwing.core.DeliveryPipelineRegistry
import com.snowball.silverwing.core.TagOperation
import com.snowball.silverwing.core.TagHistoryItem
import com.snowball.silverwing.core.TagOutputFormatter
import com.snowball.silverwing.core.TaskApplicationService
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskOperationLock
import com.snowball.silverwing.core.TaskRootMigrationService
import com.snowball.silverwing.core.FileTaskOperationLock
import com.snowball.silverwing.core.TaskBranchInfoFormatter
import com.snowball.silverwing.core.TaskWorkspaceToolAvailability
import com.snowball.silverwing.core.TaskWorkspaceToolDescriptor
import com.snowball.silverwing.core.TaskWorkspaceToolRegistry
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.TerminalLaunchCommand
import com.snowball.silverwing.core.TagNavigationPolicy
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.WorkspaceToolLaunchService
import com.snowball.silverwing.core.WorkspaceGitHealth
import com.snowball.silverwing.core.WorkspaceGitFileChange
import com.snowball.silverwing.core.WorkspaceGitFilePreview
import com.snowball.silverwing.core.WorkspaceGitFilePreviewService
import com.snowball.silverwing.core.WorkspaceGitHistoryService
import com.snowball.silverwing.core.WorkspaceGitCommit
import com.snowball.silverwing.core.WorkspaceGitStatusService
import com.snowball.silverwing.core.WorkspaceGitOperationService
import com.snowball.silverwing.core.WorkspaceCommandConfig
import com.snowball.silverwing.core.WorkspaceCommandService
import com.snowball.silverwing.core.WorkspaceGitBatchMode
import com.snowball.silverwing.core.WorkspaceGitBatchResult
import com.snowball.silverwing.core.GitWorkspaceGitStatusReader
import com.snowball.silverwing.core.GitWorkspaceGitHistoryReader
import com.snowball.silverwing.core.GitTaskBranchCatalog
import com.snowball.silverwing.core.TaskBranchCatalog
import com.snowball.silverwing.core.WorkspaceRepairConfirmation
import com.snowball.silverwing.core.WorkspaceRepairPreview
import com.snowball.silverwing.core.WorkspaceRepairResult
import com.snowball.silverwing.core.WorkspaceRepairService
import com.snowball.silverwing.core.WorkspaceProvisioningService
import com.snowball.silverwing.core.WorkspaceBranchReuseInspector
import com.snowball.silverwing.core.StandardWorktreeProvisioner
import com.snowball.silverwing.core.IndependentCloneProvisioner
import com.snowball.silverwing.core.BootstrapService
import com.snowball.silverwing.core.WorkspaceModuleRemovalService
import com.snowball.silverwing.core.toInfo
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withContext
import java.nio.file.Path
import java.util.concurrent.atomic.AtomicReference

enum class NavigationItem(val title: String, val subtitle: String) {
    TASKS("研发任务", "Tasks"),
    ARCHIVED("已归档", "Archived"),
    SERVICES("服务仓库", "Services"),
    TAG("Tag构建", "Tag Builds"),
    SETTINGS("设置", "Settings"),
}

internal fun tagAnnouncementCopyMessage(metadata: RequirementMetadata?): String {
    val qcOwners = metadata?.participants?.qcOwners.orEmpty()
        .asSequence()
        .map { it.name.trim() }
        .filter(String::isNotBlank)
        .distinct()
        .toList()
    return if (qcOwners.isEmpty()) {
        "测试Tag发版信息已复制"
    } else {
        "测试Tag发版信息已复制，发给${qcOwners.joinToString("、")}"
    }
}

internal fun buildMeegleLoginCommand(
    command: String,
    osName: String = System.getProperty("os.name"),
): String {
    require(command.isNotBlank()) { "Meegle 命令不能为空" }
    val displayed = TerminalLaunchCommand.display(
        listOf(command, "auth", "login", "--host", "project.feishu.cn", "--format", "json"),
        osName,
    )
    return if (osName.startsWith("Windows", ignoreCase = true)) "& $displayed" else displayed
}

data class DeleteRiskInspection(
    val loading: Boolean = true,
    val risks: List<DeleteRisk> = emptyList(),
    val error: String? = null,
)

sealed interface RemoteBranchesState {
    data object Idle : RemoteBranchesState
    data class Loading(val staleBranches: List<String> = emptyList()) : RemoteBranchesState
    data class Loaded(
        val branches: List<String>,
        val loadedAtNanos: Long = System.nanoTime(),
    ) : RemoteBranchesState
    data class Failed(
        val message: String,
        val staleBranches: List<String> = emptyList(),
    ) : RemoteBranchesState
}

data class WorkspaceToolOption(
    val id: String,
    val displayName: String,
    val description: String,
    val available: Boolean,
    val unavailableReason: String? = null,
)

private data class LoadedTasks(
    val manifests: List<TaskManifest>,
    val warning: String? = null,
    val manifestIssues: List<TaskManifestIssue> = emptyList(),
)

/** A task manifest that SILVERWING deliberately left unchanged because it could not read it safely. */
data class TaskManifestIssue(
    val manifestPath: String,
    val reason: String,
)

/** Desktop dependency container; feature controllers expose its application use cases to Compose. */
class DesktopApplication(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val events: EventSink = JsonlEventSink(paths),
    private val errorLogReader: ApplicationErrorLogReader = ApplicationErrorLogReader(paths),
    private val meegleExecutablePath: AtomicReference<String?> = AtomicReference(null),
    private val meegleExecutable: ConfiguredMeegleExecutable = ConfiguredMeegleExecutable(meegleExecutablePath::get),
    private val genbuExecutablePath: AtomicReference<String?> = AtomicReference(null),
    private val genbuExecutable: ConfiguredGenbuExecutable = ConfiguredGenbuExecutable(genbuExecutablePath::get),
    private val cliVersionRunner: CommandRunner = ProcessCommandRunner(),
    private val gitExecutablePath: AtomicReference<String?> = AtomicReference(null),
    private val gitExecutable: ConfiguredGitExecutable = ConfiguredGitExecutable(gitExecutablePath::get),
    private val gitClient: GitClient = GitClient(executable = gitExecutable),
    private val bootstrapService: BootstrapService = BootstrapService(git = gitClient),
    private val diagnosticsExporter: DiagnosticsExporter = DiagnosticsExporter(paths, git = gitClient, meegleExecutable = meegleExecutable),
    private val configStore: ConfigStore = ConfigStore(paths),
    private val developmentToolStartupDetection: DevelopmentToolStartupDetection =
        DevelopmentToolAutoDetectionService(configStore),
    private val manifests: ManifestStore = ManifestStore(),
    private val requirementMaterialsService: RequirementMaterialsService =
        RequirementMaterialsService(meegleExecutable = meegleExecutable),
    private val repositoryInspector: RepositoryInspector = GitRepositoryInspector(gitClient),
    private val groupConfigurations: GroupConfigurationService =
        GroupConfigurationService(configStore, repositoryInspector),
    private val operationLock: TaskOperationLock = FileTaskOperationLock(paths),
    private val repositoryLock: RepositoryOperationLock = RepositoryOperationLock(paths),
    private val agentDocuments: AgentDocumentService = AgentDocumentService(paths),
    private val taskRootMigrations: TaskRootMigrationService = TaskRootMigrationService(
        configStore = configStore,
        manifests = manifests,
        git = gitClient,
        agentDocuments = agentDocuments,
        taskLock = operationLock,
        paths = paths,
        repositoryLock = repositoryLock,
    ),
    private val provisioning: WorkspaceProvisioningService = WorkspaceProvisioningService(
        listOf(
            StandardWorktreeProvisioner(git = gitClient, bootstrap = bootstrapService, repositoryLock = repositoryLock),
            IndependentCloneProvisioner(git = gitClient, bootstrap = bootstrapService),
        ),
    ),
    private val branchReuseInspector: WorkspaceBranchReuseInspector =
        WorkspaceBranchReuseInspector(git = gitClient, repositoryLock = repositoryLock),
    private val workspaceRepairs: WorkspaceRepairService = WorkspaceRepairService(
        manifests = manifests,
        agentDocuments = agentDocuments,
        taskLock = operationLock,
        repositoryLock = repositoryLock,
        git = gitClient,
        bootstrap = bootstrapService,
    ),
    private val tasksApplication: TaskApplicationService = TaskApplicationService(
        manifests = manifests,
        provisioning = provisioning,
        agentDocuments = agentDocuments,
        requirementMaterials = requirementMaterialsService,
        lifecycle = GitWorkspaceLifecycle(git = gitClient, bootstrap = bootstrapService, repositoryLock = repositoryLock),
        operationLock = operationLock,
        branchValidator = GitBranchReferenceValidator(gitExecutable = gitExecutable),
        branchReuseInspector = branchReuseInspector,
        repairs = workspaceRepairs,
        moduleRemoval = WorkspaceModuleRemovalService(
            manifests = manifests,
            agentDocuments = agentDocuments,
            taskLock = operationLock,
            repositoryLock = repositoryLock,
            git = gitClient,
        ),
        bootstrap = bootstrapService,
    ),
    private val agentPropagation: AgentDocumentPropagationService =
        AgentDocumentPropagationService(manifests, agentDocuments, operationLock),
    private val tagDelivery: GitTagDeliveryAdapter = GitTagDeliveryAdapter(
        TagBuildService(paths = paths, git = gitClient, repositoryLock = repositoryLock),
    ),
    private val genbuTagProbes: GenbuTagProbeService = GenbuTagProbeService(
        genbu = ProcessGenbuTagStatusService(executable = genbuExecutable),
    ),
    val deliveryRegistry: DeliveryPipelineRegistry = DeliveryPipelineRegistry(listOf(tagDelivery)),
    private val requirementMetadataProvider: RequirementMetadataProvider = MeegleRequirementMetadataProvider(meegleExecutable = meegleExecutable),
    private val requirementLinkSource: MeegleRequirementLinkSource = MeegleRequirementLinkSource(metadata = requirementMetadataProvider, meegleExecutable = meegleExecutable),
    private val requirementLinkFailures: RequirementLinkFailureLog = RequirementLinkFailureLog(paths),
    private val gitStatusService: WorkspaceGitStatusService = WorkspaceGitStatusService(GitWorkspaceGitStatusReader(gitClient)),
    private val gitFilePreviewService: WorkspaceGitFilePreviewService = WorkspaceGitFilePreviewService(gitClient),
    private val gitHistoryService: WorkspaceGitHistoryService = WorkspaceGitHistoryService(GitWorkspaceGitHistoryReader(gitClient)),
    private val workspaceCommandService: WorkspaceCommandService = WorkspaceCommandService(),
    private val gitOperationService: WorkspaceGitOperationService = WorkspaceGitOperationService(gitClient, repositoryLock),
    private val taskBranchCatalog: TaskBranchCatalog = GitTaskBranchCatalog(gitClient),
    private val desktopIntegration: DesktopIntegration = DesktopIntegration(),
    private val nativePathPicker: NativePathPicker = FileKitNativePathPicker(),
    private val remoteBranchCatalog: RemoteBranchCatalog = GitRemoteBranchCatalog(gitClient),
    private val repositoryRemoteCatalog: RepositoryRemoteCatalog = GitRepositoryRemoteCatalog(gitClient),
    private val meegleProjectCatalog: MeegleProjectCatalog = CliMeegleProjectCatalog(meegleExecutable = meegleExecutable),
    private val meegleCliService: MeegleCliService = ProcessMeegleCliService(meegleExecutable = meegleExecutable),
    private val localGitInspector: LocalGitEnvironmentInspector = LocalGitEnvironmentInspector(gitExecutable = gitExecutable),
    private val workspaceToolRegistry: TaskWorkspaceToolRegistry = TaskWorkspaceToolRegistry(
        listOf(CodexWorkspaceToolLauncher(), CursorWorkspaceToolLauncher()),
    ),
    private val workspaceToolLaunchService: WorkspaceToolLaunchService = WorkspaceToolLaunchService(
        workspaceToolRegistry,
        manifests,
    ),
    private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) : AutoCloseable {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var startupMigrationWarnings: List<String> = emptyList()
    private val initial = runCatching {
        val loaded = configStore.load()
        startupMigrationWarnings = taskRootMigrations.recoverInterruptedMigration(loaded)
        if (startupMigrationWarnings.isEmpty()) loaded else configStore.load()
    }
    /**
     * A malformed on-disk configuration must never be mistaken for a new, empty
     * configuration.  The app still needs an in-memory model to render an error
     * state, but saves remain blocked by [ConfigStore] until the user fixes or
     * replaces the invalid file.
     */
    var configurationLoadError by mutableStateOf(initial.exceptionOrNull()?.message)
        private set
    var configFileSnapshot by mutableStateOf(configStore.fileSnapshot())
        private set
    var configFileSnapshotRefreshing by mutableStateOf(false)
        private set
    private val agentMonitor: AgentFileMonitor by lazy {
        AgentFileMonitor(onChange = { change ->
            scope.launch { agentInstructionsController.handleFileChange(change) }
        })
    }

    private val initialConfig = initial.getOrDefault(AppConfig()).also {
        meegleExecutablePath.set(it.meegleExecutablePath)
        gitExecutablePath.set(it.gitExecutablePath)
        genbuExecutablePath.set(it.genbuExecutablePath)
    }
    private var resolvedTerminal by mutableStateOf(TerminalLaunchCommand.resolve(initialConfig.terminalExecutable))
    private var terminalDetectionGeneration = 0L
    private val initialTasks = scanTasks(initialConfig)
    private var taskScanWarning: String? = (startupMigrationWarnings + listOfNotNull(initialTasks.warning))
        .joinToString("\n")
        .takeIf(String::isNotBlank)
    var taskManifestIssues by mutableStateOf(initialTasks.manifestIssues)
        private set
    val sessionStore = AppSessionStore(initialConfig, initialTasks.manifests)
    val operationCoordinator = OperationCoordinator(
        initialError = initial.exceptionOrNull()?.let { "配置读取失败：${it.message}" } ?: taskScanWarning,
        onError = ::recordError,
    )
    var recentErrors by mutableStateOf(errorLogReader.latest())
        private set
    private val cliInstallationService: CliInstallationService = platformCliInstallationService()
    var cliInstallationStatus by mutableStateOf(cliInstallationService.inspect())
        private set
    private val tagSkillInstallationService: TagSkillInstallationService = PackagedTagSkillInstallationService()
    var tagSkillInstallationStatus by mutableStateOf(tagSkillInstallationService.inspect())
        private set
    private val codexExtensions: CodexExtensionsApplicationService = CodexExtensionsApplicationService(
        configurations = configStore,
        extensions = CodexExtensionsService(paths = paths, gitExecutable = gitExecutable::resolve),
        branchCatalog = RemoteGitBranchCatalog(gitExecutable = gitExecutable::resolve),
    )
    private val operationRunner = OperationRunner(operationCoordinator, scope, ioDispatcher)
    private val settingsOperationCoordinator = OperationCoordinator(onError = ::recordError)
    private val settingsOperationRunner = OperationRunner(settingsOperationCoordinator, scope, ioDispatcher)
    private val meegleOperationCoordinator = OperationCoordinator(onError = ::recordError)
    private val meegleOperationRunner = OperationRunner(meegleOperationCoordinator, scope, ioDispatcher)
    internal val codexExtensionsController by lazy {
        CodexExtensionsController(
            extensions = codexExtensions,
            operations = settingsOperationRunner,
            applyConfig = ::applyConfig,
        )
    }
    private val requirementMetadataCoordinator = RequirementMetadataCoordinator(
        provider = requirementMetadataProvider,
        scope = scope,
        ioDispatcher = ioDispatcher,
    )
    val requirementController = RequirementController(
        session = sessionStore,
        scope = scope,
        coordinator = requirementMetadataCoordinator,
        linkSource = requirementLinkSource,
        failureLog = requirementLinkFailures,
        ioDispatcher = ioDispatcher,
        requirementMaterials = requirementMaterialsService,
    )
    val desktopActions = DesktopActions(
        integration = desktopIntegration,
        config = { config },
        onSettingsRequired = { navigation = NavigationItem.SETTINGS },
        onStatus = ::showStatus,
        onError = ::showError,
    )
    val taskController: TaskController by lazy {
        TaskController(
            session = sessionStore,
            configStore = configStore,
            manifests = manifests,
            repositoryInspector = repositoryInspector,
            tasks = tasksApplication,
            gitStatus = gitStatusService,
            gitFilePreviews = gitFilePreviewService,
            gitHistory = gitHistoryService,
            workspaceCommands = workspaceCommandService,
            workspaceTools = workspaceToolLaunchService,
            gitOperations = gitOperationService,
            taskBranchCatalog = taskBranchCatalog,
            operations = operationRunner,
            scope = scope,
            ioDispatcher = ioDispatcher,
            desktopActions = desktopActions,
            onConfigApplied = ::applyConfig,
            onTasksChanged = {
                deliveryController.reloadHistory()
                requirementController.reconcileTasks()
            },
            onRequirementsRefresh = requirementController::refreshAll,
            onRequirementSelected = requirementController::refresh,
            onError = ::showError,
            isBusy = { busy },
            events = events,
        )
    }
    val settingsController by lazy {
        SettingsController(
            session = sessionStore,
            configStore = configStore,
            groups = groupConfigurations,
            taskRootMigrations = taskRootMigrations,
            pathPicker = nativePathPicker,
            branchCatalog = remoteBranchCatalog,
            remoteCatalog = repositoryRemoteCatalog,
            meegleProjectCatalog = meegleProjectCatalog,
            meegleCliService = meegleCliService,
            meegleExecutable = meegleExecutable,
            gitExecutable = gitExecutable,
            genbuExecutable = genbuExecutable,
            cliVersionRunner = cliVersionRunner,
            localGitInspector = localGitInspector,
            scope = scope,
            ioDispatcher = ioDispatcher,
            operations = operationRunner,
            settingsOperations = settingsOperationRunner,
            meegleOperations = meegleOperationRunner,
            applyConfig = ::applyConfig,
            reloadTasks = { reloadTasks() },
            showError = ::showError,
            showStatus = ::showStatus,
        )
    }
    val agentInstructionsController: AgentInstructionsController by lazy {
        AgentInstructionsController(
            session = sessionStore,
            paths = paths,
            documents = agentDocuments,
            propagation = agentPropagation,
            tasks = tasksApplication,
            monitor = agentMonitor,
            operations = operationRunner,
            scope = scope,
            ioDispatcher = ioDispatcher,
            taskDirectory = ::taskDirectory,
            desktopActions = desktopActions,
            isBusy = { busy },
            showError = ::showError,
            showStatus = ::showStatus,
        )
    }
    val deliveryController by lazy {
        DeliveryController(
            session = sessionStore,
            adapter = tagDelivery,
            operations = operationRunner,
            taskDirectory = ::taskDirectory,
            refreshGitStatus = ::refreshCurrentTaskGitStatus,
            genbuTagProbes = genbuTagProbes,
            scope = scope,
            ioDispatcher = ioDispatcher,
            onError = ::showError,
        )
    }
    var config: AppConfig
        get() = sessionStore.config
        private set(value) {
            meegleExecutablePath.set(value.meegleExecutablePath)
            gitExecutablePath.set(value.gitExecutablePath)
            genbuExecutablePath.set(value.genbuExecutablePath)
            sessionStore.config = value
        }
    var repositories by mutableStateOf(config.repositories.map(RepositoryConfig::toInfo))
        private set
    var tasks: List<TaskManifest>
        get() = sessionStore.tasks
        private set(value) { sessionStore.tasks = value }
    var navigation: NavigationItem
        get() = sessionStore.navigation
        set(value) {
            sessionStore.navigation = value
            if (value in setOf(NavigationItem.TASKS, NavigationItem.ARCHIVED)) {
                taskController.refreshGitStatus()
                requirementController.refreshAll()
            }
        }
    var selectedTask: TaskManifest?
        get() = sessionStore.selectedTask
        private set(value) { sessionStore.selectedTask = value }
    val busy: Boolean get() = operationCoordinator.busy
    val activeOperation: String? get() = operationCoordinator.activeMessage
    val activeOperationCancellable: Boolean get() = operationCoordinator.cancellable
    val activeOperationCancelling: Boolean get() = operationCoordinator.cancelling
    fun cancelActiveOperation(): Boolean = operationRunner.cancel()
    val meegleBusy: Boolean get() = meegleOperationCoordinator.busy
    val settingsBusy: Boolean get() = settingsOperationCoordinator.busy
    val hasActiveOperations: Boolean get() = busy || settingsBusy || meegleBusy
    val meegleOperationCancellable: Boolean get() = meegleOperationCoordinator.cancellable
    val meegleOperationError: String? get() = meegleOperationCoordinator.errorMessage
    fun cancelMeegleOperation(): Boolean = meegleOperationRunner.cancel()
    fun refreshCliInstallationStatus() {
        cliInstallationStatus = cliInstallationService.inspect()
        tagSkillInstallationStatus = tagSkillInstallationService.inspect()
    }

    fun refreshTagSkillInstallationStatus() {
        tagSkillInstallationStatus = tagSkillInstallationService.inspect()
    }

    fun installCli(): Boolean = settingsOperationRunner.run(
        activeMessage = "正在安装 silverwing CLI…",
        successMessage = "silverwing CLI 已安装；请打开新的终端运行 silverwing。",
        block = cliInstallationService::install,
        onFailure = { refreshCliInstallationStatus() },
        onSuccess = { cliInstallationStatus = it },
    )

    fun uninstallCli(): Boolean = settingsOperationRunner.run(
        activeMessage = "正在卸载 silverwing CLI…",
        successMessage = "silverwing CLI 已卸载；已从用户 PATH 移除。",
        block = cliInstallationService::uninstall,
        onFailure = { refreshCliInstallationStatus() },
        onSuccess = { cliInstallationStatus = it },
    )

    fun installTagSkill(): Boolean = settingsOperationRunner.run(
        activeMessage = "正在安装 silverwing Skill…",
        successMessage = "silverwing Skill 已安装；请重新打开或刷新 Skill 宿主后使用 \$silverwing。",
        block = tagSkillInstallationService::install,
        onFailure = { refreshTagSkillInstallationStatus() },
        onSuccess = { tagSkillInstallationStatus = it },
    )

    fun uninstallTagSkill(): Boolean = settingsOperationRunner.run(
        activeMessage = "正在卸载 silverwing Skill…",
        successMessage = "silverwing Skill 已卸载；已删除本机的 Skill 目录。",
        block = tagSkillInstallationService::uninstall,
        onFailure = { refreshTagSkillInstallationStatus() },
        onSuccess = { tagSkillInstallationStatus = it },
    )
    val statusMessage: String? get() = operationCoordinator.statusMessage
    val errorMessage: String? get() = operationCoordinator.errorMessage
    val tagHistory: List<TagOperation> get() = deliveryController.state.history
    val tagHistoryItems: List<TagHistoryItem> get() = deliveryController.state.historyItems
    fun isTagBuildActive(operation: TagOperation): Boolean = deliveryController.isTagBuildActive(operation)
    private var tagAnnouncementCopyingGroupIds by mutableStateOf<Set<String>>(emptySet())
    fun tagHistoryRequirementLink(folderName: String): String =
        sessionStore.tasks.firstOrNull { it.folderName == folderName }?.requirementLink.orEmpty()
    fun isTagAnnouncementCopying(groupId: String): Boolean = groupId in tagAnnouncementCopyingGroupIds
    fun copyTagHistoryGroupAnnouncement(group: TagHistoryItem) {
        if (isTagAnnouncementCopying(group.groupId)) return
        val task = sessionStore.tasks.firstOrNull { it.folderName == group.folderName }
        val requirementLink = task?.requirementLink.orEmpty()
        val content = TagOutputFormatter.format(
            requirementLink = requirementLink,
            operations = groupAnnouncementOperations(group),
            includeFailures = true,
        )
        val loadedMetadata = task?.let(requirementController::loadedMetadataFor)
        if (loadedMetadata != null || task == null || FeishuWorkItemLink.parse(requirementLink) == null) {
            copyText(content, tagAnnouncementCopyMessage(loadedMetadata))
            return
        }

        tagAnnouncementCopyingGroupIds = tagAnnouncementCopyingGroupIds + group.groupId
        showStatus("获取测试姓名中，请稍后")
        requirementController.fetchMetadata(task) { metadata ->
            tagAnnouncementCopyingGroupIds = tagAnnouncementCopyingGroupIds - group.groupId
            copyText(content, tagAnnouncementCopyMessage(metadata))
        }
    }
    fun clearTagHistory(): Boolean = deliveryController.clearHistory()
    fun deleteTagHistory(operationIds: Set<String>): Boolean = deliveryController.deleteHistory(operationIds)
    fun setGenbuTagProbeVisible(visible: Boolean) = deliveryController.setGenbuTagProbeVisible(visible)
    fun refreshGenbuTagProbes(): Boolean = deliveryController.refreshGenbuTagProbes()
    val isGenbuTagProbeRefreshing: Boolean get() = deliveryController.isGenbuProbeRefreshing
    val agentRevision: Long get() = agentInstructionsController.state.revision
    val agentConflict: AgentFileChange.Conflict? get() = agentInstructionsController.state.conflict
    val deleteRiskInspections: Map<String, DeleteRiskInspection> get() = taskController.state.deleteRisks
    val pathPickerBusy: Boolean get() = settingsController.state.pathPickerBusy
    val taskRootMigrationState: TaskRootMigrationUiState get() = settingsController.state.taskRootMigration
    val remoteBranches: Map<String, RemoteBranchesState> get() = settingsController.state.remoteBranches
    val repositoryAddResult: BatchRepositoryAddResult? get() = settingsController.state.repositoryAddResult
    val workspaceGitHealth: Map<String, WorkspaceGitHealth> get() = taskController.state.gitHealth
    val workspaceRepairPreview: WorkspaceRepairPreview? get() = taskController.repairPreview
    val workspaceRepairResult: WorkspaceRepairResult? get() = taskController.repairResult
    val taskBranchCandidates: TaskBranchCandidatesState get() = taskController.branchCandidates
    val batchGitPreviewState: BatchGitPreviewState get() = taskController.batchGitPreviews

    val needsTaskRoot: Boolean get() = config.taskRoot.isNullOrBlank()
    val showsTagNavigation: Boolean get() = TagNavigationPolicy.isVisible(config)
    val globalAgentsPath: String get() = paths.globalAgents.toAbsolutePath().normalize().toString()
    fun groupAgentsPath(groupId: String): String = paths.groupAgents(groupId).toAbsolutePath().normalize().toString()

    fun workspaceToolOptions(groupId: String): List<WorkspaceToolOption> {
        val configuredIds = config.group(groupId).defaultWorkspaceToolIds
        val descriptors = workspaceToolRegistry.descriptors().associateBy(TaskWorkspaceToolDescriptor::id)
        return (descriptors.keys + configuredIds).distinct().map { toolId ->
            val descriptor = descriptors[toolId]
            when (val availability = workspaceToolRegistry.availability(toolId)) {
                TaskWorkspaceToolAvailability.Available -> WorkspaceToolOption(
                    id = toolId,
                    displayName = descriptor?.displayName ?: toolId,
                    description = descriptor?.description.orEmpty(),
                    available = true,
                )
                is TaskWorkspaceToolAvailability.Unavailable -> WorkspaceToolOption(
                    id = toolId,
                    displayName = descriptor?.displayName ?: toolId,
                    description = descriptor?.description.orEmpty(),
                    available = false,
                    unavailableReason = availability.reason,
                )
            }
        }
    }

    fun taskPath(task: TaskManifest): String = taskDirectory(task).toString()

    fun gitHealth(workspace: ServiceWorkspace): WorkspaceGitHealth? =
        taskController.gitHealth(workspace)

    fun inspectWorkspaceRepair(task: TaskManifest, workspace: ServiceWorkspace) = taskController.inspectRepair(task, workspace)
    fun clearWorkspaceWarnings(task: TaskManifest, workspace: ServiceWorkspace) = taskController.clearWorkspaceWarnings(task, workspace)
    fun rerunWorkspaceBootstrap(task: TaskManifest, workspace: ServiceWorkspace) = taskController.rerunWorkspaceBootstrap(task, workspace)
    fun repairWorkspace(task: TaskManifest, preview: WorkspaceRepairPreview, confirmation: WorkspaceRepairConfirmation) =
        taskController.repairWorkspace(task, preview, confirmation)
    fun clearWorkspaceRepairPreview() = taskController.clearRepairPreview()
    fun clearWorkspaceRepairResult() = taskController.clearRepairResult()
    fun loadTaskBranchCandidates(groupId: String, serviceIds: Set<String>) = taskController.loadTaskBranchCandidates(groupId, serviceIds)
    fun cancelTaskBranchCandidates() = taskController.cancelTaskBranchCandidates()
    fun cancelRemoteBranchLoads() = settingsController.cancelRemoteBranchLoads()

    fun requestRequirementMetadata(link: String, onResult: (RequirementMetadata?) -> Unit) {
        requirementController.requestDraftMetadata(link, onResult)
    }

    /** Saves the configured Feishu project identities without enabling any automatic query. */
    fun updateMeegleProjects(projects: List<MeegleProjectConfig>, onFailure: (Throwable) -> Unit = {}): Boolean =
        settingsController.updateMeegleProjects(projects, onFailure)

    fun updateMeegleExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean =
        settingsController.updateMeegleExecutablePath(raw, onFailure)

    fun meegleCommandResolution(): Pair<String, MeegleCommandSource> = settingsController.meegleCommandResolution()

    fun copyMeegleLoginCommand() {
        copyText(
            buildMeegleLoginCommand(meegleCommandResolution().first),
            "Meegle 登录命令已复制，请在命令行执行；完成后点击重新检测",
        )
    }

    fun updateGitExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean =
        settingsController.updateGitExecutablePath(raw, onFailure)

    fun gitCommandResolution(): Pair<String, GitCommandSource> = settingsController.gitCommandResolution()
    fun genbuCommandResolution() = settingsController.genbuCommandResolution()
    fun refreshGenbuCommandResolution() = settingsController.refreshGenbuCommandResolution()

    val genbuSettingsState: GenbuSettingsState get() = settingsController.state.genbu

    fun updateGenbuExecutablePath(rawGenbuPath: String, onFailure: (Throwable) -> Unit = {}): Boolean =
        settingsController.updateGenbuExecutablePath(rawGenbuPath, onFailure)

    fun refreshCurrentTaskGitStatus() = taskController.refreshGitStatus()

    fun addableServices(task: TaskManifest): List<GroupServiceConfig> {
        val existing = task.services.map(ServiceWorkspace::groupServiceId).toSet()
        return config.group(task.groupId).services.filter { it.enabled && it.id !in existing }
    }

    fun isGenbuProbeEnabled(operation: TagOperation): Boolean {
        val task = tasks.firstOrNull { it.folderName == operation.folderName } ?: return false
        return config.groups.firstOrNull { it.id == task.groupId }
            ?.services
            ?.firstOrNull { it.id == operation.groupServiceId }
            ?.genbuProbeEnabled == true
    }

    val enabledGenbuProbeServiceCount: Int
        get() = config.groups.sumOf { group -> group.services.count(GroupServiceConfig::genbuProbeEnabled) }

    fun canBuildTag(task: TaskManifest, workspace: ServiceWorkspace): Boolean = deliveryController.canBuild(task, workspace)

    init {
        requirementLinkFailures.cleanup()
        // Register authoritative global/group files without invoking Git or a
        // remote integration. Subsequent external writes can then propagate
        // even if the user never opens the Settings editor.
        runCatching {
            agentDocuments.ensureGlobalFile()
            agentMonitor.track(paths.globalAgents)
        }.onFailure(::showError)
        config.groups.forEach { group ->
            runCatching {
                agentDocuments.ensureGroupFile(group.id)
                agentMonitor.track(paths.groupAgents(group.id))
            }.onFailure(::showError)
        }
        refreshCurrentTaskGitStatus()
        detectDevelopmentToolsInBackground()
        detectTerminalInBackground()
    }

    /**
     * The only repository validation entry point. Startup deliberately does not
     * call this method, so opening the app never runs Git or Meegle commands.
     */
    fun refresh() = taskController.refresh()

    fun selectTask(task: TaskManifest) = taskController.select(task)

    fun setTheme(theme: ThemePreference) = settingsController.setTheme(theme)
    fun setGlobalTagEnabled(enabled: Boolean, onFailure: (Throwable) -> Unit = {}) =
        settingsController.setGlobalTagEnabled(enabled, onFailure)
    fun updateTagHistoryMaxGroups(value: Int, onFailure: (Throwable) -> Unit = {}) =
        settingsController.updateTagHistoryMaxGroups(value, onFailure)
    fun updateTaskRoot(value: String, onFailure: (Throwable) -> Unit = {}) = settingsController.updateTaskRoot(value, onFailure)
    fun confirmTaskRootMigration(onFailure: (Throwable) -> Unit = {}) = settingsController.confirmTaskRootMigration(onFailure)
    fun cancelTaskRootMigration() = settingsController.cancelTaskRootMigration()
    fun updateRequirementMaterialsRoot(value: String, onFailure: (Throwable) -> Unit = {}) =
        settingsController.updateRequirementMaterialsRoot(value, onFailure)
    fun updateRequirementMaterialsSubdirectory(value: String, onFailure: (Throwable) -> Unit = {}) =
        settingsController.updateRequirementMaterialsSubdirectory(value, onFailure)
    fun updateDevelopmentTools(
        tools: List<com.snowball.silverwing.core.DevelopmentToolConfig>,
        defaultTool: com.snowball.silverwing.core.DevelopmentToolType,
        terminal: String,
        allowTemporaryDevelopmentToolSelection: Boolean,
        onFailure: (Throwable) -> Unit = {},
    ) = settingsController.updateDevelopmentTools(tools, defaultTool, terminal, allowTemporaryDevelopmentToolSelection, onFailure)
    fun updateTaskAreaToolGroupVisibility(
        taskGit: Boolean,
        taskPath: Boolean,
        workspaceGit: Boolean,
        workspacePath: Boolean,
        branchCopyIcons: Boolean,
        requirementCopyIcons: Boolean,
        projectNameCopyIcons: Boolean,
        onFailure: (Throwable) -> Unit = {},
    ) = settingsController.updateTaskAreaToolGroupVisibility(
        taskGit,
        taskPath,
        workspaceGit,
        workspacePath,
        branchCopyIcons,
        requirementCopyIcons,
        projectNameCopyIcons,
        onFailure,
    )
    fun terminalCommandResolution() = resolvedTerminal
    fun redetectDevelopmentTools() = detectDevelopmentToolsInBackground()
    fun resetDevelopmentToolToAutomatic(type: DevelopmentToolType) {
        val configuredBeforeReset = config.developmentTools.firstOrNull { it.type == type }
        scope.launch {
            try {
                val cleared = withContext(ioDispatcher) {
                    configStore.update { current ->
                        current.copy(developmentTools = current.developmentTools.filterNot { it.type == type })
                    }
                }
                val configuredNow = config.developmentTools.firstOrNull { it.type == type }
                if (configuredNow == configuredBeforeReset) {
                    applyConfig(config.copy(developmentTools = config.developmentTools.filterNot { it.type == type }))
                }
                val result = withContext(ioDispatcher) {
                    developmentToolStartupDetection.detectAndPersist(cleared)
                }
                val detected = result.config.developmentTools.firstOrNull { it.type == type }
                if (config.developmentTools.none { it.type == type } && detected != null) {
                    applyConfig(config.copy(developmentTools = config.developmentTools + detected))
                }
                showStatus("${type.displayName} 已恢复自动探测")
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (error: Throwable) {
                showError(error)
            }
        }
    }
    fun addGroup(name: String, onCompleted: () -> Unit = {}) = settingsController.addGroup(name, onCompleted)
    fun renameGroup(groupId: String, name: String, onCompleted: () -> Unit = {}) = settingsController.renameGroup(groupId, name, onCompleted)
    fun moveGroup(groupId: String, offset: Int) = settingsController.moveGroup(groupId, offset)
    fun deleteGroup(groupId: String, onCompleted: () -> Unit = {}) = settingsController.deleteGroup(groupId, onCompleted)
    fun setGroupTagEnabled(groupId: String, enabled: Boolean) = settingsController.setGroupTagEnabled(groupId, enabled)
    fun updateGroupDefaults(groupId: String, branchPrefix: String, workspaceToolIds: List<String>, onFailure: (Throwable) -> Unit = {}) =
        settingsController.updateGroupDefaults(groupId, branchPrefix, workspaceToolIds, onFailure)
    fun chooseDirectory(initialPath: String? = null, onSelected: (String) -> Unit) = settingsController.chooseDirectory(initialPath, onSelected)
    fun chooseFile(initialPath: String? = null, onSelected: (String) -> Unit) = settingsController.chooseFile(initialPath, onSelected)
    fun chooseApplication(initialPath: String? = null, onSelected: (String) -> Unit) =
        settingsController.chooseApplication(initialPath, onSelected)
    fun chooseDirectories(initialPath: String? = null, onSelected: (List<String>) -> Unit) = settingsController.chooseDirectories(initialPath, onSelected)
    fun loadRemoteBranches(repositoryId: String, remote: String = "origin", force: Boolean = false) =
        settingsController.loadRemoteBranches(repositoryId, remote, force)
    fun remoteBranchState(repositoryId: String, remote: String) = settingsController.remoteBranchState(repositoryId, remote)
    fun loadRepositoryRemotes(repositoryId: String, force: Boolean = false) = settingsController.loadRepositoryRemotes(repositoryId, force)
    fun repositoryRemotesState(repositoryId: String) = settingsController.repositoryRemotesState(repositoryId)
    val meegleProjectCatalogState: MeegleProjectCatalogState get() = settingsController.state.meegleProjects
    val meegleCliState: MeegleCliState get() = settingsController.state.meegleCli
    val localGitSettingsState: LocalGitSettingsState get() = settingsController.state.localGit
    fun settingsSaveState(key: String): SettingsSaveState = settingsController.saveState(key)
    fun refreshLocalGit(force: Boolean = false) = settingsController.refreshLocalGit(force)
    fun refreshGenbu(force: Boolean = false) = settingsController.refreshGenbu(force)
    fun openGenbuSettings() {
        WindowPreferences.saveSettingsSection("genbu")
        navigation = NavigationItem.SETTINGS
    }
    fun loadMeegleProjects(force: Boolean = false) = settingsController.loadMeegleProjects(force)
    fun cancelMeegleProjectLoad() = settingsController.cancelMeegleProjectLoad()
    fun refreshMeegleStatus(force: Boolean = false) = settingsController.refreshMeegleStatus(force)
    fun loginMeegle() = settingsController.loginMeegle()
    fun addRepository(groupId: String, selectedDirectory: String, strategy: WorkspaceStrategy) =
        settingsController.addRepository(groupId, selectedDirectory, strategy)
    fun addRepositories(groupId: String, selectedDirectories: List<String>, onCompleted: () -> Unit = {}) =
        settingsController.addRepositories(groupId, selectedDirectories, onCompleted)
    fun clearRepositoryAddResult() = settingsController.clearRepositoryAddResult()
    fun updateService(groupId: String, service: GroupServiceConfig, onCompleted: () -> Unit = {}) =
        settingsController.updateService(groupId, service, onCompleted)
    fun moveService(groupId: String, serviceId: String, offset: Int) = settingsController.moveService(groupId, serviceId, offset)
    fun removeService(groupId: String, serviceId: String, onCompleted: () -> Unit = {}) =
        settingsController.removeService(groupId, serviceId, onCompleted)
    val agentTaskTemplates: List<AgentTaskTemplate> get() = agentInstructionsController.state.templates
    fun saveAgentTaskTemplate(id: String?, name: String, content: String) = agentInstructionsController.saveTemplate(id, name, content)
    fun deleteAgentTaskTemplate(id: String) = agentInstructionsController.deleteTemplate(id)
    fun readGlobalAgents(): String = agentInstructionsController.readGlobal()
    suspend fun readGlobalAgentsAsync(): String = agentInstructionsController.readGlobalAsync()
    fun saveGlobalAgents(content: String) = agentInstructionsController.saveGlobal(content)
    fun markGlobalAgentsEdited(content: String) = agentInstructionsController.markGlobalEdited(content)
    fun readGroupAgents(groupId: String): String = agentInstructionsController.readGroup(groupId)
    suspend fun readGroupAgentsAsync(groupId: String): String = agentInstructionsController.readGroupAsync(groupId)
    fun saveGroupAgents(groupId: String, content: String) = agentInstructionsController.saveGroup(groupId, content)
    fun markGroupAgentsEdited(groupId: String, content: String) = agentInstructionsController.markGroupEdited(groupId, content)
    val requirementMaterialsPreviewState: RequirementMaterialsPreviewState
        get() = requirementController.materialsPreviewState
    fun requestRequirementMaterialsPreview(requirementInput: String, folderName: String) =
        requirementController.requestMaterialsPreview(requirementInput, folderName)
    fun previewAgents(
        folderName: String,
        branch: String,
        groupId: String,
        serviceIds: Set<String>,
        requirementLink: String,
        notes: String,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        requirementMaterials: RequirementMaterialsDirectory = RequirementMaterialsDirectory(),
    ): AgentDocumentPreview = agentInstructionsController.preview(
        folderName,
        branch,
        groupId,
        serviceIds,
        requirementLink,
        notes,
        serviceSelections,
        requirementMaterials,
    )
    suspend fun previewAgentsAsync(
        folderName: String,
        branch: String,
        groupId: String,
        serviceIds: Set<String>,
        requirementLink: String,
        notes: String,
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        requirementMaterials: RequirementMaterialsDirectory = RequirementMaterialsDirectory(),
    ): AgentDocumentPreview = agentInstructionsController.previewAsync(
        folderName,
        branch,
        groupId,
        serviceIds,
        requirementLink,
        notes,
        serviceSelections,
        requirementMaterials,
    )
    fun previewTaskAgents(task: TaskManifest, notes: String): AgentDocumentPreview = agentInstructionsController.previewTask(task, notes)
    suspend fun previewTaskAgentsAsync(task: TaskManifest, notes: String): AgentDocumentPreview = agentInstructionsController.previewTaskAsync(task, notes)
    fun createTask(
        folderName: String,
        branch: String,
        groupId: String,
        serviceIds: List<String>,
        requirementLink: String,
        notes: String,
        workspaceToolIds: List<String> = emptyList(),
        confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onCompleted: () -> Unit = {},
    ) = taskController.create(
        folderName,
        branch,
        groupId,
        serviceIds,
        requirementLink,
        notes,
        workspaceToolIds,
        confirmedBranchReuseKeys,
        serviceSelections,
        onCompleted,
    )

    fun retryWorkspaceTool(task: TaskManifest, toolId: String) = taskController.retryWorkspaceTool(task, toolId)

    fun readTaskNotes(task: TaskManifest): String = agentInstructionsController.readTaskNotes(task)
    suspend fun readTaskNotesAsync(task: TaskManifest): String = agentInstructionsController.readTaskNotesAsync(task)
    fun saveTaskNotes(task: TaskManifest, notes: String) = agentInstructionsController.saveTaskNotes(task, notes)
    fun markTaskNotesEdited(task: TaskManifest, notes: String) = agentInstructionsController.markTaskNotesEdited(task, notes)
    fun archiveTask(task: TaskManifest, force: Boolean = false, onCompleted: () -> Unit = {}) = taskController.archive(task, onCompleted)

    fun restoreTask(task: TaskManifest, onCompleted: () -> Unit = {}) = taskController.restore(task, onCompleted)

    /** Runs Git safety checks away from Compose's event-dispatch thread. */
    fun requestDeleteRisk(task: TaskManifest) = taskController.requestDeleteRisk(task)

    fun clearDeleteRisk(task: TaskManifest) = taskController.clearDeleteRisk(task)

    fun deleteTask(task: TaskManifest, forceDiscard: Boolean, onCompleted: () -> Unit = {}) =
        taskController.delete(task, forceDiscard, onCompleted)

    fun retryRequirementMaterials(task: TaskManifest, onCompleted: () -> Unit = {}) =
        taskController.retryRequirementMaterials(task, onCompleted)

    fun buildTag(task: TaskManifest, workspace: ServiceWorkspace): Boolean {
        if (!config.tagEnabled) {
            showError(IllegalStateException("全局测试Tag已关闭"))
            return false
        }
        return deliveryController.build(task, workspace).also { started -> if (started) navigation = NavigationItem.TAG }
    }

    fun buildTags(task: TaskManifest, workspaces: List<ServiceWorkspace>): Boolean {
        if (!config.tagEnabled) {
            showError(IllegalStateException("全局测试Tag已关闭"))
            return false
        }
        return deliveryController.buildBatch(task, workspaces).also { started -> if (started) navigation = NavigationItem.TAG }
    }

    /** Resolves the exact task workspace recorded by a Tag operation. */
    fun tagOperationWorkspace(operation: TagOperation): ServiceWorkspace? =
        sessionStore.tasks
            .firstOrNull { it.folderName == operation.folderName }
            ?.services
            ?.singleOrNull { workspace ->
                workspace.groupServiceId == operation.groupServiceId && workspace.moduleId == operation.moduleId
            }

    /** Opens only the configured IDE for a conflict workspace; no Git command is run. */
    fun openConflictWorkspace(operation: TagOperation) {
        if (operation.state != com.snowball.silverwing.core.TagOperationState.CONFLICT) {
            showError(IllegalStateException("只有冲突测试Tag才能打开冲突工作区"))
            return
        }
        val task = sessionStore.tasks.firstOrNull { it.folderName == operation.folderName }
        if (task == null) {
            showError(IllegalStateException("找不到测试Tag对应的研发任务：${operation.folderName}"))
            return
        }
        val workspace = tagOperationWorkspace(operation)
        if (workspace == null) {
            showError(IllegalStateException("找不到测试Tag对应的服务工作区：${operation.groupServiceId}/${operation.moduleId}"))
            return
        }
        desktopActions.openWorkspace(workspace)
    }

    /** Re-runs a conflict Tag on the same operation record after manual resolution. */
    fun retryConflict(operation: TagOperation): Boolean {
        if (operation.state != com.snowball.silverwing.core.TagOperationState.CONFLICT) {
            showError(IllegalStateException("只有冲突测试Tag才能重试"))
            return false
        }
        return withBuildableTagOperation(operation) { task -> deliveryController.retryConflict(task, operation) }
    }

    /** Rechecks the exact conflict workspace without starting another Tag flow. */
    fun inspectConflictWorkspace(operation: TagOperation): Boolean {
        if (!tagOperationCanInspectWorkspace(operation)) {
            showError(IllegalStateException("当前测试Tag无需检测工作区"))
            return false
        }
        val task = sessionStore.tasks.firstOrNull { it.folderName == operation.folderName }
        if (task == null) {
            showError(IllegalStateException("找不到测试Tag对应的研发任务：${operation.folderName}"))
            return false
        }
        if (tagOperationWorkspace(operation) == null) {
            showError(IllegalStateException("找不到测试Tag对应的服务工作区：${operation.groupServiceId}/${operation.moduleId}"))
            return false
        }
        return deliveryController.inspectConflictWorkspace(task, operation)
    }

    fun conflictWorkspaceCheck(operation: TagOperation) = deliveryController.workspaceCheck(operation.operationId)

    /** Restarts a safely interrupted Tag operation on its original history record. */
    fun retryInterruptedTag(operation: TagOperation): Boolean {
        if (!operation.isRetryableInterruptedTag()) {
            showError(IllegalStateException("只有构建中断的测试Tag才能重试"))
            return false
        }
        return withBuildableTagOperation(operation) { task -> deliveryController.retryInterrupted(task, operation) }
    }

    /** Retries a locally failed Tag build on its original history record. */
    fun retryFailedTag(operation: TagOperation): Boolean {
        if (operation.state != com.snowball.silverwing.core.TagOperationState.FAILED) {
            showError(IllegalStateException("只有失败的测试Tag才能重试"))
            return false
        }
        return withBuildableTagOperation(operation) { task -> deliveryController.retryFailed(task, operation) }
    }

    /** Pushes the already-created local Tag of a partially completed build. */
    fun resumePartialTag(operation: TagOperation): Boolean {
        if (operation.state != com.snowball.silverwing.core.TagOperationState.PARTIAL) {
            showError(IllegalStateException("只有部分完成的测试Tag才能继续构建"))
            return false
        }
        return withBuildableTagOperation(operation) { task -> deliveryController.resumePartial(task, operation) }
    }

    /** Rebuilds a Genbu-failed Tag with the next version on the same history record. */
    fun retagBuildFailedTag(operation: TagOperation): Boolean {
        if (
            com.snowball.silverwing.core.tagRetryKind(operation) != com.snowball.silverwing.core.TagRetryKind.RETAG
        ) {
            showError(IllegalStateException(
                if (operation.genbuStatus.failureReason != null) "Genbu 实时状态查询失败，请刷新状态后再判断是否重新打Tag"
                else "只有 Genbu 构建失败的测试Tag才能重新打Tag",
            ))
            return false
        }
        return withBuildableTagOperation(operation) { task -> deliveryController.retag(task, operation) }
    }

    /** Resolves the task and workspace of a history record and verifies it can still build. */
    private fun withBuildableTagOperation(operation: TagOperation, action: (TaskManifest) -> Boolean): Boolean {
        val task = sessionStore.tasks.firstOrNull { it.folderName == operation.folderName }
        if (task == null) {
            showError(IllegalStateException("找不到测试Tag对应的研发任务：${operation.folderName}"))
            return false
        }
        val workspace = tagOperationWorkspace(operation)
        if (workspace == null) {
            showError(IllegalStateException("找不到测试Tag对应的服务工作区：${operation.groupServiceId}/${operation.moduleId}"))
            return false
        }
        if (!canBuildTag(task, workspace)) {
            showError(IllegalStateException("当前服务工作区不可构建测试Tag，请确认任务和服务仍处于就绪状态"))
            return false
        }
        return action(task)
    }

    private fun TagOperation.isRetryableInterruptedTag(): Boolean = state in setOf(
        com.snowball.silverwing.core.TagOperationState.CREATED,
        com.snowball.silverwing.core.TagOperationState.PREFLIGHT_PASSED,
        com.snowball.silverwing.core.TagOperationState.SOURCE_BRANCH_PUSHED,
    )

    fun addServices(
        task: TaskManifest,
        serviceIds: List<String>,
        confirmedBranchReuseKeys: Set<BranchReuseKey> = emptySet(),
        serviceSelections: List<TaskServiceSelection> = emptyList(),
        onCompleted: () -> Unit = {},
    ) = taskController.addServices(task, serviceIds, confirmedBranchReuseKeys, serviceSelections, onCompleted)

    fun retryFailedServices(task: TaskManifest, serviceIds: List<String>? = null) = taskController.retry(task, serviceIds)

    fun branchInfo(task: TaskManifest): String = TaskBranchInfoFormatter.format(task)

    fun branchInfo(task: TaskManifest, includeRequirementLink: Boolean): String =
        TaskBranchInfoFormatter.formatBranchInfo(task, includeRequirementLink)

    fun branchServices(task: TaskManifest, includeRequirementLink: Boolean): String =
        TaskBranchInfoFormatter.formatServices(task, includeRequirementLink)

    fun openWorkData(task: TaskManifest, type: com.snowball.silverwing.core.DevelopmentToolType = config.defaultDevelopmentTool) =
        desktopActions.openWorkData(taskDirectory(task), type)

    fun configuredDevelopmentTools(): List<com.snowball.silverwing.core.DevelopmentToolType> = config.developmentTools.map { it.type }

    fun defaultCommitMessage(task: TaskManifest, workspace: ServiceWorkspace) = taskController.defaultCommitMessage(task, workspace)
    fun commitWorkspace(task: TaskManifest, workspace: ServiceWorkspace, message: String, pushAfter: Boolean = false, expectedFingerprint: String? = null) =
        taskController.commit(task, workspace, message, pushAfter, expectedFingerprint)
    fun pushWorkspace(task: TaskManifest, workspace: ServiceWorkspace) = taskController.push(task, workspace)
    fun physicalWorkspaces(task: TaskManifest) = taskController.physicalWorkspaces(task)
    fun workspaceKey(workspace: ServiceWorkspace) = taskController.workspaceKey(workspace)
    fun batchGit(
        task: TaskManifest,
        mode: WorkspaceGitBatchMode,
        selectedWorkspaceKeys: Set<String>,
        commitMessages: Map<String, String> = emptyMap(),
        expectedFingerprints: Map<String, String> = emptyMap(),
        onCompleted: (WorkspaceGitBatchResult) -> Unit,
    ) = taskController.batchGit(task, mode, selectedWorkspaceKeys, commitMessages, expectedFingerprints, onCompleted)
    fun loadBatchGitPreviews(task: TaskManifest) = taskController.loadBatchGitPreviews(task)
    suspend fun previewWorkspaceFile(worktreePath: String, change: WorkspaceGitFileChange): WorkspaceGitFilePreview =
        taskController.previewWorkspaceFile(worktreePath, change)

    suspend fun workspaceGitHistory(worktreePath: String): List<WorkspaceGitCommit> =
        taskController.workspaceGitHistory(worktreePath)

    val workspaceCommandState: WorkspaceCommandExecutionState?
        get() = taskController.workspaceCommandState
    val workspaceCommandRunning: Boolean
        get() = taskController.workspaceCommandRunning
    fun workspaceCommands(task: TaskManifest, workspace: ServiceWorkspace): List<WorkspaceCommandConfig> =
        taskController.workspaceCommands(task, workspace)
    fun runWorkspaceCommand(task: TaskManifest, workspace: ServiceWorkspace, command: WorkspaceCommandConfig): Boolean =
        taskController.runWorkspaceCommand(task, workspace, command)
    fun cancelWorkspaceCommand() = taskController.cancelWorkspaceCommand()
    fun dismissWorkspaceCommand() = taskController.dismissWorkspaceCommand()


    fun openWorkspace(workspace: ServiceWorkspace, type: com.snowball.silverwing.core.DevelopmentToolType = workspace.developmentTool) =
        desktopActions.openWorkspace(workspace, type)

    fun testDevelopmentTool(type: com.snowball.silverwing.core.DevelopmentToolType, path: String) {
        val target = config.taskRoot?.let(Path::of)?.takeIf(java.nio.file.Files::isDirectory)
        if (target == null) {
            showError(IllegalStateException("请先配置有效的任务根目录，再测试打开开发工具"))
            return
        }
        runCatching { desktopIntegration.openDevelopmentTool(target, type, path) }.onFailure(::showError)
    }

    fun refreshErrorLog() {
        recentErrors = errorLogReader.latest()
    }

    fun openLogDirectory() {
        runCatching {
            java.nio.file.Files.createDirectories(paths.logs)
            desktopIntegration.openDirectory(paths.logs)
        }.onFailure(::showError)
    }

    fun configBackups(): List<ConfigStore.Backup> = runCatching { configStore.backups() }.getOrDefault(emptyList())
    suspend fun configBackupsAsync(): List<ConfigStore.Backup> = runInterruptible(ioDispatcher) { configStore.backups() }
    fun previewConfigImport(path: String): ConfigStore.ImportPreview = configStore.previewImport(Path.of(path))
    suspend fun previewConfigImportAsync(path: String): ConfigStore.ImportPreview = runInterruptible(ioDispatcher) {
        configStore.previewImport(Path.of(path))
    }
    suspend fun existingDevelopmentToolPathsAsync(paths: Map<DevelopmentToolType, String>): Set<DevelopmentToolType> =
        runInterruptible(ioDispatcher) {
            paths.asSequence()
                .filter { (_, path) -> path.isNotBlank() && runCatching { java.nio.file.Files.exists(Path.of(path)) }.getOrDefault(false) }
                .map { (type, _) -> type }
                .toSet()
        }
    internal suspend fun requirementMaterialsActionGroupAsync(path: String?): RequirementMaterialsActionGroup? =
        runInterruptible(ioDispatcher) { requirementMaterialsActionGroupFor(path) }

    fun configurationRecoveryGuidance(): String = recoveryGuidance(
        subject = "配置目录",
        path = configFileSnapshot.path.toString(),
        reason = configurationLoadError.orEmpty(),
    )

    fun taskManifestRecoveryGuidance(issue: TaskManifestIssue): String = recoveryGuidance(
        subject = "任务清单",
        path = issue.manifestPath,
        reason = issue.reason,
    )

    /** Refreshes the read-only configuration shard preview without parsing or writing it. */
    fun refreshConfigFileSnapshot() {
        if (configFileSnapshotRefreshing) return
        configFileSnapshotRefreshing = true
        scope.launch {
            try {
                configFileSnapshot = withContext(ioDispatcher) { configStore.fileSnapshot() }
            } finally {
                configFileSnapshotRefreshing = false
            }
        }
    }

    fun revealConfigFile() {
        if (!configFileSnapshot.exists) {
            showError(IllegalStateException("配置目录尚未创建：${configFileSnapshot.path}"))
            return
        }
        desktopActions.reveal(configFileSnapshot.path)
    }

    /** Re-scans the task root after a user repairs a task manifest outside silverwing. */
    fun refreshTaskManifestIssues() = reloadTasks()

    fun restoreConfigBackup(path: String): Boolean = operationRunner.run(
        "正在恢复配置备份…",
        "配置备份已恢复",
        block = { configStore.restore(Path.of(path)) },
        onSuccess = { applyConfig(it); reloadTasks() },
    )

    fun importConfig(path: String): Boolean = operationRunner.run(
        "正在导入配置…",
        "配置已导入",
        block = { configStore.importFrom(Path.of(path)) },
        onSuccess = { applyConfig(it); reloadTasks() },
    )

    fun exportConfig(): Boolean = operationRunner.run(
        "正在导出配置…",
        "配置已导出",
        block = { configStore.exportTo(paths.backups.resolve("silverwing-config-${System.currentTimeMillis()}.zip")) },
        onSuccess = { desktopIntegration.reveal(it) },
    )

    fun exportDiagnostics(): Boolean = operationRunner.run(
        "正在生成诊断包…",
        "诊断包已生成",
        cancellable = true,
        block = { diagnosticsExporter.export(config, configurationLoadError ?: taskScanWarning) },
        onSuccess = { desktopIntegration.reveal(it) },
    )

    fun reveal(path: String) = desktopActions.reveal(Path.of(path))
    fun openDirectory(path: String) = desktopActions.openDirectory(Path.of(path))

    fun revealGlobalAgents() = agentInstructionsController.revealGlobal()
    fun revealGroupAgents(groupId: String) = agentInstructionsController.revealGroup(groupId)
    fun terminal(path: String) = desktopActions.terminal(Path.of(path))
    fun copyCliCommandPath(command: String) = desktopActions.copyCliCommandPath(command)
    fun runCliInTerminal(command: String) = desktopActions.runCliInTerminal(command)
    fun openUrl(url: String) = desktopActions.openUrl(url)
    fun copyText(text: String, message: String = "已复制") = desktopActions.copy(text, message)

    /** Called from Window.onFocusEvent as the inexpensive external-file fallback. */
    fun onWindowFocused() {
        agentInstructionsController.onWindowFocused()
        refreshConfigFileSnapshot()
        refreshGenbuCommandResolution()
    }
    fun resolveAgentConflict(resolution: AgentConflictResolution) = agentInstructionsController.resolveConflict(resolution)
    fun dismissMessages() {
        operationCoordinator.dismiss()
    }

    fun showError(error: Throwable) {
        operationCoordinator.errorMessage = OperationFailureDetails.format(error)
        recordError(error)
    }

    private fun recordError(error: Throwable) {
        events.error(
            event = "application.error",
            message = OperationFailureDetails.format(error),
            metadata = mapOf("exception" to (error::class.qualifiedName ?: error::class.simpleName.orEmpty())),
        )
        recentErrors = errorLogReader.latest()
    }

    override fun close() {
        requirementController.close()
        agentMonitor.close()
        scope.cancel()
    }

    private fun taskDirectory(task: TaskManifest): Path = taskDirectory(config, task)

    private fun taskDirectory(config: AppConfig, task: TaskManifest): Path =
        Path.of(requireNotNull(config.taskRoot) { "尚未配置任务根目录" }).resolve(task.taskDirectoryName)

    private fun scanTasks(config: AppConfig): LoadedTasks {
        val root = config.taskRoot?.let(Path::of) ?: return LoadedTasks(emptyList())
        val scan = runCatching { manifests.scan(root) }.getOrElse { error ->
            return LoadedTasks(emptyList(), "任务目录扫描失败：${error.message ?: error::class.simpleName}")
        }
        val issues = buildList {
            scan.unsupportedDirectories.forEach { directory ->
                add(
                    TaskManifestIssue(
                        manifestPath = directory.resolve(ManifestStore.FILE_NAME).toAbsolutePath().normalize().toString(),
                        reason = scan.unsupportedReasons[directory] ?: "任务清单版本与当前 silverwing 不兼容",
                    ),
                )
            }
            scan.failures.forEach { (directory, reason) ->
                add(
                    TaskManifestIssue(
                        manifestPath = directory.resolve(ManifestStore.FILE_NAME).toAbsolutePath().normalize().toString(),
                        reason = reason,
                    ),
                )
            }
        }
        val messages = buildList {
            if (scan.unsupportedDirectories.isNotEmpty()) {
                add("已忽略 ${scan.unsupportedDirectories.size} 个与当前 silverwing $CURRENT_PRODUCT_VERSION 不兼容的任务目录")
            }
            if (scan.failures.isNotEmpty()) {
                add(
                    "${scan.failures.size} 个任务清单读取失败：" +
                        scan.failures.entries.joinToString { (path, reason) -> "${path.fileName}（$reason）" },
                )
            }
        }
        return LoadedTasks(
            manifests = scan.current.map { it.second }.sortedByDescending(TaskManifest::updatedAt),
            warning = messages.takeIf { it.isNotEmpty() }?.joinToString("；"),
            manifestIssues = issues,
        )
    }

    private fun reloadTasks(preferredFolder: String? = selectedTask?.folderName) {
        val loaded = scanTasks(config)
        taskScanWarning = loaded.warning
        taskManifestIssues = loaded.manifestIssues
        sessionStore.replaceTasks(loaded.manifests, preferredFolder)
        deliveryController.reloadHistory()
        requirementController.reconcileTasks()
        refreshCurrentTaskGitStatus()
        loaded.warning?.let { showError(IllegalStateException(it)) }
    }

    private fun applyConfig(updated: AppConfig) {
        val terminalConfigurationChanged = config.terminalExecutable != updated.terminalExecutable
        val requirementConfigurationChanged = config.meegleProjects != updated.meegleProjects ||
            config.requirementMaterialsRoot != updated.requirementMaterialsRoot ||
            config.requirementMaterialsSubdirectory != updated.requirementMaterialsSubdirectory
        val tagConfigurationChanged = config.tagEnabled != updated.tagEnabled ||
            config.tagHistoryMaxGroups != updated.tagHistoryMaxGroups
        config = updated
        configurationLoadError = null
        if (navigation == NavigationItem.TAG && !TagNavigationPolicy.isVisible(updated)) {
            navigation = NavigationItem.TASKS
        }
        if (!updated.tagEnabled) deliveryController.setGenbuTagProbeVisible(false)
        if (tagConfigurationChanged) deliveryController.reloadHistory()
        repositories = updated.repositories.map(RepositoryConfig::toInfo)
        if (terminalConfigurationChanged) detectTerminalInBackground()
        if (requirementConfigurationChanged) requirementController.onConfigurationChanged()
        updated.groups.forEach { group ->
            runCatching {
                agentDocuments.ensureGroupFile(group.id)
                agentMonitor.track(paths.groupAgents(group.id))
            }.onFailure(::showError)
        }
        refreshConfigFileSnapshot()
    }

    private fun detectDevelopmentToolsInBackground() {
        val startupConfig = config
        scope.launch {
            try {
                val result = withContext(ioDispatcher) {
                    developmentToolStartupDetection.detectAndPersist(startupConfig)
                }
                val configuredNow = config.developmentTools.map { it.type }.toSet()
                val detectedAdditions = result.config.developmentTools.filter {
                    it.type in result.addedTypes && it.type !in configuredNow
                }
                if (detectedAdditions.isNotEmpty()) {
                    applyConfig(config.copy(developmentTools = config.developmentTools + detectedAdditions))
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Throwable) {
                // Startup discovery is best-effort: missing tools or local probe
                // failures must never block startup or surface a dialog.
            }
        }
    }

    private fun detectTerminalInBackground() {
        val configuredTerminal = config.terminalExecutable
        val generation = ++terminalDetectionGeneration
        scope.launch {
            try {
                val resolution = withContext(ioDispatcher) {
                    desktopIntegration.terminalResolution(configuredTerminal)
                }
                if (generation == terminalDetectionGeneration) resolvedTerminal = resolution
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Throwable) {
                if (generation == terminalDetectionGeneration) {
                    resolvedTerminal = TerminalLaunchCommand.resolve(configuredTerminal)
                }
            }
        }
    }

    private fun showStatus(message: String) {
        operationCoordinator.statusMessage = message
        operationCoordinator.errorMessage = null
    }

    private fun recoveryGuidance(subject: String, path: String, reason: String): String = buildString {
        appendLine("$subject 加载失败")
        appendLine("文件：$path")
        if (reason.isNotBlank()) appendLine("原因：$reason")
        append("请先备份该文件；确认无需保留时，可在文件管理器中手动删除它，然后重新打开 silverwing。")
    }

}
