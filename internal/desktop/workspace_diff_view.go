package desktop

import (
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type workspaceDiffIdentity struct {
	comparison       *workspace.WorkspaceFileComparison
	path             string
	kind             domain.WorkspaceGitFileChangeKind
	language         workspace.WorkspaceFileLanguage
	oldContent       *workspace.WorkspaceFilePreviewContent
	newContent       *workspace.WorkspaceFilePreviewContent
	oldText          string
	newText          string
	oldTruncated     bool
	newTruncated     bool
	comparisonCutoff bool
}

// workspaceDiffRendererState is deliberately only the user's reading state.
// It never retains the rendered source text or MyGO buffers, so changing
// files cannot keep whole historical snapshots alive in the session cache.
type workspaceDiffRendererState struct {
	NativeOldX, NativeNewX float32
	NativeScrollY          float64
	HasNativeScroll        bool
	RowsOldX, RowsNewX     float32
	RowsScrollY            float32
	HasRowsScroll          bool
	HunkIndex              int
	HunkSelected           bool
	InitialPositioned      bool
	Collapsed              bool
	CollapseKnown          bool
}

func (identity workspaceDiffIdentity) matches(other workspaceDiffIdentity) bool {
	return identity.comparison == other.comparison && identity.path == other.path && identity.kind == other.kind && identity.language == other.language &&
		identity.oldContent == other.oldContent && identity.newContent == other.newContent &&
		identity.oldText == other.oldText && identity.newText == other.newText &&
		identity.oldTruncated == other.oldTruncated && identity.newTruncated == other.newTruncated &&
		identity.comparisonCutoff == other.comparisonCutoff
}

func workspaceDiffRendererKey(state *workspaceHistoryState) string {
	if state == nil {
		return ""
	}
	return strings.Join([]string{
		state.TaskSessionKey,
		workspaceReviewComparisonKey(state),
		state.SelectedOldPath,
		state.SelectedPath,
		string(state.SelectedKind),
	}, "\x00")
}

func workspaceDiffRendererStateFor(state *workspaceHistoryState, key string) *workspaceDiffRendererState {
	if state == nil || key == "" {
		return nil
	}
	if state.DiffRendererStates == nil {
		state.DiffRendererStates = make(map[string]*workspaceDiffRendererState)
	}
	if saved := state.DiffRendererStates[key]; saved != nil {
		return saved
	}
	saved := &workspaceDiffRendererState{}
	state.DiffRendererStates[key] = saved
	return saved
}

func (state *workspaceHistoryState) activateWorkspaceDiffRenderer(key string) *workspaceDiffRendererState {
	if state == nil || key == "" {
		return nil
	}
	if state.ActiveDiffRendererKey != key {
		state.ActiveDiffRendererKey = key
		if saved := workspaceDiffRendererStateFor(state, key); saved != nil {
			state.DiffHunkIndex = saved.HunkIndex
			state.DiffHunkSelected = saved.HunkSelected
		}
	}
	return workspaceDiffRendererStateFor(state, key)
}

func (state *workspaceHistoryState) saveActiveWorkspaceDiffRenderer() {
	if state == nil || state.ActiveDiffRendererKey == "" {
		return
	}
	saved := workspaceDiffRendererStateFor(state, state.ActiveDiffRendererKey)
	if saved == nil {
		return
	}
	saved.HunkIndex = state.DiffHunkIndex
	saved.HunkSelected = state.DiffHunkSelected
	if view := state.NativeDiff; view != nil {
		if !view.singlePane {
			state.SplitRatio = firstNonZeroSplitRatio(view.view.SplitRatio, state.SplitRatio)
		}
		position := view.view.ScrollPosition()
		saved.NativeOldX, saved.NativeNewX, saved.NativeScrollY = position.OldX, position.NewX, position.Y
		saved.HasNativeScroll = true
		saved.InitialPositioned = view.positioned
		saved.Collapsed, saved.CollapseKnown = view.collapsed, view.collapseKnown
	}
	if view := state.DiffRows; view != nil {
		view.syncScroll()
		saved.RowsOldX, saved.RowsNewX = view.oldScroll.X, view.newScroll.X
		saved.RowsScrollY = view.newScroll.Y
		saved.HasRowsScroll = true
	}
}

func (state *workspaceHistoryState) resetWorkspaceDiffView() {
	if state == nil {
		return
	}
	state.saveActiveWorkspaceDiffRenderer()
	state.NativeDiff = nil
	state.NativeDiffIdentity = nil
	state.DiffHunkIndex = 0
	state.DiffHunkSelected = false
	state.ActiveDiffRendererKey = ""
}

// workspaceNativeDiffView opts in only when the service returned both exact,
// complete sources and the synchronous MyGO diff stays within the same bounds
// as the developer-tools diff view. Every other comparison keeps its row data.
func (s *app) workspaceNativeDiffView(state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison) *diffNativeViewState {
	if state == nil || comparison == nil {
		return nil
	}
	language := workspace.WorkspaceFileLanguageFromPath(state.SelectedPath)
	if state.Preview != nil {
		language = state.Preview.Language
	}
	identity := workspaceDiffIdentity{
		comparison:       comparison,
		path:             state.SelectedPath,
		kind:             state.SelectedKind,
		language:         language,
		oldContent:       comparison.OldContent,
		newContent:       comparison.NewContent,
		comparisonCutoff: comparison.Truncated,
	}
	if comparison.OldContent != nil {
		identity.oldText = comparison.OldContent.Text
		identity.oldTruncated = comparison.OldContent.Truncated
	}
	if comparison.NewContent != nil {
		identity.newText = comparison.NewContent.Text
		identity.newTruncated = comparison.NewContent.Truncated
	}
	if state.NativeDiffIdentity == nil || !state.NativeDiffIdentity.matches(identity) {
		keepFilterPreference := state.SelectedKind == domain.WorkspaceGitAdded || state.NativeDiffIdentity != nil && state.NativeDiffIdentity.kind == domain.WorkspaceGitAdded
		state.resetWorkspaceDiffView()
		if !keepFilterPreference {
			state.ChangedLinesOnly = false
		}
		state.NativeDiffIdentity = &identity
	}
	rendererKey := workspaceDiffRendererKey(state)
	saved := state.activateWorkspaceDiffRenderer(rendererKey)

	// Source CHANGED_LINES is a strict row filter, not collapsed context.
	// The existing numbered row renderer preserves the service line numbers.
	if state.ChangedLinesOnly && state.SelectedKind != domain.WorkspaceGitAdded {
		state.saveActiveWorkspaceDiffRenderer()
		state.NativeDiff = nil
		return nil
	}
	oldText, newText, eligible := workspaceNativeDiffSourcesForKind(comparison, state.SelectedKind)
	if !eligible {
		state.NativeDiff = nil
		return nil
	}
	if state.NativeDiff == nil {
		state.NativeDiff = newDiffNativeViewState(oldText, newText)
		if hunks, ok := workspacePreparedDiffHunks(comparison, oldText, newText); ok {
			state.NativeDiff.view.SetPreparedHunks(hunks)
		}
		state.NativeDiff.view.SplitRatio = firstNonZeroSplitRatio(state.SplitRatio, .5)
		if saved != nil {
			state.NativeDiff.view.ExpandUnchanged = !saved.Collapsed
			if saved.HasNativeScroll {
				state.NativeDiff.view.SetScrollPosition(editor.DiffScrollPosition{
					OldX: saved.NativeOldX, NewX: saved.NativeNewX, Y: saved.NativeScrollY,
				})
			}
			state.NativeDiff.positioned = saved.InitialPositioned
			state.NativeDiff.collapsed, state.NativeDiff.collapseKnown = saved.Collapsed, saved.CollapseKnown
		}
		syntax := workspaceFilePreviewLanguage(state.SelectedPath, language)
		state.NativeDiff.view.Old.SetLanguage(syntax)
		state.NativeDiff.view.New.SetLanguage(syntax)
	}
	state.NativeDiff.singlePane = state.SelectedKind == domain.WorkspaceGitAdded
	state.SplitRatio = firstNonZeroSplitRatio(state.NativeDiff.view.SplitRatio, state.SplitRatio)
	return state.NativeDiff
}

// workspacePreparedDiffHunks projects the already computed service rows onto
// the complete native buffers; it never runs a second line-diff algorithm.
func workspacePreparedDiffHunks(comparison *workspace.WorkspaceFileComparison, oldText, newText string) ([]editor.DiffHunk, bool) {
	if comparison == nil || comparison.Truncated {
		return nil, false
	}
	hunks := make([]editor.DiffHunk, 0)
	a, b := 0, 0
	var pending *editor.DiffHunk
	flush := func() {
		if pending != nil {
			pending.OldEnd, pending.NewEnd = a, b
			hunks = append(hunks, *pending)
			pending = nil
		}
	}
	for _, row := range comparison.Rows {
		if row.OldLine != nil && row.OldLine.LineNumber != a+1 || row.NewLine != nil && row.NewLine.LineNumber != b+1 {
			return nil, false
		}
		if workspaceComparisonRowChanged(row) {
			if pending == nil {
				pending = &editor.DiffHunk{OldStart: a, NewStart: b}
			}
		} else {
			flush()
		}
		if row.OldLine != nil {
			a++
		}
		if row.NewLine != nil {
			b++
		}
	}
	flush()
	// Git's rows exclude the final empty line; native buffers retain it.
	// A newline present on just one side belongs to the last change.
	nOld, nNew := nativeDiffLineCount(oldText), nativeDiffLineCount(newText)
	if nOld-a != nNew-b {
		if len(hunks) > 0 && hunks[len(hunks)-1].OldEnd == a && hunks[len(hunks)-1].NewEnd == b {
			hunks[len(hunks)-1].OldEnd, hunks[len(hunks)-1].NewEnd = nOld, nNew
		} else {
			hunks = append(hunks, editor.DiffHunk{OldStart: a, OldEnd: nOld, NewStart: b, NewEnd: nNew})
		}
	}
	return hunks, true
}

func workspaceNativeDiffSources(comparison *workspace.WorkspaceFileComparison) (oldText, newText string, ok bool) {
	return workspaceNativeDiffSourcesForKind(comparison, "")
}

func workspaceNativeDiffSourcesForKind(comparison *workspace.WorkspaceFileComparison, kind domain.WorkspaceGitFileChangeKind) (oldText, newText string, ok bool) {
	if comparison == nil || comparison.Truncated {
		return "", "", false
	}
	if kind == domain.WorkspaceGitAdded {
		if comparison.NewContent == nil || comparison.NewContent.Truncated {
			return "", "", false
		}
		return "", comparison.NewContent.Text, nativeDiffTextEligible(comparison.NewContent.Text)
	}
	if comparison.OldContent == nil || comparison.NewContent == nil || comparison.OldContent.Truncated || comparison.NewContent.Truncated {
		return "", "", false
	}
	oldText, newText = comparison.OldContent.Text, comparison.NewContent.Text
	return oldText, newText, nativeDiffTextEligible(oldText, newText)
}

func nativeDiffTextEligible(texts ...string) bool {
	bytes, lines := 0, 0
	for _, text := range texts {
		if strings.ContainsRune(text, '\r') {
			return false
		}
		bytes += len(text)
		lines += nativeDiffLineCount(text)
	}
	return bytes <= nativeDiffMaxBytes && lines <= nativeDiffMaxLines
}

func firstNonZeroSplitRatio(values ...float32) float32 {
	for _, value := range values {
		if value > 0 && value < 1 {
			return value
		}
	}
	return .5
}

func (s *app) renderWorkspaceNativeComparison(c *ui.Context, p palette, state *workspaceHistoryState, view *diffNativeViewState) {
	if state == nil || view == nil {
		return
	}
	added, removed := view.stats()
	hunks := view.hunkCount()
	if state.DiffHunkSelected && state.DiffHunkIndex >= hunks {
		state.DiffHunkIndex = max(0, hunks-1)
		state.DiffHunkSelected = hunks > 0
	}
	comparison := state.Preview.Comparison
	oldTitle, newTitle := workspaceDiffSideLabels(state, comparison)
	ratio := firstNonZeroSplitRatio(state.SplitRatio, view.view.SplitRatio)
	singlePane := view.singlePane
	width, _ := c.Size()
	showStats := singlePane || width*(1-ratio) >= 260
	if !view.positioned {
		if singlePane {
			view.view.SetScrollPosition(editor.DiffScrollPosition{})
		} else if view.hunkCount() > 0 {
			view.goToHunk(0)
			state.DiffHunkIndex, state.DiffHunkSelected = 0, true
		}
		view.positioned = true
	}
	if singlePane {
		view.render(c, p, false, false)
		state.saveActiveWorkspaceDiffRenderer()
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Background(p.surface).Radius(7).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(0).Padding(0, 6).AlignItems(ui.Center).Background(p.soft).Children(func() {
			if singlePane {
				label(c, newTitle, 10, p.muted).Grow(1).MinWidth(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, false))
			} else {
				label(c, oldTitle, 10, p.muted).WidthPercent(ratio * 100).MinWidth(0).Shrink(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, true))
				ui.Box(c.Key("workspace-native-diff-title-divider")).Width(1).Shrink(0).Background(p.line)
				label(c, newTitle, 10, p.muted).Grow(1).Basis(0).MinWidth(0).NoWrap().Tooltip(workspaceDiffSideTooltip(state, comparison, false))
			}
			ui.Row(c).Gap(5).Shrink(0).AlignItems(ui.Center).Children(func() {
				if showStats {
					label(c, "+"+formatWorkspaceDiffCount(added), 10, p.green).NoWrap()
					label(c, "−"+formatWorkspaceDiffCount(removed), 10, p.danger).NoWrap()
				}
				workspaceDiffHunkButton(c, "workspace-diff-hunk-previous", "上一处", p,
					hunks > 0 && state.DiffHunkSelected && state.DiffHunkIndex > 0, func() {
						state.DiffHunkIndex--
						view.goToHunk(state.DiffHunkIndex)
					})
				hunkLabel := "无差异块"
				if hunks > 0 && state.DiffHunkSelected {
					hunkLabel = formatWorkspaceDiffCount(state.DiffHunkIndex+1) + " / " + formatWorkspaceDiffCount(hunks)
				} else if hunks > 0 {
					hunkLabel = formatWorkspaceDiffCount(hunks) + " 处差异"
				}
				label(c, hunkLabel, 10, p.muted).NoWrap()
				workspaceDiffHunkButton(c, "workspace-diff-hunk-next", "下一处", p,
					hunks > 0 && (!state.DiffHunkSelected || state.DiffHunkIndex < hunks-1), func() {
						if !state.DiffHunkSelected {
							state.DiffHunkIndex = 0
							state.DiffHunkSelected = true
						} else {
							state.DiffHunkIndex++
						}
						view.goToHunk(state.DiffHunkIndex)
					})
			})
		})
		view.view.SplitRatio = ratio
		view.render(c, p, state.ChangedLinesOnly, false)
		state.SplitRatio = view.view.SplitRatio
		state.saveActiveWorkspaceDiffRenderer()
	})
}

