package workspace

import "strings"

// parseRepositoryPorcelain reads only the XY status columns of porcelain v1.
// Callers own process options and enrich the result with commits/operation state.
func parseRepositoryPorcelain(output string) RepositoryStatus {
	var status RepositoryStatus
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		status.Staged = status.Staged || (line[0] != ' ' && line[0] != '?')
		status.Unstaged = status.Unstaged || (line[1] != ' ' && line[1] != '?')
		status.Untracked = status.Untracked || strings.HasPrefix(line, "??")
	}
	return status
}
