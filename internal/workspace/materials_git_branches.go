package workspace

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *MaterialsGitSyncService) RemoteBranches(ctx context.Context, scope MaterialsGitScope) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return nil, err
		}
	}
	result, err := s.Git.RunGlobalContext(ctx, 20*time.Second, true, true, "ls-remote", "--heads", "--", scope.URL)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.Check != nil {
		if err := s.Check(); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	branches := []string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/heads/") {
			return nil, errors.New("远程分支列表格式不完整，请重新读取")
		}
		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		if branch == "" {
			return nil, errors.New("远程分支名称为空，请重新读取")
		}
		if !seen[branch] {
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)
	return branches, nil
}
