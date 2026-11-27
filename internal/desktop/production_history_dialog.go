package desktop

import (
	"sort"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionHistoryDialog struct {
	Open, Loading              bool
	View                       *productionView
	State                      *productionUIState
	Selection                  production.Selection
	Home, Scope                string
	ServiceName                string
	Error                      string
	RowErrors                  map[string]string
	SelectedBatchID            string
	selectedTarget             production.Target
	DetailError, DetailMessage string
	ListScroll                 ui.ScrollState
	request                    *productionWorker
	readOnly                   bool
}

func (s *app) productionHistoryCurrent(d *productionHistoryDialog) bool {
	return d != nil && d.Open && d.View != nil && d.State != nil && s.productionAvailable() && s.releaseUnit.state.production == d.State && d.State.historyDialog == d &&
		s.page == "production" && s.productionView() == d.View && d.View.Selection == d.Selection &&
		s.snapshot.Home == d.Home && s.releaseUnit.state.production.loadedHome == d.Home && s.currentProductionScope() == d.Scope
}

func (s *app) closeProductionHistory(d *productionHistoryDialog) {
	d.Open = false
	if worker := d.request; worker != nil && worker.cancelOnDisable {
		worker.cancel()
		if d.View.request == worker {
			d.View.Busy, d.View.request = false, nil
		}
	}
	if s.releaseUnit.state.production == d.State && d.State.historyDialog == d {
		d.State.historyDialog = nil
		s.pruneProductionHistoricalChoices(d.State)
	}
}

func (s *app) openProductionHistory(v *productionView) {
	if !s.productionAvailable() || v == nil || v.Busy || s.productionView() != v || s.confirmationDialog != nil {
		return
	}
	name := v.Selection.ServiceID
	for _, choice := range s.releaseUnit.state.production.choices {
		if choice.ID == v.Selection.ServiceID {
			name = choice.Name
			break
		}
	}
	d := &productionHistoryDialog{Open: true, View: v, State: s.releaseUnit.state.production, Selection: v.Selection,
		Home: s.snapshot.Home, Scope: s.currentProductionScope(), ServiceName: name, RowErrors: map[string]string{},
		readOnly: s.productionGenbuProbeDisabled(v.Selection.ServiceID)}
	s.releaseUnit.state.production.historyDialog = d
	s.readProductionHistory(d)
}

func (s *app) readProductionHistory(d *productionHistoryDialog) {
	if !s.productionHistoryCurrent(d) || d.View.Busy || s.confirmationDialog != nil {
		return
	}
	d.Error = ""
	// Without a backend, an already loaded snapshot is still readable (also used
	// by offline native previews). Live reads use only the local list action.
	if s.backend == nil {
		return
	}
	s.productionRequestViewFromHistory(d.View, application.Action{Kind: "production.list"}, "", d)
	d.Loading = d.View.Busy
	if !d.Loading {
		d.Error = "暂时无法读取历史批次，请重试。"
	}
}

func (s *app) productionHistoryRecords(d *productionHistoryDialog) []production.Batch {
	var records []production.Batch
	for _, b := range d.State.batches {
		if d.State.includesProductionHistoryBatch(b, d.Selection, d.readOnly) {
			records = append(records, b)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, b := productionBatchOrderTime(records[i]), productionBatchOrderTime(records[j])
		if a.Equal(b) {
			return records[i].ID > records[j].ID
		}
		return a.After(b)
	})
	return records
}

func (state *productionUIState) includesProductionHistoryBatch(b production.Batch, selection production.Selection, readOnly bool) bool {
	return (b.Ended || readOnly) && b.Target.Selection == selection && !state.deletedBatches[b.ID]
}

func (state *productionUIState) productionHistoryCount(selection production.Selection, readOnly bool) int {
	count := 0
	for _, b := range state.batches {
		if state.includesProductionHistoryBatch(b, selection, readOnly) {
			count++
		}
	}
	return count
}

func (s *app) productionHistoryRequestCurrent(d *productionHistoryDialog, action application.Action) bool {
	if !s.productionHistoryCurrent(d) || s.confirmationDialog != nil {
		return false
	}
	switch action.Kind {
	case "production.list":
		return true
	case "production.delete":
		id, _ := action.Payload["batchId"].(string)
		if b := d.record(id); b != nil && action.Payload["confirmed"] == true {
			token, err := production.BatchRecordToken(*b)
			return err == nil && token == action.Payload["recordToken"]
		}
	case "production.refresh-build", "production.reconcile-ended", "production.open-ide":
		b := s.productionHistoryDetail(d)
		return b != nil && b.ID == action.Payload["batchId"]
	}
	return false
}

func (s *app) requestProductionHistoryDetail(d *productionHistoryDialog, b production.Batch, kind string) {
	if current := s.productionHistoryDetail(d); current == nil || current.ID != b.ID || current.Target != b.Target || d.View.Busy {
		return
	}
	d.DetailError, d.DetailMessage = "", ""
	s.productionRequestViewFromHistory(d.View, application.Action{Kind: kind, Payload: map[string]any{"batchId": b.ID}}, "", d)
}

func (s *app) finishProductionHistoryRequest(d *productionHistoryDialog, action application.Action, worker *productionWorker, generation uint64, report production.Report, result application.ActionResult, err error) {
	v := d.View
	if v.request == worker {
		v.Busy, v.request = false, nil
	}
	if d.request == worker {
		d.request = nil
	}
	d.Loading = false
	// A late list must not replace a newly selected service/home/configuration.
	// Deletion cleanup is likewise restricted to its original dialog scope.
	if v.Generation != generation || !s.productionHistoryCurrent(d) {
		return
	}
	if action.Kind == "production.list" {
		if err != nil {
			d.Error = err.Error()
			return
		}
		d.State.replaceBatches(report.Batches)
		d.State.addHistoricalChoices(s.snapshot)
		return
	}
	if action.Kind != "production.delete" {
		b := s.productionHistoryDetail(d)
		if b == nil || b.ID != action.Payload["batchId"] {
			return
		}
		if err != nil {
			if action.Kind == "production.open-ide" {
				s.recordDevelopmentToolUnavailable(err)
			}
			d.DetailError = err.Error()
			return
		}
		if action.Kind != "production.open-ide" {
			if report.Batch == nil || !report.Batch.Ended || report.Batch.ID != b.ID || report.Batch.Target != b.Target {
				d.DetailError = "返回的批次与当前记录不一致，请重新查看。"
				return
			}
			d.State.mergeBatch(*report.Batch)
		}
		d.DetailMessage = result.Message
		return
	}
	id, _ := action.Payload["batchId"].(string)
	if err != nil {
		d.RowErrors[id] = err.Error()
		return
	}
	if !d.State.productionDeleteRecordUnchanged(action) {
		d.RowErrors[id] = "批次记录已变化，请重新读取后核对。"
		return
	}
	delete(d.RowErrors, id)
	s.forgetProductionBatch(d.State, id)
	if id == d.SelectedBatchID {
		d.SelectedBatchID = ""
	}
	s.lastMessage = firstNonEmpty(result.Message, "本地批次记录已删除")
}

func (s *app) renderProductionHistoryDialog(c *ui.Context, p palette) {
	if s.releaseUnit.state.production == nil || s.releaseUnit.state.production.historyDialog == nil {
		return
	}
	d := s.releaseUnit.state.production.historyDialog
	if !s.productionHistoryCurrent(d) {
		s.closeProductionHistory(d)
		return
	}
	batch := s.productionHistoryDetail(d)
	blocked := d.View.Busy || s.confirmationDialog != nil
	width, height := c.Size()
	drawerWidth := min(float32(530), width*.68)
	if width <= 700 {
		drawerWidth = width
	}
	dialog := ui.DialogBase(c.Key("production-history-dialog"), &d.Open, func(backdrop, panel ui.Element) {
		backdrop.Row().Justify(ui.End).AlignItems(ui.Stretch).Background(ui.RGBA(0, 0, 0, .16))
		panel.Width(max(1, drawerWidth)).Height(max(1, height)).MinWidth(0).MinHeight(0).Shrink(0).
			Padding(16).Gap(12).Background(p.surface).BorderWidth(0, 0, 0, 1).BorderColor(p.line).
			Label("历史批次抽屉").Description(d.ServiceName)
		panel.Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).Shrink(0).AlignItems(ui.Center).Children(func() {
				if batch != nil && iconButtonDisabled(c, "production-history-back", "chevron-left", "返回历史批次", p, blocked).Clicked() {
					d.SelectedBatchID = ""
					batch = nil
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					title := "历史批次"
					if batch != nil {
						title = productionHistoryTitle(*batch)
					}
					label(c, title, 17, p.text).FontWeight(600).SingleLine().Tooltip(title)
					label(c, d.ServiceName, 12, p.muted).SingleLine().Tooltip(d.ServiceName)
				})
				if iconButtonDisabled(c, "production-history-close", "close", "关闭", p, s.confirmationDialog != nil).AutoFocus().Clicked() {
					d.Open = false
				}
			})
			ui.Divider(c)
			if batch == nil {
				s.renderProductionHistoryList(c, p, d, blocked)
			} else {
				s.renderProductionHistoryDetail(c, p, d, *batch, blocked)
			}
		})
	})
	if dialog.Dismissed() || !d.Open {
		s.closeProductionHistory(d)
	}
}

