package desktop

import (
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/domain"
)

func workspaceReviewNextCommitRange(commits []domain.WorkspaceGitCommit, start, end, selected string, current []string) (string, string, []string, error) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return start, end, append([]string(nil), current...), errors.New("提交标识为空，已保留当前选择。")
	}
	if _, ok := workspaceReviewCommitByHash(commits, selected); !ok {
		return start, end, append([]string(nil), current...), errors.New("所选提交已不在当前候选列表中，请刷新后重试。")
	}
	if len(current) == 1 && current[0] == selected {
		return "", "", nil, nil
	}
	if containsString(current, selected) {
		return selected, selected, []string{selected}, nil
	}
	if start == "" || end == "" {
		return selected, selected, []string{selected}, nil
	}
	if inRange, err := workspaceReviewCommitRange(commits, start, end); err == nil && containsString(inRange, selected) {
		return selected, selected, []string{selected}, nil
	}
	nextStart, nextEnd := start, end
	switch {
	case workspaceReviewIsAncestor(commits, selected, start):
		nextStart = selected
	case workspaceReviewIsAncestor(commits, end, selected):
		nextEnd = selected
	default:
		return start, end, append([]string(nil), current...), errors.New("所选提交与当前范围没有可确定的祖先关系，已保留原选择。")
	}
	hashes, err := workspaceReviewCommitRange(commits, nextStart, nextEnd)
	if err != nil {
		return start, end, append([]string(nil), current...), err
	}
	return nextStart, nextEnd, hashes, nil
}

// workspaceReviewCommitRange returns the commits in start^..end. The result
// list follows the history's display order; membership is derived only from
// parent links, never from timestamps or row positions.
func workspaceReviewCommitRange(commits []domain.WorkspaceGitCommit, start, end string) ([]string, error) {
	startCommit, startOK := workspaceReviewCommitByHash(commits, start)
	_, endOK := workspaceReviewCommitByHash(commits, end)
	if !startOK || !endOK {
		return nil, errors.New("范围端点不在当前提交历史中，请刷新后重新选择。")
	}
	if !workspaceReviewIsAncestor(commits, start, end) {
		return nil, errors.New("范围两端不在同一条可比较的祖先链上，已保留原选择。")
	}
	included := workspaceReviewAncestorSet(commits, end)
	excluded := make(map[string]bool)
	if len(startCommit.ParentHashes) > 0 {
		excluded = workspaceReviewAncestorSet(commits, startCommit.ParentHashes[0])
	}
	hashes := make([]string, 0)
	for _, commit := range commits {
		if commit.FullHash != "" && included[commit.FullHash] && !excluded[commit.FullHash] {
			hashes = append(hashes, commit.FullHash)
		}
	}
	if !containsString(hashes, start) || !containsString(hashes, end) {
		return nil, fmt.Errorf("无法从当前提交列表补全 %s 到 %s 的范围，请刷新后重试。", shortCommitID(start), shortCommitID(end))
	}
	return hashes, nil
}

func workspaceReviewCommitByHash(commits []domain.WorkspaceGitCommit, hash string) (domain.WorkspaceGitCommit, bool) {
	for _, commit := range commits {
		if commit.FullHash == hash {
			return commit, true
		}
	}
	return domain.WorkspaceGitCommit{}, false
}

func workspaceReviewIsAncestor(commits []domain.WorkspaceGitCommit, ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	if ancestor == descendant {
		return true
	}
	return workspaceReviewAncestorSet(commits, descendant)[ancestor]
}

func workspaceReviewAncestorSet(commits []domain.WorkspaceGitCommit, head string) map[string]bool {
	byHash := make(map[string]domain.WorkspaceGitCommit, len(commits))
	for _, commit := range commits {
		if commit.FullHash != "" {
			byHash[commit.FullHash] = commit
		}
	}
	seen := make(map[string]bool)
	queue := []string{head}
	for len(queue) > 0 {
		hash := queue[0]
		queue = queue[1:]
		if hash == "" || seen[hash] {
			continue
		}
		seen[hash] = true
		commit, ok := byHash[hash]
		if !ok {
			continue
		}
		queue = append(queue, commit.ParentHashes...)
	}
	return seen
}

func shortCommitID(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
