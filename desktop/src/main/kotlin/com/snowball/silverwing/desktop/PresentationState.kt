package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.TaskManifest
import com.snowball.silverwing.core.TaskLifecycleStatus

/** Owns navigation and selection independently from feature controllers. */
class AppSessionStore(
    initialConfig: AppConfig,
    initialTasks: List<TaskManifest>,
) {
    var config by mutableStateOf(initialConfig)
        internal set
    var tasks by mutableStateOf(initialTasks)
        internal set
    private var navigationState by mutableStateOf(NavigationItem.TASKS)
    var navigation: NavigationItem
        get() = navigationState
        set(value) {
            navigationState = value
            reconcileSelection()
        }
    var selectedTask by mutableStateOf(initialTasks.firstOrNull { it.lifecycleStatus == TaskLifecycleStatus.ACTIVE })
        internal set

    fun replaceTasks(newTasks: List<TaskManifest>, preferredFolder: String? = selectedTask?.folderName) {
        tasks = newTasks
        selectedTask = preferredFolder?.let { folder -> newTasks.firstOrNull { it.folderName == folder } }
        reconcileSelection()
    }

    private fun reconcileSelection() {
        val archived = when (navigationState) {
            NavigationItem.TASKS -> false
            NavigationItem.ARCHIVED -> true
            else -> return
        }
        val current = selectedTask
        if (current == null || (current.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) != archived) {
            selectedTask = tasks.firstOrNull {
                (it.lifecycleStatus == TaskLifecycleStatus.ARCHIVED) == archived
            }
        }
    }
}

/** Centralizes operation progress and one-shot user feedback. */
class OperationCoordinator(
    initialError: String? = null,
    private val onError: (Throwable) -> Unit = {},
) {
    var busy by mutableStateOf(false)
        internal set
    var activeMessage by mutableStateOf<String?>(null)
        internal set
    var cancellable by mutableStateOf(false)
        internal set
    var cancelling by mutableStateOf(false)
        internal set
    var statusMessage by mutableStateOf<String?>(null)
        internal set
    var errorMessage by mutableStateOf(initialError)
        internal set

    fun begin(message: String, canCancel: Boolean): Boolean {
        if (busy) return false
        busy = true
        activeMessage = message
        cancellable = canCancel
        cancelling = false
        return true
    }

    fun succeed(message: String) {
        busy = false
        activeMessage = null
        cancellable = false
        cancelling = false
        statusMessage = message
        errorMessage = null
    }

    fun fail(error: Throwable, showFeedback: Boolean = true) {
        busy = false
        activeMessage = null
        cancellable = false
        cancelling = false
        errorMessage = if (showFeedback) OperationFailureDetails.format(error) else null
        onError(error)
    }

    fun dismiss() {
        statusMessage = null
        errorMessage = null
    }

    fun markCancelling() {
        if (!busy || !cancellable) return
        cancelling = true
        cancellable = false
        activeMessage = "正在取消并等待当前步骤安全停止…"
    }

    fun cancelled() {
        busy = false
        activeMessage = null
        cancellable = false
        cancelling = false
        statusMessage = "操作已取消"
        errorMessage = null
    }
}

internal object OperationFailureDetails {
    fun format(error: Throwable): String = buildString {
        append(error.message ?: error::class.simpleName ?: "操作失败")
        var cause = error.cause
        while (cause != null) {
            append("\n\n原因：")
            append(cause.message ?: cause::class.simpleName ?: "未知错误")
            cause = cause.cause
        }
        error.suppressed.forEach { suppressed ->
            append("\n\n附加失败：")
            append(suppressed.message ?: suppressed::class.simpleName ?: "未知错误")
        }
    }
}
