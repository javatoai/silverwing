package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type WorkspaceGitHistoryService struct{ Git *GitClient }

func NewWorkspaceGitHistoryService(git *GitClient) *WorkspaceGitHistoryService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &WorkspaceGitHistoryService{Git: git}
}

func (s *WorkspaceGitHistoryService) Read(worktreePath, baselineRef string) ([]domain.WorkspaceGitCommit, error) {
	return s.ReadContext(context.Background(), worktreePath, baselineRef)
}

func (s *WorkspaceGitHistoryService) ReadContext(ctx context.Context, worktreePath, baselineRef string) ([]domain.WorkspaceGitCommit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	revision := "HEAD"
	if strings.TrimSpace(baselineRef) != "" {
		ref, err := ParseRemoteBranchRef(baselineRef)
		if err != nil {
			return nil, err
		}
		qualified := "refs/remotes/" + FormatRemoteBranchRef(ref)
		found, err := s.Git.RunContext(ctx, worktreePath, 2*time.Minute, false, true, "rev-parse", "--verify", "--quiet", qualified+"^{commit}")
		if err != nil {
			return nil, err
		}
		if found.ExitCode != 0 {
			return nil, fmt.Errorf("本地不存在主分支 %s；请检查任务快照或更新仓库的远端跟踪分支", FormatRemoteBranchRef(ref))
		}
		revision = qualified + "..HEAD"
	}
	// Preserve the legacy full-message shape; oversized reads fail explicitly.
	result, err := s.Git.runReadBudget(ctx, worktreePath, 2*time.Minute, 32*1024*1024,
		"log", revision, "--format=%H%x00%P%x00%cI%x00%cn%x00%ce%x00%an%x00%ae%x00%B%x00")
	if err != nil {
		if errors.Is(err, errGitReadOutputLimit) || result.ExitCode <= 0 {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result.ExitCode == 0 {
		return parseGitHistory(result.Stdout)
	}
	diagnostic := strings.ToLower(result.Stderr + "\n" + result.Stdout)
	if strings.Contains(diagnostic, "does not have any commits yet") ||
		(strings.Contains(diagnostic, "ambiguous argument 'head'") && strings.Contains(diagnostic, "unknown revision") && strings.Contains(diagnostic, "or path not in the working tree")) {
		return []domain.WorkspaceGitCommit{}, nil
	}
	return nil, &GitException{Message: "Git 提交历史读取失败", Result: result, Cause: err}
}

func parseGitHistory(output string) ([]domain.WorkspaceGitCommit, error) {
	if strings.TrimSpace(output) == "" {
		return []domain.WorkspaceGitCommit{}, nil
	}
	fields := strings.Split(output, "\x00")
	commits := make([]domain.WorkspaceGitCommit, 0)
	for index := 0; index+7 < len(fields); {
		hash := strings.Trim(fields[index], "\r\n")
		if hash == "" {
			index++
			continue
		}
		parents := strings.Fields(strings.Trim(fields[index+1], "\r\n"))
		timestamp := strings.Trim(fields[index+2], "\r\n")
		if timestamp == "" {
			return nil, fmt.Errorf("Git 提交历史缺少提交时间：%s", hash)
		}
		committedAt, err := time.Parse(time.RFC3339, timestamp)
		if err != nil {
			return nil, fmt.Errorf("无法解析提交时间：%s %s: %w", hash[:min(7, len(hash))], timestamp, err)
		}
		commits = append(commits, domain.WorkspaceGitCommit{
			FullHash: hash, ShortHash: hash[:min(7, len(hash))], ParentHashes: parents, CommittedAt: committedAt,
			CommitterName: strings.Trim(fields[index+3], "\r\n"), CommitterEmail: strings.Trim(fields[index+4], "\r\n"),
			AuthorName: strings.Trim(fields[index+5], "\r\n"), AuthorEmail: strings.Trim(fields[index+6], "\r\n"),
			Message: strings.TrimRight(fields[index+7], "\r\n"),
		})
		index += 8
	}
	return commits, nil
}