func workspaceDiffSideLabels(state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison) (string, string) {
	oldTitle, newTitle := "HEAD（修改前）", "工作区（当前）"
	if state != nil {
		switch state.CompareMode {
		case workspaceReviewMainline:
			oldTitle = "主分支基线"
		case workspaceReviewCommits:
			oldTitle = "范围起点"
			newTitle = "所选提交"
		case workspaceReviewUncommitted:
			oldTitle, newTitle = "HEAD（修改前）", "工作区（当前）"
		}
	}
	if comparison != nil {
		if comparison.OldContent != nil {
			switch comparison.OldContent.Origin {
			case workspace.PreviewOriginMainline:
				oldTitle = "主分支基线"
			case workspace.PreviewOriginBaseline:
				oldTitle = "范围起点"
			case workspace.PreviewOriginHead:
				oldTitle = "HEAD（修改前）"
			}
		} else {
			oldTitle += "（文件不存在）"
		}
		if comparison.NewContent != nil {
			switch comparison.NewContent.Origin {
			case workspace.PreviewOriginSelectedCommits:
				newTitle = "所选提交"
			case workspace.PreviewOriginWorktree:
				newTitle = "工作区（当前）"
			case workspace.PreviewOriginHead:
				newTitle = "当前提交"
			case workspace.PreviewOriginSimulated:
				newTitle = "模拟结果"
			}
		} else {
			newTitle += "（文件已删除）"
		}
	}
	return oldTitle, newTitle
}

