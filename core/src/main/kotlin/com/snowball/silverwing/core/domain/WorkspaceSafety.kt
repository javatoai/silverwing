package com.snowball.silverwing.core

import java.nio.file.Path

/** A destructive-operation guard derived from the live Git state of one workspace. */
data class DeleteRisk(
    val serviceName: String,
    val staged: Boolean,
    val unstaged: Boolean,
    val untracked: Boolean,
    val operationInProgress: String?,
    val unpushedCommits: Int = 0,
    val statusCheckError: String? = null,
)

internal fun Path.canonicalOrNormalized(): Path =
    runCatching { toRealPath() }.getOrElse { toAbsolutePath().normalize() }