func (s *app) renderProductionHistoryList(c *ui.Context, p palette, d *productionHistoryDialog, blocked bool) {
	records := s.productionHistoryRecords(d)
	ui.Scroll(c.Key("production-history-list")).Grow(1).FillWidth().MinWidth(0).MinHeight(0).
		TrackScroll(&d.ListScroll).Gap(2).Label("历史批次列表").Children(func() {
		switch {
		case d.Loading:
			label(c, "正在读取历史批次…", 13, p.muted).Padding(12, 0)
		case d.Error != "":
			label(c, "历史批次读取失败："+d.Error, 12, p.danger).FillWidth().Wrap().Selectable()
			if productionButton(c, "production-history-retry", "重试", p, false, blocked) {
				s.readProductionHistory(d)
			}
		case len(records) == 0:
			label(c, "暂无历史批次", 13, p.muted).Padding(12, 0)
		default:
			for index, b := range records {
				if index > 0 {
					ui.Divider(c)
				}
				s.renderProductionHistoryRow(c, p, d, b, blocked)
			}
		}
	})
}

func productionHistoryTitle(b production.Batch) string {
	return firstNonEmpty(b.ReleaseBranch, "初始化批次")
}

func productionHistoryCreated(b production.Batch) string {
	if b.InitialSHA == "" || b.ReleaseBranch == "" {
		return "未创建 release 分支"
	}
	created := productionReleaseCreatedTime(b)
	if created.IsZero() {
		return "创建时间未知"
	}
	return created.Local().Format("2006-01-02 15:04")
}

