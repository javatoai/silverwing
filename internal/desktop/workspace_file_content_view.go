package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

const workspaceFilePreviewTruncationNotice = "内容较大，预览已截断（前 512 KB）"

func (state *workspaceHistoryState) resetWorkspaceContentPreview() {
	if state != nil {
		state.ContentPreview = nativeCodePreviewState{}
	}
}

// The workspace preview owns one current editor. Its native buffer may
// normalize line endings for display; the service source stays unchanged.
func renderWorkspaceFileContentPreview(c *ui.Context, p palette, state *workspaceHistoryState, preview *workspace.WorkspaceGitFilePreview) ui.Element {
	bindWorkspaceFileContentPreview(state, preview)
	return renderNativeCodePreview(c, p, &state.ContentPreview,
		workspaceFileContentIdentity(state, preview), state.SelectedPath, preview.Content.Text,
		"workspace-file-content-preview")
}

func bindWorkspaceFileContentPreview(state *workspaceHistoryState, preview *workspace.WorkspaceGitFilePreview) *codeeditor.State {
	previous := state.ContentPreview.view
	view := state.ContentPreview.bind(workspaceFileContentIdentity(state, preview), state.SelectedPath, preview.Content.Text)
	if view != previous {
		view.Buffer.SetLanguage(workspaceFilePreviewLanguage(state.SelectedPath, preview.Language))
	}
	view.SoftWrap = false
	return view
}

func workspaceFileContentIdentity(state *workspaceHistoryState, preview *workspace.WorkspaceGitFilePreview) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t",
		state.TaskID, state.ServiceID, state.ModuleID, state.Path, state.Mode, state.BaselineRef,
		state.SelectedPath, state.SelectedKind, preview.Content.Origin, preview.Language, preview.Content.Truncated)
}

func workspacePreviewDisplayedMode(preview *workspace.WorkspaceGitFilePreview, selected workspace.WorkspaceFilePreviewMode) workspace.WorkspaceFilePreviewMode {
	if selected == workspace.PreviewComparison && preview.Comparison != nil && len(preview.Comparison.Rows) > 0 {
		return workspace.PreviewComparison
	}
	if preview.Content != nil {
		return workspace.PreviewContent
	}
	return workspace.PreviewComparison
}

func workspaceFilePreviewTruncationMessage(preview *workspace.WorkspaceGitFilePreview, mode workspace.WorkspaceFilePreviewMode) string {
	truncated := mode == workspace.PreviewContent && preview.Content != nil && preview.Content.Truncated ||
		mode == workspace.PreviewComparison && preview.Comparison != nil && preview.Comparison.Truncated
	if truncated && !strings.Contains(preview.Notice, workspaceFilePreviewTruncationNotice) {
		return workspaceFilePreviewTruncationNotice
	}
	return ""
}
