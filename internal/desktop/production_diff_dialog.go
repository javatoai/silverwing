package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type productionDiffDialog struct {
	Open                                              bool
	Title, SourceRef, TargetRef, SourceSHA, TargetSHA string
	Home, Scope, BatchID, ManualTag                   string
	Incoming                                          bool
	View                                              *productionView
	Target                                            production.Target
	Review                                            workspaceHistoryState
	Commits                                           string
	Tab                                               string
	Generation                                        uint64
	cancel                                            context.CancelFunc
}

func (s *app) openProductionDiff(v *productionView, title, sourceRef, targetRef, sourceSHA, targetSHA string, incoming bool) {
	if v == nil || v.Preview == nil || sourceSHA == "" || targetSHA == "" || s.page != "production" {
		return
	}
	state := s.ensureProduction()
	s.closeProductionDiff()
	d := &productionDiffDialog{Open: true, Title: title, SourceRef: sourceRef, TargetRef: targetRef, SourceSHA: sourceSHA, TargetSHA: targetSHA, Incoming: incoming,
		Home: s.snapshot.Home, Scope: productionConfigScope(s.configJSON), BatchID: v.BatchID, ManualTag: v.ManualTag, View: v, Target: v.Preview.Target}
	d.Review = workspaceHistoryState{Open: true, Mode: "production", FileView: "tree", LeftWidth: 250, TaskSessionKey: fmt.Sprintf("production-diff-%p", d), CompareMode: workspaceReviewMainline}
	d.Review.DiffBaseLabel, d.Review.DiffTargetLabel, d.Review.FileContentLabel = targetRef, "源提交 · "+sourceRef, "源提交内容"
	if incoming {
		d.Review.DiffBaseLabel = "合并基点"
	}
	d.Review.FileSelected = func(file workspace.CompareChange) { s.selectProductionDiffFile(d, file) }
	state.diffDialog = d
	s.requestProductionDiff(d, nil)
}

func (s *app) productionDiffCurrent(d *productionDiffDialog) bool {
	return d != nil && d.Open && s.production != nil && s.production.diffDialog == d && s.page == "production" &&
		s.productionView() == d.View && s.snapshot.Home == d.Home && productionConfigScope(s.configJSON) == d.Scope &&
		d.View.BatchID == d.BatchID && d.View.ManualTag == d.ManualTag && d.View.Preview != nil && d.View.Preview.Target == d.Target
}

func (s *app) closeProductionDiff() {
	if s.production == nil || s.production.diffDialog == nil {
		return
	}
	d := s.production.diffDialog
	d.Open = false
	if d.cancel != nil {
		d.cancel()
	}
	d.Review.resetWorkspaceDiffView()
	d.Review.Preview, d.Review.FileSelected = nil, nil
	s.production.diffDialog = nil
}

func (s *app) selectProductionDiffFile(d *productionDiffDialog, file workspace.CompareChange) {
	if !s.productionDiffCurrent(d) {
		return
	}
	r := &d.Review
	r.resetWorkspaceDiffView()
	r.SelectedPath, r.SelectedOldPath, r.SelectedKind = file.Path, file.OldPath, workspaceGitChangeKind(file.Kind)
	r.Preview, r.PreviewMode, r.Error = nil, "", ""
	r.PreviewStartedAt, r.PreviewFirstFrame = time.Now(), false
	s.requestProductionDiff(d, &file)
}

