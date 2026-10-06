package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.RequirementAiNamingModel
import com.snowball.silverwing.core.SilverWingTime
import com.snowball.silverwing.core.BatchRepositoryAddResult
import com.snowball.silverwing.core.ConfigStore
import com.snowball.silverwing.core.GroupConfigurationService
import com.snowball.silverwing.core.TaskCreationDefaultsService
import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.MeegleProjectConfig
import com.snowball.silverwing.core.DevelopmentToolConfig
import com.snowball.silverwing.core.DevelopmentToolType
import com.snowball.silverwing.core.MeegleProjectCatalog
import com.snowball.silverwing.core.MeegleCliService
import com.snowball.silverwing.core.MeegleCliStatus
import com.snowball.silverwing.core.MeegleDeviceCodeChallenge
import com.snowball.silverwing.core.MeegleDeviceCodeLoginResult
import com.snowball.silverwing.core.MeegleCommandSource
import com.snowball.silverwing.core.MeegleExecutable
import com.snowball.silverwing.core.normalizeMeegleExecutablePath
import com.snowball.silverwing.core.CodexCommandSource
import com.snowball.silverwing.core.CodexExecutable
import com.snowball.silverwing.core.normalizeCodexExecutablePath
import com.snowball.silverwing.core.LarkCliService
import com.snowball.silverwing.core.LarkCliStatus
import com.snowball.silverwing.core.LarkAuthenticationState
import com.snowball.silverwing.core.LarkDeviceCodeChallenge
import com.snowball.silverwing.core.LarkCommandSource
import com.snowball.silverwing.core.LarkExecutable
import com.snowball.silverwing.core.normalizeLarkExecutablePath
import com.snowball.silverwing.core.GitCommandSource
import com.snowball.silverwing.core.GitExecutable
import com.snowball.silverwing.core.GenbuCommandSource
import com.snowball.silverwing.core.GenbuExecutable
import com.snowball.silverwing.core.normalizeGenbuExecutablePath
import com.snowball.silverwing.core.normalizeGitExecutablePath
import com.snowball.silverwing.core.MeegleProjectSummary
import com.snowball.silverwing.core.LocalGitEnvironmentInspector
import com.snowball.silverwing.core.LocalGitEnvironmentSnapshot
import com.snowball.silverwing.core.CommandVersionProbe
import com.snowball.silverwing.core.CommandVersionStatus
import com.snowball.silverwing.core.CommandRunner
import com.snowball.silverwing.core.CommandProxyTarget
import com.snowball.silverwing.core.commandProxyAuthentication
import com.snowball.silverwing.core.normalizeCommandProxyNoProxy
import com.snowball.silverwing.core.normalizeCommandProxyUrl
import com.snowball.silverwing.core.ProcessCommandRunner
import com.snowball.silverwing.core.RemoteBranchCatalog
import com.snowball.silverwing.core.RepositoryRemoteCatalog
import com.snowball.silverwing.core.GitRepositoryRemoteCatalog
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskRootMigrationPreview
import com.snowball.silverwing.core.TaskRootMigrationProgress
import com.snowball.silverwing.core.TaskRootMigrationResult
import com.snowball.silverwing.core.TaskRootMigrationService
import com.snowball.silverwing.core.ThemePreference
import com.snowball.silverwing.core.WorkspaceStrategy
import com.snowball.silverwing.core.validateRequirementMaterialsSubdirectory
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.withContext
import java.nio.file.Files
import java.nio.file.Path

data class SettingsUiState(
    val config: AppConfig,
    val remoteBranches: Map<String, RemoteBranchesState>,
    val repositoryRemotes: Map<String, RepositoryRemotesState>,
    val repositoryAddResult: BatchRepositoryAddResult?,
    val pathPickerBusy: Boolean,
    val meegleProjects: MeegleProjectCatalogState,
    val meegleCli: MeegleCliState,
    val larkCli: LarkCliState,
    val localGit: LocalGitSettingsState,
    val genbu: GenbuSettingsState,
    val saveStates: Map<String, SettingsSaveState>,
    val taskRootMigration: TaskRootMigrationUiState,
)

enum class SettingsSaveState { IDLE, SAVING, SAVED, FAILED }

sealed interface TaskRootMigrationUiState {
    data object Idle : TaskRootMigrationUiState
    data class Preview(val preview: TaskRootMigrationPreview) : TaskRootMigrationUiState
    data class Migrating(
        val preview: TaskRootMigrationPreview,
        val progress: TaskRootMigrationProgress? = null,
    ) : TaskRootMigrationUiState
}

private sealed interface TaskRootChangeOutcome {
    data class PreviewRequired(val preview: TaskRootMigrationPreview) : TaskRootChangeOutcome
    data class Applied(val result: TaskRootMigrationResult) : TaskRootChangeOutcome
}

sealed interface MeegleProjectCatalogState {
    data object Idle : MeegleProjectCatalogState
    data object Loading : MeegleProjectCatalogState
    data class Loaded(val projects: List<MeegleProjectSummary>) : MeegleProjectCatalogState
    data class Failed(val message: String) : MeegleProjectCatalogState
}

sealed interface MeegleCliState {
    data object Idle : MeegleCliState
    data class Loading(val previous: MeegleCliStatus? = null) : MeegleCliState
    data class Ready(val status: MeegleCliStatus) : MeegleCliState
    data class Failed(val message: String) : MeegleCliState
}

sealed interface LarkCliState {
    data object Idle : LarkCliState
    data class Loading(val previous: LarkCliStatus? = null) : LarkCliState
    data class Ready(val status: LarkCliStatus) : LarkCliState
    data class Failed(val message: String) : LarkCliState
}

/** 只保存可展示的信息；设备码本身只留在核心服务的短期挑战对象中。 */
data class MeegleDeviceCodeLoginUiState(
    val authorizationUrl: String,
    val userCode: String,
    val expiresInSeconds: Long,
    /** 自动轮询期间不再要求用户额外点击“完成授权”。 */
    val polling: Boolean = false,
    /** 仅保存已脱敏的可恢复错误，方便用户决定重新检测还是重新生成验证码。 */
    val error: String? = null,
)

data class LarkDeviceCodeLoginUiState(
    val authorizationUrl: String,
    val expiresInSeconds: Long,
    /** 只用于页面显示倒计时，不包含授权凭据。 */
    val expiresAtEpochMillis: Long? = null,
    val polling: Boolean = false,
    val error: String? = null,
)

/** 设备码通常只存活数分钟；限制异常响应的值，避免溢出或无限后台轮询。 */
private const val MAX_MEEGLE_DEVICE_CODE_LIFETIME_SECONDS = 24L * 60L * 60L
private const val MAX_MEEGLE_DEVICE_CODE_POLL_DELAY_MILLIS = 60_000L
private const val MAX_MEEGLE_DEVICE_CODE_ERROR_LENGTH = 240
private const val MAX_LARK_DEVICE_CODE_LIFETIME_SECONDS = 24L * 60L * 60L
private const val MAX_LARK_DEVICE_CODE_ERROR_LENGTH = 240

sealed interface LocalGitSettingsState {
    data object Idle : LocalGitSettingsState
    data class Loading(val previous: LocalGitEnvironmentSnapshot? = null) : LocalGitSettingsState
    data class Loaded(val snapshot: LocalGitEnvironmentSnapshot) : LocalGitSettingsState
    data class Failed(val message: String) : LocalGitSettingsState
}

sealed interface GenbuSettingsState {
    data object Idle : GenbuSettingsState
    data object Loading : GenbuSettingsState
    data class Loaded(
        val command: String,
        val source: GenbuCommandSource,
        val detectedAt: String,
        val version: CommandVersionStatus? = null,
    ) : GenbuSettingsState
    data class Failed(val message: String) : GenbuSettingsState
}

sealed interface RepositoryRemotesState {
    data object Idle : RepositoryRemotesState
    data object Loading : RepositoryRemotesState
    data class Loaded(val remotes: List<String>) : RepositoryRemotesState
    data class Failed(val message: String) : RepositoryRemotesState
}

private data class MeegleExecutableAutoSave(
    val config: AppConfig,
    val savedDetectedPath: Boolean,
)

private data class LarkExecutableAutoSave(
    val config: AppConfig,
    val savedDetectedPath: Boolean,
)

private data class GitExecutableAutoSave(
    val config: AppConfig,
    val savedDetectedPath: Boolean,
)

private data class GenbuExecutableAutoSave(
    val config: AppConfig,
    val savedDetectedPath: Boolean,
)

