package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.LocalSkillCatalog
import com.snowball.silverwing.core.LocalSkillCatalogApplicationService
import com.snowball.silverwing.core.LocalSkillCatalogItem
import com.snowball.silverwing.core.LocalSkillFileCatalog
import com.snowball.silverwing.core.LocalSkillFileEntry
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible

internal sealed interface LocalSkillCatalogLoadState {
    data object Idle : LocalSkillCatalogLoadState
    data object Loading : LocalSkillCatalogLoadState
    data class Loaded(val catalog: LocalSkillCatalog) : LocalSkillCatalogLoadState
    data class Failed(val message: String) : LocalSkillCatalogLoadState
}

internal sealed interface LocalSkillFilesState {
    data object Empty : LocalSkillFilesState
    data object Loading : LocalSkillFilesState
    data class Loaded(val catalog: LocalSkillFileCatalog) : LocalSkillFilesState
    data class Failed(val directoryName: String, val message: String) : LocalSkillFilesState
}

internal sealed interface LocalSkillFilePreviewState {
    data object Empty : LocalSkillFilePreviewState
    data object Loading : LocalSkillFilePreviewState
    data class Loaded(val directoryName: String, val relativePath: String, val content: String) : LocalSkillFilePreviewState
    data class Failed(val directoryName: String, val relativePath: String, val message: String) : LocalSkillFilePreviewState
}

internal sealed interface LocalSkillUninstallState {
    data object Idle : LocalSkillUninstallState
    data class Removing(val directoryName: String) : LocalSkillUninstallState
    data class Succeeded(val directoryName: String) : LocalSkillUninstallState
    data class Failed(val directoryName: String, val message: String) : LocalSkillUninstallState
}

/**
 * Presentation-only state for the Skills browser. The owning controller lives for one
 * desktop application run, so pane sizes survive navigation but are never persisted.
 */
internal data class LocalSkillsPaneWidthPreferences(
    val skillListWidthDp: Float? = null,
    val directoryWidthDp: Float? = null,
)

internal class LocalSkillsPaneWidthSession {
    var preferences by mutableStateOf(LocalSkillsPaneWidthPreferences())
}

/** Keeps local-Skill reads and explicit removal operations off the Compose thread. */
internal class LocalSkillsController(
    private val skills: LocalSkillCatalogApplicationService,
    private val uninstallLocalSkill: (String) -> Unit,
    private val scope: CoroutineScope,
    private val ioDispatcher: CoroutineDispatcher,
) {
    var catalogState by mutableStateOf<LocalSkillCatalogLoadState>(LocalSkillCatalogLoadState.Idle)
        private set
    var filesState by mutableStateOf<LocalSkillFilesState>(LocalSkillFilesState.Empty)
        private set
    var previewState by mutableStateOf<LocalSkillFilePreviewState>(LocalSkillFilePreviewState.Empty)
        private set
    var uninstallState by mutableStateOf<LocalSkillUninstallState>(LocalSkillUninstallState.Idle)
        private set

    /** Retained only while this [LocalSkillsController] remains alive for the current app run. */
    val paneWidthSession = LocalSkillsPaneWidthSession()

    private var catalogRequest = 0L
    private var filesRequest = 0L
    private var previewRequest = 0L
    private var uninstallRequest = 0L
    private var catalogJob: Job? = null
    private var filesJob: Job? = null
    private var previewJob: Job? = null

    fun refresh() {
        val request = ++catalogRequest
        filesRequest += 1
        previewRequest += 1
        catalogJob?.cancel()
        filesJob?.cancel()
        previewJob?.cancel()
        filesState = LocalSkillFilesState.Empty
        previewState = LocalSkillFilePreviewState.Empty
        catalogState = LocalSkillCatalogLoadState.Loading
        catalogJob = scope.launch {
            try {
                val catalog = runInterruptible(ioDispatcher) { skills.list() }
                if (request == catalogRequest) catalogState = LocalSkillCatalogLoadState.Loaded(catalog)
            } catch (cancelled: CancellationException) {
                if (request == catalogRequest) catalogState = LocalSkillCatalogLoadState.Idle
                throw cancelled
            } catch (error: Throwable) {
                if (request == catalogRequest) {
                    catalogState = LocalSkillCatalogLoadState.Failed(error.message ?: "无法读取本机 Skill 目录")
                }
            }
        }
    }

    fun loadFiles(skill: LocalSkillCatalogItem) {
        val request = ++filesRequest
        previewRequest += 1
        filesJob?.cancel()
        previewJob?.cancel()
        filesState = LocalSkillFilesState.Loading
        previewState = LocalSkillFilePreviewState.Empty
        filesJob = scope.launch {
            try {
                val catalog = runInterruptible(ioDispatcher) { skills.files(skill.directoryName) }
                if (request == filesRequest) filesState = LocalSkillFilesState.Loaded(catalog)
            } catch (cancelled: CancellationException) {
                if (request == filesRequest) filesState = LocalSkillFilesState.Empty
                throw cancelled
            } catch (error: Throwable) {
                if (request == filesRequest) {
                    filesState = LocalSkillFilesState.Failed(
                        skill.directoryName,
                        error.message ?: "无法读取 Skill 文件列表",
                    )
                }
            }
        }
    }

    fun preview(skill: LocalSkillCatalogItem, file: LocalSkillFileEntry) {
        val request = ++previewRequest
        previewJob?.cancel()
        previewState = LocalSkillFilePreviewState.Loading
        previewJob = scope.launch {
            try {
                val content = runInterruptible(ioDispatcher) { skills.preview(skill.directoryName, file.relativePath) }
                if (request == previewRequest) {
                    previewState = LocalSkillFilePreviewState.Loaded(skill.directoryName, file.relativePath, content)
                }
            } catch (cancelled: CancellationException) {
                if (request == previewRequest) previewState = LocalSkillFilePreviewState.Empty
                throw cancelled
            } catch (error: Throwable) {
                if (request == previewRequest) {
                    previewState = LocalSkillFilePreviewState.Failed(
                        skill.directoryName,
                        file.relativePath,
                        error.message ?: "无法读取 Skill 文档",
                    )
                }
            }
        }
    }

    fun uninstall(skill: LocalSkillCatalogItem) {
        if (uninstallState is LocalSkillUninstallState.Removing) return
        val request = ++uninstallRequest
        uninstallState = LocalSkillUninstallState.Removing(skill.directoryName)
        scope.launch {
            try {
                runInterruptible(ioDispatcher) { uninstallLocalSkill(skill.directoryName) }
                if (request == uninstallRequest) {
                    uninstallState = LocalSkillUninstallState.Succeeded(skill.directoryName)
                    refresh()
                }
            } catch (_: CancellationException) {
                // The application is closing; there is no user-visible failure to report.
            } catch (error: Throwable) {
                if (request == uninstallRequest) {
                    uninstallState = LocalSkillUninstallState.Failed(
                        skill.directoryName,
                        error.message ?: "卸载 Skill 失败",
                    )
                }
            }
        }
    }
}
