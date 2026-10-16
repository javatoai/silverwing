package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

// loadExtensionBranches mirrors the source editor's read-only git ls-remote
// branch picker. It does not clone, fetch, or execute repository content.
func (s *Service) loadExtensionBranches(ctx context.Context, payload map[string]any) ([]string, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	repositoryURL := payloadString(payload, "repositoryUrl")
	if err := domain.ValidateRemoteGitURL(repositoryURL); err != nil {
		return nil, err
	}
	command := s.gitExecutable(cfg)
	result, err := s.runner.Run(ctx, process.Spec{
		Executable: command,
		Arguments:  []string{"ls-remote", "--heads", repositoryURL},
		Timeout:    time.Minute,
	})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		message := firstNonBlank(result.Stderr, result.Stdout)
		if message == "" {
			message = fmt.Sprintf("退出码 %d", result.ExitCode)
		}
		return nil, fmt.Errorf("Git 分支查询失败：ls-remote --heads %s：%s", repositoryURL, redactProxy(cfg, message))
	}
	branches := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, "refs/heads/") {
			continue
		}
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if domain.ValidateGitRef(&branch) != nil || seen[branch] {
			continue
		}
		seen[branch] = true
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches, nil
}