/** Configuration and repository use cases with no dependency on DesktopApplication. */
class SettingsController internal constructor(
    private val session: AppSessionStore,
    private val configStore: ConfigStore,
    private val groups: GroupConfigurationService,
    private val taskCreationDefaults: TaskCreationDefaultsService = TaskCreationDefaultsService(configStore),
    private val taskRootMigrations: TaskRootMigrationService,
    private val pathPicker: NativePathPicker,
    private val branchCatalog: RemoteBranchCatalog,
    private val remoteCatalog: RepositoryRemoteCatalog = GitRepositoryRemoteCatalog(),
    private val meegleProjectCatalog: MeegleProjectCatalog,
    private val meegleCliService: MeegleCliService,
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(),
    private val codexExecutable: CodexExecutable = CodexExecutable.pathFallback(),
    private val larkExecutable: LarkExecutable = LarkExecutable.pathFallback(),
    private val larkCliService: LarkCliService = com.snowball.silverwing.core.ProcessLarkCliService(larkExecutable = larkExecutable),
    private val gitExecutable: GitExecutable = GitExecutable.pathFallback(),
    private val genbuExecutable: GenbuExecutable = GenbuExecutable.pathFallback(),
    private val cliVersionRunner: CommandRunner = ProcessCommandRunner(),
    private val genbuVersionRunner: CommandRunner = cliVersionRunner,
    private val localGitInspector: LocalGitEnvironmentInspector,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
    private val operations: OperationRunner,
    private val settingsOperations: OperationRunner,
    private val meegleOperations: OperationRunner,
    private val larkOperations: OperationRunner = meegleOperations,
    private val applyConfig: (AppConfig) -> Unit,
    private val reloadTasks: () -> Unit,
    private val showError: (Throwable) -> Unit,
    private val showStatus: (String) -> Unit,
    /** 将浏览器打开动作留在桌面层，便于测试且不让设置控制器依赖 AWT。 */
    private val openMeegleAuthorizationPage: (String) -> Result<Unit> = { Result.success(Unit) },
    private val openLarkAuthorizationPage: (String) -> Result<Unit> = { Result.success(Unit) },
) {
    private val remoteBranchJobs = mutableMapOf<String, Job>()
    private val repositoryRemoteJobs = mutableMapOf<String, Job>()
    private var remoteRepositoryRoots by mutableStateOf<Map<String, String>>(emptyMap())
    private var remoteBranches by mutableStateOf<Map<String, RemoteBranchesState>>(emptyMap())
    private var repositoryRemotes by mutableStateOf<Map<String, RepositoryRemotesState>>(emptyMap())
    private var repositoryAddResult by mutableStateOf<BatchRepositoryAddResult?>(null)
    private var pathPickerBusy by mutableStateOf(false)
    private var pathPickerJob: Job? = null
    private var meegleProjects by mutableStateOf<MeegleProjectCatalogState>(MeegleProjectCatalogState.Idle)
    private var meegleProjectJob: Job? = null
    private var meegleCli by mutableStateOf<MeegleCliState>(MeegleCliState.Idle)
    /**
     * 状态刷新与设备码登录会并行运行。每次新请求递增版本号，避免较早的 CLI
     * 结果在较晚返回时覆盖已经确认成功的登录状态。
     */
    private var meegleStatusRefreshGeneration = 0L
    private var meegleStatusRefreshJob: Job? = null
    private var meegleDeviceCodeLogin by mutableStateOf<MeegleDeviceCodeLoginUiState?>(null)
    private var meegleDeviceCodeChallenge: MeegleDeviceCodeChallenge? = null
    private var meegleDeviceCodePollJob: Job? = null
    private var meegleDeviceCodeExpiresAtMillis: Long? = null
    private var meegleDeviceCodeBrowserOpenFailed = false
    private var larkCli by mutableStateOf<LarkCliState>(LarkCliState.Idle)
    private var larkStatusRefreshGeneration = 0L
    private var larkStatusRefreshJob: Job? = null
    private var larkDeviceCodeLogin by mutableStateOf<LarkDeviceCodeLoginUiState?>(null)
    private var larkDeviceCodeChallenge: LarkDeviceCodeChallenge? = null
    private var larkDeviceCodePollJob: Job? = null
    private var larkDeviceCodeExpiresAtMillis: Long? = null
    private var localGit by mutableStateOf<LocalGitSettingsState>(LocalGitSettingsState.Idle)
    private var genbu by mutableStateOf<GenbuSettingsState>(GenbuSettingsState.Idle)
    private var genbuJob: Job? = null
    private var localGitJob: Job? = null
    private var genbuCommand by mutableStateOf(genbuExecutable.current() to genbuExecutable.source())
    private var saveStates by mutableStateOf<Map<String, SettingsSaveState>>(emptyMap())
    private var taskRootMigration by mutableStateOf<TaskRootMigrationUiState>(TaskRootMigrationUiState.Idle)

    val state: SettingsUiState
        get() = SettingsUiState(
            session.config,
            remoteBranches,
            repositoryRemotes,
            repositoryAddResult,
            pathPickerBusy,
            meegleProjects,
            meegleCli,
            larkCli,
            localGit,
            genbu,
            saveStates,
            taskRootMigration,
        )

    fun saveState(key: String): SettingsSaveState = saveStates[key] ?: SettingsSaveState.IDLE

    val meegleDeviceCodeLoginState: MeegleDeviceCodeLoginUiState? get() = meegleDeviceCodeLogin
    val larkDeviceCodeLoginState: LarkDeviceCodeLoginUiState? get() = larkDeviceCodeLogin

    fun updateMeegleProjects(projects: List<MeegleProjectConfig>, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存飞书需求配置…",
        "飞书需求配置已保存",
        onFailure,
        "feishu",
        settingsOperations,
    ) { config ->
        config.copy(
            meegleProjects = projects,
            meegleDefaultSprintProjectKey = config.meegleDefaultSprintProjectKey?.takeIf { key ->
                projects.any { it.projectKey == key }
            },
        )
    }

    fun updateMeegleDefaultSprintProjectKey(projectKey: String?, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存默认 Sprint 团队空间…",
        "默认 Sprint 团队空间已保存",
        onFailure,
        "feishu",
        settingsOperations,
    ) { config ->
        require(projectKey == null || config.meegleProjects.any { it.projectKey == projectKey }) {
            "默认 Sprint 团队空间必须是已配置的 Meegle 项目"
        }
        config.copy(meegleDefaultSprintProjectKey = projectKey)
    }

    fun updateMeegleExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 Meegle 命令路径…",
        "Meegle 命令路径已保存",
        onFailure,
        "feishu",
        settingsOperations,
        onCompleted = { refreshMeegleStatus(force = true) },
    ) { config ->
        config.copy(meegleExecutablePath = normalizeMeegleExecutablePath(raw))
    }

    /** The currently effective Meegle command and where it came from; safe on the UI thread. */
    fun meegleCommandResolution(): Pair<String, MeegleCommandSource> =
        meegleExecutable.current() to meegleExecutable.source()

    fun updateCodexExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 Codex CLI 命令路径…",
        "Codex CLI 命令路径已保存",
        onFailure,
        "task-creation",
        settingsOperations,
    ) { config ->
        config.copy(codexExecutablePath = normalizeCodexExecutablePath(raw))
    }

    /** The currently effective Codex command and where it came from; safe on the UI thread. */
    fun codexCommandResolution(): Pair<String, CodexCommandSource> =
        codexExecutable.current() to codexExecutable.source()

    /** Saves the shared proxy endpoint and the exact command families that may use it. */
    fun updateCommandProxy(
        rawUrl: String,
        rawNoProxy: String,
        rawUsername: String,
        rawPassword: String,
        targets: Set<CommandProxyTarget>,
        onFailure: (Throwable) -> Unit = {},
    ): Boolean = mutate(
        "正在保存命令网络代理…",
        "命令网络代理已保存",
        onFailure,
        "network-proxy",
        settingsOperations,
    ) { config ->
        val normalized = normalizeCommandProxyUrl(rawUrl)
        val normalizedNoProxy = normalizeCommandProxyNoProxy(rawNoProxy)
        val authentication = commandProxyAuthentication(rawUsername, rawPassword)
        require(normalized != null || (normalizedNoProxy == null && authentication == null)) {
            "请先填写代理主机和端口，再设置例外地址或认证信息"
        }
        config.copy(
            commandProxyUrl = normalized,
            commandProxyNoProxy = if (normalized == null) null else normalizedNoProxy,
            commandProxyUsername = if (normalized == null) null else authentication?.username,
            commandProxyPassword = if (normalized == null) null else authentication?.password,
            commandProxyTargets = if (normalized == null) emptySet() else targets,
        )
    }

    fun updateLarkExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 Lark CLI 命令路径…",
        "Lark CLI 命令路径已保存",
        onFailure,
        "lark",
        settingsOperations,
        onCompleted = { refreshLarkStatus(force = true) },
    ) { config ->
        config.copy(larkExecutablePath = normalizeLarkExecutablePath(raw))
    }

    fun larkCommandResolution(): Pair<String, LarkCommandSource> =
        larkExecutable.current() to larkExecutable.source()

    fun updateGitExecutablePath(raw: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 Git 命令路径…",
        "Git 命令路径已保存",
        onFailure,
        "git",
        settingsOperations,
        onCompleted = { refreshLocalGit(force = true) },
    ) { config ->
        config.copy(gitExecutablePath = normalizeGitExecutablePath(raw))
    }

    /** The currently effective Git command and where it came from; safe on the UI thread. */
    fun gitCommandResolution(): Pair<String, GitCommandSource> =
        gitExecutable.current() to gitExecutable.source()

    fun genbuCommandResolution(): Pair<String, GenbuCommandSource> {
        val configured = session.config.genbuExecutablePath?.takeIf(String::isNotBlank)
        if (configured == null) return genbuCommand
        val source = if (session.config.genbuExecutableAutoDetected) GenbuCommandSource.PROBED else GenbuCommandSource.CONFIGURED
        return configured to source
    }

    /** Lightweight window-focus refresh: detects once, persists a first-time result, writes no audit. */
    fun refreshGenbuCommandResolution() {
        val existingGenbuPath = session.config.genbuExecutablePath
        val existingAutoDetected = session.config.genbuExecutableAutoDetected
        val shouldAutoDetect = existingGenbuPath.isNullOrBlank()
        scope.launch {
            val (autoSave, resolution) = withContext(ioDispatcher) {
                runCatching {
                    val autoSave = autoSaveGenbuExecutablePath(shouldAutoDetect, existingGenbuPath)
                    autoSave to (genbuExecutable.current() to genbuExecutable.source())
                }.getOrElse {
                    null to (genbuExecutable.current() to genbuExecutable.source())
                }
            }
            autoSave?.let {
                applyGenbuAutoSave(it, existingGenbuPath, existingAutoDetected)
                if (it.savedDetectedPath) {
                    setSaveState("genbu", SettingsSaveState.SAVED)
                    showStatus("已自动检测并保存 Genbu 命令路径")
                }
            }
            genbuCommand = resolution
        }
    }

    fun updateGenbuExecutablePath(
        rawGenbuPath: String,
        onFailure: (Throwable) -> Unit = {},
    ): Boolean = mutate(
        "正在保存 Genbu 路径…",
        "Genbu 路径已保存",
        onFailure,
        "genbu",
        settingsOperations,
        onCompleted = { refreshGenbu(force = true) },
    ) { config ->
        val normalized = normalizeGenbuExecutablePath(rawGenbuPath)
        config.copy(
            genbuExecutablePath = normalized,
            genbuExecutableAutoDetected = config.genbuExecutableAutoDetected &&
                normalized == config.genbuExecutablePath,
        )
    }

    fun refreshGenbu(force: Boolean = false) {
        if (!force && genbu is GenbuSettingsState.Loading) return
        if (force) genbuJob?.cancel()
        val existingGenbuPath = session.config.genbuExecutablePath
        val existingAutoDetected = session.config.genbuExecutableAutoDetected
        val shouldAutoDetect = existingGenbuPath.isNullOrBlank() ||
            session.config.genbuExecutableAutoDetected
        genbu = GenbuSettingsState.Loading
        genbuJob = scope.launch {
            val (autoSave, result) = withContext(ioDispatcher) {
                val autoSaveResult = runCatching { autoSaveGenbuExecutablePath(shouldAutoDetect, existingGenbuPath) }
                if (autoSaveResult.isFailure) {
                    null to Result.failure(autoSaveResult.exceptionOrNull()!!)
                } else {
                    autoSaveResult.getOrNull() to runCatching {
                        // A fresh detection result is authoritative; the in-memory configured
                        // path still holds the pre-save value until applyConfig runs.
                        val saved = autoSaveResult.getOrNull()?.config?.genbuExecutablePath
                        val command = saved ?: genbuExecutable.probe()
                        val source = if (saved != null) GenbuCommandSource.PROBED else genbuExecutable.source()
                        Triple(command, source, readCliVersion(command))
                    }
                }
            }
            autoSave?.let {
                applyGenbuAutoSave(it, existingGenbuPath, existingAutoDetected)
                if (it.savedDetectedPath) {
                    setSaveState("genbu", SettingsSaveState.SAVED)
                    showStatus("已自动检测并保存 Genbu 命令路径")
                }
            }
            val detectedAt = SilverWingTime.format(java.time.Instant.now())
            val resolved = result.fold(
                onSuccess = { (command, source, version) -> GenbuSettingsState.Loaded(
                    command,
                    if (session.config.genbuExecutableAutoDetected) GenbuCommandSource.PROBED else source,
                    detectedAt,
                    version,
                ) },
                onFailure = { GenbuSettingsState.Failed(it.message ?: "检测 Genbu 失败") },
            )
            genbu = resolved
            if (resolved is GenbuSettingsState.Loaded) genbuCommand = resolved.command to resolved.source
        }
    }

    private fun readCliVersion(command: String): CommandVersionStatus =
        CommandVersionProbe.probe(command, genbuVersionRunner)

    private fun applyGenbuAutoSave(saved: GenbuExecutableAutoSave, expectedPath: String?, expectedAutoDetected: Boolean) {
        val current = session.config
        if (current.genbuExecutablePath == expectedPath && current.genbuExecutableAutoDetected == expectedAutoDetected) {
            applyConfig(current.copy(
                genbuExecutablePath = saved.config.genbuExecutablePath,
                genbuExecutableAutoDetected = saved.config.genbuExecutableAutoDetected,
            ))
        }
    }

    private fun autoSaveGenbuExecutablePath(shouldAutoDetect: Boolean, existingPath: String?): GenbuExecutableAutoSave? {
        if (!shouldAutoDetect) return null
        // A still-valid auto-detected path stays authoritative; rescanning while it works could
        // silently swap in a different copy sitting earlier in the scan order. Only a broken
        // path (deleted/moved installation) triggers a fresh scan that may replace it.
        val existingValid = !existingPath.isNullOrBlank() &&
            runCatching { normalizeGenbuExecutablePath(existingPath) }.getOrNull() != null
        if (existingValid) return null
        val detected = genbuExecutable.detect() ?: return null
        val normalized = normalizeGenbuExecutablePath(detected)
            ?: error("自动探测到的 Genbu 命令路径为空")
        var savedDetectedPath = false
        val updated = configStore.update { current ->
            val replaceable = current.genbuExecutablePath.isNullOrBlank() || current.genbuExecutableAutoDetected
            if (replaceable && current.genbuExecutablePath != normalized) {
                savedDetectedPath = true
                current.copy(genbuExecutablePath = normalized, genbuExecutableAutoDetected = true)
            } else current
        }
        return GenbuExecutableAutoSave(updated, savedDetectedPath)
    }

    fun setTheme(theme: ThemePreference): Boolean = mutate(
        "正在更新主题…",
        "主题已更新",
        saveKey = "basic",
        runner = settingsOperations,
    ) { it.copy(theme = theme) }

    fun setGlobalTagEnabled(enabled: Boolean, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在更新全局测试Tag开关…",
        "全局测试Tag开关已更新",
        onFailure = onFailure,
        saveKey = "tag",
        runner = settingsOperations,
    ) { it.copy(tagEnabled = enabled) }

    fun setAllowTaskTagTargetEditing(enabled: Boolean, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在更新任务测试目标分支修改开关…",
        "任务测试目标分支修改开关已更新",
        onFailure = onFailure,
        saveKey = "tag",
        runner = settingsOperations,
    ) { it.copy(allowTaskTagTargetEditing = enabled) }

    fun updateTagHistoryMaxGroups(value: Int, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存Tag历史设置…",
        "Tag历史设置已保存",
        onFailure = onFailure,
        saveKey = "tag",
        runner = settingsOperations,
    ) { it.copy(tagHistoryMaxGroups = value) }

    fun updateTaskRoot(value: String, onFailure: (Throwable) -> Unit = {}): Boolean = settingsOperations.run(
        "正在检查任务根目录…",
        "任务根目录检查完成",
        block = {
            val path = Path.of(value).toAbsolutePath().normalize()
            val preview = taskRootMigrations.preview(session.config, path)
            require(preview.canMigrate) { preview.blockers.joinToString("；") }
            if (preview.taskCount == 0) {
                TaskRootChangeOutcome.Applied(taskRootMigrations.migrate(session.config, path))
            } else {
                TaskRootChangeOutcome.PreviewRequired(preview)
            }
        },
        onSuccess = { outcome ->
            when (outcome) {
                is TaskRootChangeOutcome.Applied -> applyTaskRootMigration(outcome.result)
                is TaskRootChangeOutcome.PreviewRequired -> {
                    taskRootMigration = TaskRootMigrationUiState.Preview(outcome.preview)
                    setSaveState("paths", SettingsSaveState.IDLE)
                }
            }
        },
        onFailure = { setSaveState("paths", SettingsSaveState.FAILED); onFailure(it) },
    ).also { started -> setSaveState("paths", if (started) SettingsSaveState.SAVING else SettingsSaveState.FAILED) }

    fun confirmTaskRootMigration(onFailure: (Throwable) -> Unit = {}): Boolean {
        val preview = (taskRootMigration as? TaskRootMigrationUiState.Preview)?.preview ?: return false
        taskRootMigration = TaskRootMigrationUiState.Migrating(preview)
        return settingsOperations.run(
            "正在迁移任务目录…",
            "任务目录迁移完成",
            block = {
                taskRootMigrations.migrate(session.config, preview.targetRoot) { progress ->
                    scope.launch {
                        val current = taskRootMigration as? TaskRootMigrationUiState.Migrating
                        if (current?.preview?.targetRoot == preview.targetRoot) {
                            taskRootMigration = current.copy(progress = progress)
                        }
                    }
                }
            },
            onSuccess = ::applyTaskRootMigration,
            onFailure = { error ->
                taskRootMigration = TaskRootMigrationUiState.Preview(preview)
                setSaveState("paths", SettingsSaveState.FAILED)
                onFailure(error)
            },
        ).also { started ->
            setSaveState("paths", if (started) SettingsSaveState.SAVING else SettingsSaveState.FAILED)
            if (!started) taskRootMigration = TaskRootMigrationUiState.Preview(preview)
        }
    }

    fun cancelTaskRootMigration() {
        if (taskRootMigration is TaskRootMigrationUiState.Preview) {
            taskRootMigration = TaskRootMigrationUiState.Idle
            setSaveState("paths", SettingsSaveState.IDLE)
        }
    }

    private fun applyTaskRootMigration(result: TaskRootMigrationResult) {
        applyConfig(result.config)
        reloadTasks()
        taskRootMigration = TaskRootMigrationUiState.Idle
        setSaveState("paths", SettingsSaveState.SAVED)
        if (result.cleanupFailures.isEmpty()) {
            showStatus(if (result.migratedTasks == 0) "任务根目录已保存" else "已迁移 ${result.migratedTasks} 个任务")
        } else {
            showStatus("迁移已生效，以下旧目录待清理：${result.cleanupFailures.joinToString()}")
        }
    }

    /** Saves and normalizes the optional requirement-materials root independently. */
    fun updateRequirementMaterialsRoot(value: String, onFailure: (Throwable) -> Unit = {}): Boolean = settingsOperations.run(
        "正在保存任务资料根目录…",
        "任务资料根目录已保存",
        block = {
            val normalized = value.trim().takeIf(String::isNotEmpty)?.let {
                val path = Path.of(it).toAbsolutePath().normalize()
                Files.createDirectories(path)
                path.toString()
            }
            configStore.update { it.copy(requirementMaterialsRoot = normalized) }
        },
        onSuccess = { applyConfig(it); setSaveState("requirement-materials-root", SettingsSaveState.SAVED) },
        onFailure = { setSaveState("requirement-materials-root", SettingsSaveState.FAILED); onFailure(it) },
    ).also { started -> setSaveState("requirement-materials-root", if (started) SettingsSaveState.SAVING else SettingsSaveState.FAILED) }

    /** Saves the optional single-segment child directory independently. */
    fun updateRequirementMaterialsSubdirectory(value: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存任务资料子目录…",
        "任务资料子目录已保存",
        onFailure,
        "requirement-materials-subdirectory",
        settingsOperations,
    ) { config ->
        config.copy(requirementMaterialsSubdirectory = validateRequirementMaterialsSubdirectory(value))
    }

    fun updateDevelopmentTools(
        tools: List<DevelopmentToolConfig>,
        defaultTool: DevelopmentToolType,
        terminal: String,
        allowTemporaryDevelopmentToolSelection: Boolean,
        onFailure: (Throwable) -> Unit = {},
    ): Boolean = mutate(
        "正在保存开发工具配置…",
        "开发工具配置已保存",
        onFailure,
        "tools",
        settingsOperations,
    ) {
        it.copy(
            developmentTools = tools,
            defaultDevelopmentTool = defaultTool,
            terminalExecutable = terminal.trim().ifBlank { null },
            allowTemporaryDevelopmentToolSelection = allowTemporaryDevelopmentToolSelection,
        )
    }

    /** Persists all task-area action and inline-information visibility preferences together. */
    fun updateTaskAreaToolGroupVisibility(
        taskGit: Boolean,
        taskPath: Boolean,
        workspaceGit: Boolean,
        workspacePath: Boolean,
        branchCopyIcons: Boolean,
        requirementCopyIcons: Boolean,
        projectNameCopyIcons: Boolean,
        onFailure: (Throwable) -> Unit = {},
    ): Boolean = mutate(
        "正在保存任务区工具栏设置…",
        "任务区工具栏设置已保存",
        onFailure,
        "task-area",
        settingsOperations,
    ) {
        it.copy(
            showTaskDetailGitActionGroup = taskGit,
            showTaskDetailPathActionGroup = taskPath,
            showWorkspaceGitActionGroup = workspaceGit,
            showWorkspacePathActionGroup = workspacePath,
            // Saving the individual controls upgrades a legacy global-off choice into
            // equivalent per-group values, so the user can selectively re-enable one.
            showTaskAreaCopyIcons = true,
            showTaskAreaBranchCopyIcons = branchCopyIcons,
            showTaskAreaRequirementCopyIcons = requirementCopyIcons,
            showTaskAreaProjectNameCopyIcons = projectNameCopyIcons,
        )
    }

    fun updateBlockedGitWriteBranches(branches: List<String>, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 Git 写保护分支…",
        "Git 写保护已保存",
        onFailure,
        "git-write-policy",
        settingsOperations,
    ) { config ->
        val normalized = branches.map(String::trim).filter(String::isNotEmpty)
        require(normalized.map(String::lowercase).distinct().size == normalized.size) { "Git 写保护分支不能重复（忽略大小写）" }
        config.copy(blockedGitWriteBranches = normalized)
    }

    fun refreshLocalGit(force: Boolean = false) {
        if (!force && localGit is LocalGitSettingsState.Loading) return
        localGitJob?.cancel()
        val existingGitPath = session.config.gitExecutablePath
        val shouldAutoDetect = existingGitPath.isNullOrBlank()
        localGit = LocalGitSettingsState.Loading((localGit as? LocalGitSettingsState.Loaded)?.snapshot)
        localGitJob = scope.launch {
            val (autoSave, result) = withContext(ioDispatcher) {
                val autoSaveResult = runCatching { autoSaveGitExecutablePath(shouldAutoDetect) }
                if (autoSaveResult.isFailure) {
                    null to Result.failure(autoSaveResult.exceptionOrNull()!!)
                } else {
                    autoSaveResult.getOrNull() to runCatching { localGitInspector.inspect() }
                }
            }
            autoSave?.let {
                val current = session.config
                if (current.gitExecutablePath == existingGitPath) {
                    applyConfig(current.copy(gitExecutablePath = it.config.gitExecutablePath))
                }
                if (it.savedDetectedPath) {
                    setSaveState("git", SettingsSaveState.SAVED)
                    showStatus("已自动检测并保存 Git 命令路径")
                }
            }
            if (shouldAutoDetect && autoSave == null && result.isFailure) {
                setSaveState("git", SettingsSaveState.FAILED)
            }
            localGit = result.fold(
                onSuccess = { LocalGitSettingsState.Loaded(it) },
                onFailure = { LocalGitSettingsState.Failed(it.message ?: "读取本地 Git 信息失败") },
            )
        }
    }

    private fun autoSaveGitExecutablePath(shouldAutoDetect: Boolean): GitExecutableAutoSave? {
        if (!shouldAutoDetect) return null
        val detected = gitExecutable.probe()
        if (gitExecutable.source() != GitCommandSource.PROBED) return null
        val normalized = normalizeGitExecutablePath(detected)
            ?: error("自动探测到的 Git 命令路径为空")
        var savedDetectedPath = false
        val updated = configStore.update { current ->
            if (current.gitExecutablePath.isNullOrBlank()) {
                savedDetectedPath = true
                current.copy(gitExecutablePath = normalized)
            } else {
                current
            }
        }
        return GitExecutableAutoSave(updated, savedDetectedPath)
    }

    fun addGroup(name: String, onCompleted: () -> Unit = {}) = mutateWithService("正在创建组…", "组已创建", onCompleted) { groups.addGroup(name) }
    fun renameGroup(groupId: String, name: String, onCompleted: () -> Unit = {}) = mutateWithService("正在重命名组…", "组已重命名", onCompleted) { groups.renameGroup(groupId, name) }
    fun moveGroup(groupId: String, offset: Int) = mutateWithService("正在更新组顺序…", "组顺序已更新") { groups.moveGroup(groupId, offset) }
    fun deleteGroup(groupId: String, onCompleted: () -> Unit = {}) = mutateWithService("正在删除空组…", "空组已删除", onCompleted) {
        require(session.tasks.none { it.groupId == groupId }) { "该组还有研发任务，不能删除" }
        groups.deleteGroup(groupId)
    }
    fun setGroupTagEnabled(groupId: String, enabled: Boolean) = mutateWithService(
        "正在更新组测试Tag开关…",
        "组测试Tag开关已更新",
        saveKey = "groups",
        runner = settingsOperations,
    ) {
        groups.setGroupTagEnabled(groupId, enabled)
    }
    fun updateDefaultBranchPrefix(prefix: String, onFailure: (Throwable) -> Unit = {}) = mutateWithService(
        "正在保存默认分支名前缀…",
        "默认分支名前缀已保存",
        onFailure = onFailure,
        saveKey = "branch-naming",
        runner = settingsOperations,
    ) {
        taskCreationDefaults.updateBranchPrefix(prefix)
    }

    fun updateDefaultWorkspaceToolIds(toolIds: List<String>, onFailure: (Throwable) -> Unit = {}) = mutateWithService(
        "正在保存任务创建默认工具…",
        "任务创建默认工具已保存",
        onFailure = onFailure,
        saveKey = "task-creation",
        runner = settingsOperations,
    ) {
        taskCreationDefaults.updateWorkspaceToolIds(toolIds)
    }

    fun setAiRequirementNamingEnabled(enabled: Boolean, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 AI 命名设置…",
        if (enabled) "已开启 AI 需求命名" else "已关闭 AI 需求命名",
        onFailure = onFailure,
        saveKey = "task-creation",
        runner = settingsOperations,
    ) { it.copy(aiRequirementNamingEnabled = enabled) }

    fun setAiRequirementNamingPrewarmEnabled(enabled: Boolean): Boolean = mutate(
        "正在保存命名预热设置…", "命名预热设置已保存", saveKey = "task-creation", runner = settingsOperations,
    ) { it.copy(aiRequirementNamingPrewarmEnabled = enabled) }

    fun setAiRequirementNamingModel(model: String, onFailure: (Throwable) -> Unit = {}): Boolean = mutate(
        "正在保存 AI 命名模型…",
        "AI 命名模型已保存",
        onFailure = onFailure,
        saveKey = "task-creation",
        runner = settingsOperations,
    ) { it.copy(aiRequirementNamingModel = RequirementAiNamingModel.requireValid(model.trim())) }

    fun refreshMeegleStatus(force: Boolean = false) {
        if (!force && meegleCli is MeegleCliState.Loading) return
        meegleStatusRefreshJob?.cancel()
        val generation = ++meegleStatusRefreshGeneration
        val existingMeeglePath = session.config.meegleExecutablePath
        val shouldAutoDetect = existingMeeglePath.isNullOrBlank()
        meegleCli = MeegleCliState.Loading((meegleCli as? MeegleCliState.Ready)?.status)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            try {
                val (autoSave, result) = withContext(ioDispatcher) {
                    val autoSaveResult = runCatching { autoSaveMeegleExecutablePath(shouldAutoDetect) }
                    if (autoSaveResult.isFailure) {
                        null to Result.failure(autoSaveResult.exceptionOrNull()!!)
                    } else {
                        autoSaveResult.getOrNull() to runCatching { meegleCliService.status() }
                    }
                }
                // 旧请求即使已经在后台完成，也不能再改写当前页面的登录状态。
                if (!isCurrentMeegleStatusRefresh(generation, currentJob)) return@launch
                autoSave?.let {
                    val current = session.config
                    if (current.meegleExecutablePath == existingMeeglePath) {
                        applyConfig(current.copy(meegleExecutablePath = it.config.meegleExecutablePath))
                    }
                    if (it.savedDetectedPath) {
                        setSaveState("feishu", SettingsSaveState.SAVED)
                        showStatus("已自动检测并保存 Meegle 命令路径")
                    }
                }
                if (shouldAutoDetect && autoSave == null && result.isFailure) {
                    setSaveState("feishu", SettingsSaveState.FAILED)
                }
                meegleCli = result.fold(
                    onSuccess = { MeegleCliState.Ready(it) },
                    onFailure = { MeegleCliState.Failed(it.message ?: "检查 Meegle CLI 状态失败") },
                )
                if (result.getOrNull()?.authenticated == true) {
                    clearMeegleDeviceCodeLogin()
                    loadMeegleProjects(force = true)
                }
            } finally {
                if (meegleStatusRefreshJob === currentJob) meegleStatusRefreshJob = null
            }
        }
        meegleStatusRefreshJob = job
        job.start()
    }

    private fun autoSaveMeegleExecutablePath(shouldAutoDetect: Boolean): MeegleExecutableAutoSave? {
        if (!shouldAutoDetect) return null
        val detected = meegleExecutable.probe()
        if (meegleExecutable.source() != MeegleCommandSource.PROBED) return null
        val normalized = normalizeMeegleExecutablePath(detected)
            ?: error("自动探测到的 Meegle 命令路径为空")
        var savedDetectedPath = false
        val updated = configStore.update { current ->
            if (current.meegleExecutablePath.isNullOrBlank()) {
                savedDetectedPath = true
                current.copy(meegleExecutablePath = normalized)
            } else {
                current
            }
        }
        return MeegleExecutableAutoSave(updated, savedDetectedPath)
    }

    fun refreshLarkStatus(force: Boolean = false) {
        if (!force && larkCli is LarkCliState.Loading) return
        larkStatusRefreshJob?.cancel()
        val generation = ++larkStatusRefreshGeneration
        val existingPath = session.config.larkExecutablePath
        val shouldAutoDetect = existingPath.isNullOrBlank()
        larkCli = LarkCliState.Loading((larkCli as? LarkCliState.Ready)?.status)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            try {
                val (autoSave, result) = withContext(ioDispatcher) {
                    val autoSaveResult = runCatching { autoSaveLarkExecutablePath(shouldAutoDetect) }
                    if (autoSaveResult.isFailure) {
                        null to Result.failure(autoSaveResult.exceptionOrNull()!!)
                    } else {
                        autoSaveResult.getOrNull() to runCatching { larkCliService.status() }
                    }
                }
                if (!isCurrentLarkStatusRefresh(generation, currentJob)) return@launch
                autoSave?.let {
                    val current = session.config
                    if (current.larkExecutablePath == existingPath) {
                        applyConfig(current.copy(larkExecutablePath = it.config.larkExecutablePath))
                    }
                    if (it.savedDetectedPath) {
                        setSaveState("lark", SettingsSaveState.SAVED)
                        showStatus("已自动检测并保存 Lark CLI 命令路径")
                    }
                }
                if (shouldAutoDetect && autoSave == null && result.isFailure) {
                    setSaveState("lark", SettingsSaveState.FAILED)
                }
                larkCli = result.fold(
                    onSuccess = { LarkCliState.Ready(it) },
                    onFailure = { LarkCliState.Failed(it.message ?: "检查 Lark CLI 状态失败") },
                )
            } finally {
                if (larkStatusRefreshJob === currentJob) larkStatusRefreshJob = null
            }
        }
        larkStatusRefreshJob = job
        job.start()
    }

    private fun autoSaveLarkExecutablePath(shouldAutoDetect: Boolean): LarkExecutableAutoSave? {
        if (!shouldAutoDetect) return null
        val detected = larkExecutable.probe()
        if (larkExecutable.source() != LarkCommandSource.PROBED) return null
        val normalized = normalizeLarkExecutablePath(detected)
            ?: error("自动探测到的 Lark CLI 命令路径为空")
        var savedDetectedPath = false
        val updated = configStore.update { current ->
            if (current.larkExecutablePath.isNullOrBlank()) {
                savedDetectedPath = true
                current.copy(larkExecutablePath = normalized)
            } else {
                current
            }
        }
        return LarkExecutableAutoSave(updated, savedDetectedPath)
    }

    fun logoutLark(): Boolean {
        val started = larkOperations.run(
            activeMessage = "正在退出 Lark 登录…",
            successMessage = "Lark 已退出登录",
            cancellable = true,
            block = larkCliService::logout,
            onSuccess = {
                markLarkLoggedOut()
                refreshLarkStatus(force = true)
            },
        )
        if (started) {
            invalidateLarkStatusRefresh()
            clearLarkDeviceCodeLogin()
        }
        return started
    }

    private fun markLarkLoggedOut() {
        val previous = (larkCli as? LarkCliState.Ready)?.status
        larkCli = LarkCliState.Ready(
            previous?.copy(
                authenticated = false,
                authenticationState = LarkAuthenticationState.LOGIN_REQUIRED,
                tokenStatus = null,
                expiresAt = null,
                authenticationError = null,
            )
                ?: LarkCliStatus(installed = true, authenticated = false),
        )
        clearLarkDeviceCodeLogin()
    }

    fun loginLark(domains: List<String>): Boolean {
        if (domains.isEmpty()) {
            showError(IllegalArgumentException("至少选择一个 Lark 业务域"))
            return false
        }
        // 生成新授权前先取消旧挑战；成功回调在后台异步执行，不能在 run 返回后清理新挑战。
        clearLarkDeviceCodeLogin()
        val started = larkOperations.run(
            activeMessage = "正在生成 Lark 登录授权链接…",
            successMessage = "已打开 Lark 授权页，正在自动检测登录状态",
            cancellable = true,
            block = { larkCliService.beginDeviceCodeLogin(domains) },
            onSuccess = { challenge ->
                larkDeviceCodeChallenge = challenge
                larkDeviceCodeExpiresAtMillis = deviceCodeExpiresAtMillis(challenge.expiresInSeconds)
                larkDeviceCodeLogin = LarkDeviceCodeLoginUiState(
                    authorizationUrl = challenge.verificationUrl,
                    expiresInSeconds = challenge.expiresInSeconds,
                    expiresAtEpochMillis = larkDeviceCodeExpiresAtMillis,
                )
                val browserError = openLarkAuthorizationPage(challenge.verificationUrl).exceptionOrNull()
                startLarkDeviceCodePolling(challenge, browserError?.let { larkBrowserErrorMessage(challenge, it) })
            },
        )
        if (started) {
            invalidateLarkStatusRefresh()
        }
        return started
    }

    fun openLarkDeviceCodeAuthorizationUrl(): Boolean {
        val challenge = larkDeviceCodeChallenge ?: run {
            showError(IllegalStateException("请先生成 Lark 登录授权链接"))
            return false
        }
        val error = openLarkAuthorizationPage(challenge.verificationUrl).exceptionOrNull()
        updateLarkDeviceCodeLogin(challenge) {
            it.copy(error = error?.let { failure -> larkBrowserErrorMessage(challenge, failure) })
        }
        return error == null
    }

    fun cancelLarkDeviceCodeLogin() = clearLarkDeviceCodeLogin()

    private fun startLarkDeviceCodePolling(challenge: LarkDeviceCodeChallenge, initialError: String? = null) {
        if (larkDeviceCodeChallenge !== challenge) return
        larkDeviceCodePollJob?.cancel()
        updateLarkDeviceCodeLogin(challenge) { it.copy(polling = true, error = initialError) }
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            try {
                if (isLarkDeviceCodeExpired()) {
                    markLarkDeviceCodeExpired(challenge)
                    return@launch
                }
                try {
                    runInterruptible(ioDispatcher) { larkCliService.completeDeviceCodeLogin(challenge) }
                    val status = runInterruptible(ioDispatcher) { larkCliService.status() }
                    if (status.authenticated && isCurrentLarkDeviceCodePolling(challenge, currentJob)) {
                        invalidateLarkStatusRefresh()
                        larkCli = LarkCliState.Ready(status)
                        clearLarkDeviceCodeLogin(cancelPolling = false)
                        showStatus("Lark 登录成功")
                        return@launch
                    }
                    updateLarkDeviceCodeLogin(challenge) {
                        it.copy(polling = false, error = "授权已完成，但未能确认 Lark 登录状态，请重新检测。")
                    }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (error: Throwable) {
                    if (isCurrentLarkDeviceCodePolling(challenge, currentJob)) {
                        val message = larkPollingErrorMessage(challenge, error)
                        clearLarkDeviceCodeLogin(cancelPolling = false)
                        showError(IllegalStateException(message))
                    }
                }
            } finally {
                if (larkDeviceCodePollJob === currentJob) larkDeviceCodePollJob = null
            }
        }
        larkDeviceCodePollJob = job
        job.start()
    }

    private fun isCurrentLarkDeviceCodePolling(challenge: LarkDeviceCodeChallenge, job: Job?): Boolean =
        larkDeviceCodeChallenge === challenge && larkDeviceCodePollJob === job

    private fun isCurrentLarkStatusRefresh(generation: Long, job: Job?): Boolean =
        larkStatusRefreshGeneration == generation && larkStatusRefreshJob === job

    private fun invalidateLarkStatusRefresh() {
        larkStatusRefreshGeneration += 1
        larkStatusRefreshJob?.cancel()
        larkStatusRefreshJob = null
    }

    private fun updateLarkDeviceCodeLogin(
        challenge: LarkDeviceCodeChallenge,
        update: (LarkDeviceCodeLoginUiState) -> LarkDeviceCodeLoginUiState,
    ) {
        if (larkDeviceCodeChallenge === challenge) larkDeviceCodeLogin = larkDeviceCodeLogin?.let(update)
    }

    private fun isLarkDeviceCodeExpired(): Boolean =
        larkDeviceCodeExpiresAtMillis?.let { System.currentTimeMillis() >= it } ?: true

    private fun markLarkDeviceCodeExpired(challenge: LarkDeviceCodeChallenge) {
        if (larkDeviceCodeChallenge === challenge) {
            clearLarkDeviceCodeLogin(cancelPolling = false)
            showError(IllegalStateException("Lark 授权链接已过期，请重新登录。"))
        }
    }

    private fun deviceCodeExpiresAtMillis(expiresInSeconds: Long): Long =
        System.currentTimeMillis() + expiresInSeconds.coerceAtMost(MAX_LARK_DEVICE_CODE_LIFETIME_SECONDS) * 1_000L

    private fun larkPollingErrorMessage(challenge: LarkDeviceCodeChallenge, error: Throwable): String {
        val detail = challenge.redactSecrets(error.message.orEmpty())
            .lineSequence()
            .firstOrNull()
            ?.trim()
            ?.take(MAX_LARK_DEVICE_CODE_ERROR_LENGTH)
            .orEmpty()
        return if (detail.isBlank()) "自动检测 Lark 授权失败，请重试。" else "自动检测 Lark 授权失败：$detail"
    }

    private fun larkBrowserErrorMessage(challenge: LarkDeviceCodeChallenge, error: Throwable): String =
        challenge.redactSecrets(error.message.orEmpty())
            .lineSequence()
            .firstOrNull()
            ?.trim()
            ?.take(MAX_LARK_DEVICE_CODE_ERROR_LENGTH)
            .orEmpty()
            .ifBlank { "无法打开 Lark 授权页，请手动复制授权链接。" }

    private fun clearLarkDeviceCodeLogin(cancelPolling: Boolean = true) {
        if (cancelPolling) larkDeviceCodePollJob?.cancel()
        larkDeviceCodePollJob = null
        larkDeviceCodeChallenge = null
        larkDeviceCodeExpiresAtMillis = null
        larkDeviceCodeLogin = null
    }

    /**
     * 只退出 Meegle CLI 的本地身份，不删除用户已保存的项目映射；重新登录后仍可复用它们。
     * 退出开始时同时终止设备码轮询和旧状态查询，避免它们把 UI 又改回“已登录”。
     */
    fun logoutMeegle(): Boolean {
        val started = meegleOperations.run(
            activeMessage = "正在退出 Meegle 登录…",
            successMessage = "Meegle 已退出登录",
            cancellable = true,
            block = meegleCliService::logout,
            onSuccess = {
                markMeegleLoggedOut()
                // 再读取一次真实 CLI 状态，能识别环境变量或其他 profile 仍提供凭据的情况。
                refreshMeegleStatus(force = true)
            },
        )
        if (started) {
            invalidateMeegleStatusRefresh()
            clearMeegleDeviceCodeLogin()
        }
        return started
    }

    private fun markMeegleLoggedOut() {
        val previous = (meegleCli as? MeegleCliState.Ready)?.status
        meegleCli = MeegleCliState.Ready(
            previous?.copy(
                authenticated = false,
                expiresInMinutes = null,
                authenticationError = null,
            ) ?: MeegleCliStatus(installed = true, authenticated = false),
        )
        clearMeegleDeviceCodeLogin()
        meegleProjectJob?.cancel()
        meegleProjectJob = null
        // 只清理本次会话中读取到的目录缓存，绝不删除用户保存的项目配置。
        meegleProjects = MeegleProjectCatalogState.Idle
    }

    /**
     * 子进程没有交互式终端，不能执行需要 localhost 回调的默认 OAuth 流程。
     * 因此使用设备码：生成后自动打开浏览器，并在后台按服务端给出的间隔轮询授权状态。
     */
    fun loginMeegle(): Boolean {
        // 生成新授权前先取消旧挑战；成功回调在后台异步执行，不能在 run 返回后清理新挑战。
        clearMeegleDeviceCodeLogin()
        val started = meegleOperations.run(
            activeMessage = "正在生成 Meegle 登录验证码…",
            successMessage = "已打开 Meegle 授权页，正在自动检测登录状态",
            cancellable = true,
            block = { meegleCliService.beginDeviceCodeLogin("project.feishu.cn") },
            onSuccess = { challenge ->
                meegleDeviceCodeChallenge = challenge
                meegleDeviceCodeExpiresAtMillis = deviceCodeExpiresAtMillis(challenge)
                meegleDeviceCodeLogin = MeegleDeviceCodeLoginUiState(
                    authorizationUrl = challenge.authorizationUrl,
                    userCode = challenge.userCode,
                    expiresInSeconds = challenge.expiresInSeconds,
                )
                val browserOpenError = openMeegleAuthorizationUrl(challenge)
                meegleDeviceCodeBrowserOpenFailed = browserOpenError != null
                startMeegleDeviceCodePolling(challenge, initialError = browserOpenError)
            },
        )
        if (started) {
            // 新登录已经开始，先使旧的“未登录”检查失效，避免它稍后覆盖授权结果。
            invalidateMeegleStatusRefresh()
        }
        return started
    }

    /** 用户在自动检测暂时失败后可立即再试一次；正常流程无需点击此操作。 */
    fun completeMeegleDeviceCodeLogin(): Boolean {
        val challenge = meegleDeviceCodeChallenge ?: run {
            showError(IllegalStateException("请先生成 Meegle 登录验证码"))
            return false
        }
        if (meegleDeviceCodePollJob?.isActive == true) return false
        if (isMeegleDeviceCodeExpired()) {
            updateMeegleDeviceCodeLogin(challenge) { it.copy(polling = false, error = "授权码已过期，请重新生成验证码。") }
            return false
        }
        startMeegleDeviceCodePolling(challenge, initialDelayMillis = 0)
        return true
    }

    /** 自动打开失败时，用户仍可从卡片手动重新打开授权页。 */
    fun openMeegleDeviceCodeAuthorizationUrl(): Boolean {
        val challenge = meegleDeviceCodeChallenge ?: run {
            showError(IllegalStateException("请先生成 Meegle 登录验证码"))
            return false
        }
        val error = openMeegleAuthorizationUrl(challenge)
        meegleDeviceCodeBrowserOpenFailed = error != null
        updateMeegleDeviceCodeLogin(challenge) { it.copy(error = error) }
        return error == null
    }

    fun cancelMeegleDeviceCodeLogin() {
        clearMeegleDeviceCodeLogin()
    }

    /**
     * 使用独立 Job 轮询，避免把几分钟的设备码等待占用为一个全局“忙碌”操作。
     * Job 只会更新与自己对应的挑战，重新生成或取消验证码时旧 Job 的结果会被忽略。
     */
    private fun startMeegleDeviceCodePolling(
        challenge: MeegleDeviceCodeChallenge,
        initialDelayMillis: Long = challenge.pollingIntervalSeconds * 1_000L,
        initialError: String? = null,
    ) {
        if (meegleDeviceCodeChallenge !== challenge) return
        meegleDeviceCodePollJob?.cancel()
        updateMeegleDeviceCodeLogin(challenge) { it.copy(polling = true, error = initialError) }
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            var pollDelayMillis = initialDelayMillis.coerceAtLeast(0L)
            var authorizationAccepted = false
            try {
                while (isCurrentMeegleDeviceCodePolling(challenge, currentJob)) {
                    if (isMeegleDeviceCodeExpired()) {
                        markMeegleDeviceCodeExpired(challenge, authorizationAccepted)
                        return@launch
                    }
                    if (pollDelayMillis > 0) delay(pollDelayMillis)
                    if (!isCurrentMeegleDeviceCodePolling(challenge, currentJob)) return@launch
                    if (isMeegleDeviceCodeExpired()) {
                        markMeegleDeviceCodeExpired(challenge, authorizationAccepted)
                        return@launch
                    }
                    try {
                        if (authorizationAccepted) {
                            val status = runInterruptible(ioDispatcher) { meegleCliService.status() }
                            if (status.authenticated && isCurrentMeegleDeviceCodePolling(challenge, currentJob)) {
                                invalidateMeegleStatusRefresh()
                                clearMeegleDeviceCodeLogin(cancelPolling = false)
                                meegleCli = MeegleCliState.Ready(status)
                                loadMeegleProjects(force = true)
                                showStatus("Meegle 登录成功")
                                return@launch
                            }
                            updateMeegleDeviceCodeLogin(challenge) {
                                it.copy(error = "已收到授权结果，正在确认登录状态…")
                            }
                            pollDelayMillis = challenge.pollingIntervalSeconds * 1_000L
                        } else {
                            when (runInterruptible(ioDispatcher) { meegleCliService.completeDeviceCodeLogin(challenge) }) {
                                MeegleDeviceCodeLoginResult.AUTHORIZED -> {
                                    authorizationAccepted = true
                                    pollDelayMillis = 0L
                                }

                                MeegleDeviceCodeLoginResult.PENDING -> {
                                    if (!meegleDeviceCodeBrowserOpenFailed) {
                                        updateMeegleDeviceCodeLogin(challenge) { it.copy(error = null) }
                                    }
                                    pollDelayMillis = challenge.pollingIntervalSeconds * 1_000L
                                }

                                MeegleDeviceCodeLoginResult.SLOW_DOWN -> {
                                    pollDelayMillis = (pollDelayMillis + 5_000L)
                                        .coerceAtLeast(challenge.pollingIntervalSeconds * 1_000L)
                                        .coerceAtMost(MAX_MEEGLE_DEVICE_CODE_POLL_DELAY_MILLIS)
                                }

                                MeegleDeviceCodeLoginResult.EXPIRED -> {
                                    markMeegleDeviceCodeExpired(challenge, authorizationAccepted = false)
                                    return@launch
                                }
                            }
                        }
                    } catch (cancelled: CancellationException) {
                        throw cancelled
                    } catch (error: Throwable) {
                        if (isCurrentMeegleDeviceCodePolling(challenge, currentJob)) {
                            updateMeegleDeviceCodeLogin(challenge) {
                                it.copy(polling = true, error = pollingErrorMessage(challenge, error))
                            }
                            pollDelayMillis = challenge.pollingIntervalSeconds * 1_000L
                        }
                    }
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } finally {
                if (meegleDeviceCodePollJob === currentJob) meegleDeviceCodePollJob = null
            }
        }
        meegleDeviceCodePollJob = job
        job.start()
    }

    private fun isCurrentMeegleDeviceCodePolling(challenge: MeegleDeviceCodeChallenge, job: Job?): Boolean =
        meegleDeviceCodeChallenge === challenge && meegleDeviceCodePollJob === job

    private fun isCurrentMeegleStatusRefresh(generation: Long, job: Job?): Boolean =
        meegleStatusRefreshGeneration == generation && meegleStatusRefreshJob === job

    /** 让正在后台运行的旧状态检查失效；它返回后也不能覆盖较新的登录结果。 */
    private fun invalidateMeegleStatusRefresh() {
        meegleStatusRefreshGeneration += 1
        meegleStatusRefreshJob?.cancel()
        meegleStatusRefreshJob = null
    }

    private fun updateMeegleDeviceCodeLogin(
        challenge: MeegleDeviceCodeChallenge,
        update: (MeegleDeviceCodeLoginUiState) -> MeegleDeviceCodeLoginUiState,
    ) {
        if (meegleDeviceCodeChallenge === challenge) {
            meegleDeviceCodeLogin = meegleDeviceCodeLogin?.let(update)
        }
    }

    private fun isMeegleDeviceCodeExpired(): Boolean =
        meegleDeviceCodeExpiresAtMillis?.let { System.currentTimeMillis() >= it } ?: true

    private fun markMeegleDeviceCodeExpired(challenge: MeegleDeviceCodeChallenge, authorizationAccepted: Boolean) {
        updateMeegleDeviceCodeLogin(challenge) {
            it.copy(
                polling = false,
                error = if (authorizationAccepted) {
                    "已收到授权结果，但未能确认登录状态。请点击“刷新”确认。"
                } else {
                    "授权码已过期，请重新生成验证码。"
                },
            )
        }
    }

    private fun deviceCodeExpiresAtMillis(challenge: MeegleDeviceCodeChallenge): Long =
        System.currentTimeMillis() + challenge.expiresInSeconds.coerceAtMost(MAX_MEEGLE_DEVICE_CODE_LIFETIME_SECONDS) * 1_000L

    private fun pollingErrorMessage(challenge: MeegleDeviceCodeChallenge, error: Throwable): String {
        val detail = challenge.redactSecrets(error.message.orEmpty())
            .lineSequence()
            .firstOrNull()
            ?.trim()
            ?.take(MAX_MEEGLE_DEVICE_CODE_ERROR_LENGTH)
            .orEmpty()
        return if (detail.isBlank()) {
            "自动检测授权状态失败，将自动重试。"
        } else {
            "自动检测授权状态失败：$detail。将自动重试。"
        }
    }

    private fun openMeegleAuthorizationUrl(challenge: MeegleDeviceCodeChallenge): String? =
        openMeegleAuthorizationPage(challenge.authorizationUrl).exceptionOrNull()
            ?.let { "未能自动打开授权页，请点击“打开授权页”重试。" }

    private fun clearMeegleDeviceCodeLogin(cancelPolling: Boolean = true) {
        if (cancelPolling) meegleDeviceCodePollJob?.cancel()
        meegleDeviceCodePollJob = null
        meegleDeviceCodeChallenge = null
        meegleDeviceCodeExpiresAtMillis = null
        meegleDeviceCodeBrowserOpenFailed = false
        meegleDeviceCodeLogin = null
    }

    fun chooseDirectory(initial: String? = null, selected: (String) -> Unit) = choose({ pathPicker.pickDirectory(initial) }) { it?.let(selected) }
    fun chooseFile(initial: String? = null, selected: (String) -> Unit) = choose({ pathPicker.pickFile(initial) }) { it?.let(selected) }
    fun chooseApplication(initial: String? = null, selected: (String) -> Unit) =
        choose({ pathPicker.pickApplication(initial) }) { it?.let(selected) }
    fun chooseDirectories(initial: String? = null, selected: (List<String>) -> Unit) = choose({ pathPicker.pickDirectories(initial) }) {
        it?.takeIf(List<String>::isNotEmpty)?.let(selected)
    }

    fun loadRemoteBranches(repositoryId: String, remote: String = "origin", force: Boolean = false) {
        val key = "$repositoryId|$remote"
        val repository = session.config.repositories.firstOrNull { it.id == repositoryId }
            ?: return showError(IllegalArgumentException("找不到仓库：$repositoryId"))
        prepareRemoteRepository(repositoryId, repository.rootPath)
        val current = remoteBranches[key]
        if (!force && current is RemoteBranchesState.Loading) return
        if (!force && current is RemoteBranchesState.Loaded && !RemoteBranchCachePolicy.isExpired(current.loadedAtNanos)) return
        val staleBranches = when (current) {
            is RemoteBranchesState.Loaded -> current.branches
            is RemoteBranchesState.Failed -> current.staleBranches
            is RemoteBranchesState.Loading -> current.staleBranches
            RemoteBranchesState.Idle, null -> emptyList()
        }
        remoteBranches = remoteBranches + (key to RemoteBranchesState.Loading(staleBranches))
        if (force) remoteBranchJobs.remove(key)?.cancel()
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            try {
                val branches = runInterruptible(ioDispatcher) { branchCatalog.list(Path.of(repository.rootPath), remote) }
                if (remoteBranchJobs[key] === currentJob && isCurrentRemoteRepository(repositoryId, repository.rootPath)) {
                    remoteBranches = remoteBranches + (key to RemoteBranchesState.Loaded(branches))
                }
            } catch (cancelled: CancellationException) {
                if (remoteBranchJobs[key] === currentJob && isCurrentRemoteRepository(repositoryId, repository.rootPath)) {
                    remoteBranches = remoteBranches + (key to (
                        staleBranches.takeIf(List<String>::isNotEmpty)?.let(RemoteBranchesState::Loaded)
                            ?: RemoteBranchesState.Idle
                        ))
                }
                throw cancelled
            } catch (error: Throwable) {
                if (remoteBranchJobs[key] === currentJob && isCurrentRemoteRepository(repositoryId, repository.rootPath)) {
                    remoteBranches = remoteBranches + (key to RemoteBranchesState.Failed(error.message ?: "远程分支加载失败", staleBranches))
                }
            } finally {
                if (remoteBranchJobs[key] === currentJob) remoteBranchJobs.remove(key)
            }
        }
        remoteBranchJobs[key] = job
        job.start()
    }

    fun loadRepositoryRemotes(repositoryId: String, force: Boolean = false) {
        val repository = session.config.repositories.firstOrNull { it.id == repositoryId }
            ?: return showError(IllegalArgumentException("找不到仓库：$repositoryId"))
        prepareRemoteRepository(repositoryId, repository.rootPath)
        if (!force && repositoryRemotes[repositoryId] is RepositoryRemotesState.Loading) return
        if (!force && repositoryRemotes[repositoryId] is RepositoryRemotesState.Loaded) return
        repositoryRemoteJobs.remove(repositoryId)?.cancel()
        repositoryRemotes = repositoryRemotes + (repositoryId to RepositoryRemotesState.Loading)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            fun current() = repositoryRemoteJobs[repositoryId] === currentJob &&
                isCurrentRemoteRepository(repositoryId, repository.rootPath)
            try {
                val remotes = runInterruptible(ioDispatcher) { remoteCatalog.list(Path.of(repository.rootPath)) }
                if (current()) repositoryRemotes = repositoryRemotes + (repositoryId to RepositoryRemotesState.Loaded(remotes))
            } catch (cancelled: CancellationException) {
                if (current()) repositoryRemotes = repositoryRemotes + (repositoryId to RepositoryRemotesState.Idle)
                throw cancelled
            } catch (error: Throwable) {
                if (current()) repositoryRemotes = repositoryRemotes +
                    (repositoryId to RepositoryRemotesState.Failed(error.message ?: "Git 远程加载失败"))
            } finally {
                if (repositoryRemoteJobs[repositoryId] === currentJob) repositoryRemoteJobs.remove(repositoryId)
            }
        }
        repositoryRemoteJobs[repositoryId] = job
        job.start()
    }

    fun repositoryRemotesState(repositoryId: String): RepositoryRemotesState =
        if (hasCurrentRemoteRepository(repositoryId)) repositoryRemotes[repositoryId] ?: RepositoryRemotesState.Idle
        else RepositoryRemotesState.Idle

    /** 仓库 ID 不变、路径改变时，名称与分支必须一起失效；旧请求只能清理自身。 */
    private fun prepareRemoteRepository(repositoryId: String, rootPath: String) {
        if (remoteRepositoryRoots[repositoryId] == rootPath) return
        remoteRepositoryRoots = remoteRepositoryRoots + (repositoryId to rootPath)
        repositoryRemoteJobs.remove(repositoryId)?.cancel()
        repositoryRemotes = repositoryRemotes - repositoryId
        val prefix = "$repositoryId|"
        remoteBranchJobs.keys.filter { it.startsWith(prefix) }.forEach { remoteBranchJobs.remove(it)?.cancel() }
        remoteBranches = remoteBranches.filterKeys { !it.startsWith(prefix) }
    }

    private fun isCurrentRemoteRepository(repositoryId: String, rootPath: String): Boolean =
        remoteRepositoryRoots[repositoryId] == rootPath &&
            session.config.repositories.firstOrNull { it.id == repositoryId }?.rootPath == rootPath

    private fun hasCurrentRemoteRepository(repositoryId: String): Boolean =
        remoteRepositoryRoots[repositoryId]?.let { isCurrentRemoteRepository(repositoryId, it) } == true

    fun cancelRemoteBranchLoads() {
        val jobs = remoteBranchJobs.values.toList()
        remoteBranchJobs.clear()
        jobs.forEach(Job::cancel)
        remoteBranches = remoteBranches.mapValues { (_, state) ->
            when (state) {
                is RemoteBranchesState.Loading -> state.staleBranches.takeIf(List<String>::isNotEmpty)
                    ?.let(RemoteBranchesState::Loaded) ?: RemoteBranchesState.Idle
                else -> state
            }
        }
    }

    fun remoteBranchState(repositoryId: String, remote: String): RemoteBranchesState =
        if (hasCurrentRemoteRepository(repositoryId)) remoteBranches["$repositoryId|$remote"] ?: RemoteBranchesState.Idle
        else RemoteBranchesState.Idle

    fun loadMeegleProjects(force: Boolean = false) {
        if (!force && (meegleProjects is MeegleProjectCatalogState.Loading || meegleProjects is MeegleProjectCatalogState.Loaded)) return
        if (force) meegleProjectJob?.cancel()
        meegleProjects = MeegleProjectCatalogState.Loading
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val currentJob = currentCoroutineContext()[Job]
            try {
                val projects = runInterruptible(ioDispatcher) { meegleProjectCatalog.list() }
                if (meegleProjectJob === currentJob) meegleProjects = MeegleProjectCatalogState.Loaded(projects)
            } catch (cancelled: CancellationException) {
                if (meegleProjectJob === currentJob) meegleProjects = MeegleProjectCatalogState.Idle
                throw cancelled
            } catch (failure: Exception) {
                if (meegleProjectJob === currentJob) meegleProjects = MeegleProjectCatalogState.Failed(failure.message ?: "读取 Meegle 项目失败")
            } finally {
                if (meegleProjectJob === currentJob) meegleProjectJob = null
            }
        }
        meegleProjectJob = job
        job.invokeOnCompletion {
            if (meegleProjectJob === job) {
                meegleProjectJob = null
                if (meegleProjects is MeegleProjectCatalogState.Loading) meegleProjects = MeegleProjectCatalogState.Idle
            }
        }
        job.start()
    }

    fun cancelMeegleProjectLoad() {
        meegleProjectJob?.cancel()
        meegleProjectJob = null
        if (meegleProjects is MeegleProjectCatalogState.Loading) meegleProjects = MeegleProjectCatalogState.Idle
    }

    fun addRepository(groupId: String, directory: String, strategy: WorkspaceStrategy): Boolean = operations.run(
        "正在添加服务…",
        "服务已添加",
        cancellable = true,
        block = { groups.addRepository(groupId, Path.of(directory), strategy) },
        onSuccess = applyConfig,
    )

    fun addRepositories(groupId: String, paths: List<String>, onCompleted: () -> Unit = {}): Boolean = operations.run(
        "正在批量添加仓库…",
        "仓库批量添加完成",
        cancellable = true,
        block = { groups.addRepositories(groupId, paths.map(Path::of)) },
        onSuccess = { result ->
            applyConfig(result.config)
            repositoryAddResult = result
            val suffix = if (result.skipped.isNotEmpty()) "，跳过 ${result.skipped.size} 个目录" else ""
            showStatus("已添加 ${result.added.size} 个服务$suffix")
            onCompleted()
        },
    )

    fun clearRepositoryAddResult() { repositoryAddResult = null }
    fun updateService(groupId: String, service: GroupServiceConfig, onFailure: (Throwable) -> Unit = {}, onCompleted: () -> Unit = {}) =
        mutateWithService("正在保存服务配置…", "服务配置已保存", onCompleted, onFailure) { groups.updateService(groupId, service) }
    fun moveService(groupId: String, serviceId: String, offset: Int) = mutateWithService("正在更新服务顺序…", "服务顺序已更新") { groups.moveService(groupId, serviceId, offset) }
    fun removeService(groupId: String, serviceId: String, onCompleted: () -> Unit = {}) = mutateWithService("正在移除服务…", "服务已移除", onCompleted) {
        require(session.tasks.none { task -> task.groupId == groupId && task.services.any { it.groupServiceId == serviceId } }) {
            "该服务仍被研发任务引用，不能从组内移除"
        }
        groups.removeService(groupId, serviceId)
    }

    private fun mutate(
        active: String,
        success: String,
        onFailure: (Throwable) -> Unit = {},
        saveKey: String? = null,
        runner: OperationRunner = operations,
        onCompleted: () -> Unit = {},
        transform: (AppConfig) -> AppConfig,
    ): Boolean = runner.run(
        active,
        success,
        block = { configStore.update(transform) },
        onSuccess = { applyConfig(it); saveKey?.let { key -> setSaveState(key, SettingsSaveState.SAVED) }; onCompleted() },
        onFailure = { error -> saveKey?.let { key -> setSaveState(key, SettingsSaveState.FAILED) }; onFailure(error) },
    ).also { started -> saveKey?.let { setSaveState(it, if (started) SettingsSaveState.SAVING else SettingsSaveState.FAILED) } }

    private fun mutateWithService(
        active: String,
        success: String,
        onCompleted: () -> Unit = {},
        onFailure: (Throwable) -> Unit = {},
        saveKey: String? = null,
        runner: OperationRunner = operations,
        block: () -> AppConfig,
    ): Boolean = runner.run(
        active,
        success,
        block = block,
        onSuccess = {
            applyConfig(it)
            saveKey?.let { key -> setSaveState(key, SettingsSaveState.SAVED) }
            onCompleted()
        },
        onFailure = { error -> saveKey?.let { key -> setSaveState(key, SettingsSaveState.FAILED) }; onFailure(error) },
    ).also { started -> saveKey?.let { setSaveState(it, if (started) SettingsSaveState.SAVING else SettingsSaveState.FAILED) } }

    private fun setSaveState(key: String, state: SettingsSaveState) { saveStates = saveStates + (key to state) }

    private fun <T> choose(pick: suspend () -> T?, complete: (T?) -> Unit) {
        if (pathPickerBusy) return
        val job = scope.launch(start = CoroutineStart.LAZY) {
            try {
                val selected = pick()
                currentCoroutineContext().ensureActive()
                complete(selected)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Exception) {
                showError(failure)
            } finally {
                if (pathPickerJob === currentCoroutineContext()[Job]) pathPickerBusy = false
            }
        }
        pathPickerJob = job
        pathPickerBusy = true
        job.invokeOnCompletion {
            if (pathPickerJob === job) {
                pathPickerJob = null
                pathPickerBusy = false
            }
        }
        job.start()
    }
}

internal object RemoteBranchCachePolicy {
    private const val CACHE_TTL_NANOS = 30_000_000_000L

    fun isExpired(loadedAtNanos: Long, nowNanos: Long = System.nanoTime()): Boolean =
        nowNanos - loadedAtNanos >= CACHE_TTL_NANOS
}
