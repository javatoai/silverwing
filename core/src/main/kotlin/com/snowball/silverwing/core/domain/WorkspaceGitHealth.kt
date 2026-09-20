package com.snowball.silverwing.core

import java.time.Instant

enum class WorkspaceGitHealthState { CHECKING, READY, FAILED, MISSING }

enum class LocalPushState { PUSHED, AHEAD, REMOTE_BRANCH_MISSING, NO_UPSTREAM, FAILED }

enum class WorkspaceGitFileChangeKind {
    MODIFIED,
    ADDED,
    DELETED,
    RENAMED,
    COPIED,
    TYPE_CHANGED,
    CONFLICTED,
    UNTRACKED,
}

data class WorkspaceGitFileChange(
    val path: String,
    val kind: WorkspaceGitFileChangeKind,
)

data class WorkspaceGitCommit(
    val shortHash: String,
    val committedAt: Instant,
    val message: String,
    val authorName: String = "",
)

enum class WorkspaceGitIssue {
    NONE,
    MISSING,
    NOT_GIT,
    IDENTITY_MISMATCH,
    BRANCH_MISMATCH,
    DETACHED_HEAD,
    OPERATION_IN_PROGRESS,
    INSPECTION_FAILED,
}

data class WorkspaceGitHealth(
    val state: WorkspaceGitHealthState,
    val dirtyFileCount: Int = 0,
    val pushState: LocalPushState = LocalPushState.FAILED,
    val unpushedCommitCount: Int = 0,
    val message: String? = null,
    val issue: WorkspaceGitIssue = WorkspaceGitIssue.NONE,
    val actualBranch: String? = null,
    val expectedBranch: String? = null,
    val dirtyFiles: List<WorkspaceGitFileChange> = emptyList(),
)
