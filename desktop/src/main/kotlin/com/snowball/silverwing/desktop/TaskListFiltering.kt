package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.TaskManifest

internal fun filterTasks(
    tasks: List<TaskManifest>,
    query: String,
    requirementTitle: (TaskManifest) -> String?,
): List<TaskManifest> = tasks.filter { task ->
    val normalizedQuery = query.trim()
    val searchable = buildList {
        add(task.folderName)
        add(task.featureBranch)
        add(task.requirementLink)
        requirementTitle(task)?.let(::add)
        task.services.forEach { workspace ->
            add(workspace.serviceName)
            add(workspace.moduleName)
        }
    }
    normalizedQuery.isBlank() || searchable.any { it.contains(normalizedQuery, ignoreCase = true) }
}
