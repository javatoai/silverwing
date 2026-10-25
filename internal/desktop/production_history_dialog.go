package desktop

import (
	"fmt"
	"sort"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionHistoryDialog struct {
	Open, Loading bool
	View          *productionView
	State         *productionUIState
	Selection     production.Selection
	Home, Scope   string
	ServiceName   string
	Error         string
	RowErrors     map[string]string
}

func (s *app) productionHistoryCurrent(d *productionHistoryDialog) bool {
	return d != nil && d.Open && s.production == d.State && s.production.historyDialog == d &&
		s.page == "production" && s.productionView() == d.View && d.View.Selection == d.Selection &&
		s.snapshot.Home == d.Home && s.production.loadedHome == d.Home && s.currentProductionScope() == d.Scope
}

func (s *app) openProductionHistory(v *productionView) {
	if v == nil || v.Busy || s.productionView() != v || s.confirmationDialog != nil {
		return
	}
	name := v.Selection.ServiceID
	for _, choice := range s.production.choices {
		if choice.GroupID == v.Selection.GroupID && choice.ID == v.Selection.ServiceID {
			name = choice.Name
			break
		}
	}
	d := &productionHistoryDialog{Open: true, View: v, State: s.production, Selection: v.Selection,
		Home: s.snapshot.Home, Scope: s.currentProductionScope(), ServiceName: name, RowErrors: map[string]string{}}
	s.production.historyDialog = d
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
		if b.Ended && b.Target.Selection == d.Selection && !d.State.deletedBatches[b.ID] {
			records = append(records, b)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, records[i].CreatedAt)
		b, _ := time.Parse(time.RFC3339Nano, records[j].CreatedAt)
		if a.Equal(b) {
			return records[i].ID > records[j].ID
		}
		return a.After(b)
	})
	return records
}

func (s *app) productionHistoryRequestCurrent(d *productionHistoryDialog, action application.Action) bool {
	if !s.productionHistoryCurrent(d) || s.confirmationDialog != nil {
		return false
	}
	if action.Kind == "production.list" {
		return true
	}
	if action.Kind != "production.delete" || action.Payload["confirmed"] != true {
		return false
	}
	for _, b := range s.productionHistoryRecords(d) {
		if b.ID == action.Payload["batchId"] {
			token, err := production.BatchRecordToken(b)
			return err == nil && token == action.Payload["recordToken"]
		}
	}
	return false
}

func (s *app) finishProductionHistoryRequest(d *productionHistoryDialog, action application.Action, worker *productionWorker, generation uint64, report production.Report, result application.ActionResult, err error) {
	v := d.View
	if v.request == worker {
		v.Busy, v.request = false, nil
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
	s.lastMessage = firstNonEmpty(result.Message, "本地批次记录已删除")
}

func (s *app) renderProductionHistoryDialog(c *ui.Context, p palette) {
	if s.production == nil || s.production.historyDialog == nil {
		return
	}
	d := s.production.historyDialog
	if !s.productionHistoryCurrent(d) {
		d.Open = false
		s.production.historyDialog = nil
		s.pruneProductionHistoricalChoices(s.production)
		return
	}
	records := s.productionHistoryRecords(d)
	blocked := d.View.Busy || s.confirmationDialog != nil
	dialog := ui.DialogBase(c.Key("production-history-dialog"), &d.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		panel.Width(max(float32(1), min(float32(720), width-32))).MaxHeight(height-32).
			Padding(20).Gap(16).Background(p.surface).Border(1, p.line).Radius(16).Label("历史批次弹窗")
		panel.Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Shrink(0).Children(func() {
				label(c, "历史批次", 18, p.text).FontWeight(600)
				label(c, fmt.Sprintf("%s · %d 个", d.ServiceName, len(records)), 12, p.muted).Wrap()
			})
			ui.Scroll(c.Key("production-history-list")).FillWidth().MinWidth(0).MinHeight(0).MaxHeight(max(float32(80), height-220)).Gap(0).Label("历史批次列表").Children(func() {
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
							ui.Box(c).FillWidth().Height(1).Background(p.line)
						}
						s.renderProductionHistoryRow(c, p, d, b, blocked, width < 760)
					}
				}
			})
			ui.Row(c).FillWidth().Justify(ui.End).Shrink(0).Children(func() {
				b := ui.ButtonBase(c.Key("production-history-close")).Height(34).Padding(0, 14).Center().Radius(20).Border(1, p.line).Disabled(s.confirmationDialog != nil).Label("关闭").AutoFocus()
				if b.Hovered() {
					b.Background(p.soft)
				}
				if b.Focused() {
					b.Border(2, p.blue)
				}
				b.Children(func() { label(c, "关闭", 13, p.text).NoWrap() })
				if b.Clicked() {
					d.Open = false
				}
			})
		})
	})
	if dialog.Dismissed() {
		d.Open = false
	}
}

func (s *app) renderProductionHistoryRow(c *ui.Context, p palette, d *productionHistoryDialog, b production.Batch, blocked, narrow bool) {
	ui.Column(c.Key("production-history-row-"+b.ID)).FillWidth().MinWidth(0).Padding(14, 0).Gap(8).Children(func() {
		row := ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center)
		if narrow {
			row.Column().AlignItems(ui.Stretch)
		}
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				name := firstNonEmpty(b.ReleaseBranch, "初始化批次")
				label(c, name, 13, p.text).FillWidth().Wrap().Selectable().Tooltip(name)
				timestamp := "创建时间未知"
				if created, err := time.Parse(time.RFC3339Nano, b.CreatedAt); err == nil {
					timestamp = created.Local().Format("2006-01-02 15:04")
				}
				label(c, timestamp, 11, p.muted).NoWrap()
			})
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				label(c, "已结束", 12, p.muted).Width(48).NoWrap()
				current := d.View.BatchID == b.ID
				viewLabel := "查看"
				if current {
					viewLabel = "当前查看"
				}
				ui.Box(c).Width(90).AlignItems(ui.End).Children(func() {
					if productionButton(c, "production-history-view-"+b.ID, viewLabel, p, false, blocked || current) {
						s.viewProductionHistory(d, b)
					}
				})
				deleteLabel := "删除"
				if d.RowErrors[b.ID] != "" {
					deleteLabel = "重试删除"
				}
				ui.Box(c).Width(82).AlignItems(ui.End).Children(func() {
					button := ui.ButtonBase(c.Key("production-history-delete-"+b.ID)).Height(34).Padding(0, 14).Radius(20).Center().Border(1, p.line).Label(deleteLabel).Tooltip("仅删除本地批次记录").Disabled(blocked)
					color := p.danger
					if blocked {
						color = p.muted
					} else if button.Focused() {
						button.Border(2, p.blue)
					} else if button.Hovered() {
						button.Background(p.soft)
					}
					button.Children(func() { label(c, deleteLabel, 13, color).NoWrap() })
					if button.Clicked() {
						s.confirmProductionDeleteRecord(d.View, b, d)
					}
				})
			})
		})
		if message := d.RowErrors[b.ID]; message != "" {
			label(c, "删除失败："+message, 12, p.danger).FillWidth().Wrap().Selectable()
		}
	})
}
