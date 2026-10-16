package domain

import "time"

type WorkspaceGitHealthState string

const (
	WorkspaceGitChecking WorkspaceGitHealthState = "CHECKING"
	WorkspaceGitReady    WorkspaceGitHealthState = "READY"
	WorkspaceGitFailed   WorkspaceGitHealthState = "FAILED"
	WorkspaceGitMissing  WorkspaceGitHealthState = "MISSING"
)

type LocalPushState string

const (
	LocalPushPushed              LocalPushState = "PUSHED"
	LocalPushAhead               LocalPushState = "AHEAD"
	LocalPushRemoteBranchMissing LocalPushState = "REMOTE_BRANCH_MISSING"
	LocalPushNoUpstream          LocalPushState = "NO_UPSTREAM"
	LocalPushFailed              LocalPushState = "FAILED"
)

type WorkspaceGitFileChangeKind string

const (
	WorkspaceGitModified    WorkspaceGitFileChangeKind = "MODIFIED"
	WorkspaceGitAdded       WorkspaceGitFileChangeKind = "ADDED"
	WorkspaceGitDeleted     WorkspaceGitFileChangeKind = "DELETED"
	WorkspaceGitRenamed     WorkspaceGitFileChangeKind = "RENAMED"
	WorkspaceGitCopied      WorkspaceGitFileChangeKind = "COPIED"
	WorkspaceGitTypeChanged WorkspaceGitFileChangeKind = "TYPE_CHANGED"
	WorkspaceGitConflicted  WorkspaceGitFileChangeKind = "CONFLICTED"
	WorkspaceGitUntracked   WorkspaceGitFileChangeKind = "UNTRACKED"
)

type WorkspaceGitFileChange struct {
	Path string
	Kind WorkspaceGitFileChangeKind
}

type WorkspaceGitCommit struct {
	ShortHash      string
	ParentHashes   []string `json:"parentHashes"`
	CommittedAt    time.Time
	Message        string
	AuthorName     string
	FullHash       string
	CommitterName  string
	CommitterEmail string
	AuthorEmail    string
}

type WorkspaceGitIssue string

const (
	WorkspaceGitIssueNone                WorkspaceGitIssue = "NONE"
	WorkspaceGitIssueMissing             WorkspaceGitIssue = "MISSING"
	WorkspaceGitIssueNotGit              WorkspaceGitIssue = "NOT_GIT"
	WorkspaceGitIssueIdentityMismatch    WorkspaceGitIssue = "IDENTITY_MISMATCH"
	WorkspaceGitIssueBranchMismatch      WorkspaceGitIssue = "BRANCH_MISMATCH"
	WorkspaceGitIssueDetachedHead        WorkspaceGitIssue = "DETACHED_HEAD"
	WorkspaceGitIssueOperationInProgress WorkspaceGitIssue = "OPERATION_IN_PROGRESS"
	WorkspaceGitIssueInspectionFailed    WorkspaceGitIssue = "INSPECTION_FAILED"
)

type WorkspaceGitHealth struct {
	State               WorkspaceGitHealthState
	DirtyFileCount      int
	PushState           LocalPushState
	UnpushedCommitCount int
	Message             *string
	Issue               WorkspaceGitIssue
	ActualBranch        *string
	ExpectedBranch      *string
	DirtyFiles          []WorkspaceGitFileChange
}
