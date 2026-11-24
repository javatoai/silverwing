package desktop

import (
	"context"
	"fmt"
	"slices"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type genbuReadRequest struct {
	cancel context.CancelFunc
}

func cancelGenbuRead(slot **genbuReadRequest) {
	if request := *slot; request != nil {
		*slot = nil
		request.cancel()
	}
}

func (s *app) runGenbuRead(slot **genbuReadRequest, current func() bool, action application.Action, publish func(application.ActionResult, error)) {
	cancelGenbuRead(slot)
	if !current() || s.taskServicesUnit.stopped {
		return
	}
	win, backend := s.win, s.backend
	if win == nil || win.IsDestroyed() || backend == nil {
		publish(application.ActionResult{}, fmt.Errorf("Genbu 查询窗口或后端不可用"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &genbuReadRequest{cancel: cancel}
	*slot = request
	wg := &s.taskServicesUnit.wg
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		result, err := backend.Dispatch(ctx, action)
		if ctx.Err() != nil || win.IsDestroyed() {
			return
		}
		done := make(chan struct{})
		win.Update(func() {
			defer close(done)
			if ctx.Err() != nil || *slot != request {
				return
			}
			*slot = nil
			if current() {
				publish(result, err)
			}
		})
		select {
		case <-done:
		case <-ctx.Done():
		}
	}()
}

func genbuReadData[T any](result application.ActionResult, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	value, ok := result.Data.(T)
	if !ok {
		return zero, fmt.Errorf("无法识别 Genbu 查询结果")
	}
	return value, nil
}

type genbuServiceReadScope struct {
	view  *taskServicesView
	row   *taskServiceRow
	epoch uint64
}

func (s *app) genbuServiceReadCurrent(scope genbuServiceReadScope) bool {
	v, row := scope.view, scope.row
	return row != nil && row.genbuName != "" && s.taskServicesCurrent(v) &&
		v.epoch == scope.epoch && row.environment == v.environment && slices.Contains(v.rows, row)
}

func productionTagRecordCurrent(current *production.Batch, b production.Batch, tag production.Tag) bool {
	if current == nil || current.ID != b.ID || current.Target != b.Target {
		return false
	}
	return slices.ContainsFunc(current.Tags, func(item production.Tag) bool {
		return item.Name == tag.Name && item.Commit == tag.Commit && item.Object == tag.Object
	})
}

func (s *app) openProductionBuildLog(b production.Batch, tag production.Tag, currentBatch func() *production.Batch) {
	state := s.releaseUnit.state.production
	if !s.productionAvailable() || state == nil || b.ID == "" || tag.Name == "" || b.Target.GenbuService == "" {
		return
	}
	home, config := state.loadedHome, state.configScope
	current := func() bool {
		return s.releaseUnit.state.production == state && state.loadedHome == home && state.configScope == config &&
			!state.deletedBatches[b.ID] && productionTagRecordCurrent(currentBatch(), b, tag)
	}
	if !current() {
		return
	}
	s.openGenbuBuildLog(firstNonEmpty(b.Target.ServiceName, b.Target.ServiceID), b.Target.GenbuService, "PROD", tag.Name, current,
		application.Action{Kind: "production.build-log", Payload: map[string]any{"batchId": b.ID, "tag": tag.Name}})
}

func (s *app) closeGenbuReadDialogs() {
	if d := s.genbuBuildLog; d != nil {
		s.genbuBuildLog = nil
		d.open = false
		cancelGenbuRead(&d.logRequest)
		d.text, d.preview = "", nativeCodePreviewState{}
	}
	if d := s.genbuApprovals; d != nil {
		s.genbuApprovals = nil
		d.open = false
		cancelGenbuRead(&d.listRequest)
		cancelGenbuRead(&d.detailRequest)
		d.list, d.detail = production.GenbuApprovalPage{}, production.GenbuApprovalDetail{}
		d.comparison, d.comparisonFile = nil, ""
		d.metadataOpen = false
	}
}

func (s *app) renderGenbuReadDialogs(c *ui.Context, p palette) {
	if d := s.genbuBuildLog; d != nil {
		if !s.genbuBuildLogCurrent(d) {
			s.closeGenbuReadDialogs()
			return
		}
		s.renderGenbuBuildLogDialog(c, p, d)
	}
	if d := s.genbuApprovals; d != nil {
		if !s.genbuApprovalsCurrent(d) {
			s.closeGenbuReadDialogs()
			return
		}
		s.renderGenbuApprovalsDialog(c, p, d)
	}
}

func genbuReadDialog(c *ui.Context, p palette, key, title, scope string, open *bool, closeDialog func(), content func(ui.Element)) {
	width, height := c.Size()
	dialog := ui.DialogBase(c.Key(key), open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Width(max(1, width*.85)).Height(max(1, height*.88)).Padding(18).Gap(10).
			Background(p.surface).Border(1, p.line).Radius(16).Label(title).Description(scope)
		panel.Children(func() {
			ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, title, 17, p.text).FontWeight(600)
					label(c, scope, 12, p.muted).SingleLine().Tooltip(scope)
				})
				if iconButton(c, key+"-close", "close", "关闭", p).Clicked() {
					closeDialog()
				}
			})
			ui.Divider(c)
			if *open {
				content(panel)
			}
		})
	})
	if dialog.Dismissed() || !*open {
		closeDialog()
	}
}

func genbuReadPagination(c *ui.Context, p palette, page, size, total int, showTotal, disabled bool, move func(int)) {
	pages := 1
	if total > 0 && size > 0 {
		pages = (total-1)/size + 1
	}
	ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		pageLabel := fmt.Sprintf("第 %d / %d 页", page, pages)
		if showTotal {
			pageLabel += fmt.Sprintf(" · 共 %d 条", total)
		}
		label(c, pageLabel, 11, p.muted).Grow(1).NoWrap()
		if ui.Button(c.Key("previous"), "上一页").Disabled(disabled || page <= 1).Clicked() {
			move(page - 1)
		}
		if ui.Button(c.Key("next"), "下一页").Disabled(disabled || page >= pages).Clicked() {
			move(page + 1)
		}
	})
}
