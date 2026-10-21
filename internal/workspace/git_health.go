package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type PorcelainV2Status struct {
	Branch, Upstream string
	Ahead, Behind    *int
	ChangedFiles     []domain.WorkspaceGitFileChange
}

func ParsePorcelainV2(output string) PorcelainV2Status {
	fields := strings.Split(output, "\x00")
	fileIndexes := make(map[string]int)
	result := PorcelainV2Status{}
	addFile := func(change domain.WorkspaceGitFileChange) {
		if index, exists := fileIndexes[change.Path]; exists {
			result.ChangedFiles[index] = change
			return
		}
		fileIndexes[change.Path] = len(result.ChangedFiles)
		result.ChangedFiles = append(result.ChangedFiles, change)
	}
	for index := 0; index < len(fields); index++ {
		record := fields[index]
		switch {
		case strings.HasPrefix(record, "# branch.head "):
			result.Branch = strings.TrimPrefix(record, "# branch.head ")
		case strings.HasPrefix(record, "# branch.upstream "):
			result.Upstream = strings.TrimPrefix(record, "# branch.upstream ")
		case strings.HasPrefix(record, "# branch.ab "):
			match := regexp.MustCompile(`\+(\d+) -(\d+)`).FindStringSubmatch(strings.TrimPrefix(record, "# branch.ab "))
			if len(match) == 3 {
				a, _ := strconv.Atoi(match[1])
				b, _ := strconv.Atoi(match[2])
				result.Ahead = &a
				result.Behind = &b
			}
		case strings.HasPrefix(record, "1 "):
			if change, ok := parsePorcelainFile(record, '1', 8); ok {
				addFile(change)
			}
		case strings.HasPrefix(record, "2 "):
			if change, ok := parsePorcelainFile(record, '2', 9); ok {
				addFile(change)
			}
			index++
		case strings.HasPrefix(record, "u "):
			if change, ok := parsePorcelainFile(record, 'u', 10); ok {
				addFile(change)
			}
		case strings.HasPrefix(record, "? "):
			path := strings.TrimPrefix(record, "? ")
			if path != "" {
				addFile(domain.WorkspaceGitFileChange{Path: path, Kind: domain.WorkspaceGitUntracked})
			}
		}
	}
	return result
}

func parsePorcelainFile(record string, recordType byte, fieldCount int) (domain.WorkspaceGitFileChange, bool) {
	parts := strings.SplitN(record, " ", fieldCount+1)
	if len(parts) <= fieldCount || parts[fieldCount] == "" {
		return domain.WorkspaceGitFileChange{}, false
	}
	xy := ""
	if len(parts) > 1 {
		xy = parts[1]
	}
	kind := domain.WorkspaceGitModified
	switch {
	case recordType == 'u' || strings.ContainsRune(xy, 'U'):
		kind = domain.WorkspaceGitConflicted
	case recordType == '2' && strings.ContainsRune(xy, 'C'):
		kind = domain.WorkspaceGitCopied
	case recordType == '2' && strings.ContainsRune(xy, 'R'):
		kind = domain.WorkspaceGitRenamed
	case strings.ContainsRune(xy, 'D'):
		kind = domain.WorkspaceGitDeleted
	case strings.ContainsRune(xy, 'A'):
		kind = domain.WorkspaceGitAdded
	case strings.ContainsRune(xy, 'T'):
		kind = domain.WorkspaceGitTypeChanged
	case strings.ContainsRune(xy, 'R'):
		kind = domain.WorkspaceGitRenamed
	case strings.ContainsRune(xy, 'C'):
		kind = domain.WorkspaceGitCopied
	}
	return domain.WorkspaceGitFileChange{Path: parts[fieldCount], Kind: kind}, true
}

type WorkspaceGitStatusReader interface {
	Read(workspace domain.ServiceWorkspace) domain.WorkspaceGitHealth
}
type GitWorkspaceGitStatusReader struct{ Git *GitClient }

func NewWorkspaceGitStatusReader(git *GitClient) *GitWorkspaceGitStatusReader {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &GitWorkspaceGitStatusReader{Git: git}
}

func (r *GitWorkspaceGitStatusReader) Read(ws domain.ServiceWorkspace) domain.WorkspaceGitHealth {
	return r.ReadContext(context.Background(), ws)
}

