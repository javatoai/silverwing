package platform

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// BuildCodexOpenURI creates the registered Codex desktop URI without opening
// it. forceNew always wins over a supplied thread ID and binds the new session
// to the given workspace path.
func BuildCodexOpenURI(workspacePath, threadID string, forceNew bool) (string, error) {
	if !filepath.IsAbs(workspacePath) {
		return "", fmt.Errorf("Codex 工作区路径必须是绝对路径")
	}
	if forceNew {
		threadID = ""
	}
	if threadID != "" {
		if !codexThreadIDPattern.MatchString(threadID) {
			return "", fmt.Errorf("只能打开有效的 Codex 会话 ID")
		}
		return "codex://threads/" + strings.ToLower(threadID), nil
	}
	encoded := strings.ReplaceAll(url.QueryEscape(workspacePath), "+", "%20")
	return "codex://threads/new?path=" + encoded, nil
}
