package workspace

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

const (
	WorkspaceGitHistoryPageSize = 100
	historyPageOutputBytes      = 512 * 1024
	historyMessageOutputBytes   = 256 * 1024
)

var ErrWorkspaceGitHistoryChanged = errors.New("提交历史的 HEAD 或主分支已变化，请刷新后重新选择")

// These transient read DTOs never enter task manifests. Subsequent pages and
// expanded messages carry both immutable object IDs from the first page.
type WorkspaceGitHistoryPageRequest struct {
	HeadSHA     string `json:"headSha"`
	BaselineSHA string `json:"baselineSha"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
}

type WorkspaceGitHistoryPage struct {
	Commits       []domain.WorkspaceGitCommit `json:"commits"`
	HeadSHA       string                      `json:"headSha"`
	BaselineSHA   string                      `json:"baselineSha"`
	Offset        int                         `json:"offset"`
	NextOffset    int                         `json:"nextOffset"`
	HasMore       bool                        `json:"hasMore"`
	SummariesOnly bool                        `json:"summariesOnly"`
}

type WorkspaceGitCommitMessage struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	Truncated bool   `json:"truncated"`
	Notice    string `json:"notice,omitempty"`
}

func historyPageLimit(request WorkspaceGitHistoryPageRequest) (int, error) {
	if request.Offset < 0 || request.Limit < 0 || request.Limit > 200 ||
		request.HeadSHA != "" && !validGitObjectID(request.HeadSHA) ||
		request.BaselineSHA != "" && !validGitObjectID(request.BaselineSHA) ||
		request.Offset > 0 && request.HeadSHA == "" {
		return 0, errors.New("提交历史分页参数无效，请从第一页重新读取")
	}
	if request.Limit == 0 {
		return WorkspaceGitHistoryPageSize, nil
	}
	return request.Limit, nil
}

func (s *WorkspaceGitHistoryService) historySnapshot(ctx context.Context, path, baselineRef string) (head, baseline string, err error) {
	result, err := s.Git.runReadBudget(ctx, path, time.Minute, 4096, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		if ctx.Err() == nil && result.ExitCode == 1 && strings.TrimSpace(result.Stdout) == "" && !result.StdoutTruncated && !result.StderrTruncated {
			return "", "", nil // An unborn repository has no history.
		}
		return "", "", err
	}
	head = strings.TrimSpace(result.Stdout)
	if !validGitObjectID(head) {
		return "", "", errors.New("Git 历史 HEAD 不是完整提交 SHA")
	}
	if strings.TrimSpace(baselineRef) != "" {
		ref, parseErr := ParseRemoteBranchRef(baselineRef)
		if parseErr != nil {
			return "", "", parseErr
		}
		result, err = s.Git.runReadBudget(ctx, path, time.Minute, 4096, "rev-parse", "--verify", "--quiet", "refs/remotes/"+FormatRemoteBranchRef(ref)+"^{commit}")
		if err != nil {
			return "", "", fmt.Errorf("本地不存在主分支 %s；请检查任务快照或更新仓库的远端跟踪分支：%w", FormatRemoteBranchRef(ref), err)
		}
		baseline = strings.TrimSpace(result.Stdout)
		if !validGitObjectID(baseline) {
			return "", "", errors.New("Git 历史主分支不是完整提交 SHA")
		}
	}
	return head, baseline, nil
}

func historySnapshotMatches(request WorkspaceGitHistoryPageRequest, head, baseline string) bool {
	return request.HeadSHA == "" && request.Offset == 0 && request.BaselineSHA == "" || request.HeadSHA == head && request.BaselineSHA == baseline
}

func (s *WorkspaceGitHistoryService) ReadPageContext(ctx context.Context, path, baselineRef string, request WorkspaceGitHistoryPageRequest) (WorkspaceGitHistoryPage, error) {
	limit, err := historyPageLimit(request)
	if err != nil {
		return WorkspaceGitHistoryPage{}, err
	}
	head, baseline, err := s.historySnapshot(ctx, path, baselineRef)
	if err != nil {
		return WorkspaceGitHistoryPage{}, err
	}
	if !historySnapshotMatches(request, head, baseline) {
		return WorkspaceGitHistoryPage{}, ErrWorkspaceGitHistoryChanged
	}
	page := WorkspaceGitHistoryPage{Commits: []domain.WorkspaceGitCommit{}, HeadSHA: head, BaselineSHA: baseline, Offset: request.Offset, NextOffset: request.Offset, SummariesOnly: true}
	if head == "" {
		return page, nil
	}
	revision := head
	if baseline != "" {
		revision = baseline + ".." + head
	}
	result, err := s.Git.runReadBudget(ctx, path, 2*time.Minute, historyPageOutputBytes,
		"log", "--topo-order", "--no-decorate", "--skip="+strconv.Itoa(request.Offset), "--max-count="+strconv.Itoa(limit+1),
		"--format=%H%x00%P%x00%cI%x00%cn%x00%ce%x00%an%x00%ae%x00%<(180,trunc)%s%x00", "--end-of-options", revision, "--")
	if err != nil {
		return WorkspaceGitHistoryPage{}, err
	}
	commits, err := parseGitHistoryPage(result.Stdout, limit+1)
	if err != nil {
		return WorkspaceGitHistoryPage{}, err
	}
	page.HasMore = len(commits) > limit
	page.Commits = commits[:min(limit, len(commits))]
	page.NextOffset += len(page.Commits)
	currentHead, currentBase, err := s.historySnapshot(ctx, path, baselineRef)
	if err != nil {
		return WorkspaceGitHistoryPage{}, err
	}
	if currentHead != head || currentBase != baseline {
		return WorkspaceGitHistoryPage{}, ErrWorkspaceGitHistoryChanged
	}
	return page, ctx.Err()
}

func parseGitHistoryPage(output string, maxCount int) ([]domain.WorkspaceGitCommit, error) {
	if strings.TrimSpace(output) != "" {
		fields := strings.Split(output, "\x00")
		if len(fields) < 9 || (len(fields)-1)%8 != 0 || strings.TrimSpace(fields[len(fields)-1]) != "" {
			return nil, errors.New("Git 提交历史输出不完整，未接受此页")
		}
	}
	commits, err := parseGitHistory(output)
	if err != nil {
		return nil, err
	}
	if len(commits) > maxCount {
		return nil, errors.New("Git 提交历史页超出请求数量")
	}
	for _, commit := range commits {
		if !validGitObjectID(commit.FullHash) {
			return nil, errors.New("Git 提交历史包含无效提交 SHA")
		}
	}
	return commits, nil
}

func (s *WorkspaceGitHistoryService) ReadMessageContext(ctx context.Context, path, baselineRef string, request WorkspaceGitHistoryPageRequest, sha string) (WorkspaceGitCommitMessage, error) {
	if _, err := historyPageLimit(request); err != nil {
		return WorkspaceGitCommitMessage{}, err
	}
	if request.HeadSHA == "" || !validGitObjectID(sha) {
		return WorkspaceGitCommitMessage{}, errors.New("提交详情必须绑定已读取历史的完整 SHA")
	}
	head, baseline, err := s.historySnapshot(ctx, path, baselineRef)
	if err != nil {
		return WorkspaceGitCommitMessage{}, err
	}
	if !historySnapshotMatches(request, head, baseline) {
		return WorkspaceGitCommitMessage{}, ErrWorkspaceGitHistoryChanged
	}
	ancestor := func(target string) (bool, error) {
		result, err := s.Git.runReadBudget(ctx, path, time.Minute, 4096, "merge-base", "--is-ancestor", sha, target)
		if result.ExitCode == 1 && ctx.Err() == nil && !result.StdoutTruncated && !result.StderrTruncated {
			return false, nil
		}
		return err == nil, err
	}
	inHead, err := ancestor(head)
	if err != nil {
		return WorkspaceGitCommitMessage{}, err
	}
	if !inHead {
		return WorkspaceGitCommitMessage{}, errors.New("所选提交不属于当前历史")
	}
	if baseline != "" {
		inBase, err := ancestor(baseline)
		if err != nil {
			return WorkspaceGitCommitMessage{}, err
		}
		if inBase {
			return WorkspaceGitCommitMessage{}, errors.New("所选提交不属于主分支之后的历史")
		}
	}
	result, err := s.Git.runReadBudget(ctx, path, time.Minute, historyMessageOutputBytes, "show", "--no-patch", "--format=%B", "--end-of-options", sha, "--")
	message := WorkspaceGitCommitMessage{SHA: sha, Message: strings.TrimRight(strings.ToValidUTF8(result.Stdout, "�"), "\r\n")}
	if err != nil {
		if ctx.Err() != nil || !errors.Is(err, errGitReadOutputLimit) || !result.StdoutTruncated {
			return WorkspaceGitCommitMessage{}, err
		}
		message.Truncated = true
		message.Notice = "提交信息超过 256 KiB，仅展示前 256 KiB；这不是完整提交信息。"
	}
	currentHead, currentBase, err := s.historySnapshot(ctx, path, baselineRef)
	if err != nil {
		return WorkspaceGitCommitMessage{}, err
	}
	if currentHead != head || currentBase != baseline {
		return WorkspaceGitCommitMessage{}, ErrWorkspaceGitHistoryChanged
	}
	return message, ctx.Err()
}
