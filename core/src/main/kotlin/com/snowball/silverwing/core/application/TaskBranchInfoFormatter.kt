package com.snowball.silverwing.core

object TaskBranchInfoFormatter {
    /**
     * Formats only the service names, one per line.
     *
     * The optional requirement link uses the same heading and spacing as the
     * complete branch-info format.
     */
    fun formatServices(task: TaskManifest, includeRequirementLink: Boolean = false): String = buildString {
        appendRequirementLink(task, includeRequirementLink)
        task.services.forEachIndexed { index, workspace ->
            if (index > 0) append('\n')
            append(workspace.serviceName)
        }
    }

    /** Formats each service together with its branch, one per line. */
    fun formatBranchInfo(task: TaskManifest, includeRequirementLink: Boolean = true): String = buildString {
        appendRequirementLink(task, includeRequirementLink)
        task.services.forEachIndexed { index, workspace ->
            if (index > 0) append('\n')
            append(workspace.serviceName)
            append('：')
            append(workspace.branch)
        }
    }

    /** Preserves the original complete branch-info output. */
    fun format(task: TaskManifest): String = formatBranchInfo(task, includeRequirementLink = true)

    private fun StringBuilder.appendRequirementLink(task: TaskManifest, includeRequirementLink: Boolean) {
        if (includeRequirementLink && task.requirementLink.isNotBlank()) {
            append("需求链接：")
            append(task.requirementLink.trim())
            append("\n\n")
        }
    }
}