func productionHistoryBuild(b production.Batch) string {
	if b.Intent != nil && b.Intent.Kind == "tag" && !b.Intent.ResultConfirmed {
		return "Tag 待核对"
	}
	if len(b.Tags) == 0 {
		return "未构建"
	}
	return productionTagBuildLabel(b.Tags[len(b.Tags)-1].Status.Build)
}

func (s *app) renderProductionHistoryRow(c *ui.Context, p palette, d *productionHistoryDialog, b production.Batch, blocked bool) {
	name, timestamp := productionHistoryTitle(b), productionHistoryCreated(b)
	build, description := "", timestamp
	if !d.readOnly {
		build = productionHistoryBuild(b)
		description += "，" + build
	}
	ui.Column(c.Key("production-history-row-" + b.ID)).FillWidth().MinWidth(0).Gap(4).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
			row := ui.ButtonBase(c.Key("production-history-view-"+b.ID)).Row().Grow(1).MinWidth(0).MinHeight(60).
				Padding(8, 10).Gap(8).Radius(8).AlignItems(ui.Center).FocusRing(true).Label(name).
				Description(description).Disabled(blocked)
			if row.Hovered() {
				row.Background(p.soft)
			}
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					label(c, name, 13, p.text).FontWeight(600).SingleLine().Tooltip(name)
					label(c, timestamp, 11, p.muted).SingleLine().Tooltip("创建时间")
				})
				if build != "" {
					compactState(c, build, p)
				}
			})
			if row.Clicked() {
				s.viewProductionHistory(d, b)
			}
			if !d.readOnly {
				miniButton(c, p, "production-history-more-"+b.ID, "more", "更多批次操作 · "+name, false).Shrink(0).Disabled(blocked).Menu(func(menu *ui.Menu) {
					deleteLabel := "删除"
					if d.RowErrors[b.ID] != "" {
						deleteLabel = "重试删除"
					}
					if menu.Item(deleteLabel).IconSVG(icons["delete"]).Danger().Disabled(blocked).Chosen() {
						s.confirmProductionDeleteRecord(d.View, b, d)
					}
				})
			}
		})
		if message := d.RowErrors[b.ID]; message != "" {
			label(c, "删除失败："+message, 12, p.danger).FillWidth().Wrap().Selectable()
		}
	})
}

func (s *app) renderProductionHistoryDetail(c *ui.Context, p palette, d *productionHistoryDialog, b production.Batch, blocked bool) {
	ui.Scroll(c.Key("production-history-detail-" + b.ID)).Grow(1).FillWidth().MinWidth(0).MinHeight(0).
		Gap(14).Label("历史批次详情").Children(func() {
		summary := productionHistoryCreated(b)
		if !productionReleaseCreatedTime(b).IsZero() {
			summary = "创建于 " + summary
		}
		status := "已结束"
		if !b.Ended {
			status = productionPhase(b.Phase)
		}
		label(c, status+" · "+summary, 12, p.muted).Wrap()
		if d.DetailError != "" {
			label(c, d.DetailError, 12, p.danger).Wrap().Selectable()
		} else if d.DetailMessage != "" {
			label(c, d.DetailMessage, 12, p.muted).Wrap()
		}
		s.renderProductionPendingHistory(c, p, d, b, blocked)
		productionCard(c, p, "production-ended-merges", "已合并分支", productionCardStatus{}, func() {
			s.renderProductionMergedBranches(c, p, b)
		}, nil)
		productionCard(c, p, "production-ended-tags", "生产 Tag 记录", productionCardStatus{}, func() {
			if len(b.Tags) == 0 {
				label(c, "暂无生产 Tag", 12, p.muted)
				return
			}
			s.renderProductionHistoryTagRecords(c, p, d, b)
		}, func() {
			if !d.readOnly && len(b.Tags) > 0 && productionButton(c, "production-ended-refresh-build", "刷新构建状态", p, false, blocked) {
				s.requestProductionHistoryDetail(d, b, "production.refresh-build")
			}
		})
		if len(b.RetainedWorktrees) > 0 {
			productionCard(c, p, "production-ended-worktrees", "保留的工作区", productionCardStatus{}, func() {
				for _, path := range b.RetainedWorktrees {
					label(c, path, 12, p.text).Wrap().Selectable()
				}
			}, nil)
		}
	})
}