// Diff reads have their own cancellation/generation and never enter the task
// request router or mutate the production view's current write operation.
func (s *app) requestProductionDiff(d *productionDiffDialog, file *workspace.CompareChange) {
	if !s.productionDiffCurrent(d) || s.backend == nil {
		return
	}
	state := s.production
	window := state.window
	if s.win != nil {
		window = s.win
	}
	if window == nil || window.IsDestroyed() {
		return
	}
	if d.cancel != nil {
		d.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	d.cancel = cancel
	d.Generation++
	generation := d.Generation
	d.Review.Error = ""
	d.Review.FilesLoading, d.Review.PreviewLoading = file == nil, file != nil
	action := application.Action{Kind: "production.diff-files", Payload: map[string]any{"target": d.Target, "sourceSha": d.SourceSHA, "targetSha": d.TargetSHA, "incoming": d.Incoming}}
	if file != nil {
		action.Kind, action.Path = "production.diff-preview", file.Path
		action.Payload["oldPath"], action.Payload["fileKind"] = file.OldPath, file.Kind
	}
	worker := &productionWorker{cancel: cancel, done: make(chan struct{})}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		return
	}
	state.workers[worker] = struct{}{}
	state.mu.Unlock()
	backend := s.backend
	go func() {
		defer func() {
			cancel()
			state.mu.Lock()
			delete(state.workers, worker)
			close(worker.done)
			state.mu.Unlock()
		}()
		result, err := backend.Dispatch(ctx, action)
		var data production.DiffResult
		if err == nil {
			if direct, ok := result.Data.(production.DiffResult); ok {
				data = direct
			} else {
				var raw []byte
				raw, err = json.Marshal(result.Data)
				if err == nil {
					err = json.Unmarshal(raw, &data)
				}
			}
		}
		if window.IsDestroyed() {
			return
		}
		window.Update(func() {
			state.mu.Lock()
			closed := state.closed
			state.mu.Unlock()
			if closed || !s.productionDiffCurrent(d) || d.Generation != generation {
				return
			}
			r := &d.Review
			r.FilesLoading, r.PreviewLoading = false, false
			if err != nil {
				r.Error = err.Error()
				window.Invalidate()
				return
			}
			if data.Files.TargetCommit != d.SourceSHA || data.Files.BaseCommit == "" || !d.Incoming && data.Files.BaseCommit != d.TargetSHA {
				r.Error = "差异返回的提交与当前目标不匹配，请重新打开。"
				window.Invalidate()
				return
			}
			if file == nil {
				r.Files, r.BaseCommit, r.TargetCommit = data.Files.Files, data.Files.BaseCommit, data.Files.TargetCommit
				d.Commits = data.Commits
				if len(r.Files) > 0 {
					s.selectProductionDiffFile(d, r.Files[0])
				}
			} else {
				if data.Files.BaseCommit != r.BaseCommit {
					r.Error = "差异基线已变化，请重新打开。"
					window.Invalidate()
					return
				}
				r.Preview = data.Preview
			}
			window.Invalidate()
		})
	}()
}

func (s *app) renderProductionDiffDialog(c *ui.Context, p palette) {
	if s.production == nil || s.production.diffDialog == nil {
		return
	}
	d := s.production.diffDialog
	if !s.productionDiffCurrent(d) {
		s.closeProductionDiff()
		return
	}
	r := &d.Review
	dialog := ui.DialogBase(c.Key("production-diff-dialog"), &d.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		panel.Width(min(float32(1360), width-32)).Height(min(float32(850), height-32)).MinWidth(0).MinHeight(0).Padding(16).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.closeProductionDiff() })
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, d.Title, 18, p.text).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
				if productionButton(c, "production-diff-close", "关闭", p, false, false) {
					s.closeProductionDiff()
				}
			})
			if !d.Open {
				return
			}
			sourceTooltip := d.SourceRef + "\n" + d.SourceSHA
			if d.Incoming {
				sourceTooltip += "\n文件差异按合并基点计算待合入变更。"
			}
			label(c, "源："+d.SourceRef, 11, p.muted).FillWidth().MinWidth(0).SingleLine().Tooltip(sourceTooltip)
			label(c, "目标："+d.TargetRef, 11, p.muted).FillWidth().MinWidth(0).SingleLine().Tooltip(d.TargetRef + "\n" + d.TargetSHA)
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				workspaceReviewModeButton(c, "production-diff-files", "文件差异", d.Tab != "commits", p, func() { d.Tab = "files" })
				workspaceReviewModeButton(c, "production-diff-commits", "提交记录", d.Tab == "commits", p, func() { d.Tab = "commits" })
			})
			if r.Error != "" {
				label(c, r.Error, 12, p.danger).Wrap()
				if productionButton(c, "production-diff-retry", "重试", p, false, false) {
					if r.SelectedPath == "" {
						s.requestProductionDiff(d, nil)
					} else {
						f := workspace.CompareChange{Path: r.SelectedPath, OldPath: r.SelectedOldPath, Kind: string(r.SelectedKind)}
						s.requestProductionDiff(d, &f)
					}
				}
			}
			ui.Divider(c)
			if d.Tab == "commits" {
				if d.Incoming {
					label(c, "< 目标独有 · > 来源独有", 11, p.muted).Wrap()
				}
				ui.Scroll(c.Key("production-diff-log")).Grow(1).FillWidth().MinHeight(0).Children(func() { label(c, firstNonEmpty(d.Commits, "没有独有提交。"), 12, p.text).Wrap().Selectable() })
				return
			}
			if !r.FilesLoading && r.Error == "" && r.TargetCommit != "" && len(r.Files) == 0 {
				label(c, "无差异", 12, p.muted)
				return
			}
			ui.Row(c).Grow(1).FillWidth().MinHeight(0).MinWidth(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
				left := min(float32(280), max(float32(160), (width-64)*.25))
				if r.LeftCollapsed {
					left = 36
				}
				ui.Column(c).Width(left).MinHeight(0).Shrink(0).Children(func() { s.renderWorkspaceReviewFileList(c, p, r, "production-diff") })
				ui.Box(c).Width(1).Background(p.line)
				ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() { s.renderWorkspaceReviewPreview(c, p, r, "production-diff") })
			})
		})
	})
	if dialog.Dismissed() || !d.Open {
		s.closeProductionDiff()
	}
}