func workspaceDiffSideTooltip(state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison, old bool) string {
	oldTitle, newTitle := workspaceDiffSideLabels(state, comparison)
	parts := []string{newTitle}
	if old {
		parts[0] = oldTitle
	}
	if state != nil {
		if old {
			if state.BaselineRef != "" {
				parts = append(parts, "主分支引用："+state.BaselineRef)
			}
			if state.BaseCommit != "" {
				parts = append(parts, "基准提交："+state.BaseCommit)
			}
		} else if state.TargetCommit != "" {
			parts = append(parts, "目标快照："+state.TargetCommit)
		}
	}
	return strings.Join(parts, "\n")
}

func workspaceDiffHunkButton(c *ui.Context, key, text string, p palette, enabled bool, clicked func()) {
	button := ui.ButtonBase(c.Key(key)).Width(26).Height(24).Center().Radius(6).Label(text).Tooltip(text).Disabled(!enabled)
	if !enabled {
		button.Background(p.soft)
	} else if button.Hovered() {
		button.Background(p.selected)
	} else {
		button.Background(p.surface)
	}
	button.Children(func() {
		name := "chevron-right"
		if text == "上一处" {
			name = "chevron-left"
		}
		icon(c, name, 14, p.text)
	})
	if button.Clicked() && enabled {
		clicked()
	}
}

func formatWorkspaceDiffCount(count int) string {
	return strconv.Itoa(count)
}