func (r *GitWorkspaceGitStatusReader) ReadContext(ctx context.Context, ws domain.ServiceWorkspace) domain.WorkspaceGitHealth {
	if err := ctx.Err(); err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueInspectionFailed, err.Error())
	}
	target := mustAbs(ws.WorktreePath)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return domain.WorkspaceGitHealth{State: domain.WorkspaceGitMissing, Issue: domain.WorkspaceGitIssueMissing, ExpectedBranch: ptr(ws.Branch), Message: ptr("工作区不存在：" + target)}
	}
	health := domain.WorkspaceGitHealth{ExpectedBranch: ptr(ws.Branch), DirtyFiles: []domain.WorkspaceGitFileChange{}}
	commonResult, err := r.readOnlyGit(ctx, target, 10*time.Minute, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueNotGit, "不是有效的 Git 工作区："+err.Error())
	}
	common, err := filepath.Abs(strings.TrimSpace(commonResult.Stdout))
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueNotGit, "不是有效的 Git 工作区："+err.Error())
	}
	topResult, err := r.readOnlyGit(ctx, target, 10*time.Minute, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueNotGit, "不是有效的 Git 工作区："+err.Error())
	}
	top, err := filepath.Abs(strings.TrimSpace(topResult.Stdout))
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueNotGit, "不是有效的 Git 工作区："+err.Error())
	}
	if !samePath(top, target) {
		return statusFailure(ws, domain.WorkspaceGitIssueNotGit, "目标目录不是 Git 顶层目录："+target)
	}
	switch ws.Strategy {
	case domain.WorkspaceStandardWorktree:
		expectedResult, err := r.readOnlyGit(ctx, ws.RepositoryPath, 10*time.Minute, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "主仓库 Git 身份已失效："+err.Error())
		}
		expected, err := filepath.Abs(strings.TrimSpace(expectedResult.Stdout))
		if err != nil {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "主仓库 Git 身份已失效："+err.Error())
		}
		if !samePath(common, expected) {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "Git 仓库身份与任务记录不匹配："+target)
		}
	case domain.WorkspaceIndependentClone:
		remote := ws.PushRemote
		if strings.TrimSpace(remote) == "" || ws.OriginURL == nil {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "Git 仓库身份与任务记录不匹配："+target)
		}
		originResult, err := r.readOnlyGit(ctx, target, 2*time.Minute, false, "config", "--get", "remote."+remote+".url")
		if err != nil {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "Git 仓库身份与任务记录不匹配："+target)
		}
		origin := ""
		if originResult.ExitCode == 0 {
			origin = strings.TrimSpace(originResult.Stdout)
		}
		if originResult.ExitCode != 0 || strings.TrimSpace(origin) != strings.TrimSpace(*ws.OriginURL) {
			return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "Git 仓库身份与任务记录不匹配："+target)
		}
	default:
		return statusFailure(ws, domain.WorkspaceGitIssueIdentityMismatch, "工作区策略不受支持")
	}
	operation, err := r.readOnlyGit(ctx, target, time.Minute, false, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueInspectionFailed, err.Error())
	}
	gitDir := strings.TrimSpace(operation.Stdout)
	for _, entry := range []struct{ name, kind string }{{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"}} {
		if _, err := os.Stat(filepath.Join(gitDir, entry.name)); err == nil {
			health.State = domain.WorkspaceGitFailed
			health.Issue = domain.WorkspaceGitIssueOperationInProgress
			health.Message = ptr("存在进行中的 Git 操作：" + entry.kind)
			health.PushState = domain.LocalPushFailed
			return health
		}
	}
	status, err := r.readOnlyGit(ctx, target, 2*time.Minute, true, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return statusFailure(ws, domain.WorkspaceGitIssueInspectionFailed, err.Error())
	}
	parsed := ParsePorcelainV2(status.Stdout)
	if parsed.Branch == "" || parsed.Branch == "(detached)" {
		health.State = domain.WorkspaceGitFailed
		health.Issue = domain.WorkspaceGitIssueDetachedHead
		health.Message = ptr("工作区处于 Detached HEAD，期望分支 " + ws.Branch)
		health.PushState = domain.LocalPushFailed
		return health
	}
	if parsed.Branch != ws.Branch {
		health.State = domain.WorkspaceGitFailed
		health.Issue = domain.WorkspaceGitIssueBranchMismatch
		health.ActualBranch = ptr(parsed.Branch)
		health.Message = ptr(fmt.Sprintf("分支不一致：当前 %s，期望 %s", parsed.Branch, ws.Branch))
		health.PushState = domain.LocalPushFailed
		return health
	}
	health.State = domain.WorkspaceGitReady
	health.Issue = domain.WorkspaceGitIssueNone
	health.ExpectedBranch = ptr(ws.Branch)
	health.ActualBranch = ptr(parsed.Branch)
	health.DirtyFiles = parsed.ChangedFiles
	health.DirtyFileCount = len(parsed.ChangedFiles)
	switch {
	case parsed.Upstream == "":
		health.PushState = domain.LocalPushNoUpstream
	case parsed.Ahead == nil:
		health.PushState = domain.LocalPushRemoteBranchMissing
	case *parsed.Ahead == 0:
		health.PushState = domain.LocalPushPushed
	default:
		health.PushState = domain.LocalPushAhead
		health.UnpushedCommitCount = *parsed.Ahead
	}
	return health
}

func (r *GitWorkspaceGitStatusReader) readOnlyGit(ctx context.Context, repository string, timeout time.Duration, check bool, arguments ...string) (process.Result, error) {
	return r.Git.RunContext(ctx, repository, timeout, check, true, arguments...)
}

func statusFailure(ws domain.ServiceWorkspace, issue domain.WorkspaceGitIssue, message string) domain.WorkspaceGitHealth {
	return domain.WorkspaceGitHealth{State: domain.WorkspaceGitFailed, Issue: issue, PushState: domain.LocalPushFailed, Message: ptr(message), ExpectedBranch: ptr(ws.Branch), DirtyFiles: []domain.WorkspaceGitFileChange{}}
}
