package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionToastTester(s *app, shown *[]ui.Toast) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		setTheme(c, false)
		s.renderOperationNotifications(c)
		ui.ToastViewportBase(c, func(_ ui.Element, items []ui.Toast) {
			*shown = append([]ui.Toast(nil), items...)
		})
	}, 640, 400)
}

func TestProductionProgressToastFollowsRequestLifetime(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			s, v, _, base, window := productionRequestFixture(t)
			s.tasks = []task{{id: "current-task", title: "kept"}}
			s.notes[0] = "unsaved draft"
			backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 1)}
			s.backend = backend
			var shown []ui.Toast
			tester := productionToastTester(s, &shown)
			if len(shown) != 0 {
				t.Fatal("idle production view displayed progress")
			}
			s.productionInspect("")
			ctx := <-backend.contexts
			productionTestCall(t, base)
			tester.Frame()
			if len(shown) != 1 || shown[0].Timeout != -1 || shown[0].Action != "取消" || shown[0].OnAction == nil {
				t.Fatalf("missing persistent cancellable progress: %+v", shown)
			}
			toastID, generation := shown[0].ID, v.Generation
			const progress = "读取线上版本及远端引用"
			production.EmitProgress(ctx, production.Progress{Selection: v.Selection, BatchID: v.BatchID, Step: 1, State: "running", Message: progress})
			productionTestUpdate(t, window)
			tester.Frame()
			if len(shown) != 1 || shown[0].ID != toastID || shown[0].Title != progress {
				t.Fatalf("progress stage duplicated or failed to replace the toast: %+v", shown)
			}
			s.page = "tasks"
			tester.Frame()
			if len(shown) != 1 || !strings.Contains(shown[0].Title, "示例服务："+progress) || v.Generation != generation || len(base.calls) != 0 {
				t.Fatal("navigation changed the request or lost its service identity")
			}
			if outcome == "cancel" {
				shown[0].OnAction()
				if ctx.Err() == nil {
					t.Fatal("toast cancel did not cancel its backend context")
				}
			} else if outcome == "failure" {
				base.replies <- productionTestAnswer{err: errors.New("读取远端失败")}
			} else {
				base.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: v.Preview}}}
			}
			productionTestUpdate(t, window)
			tester.Frame()
			if len(shown) != 0 || v.Busy || v.request != nil {
				t.Fatal("finished request left stale progress or a live cancel target")
			}
			if s.currentTask().id != "current-task" || s.notes[0] != "unsaved draft" || s.page != "tasks" {
				t.Fatal("progress/cancellation changed the task, draft or page")
			}
			if outcome == "failure" && !strings.Contains(s.productionSection(v, nil, 1).Message, "读取远端失败") {
				t.Fatal("moving progress hid the persistent failure reason")
			}
		})
	}
}

func TestProductionOldToastCannotCancelANewerRequest(t *testing.T) {
	s, v, _, base, window := productionRequestFixture(t)
	backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 2)}
	s.backend = backend
	var shown []ui.Toast
	tester := productionToastTester(s, &shown)
	s.productionInspect("")
	<-backend.contexts
	productionTestCall(t, base)
	tester.Frame()
	oldCancel, toastID := shown[0].OnAction, shown[0].ID
	base.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: v.Preview}}}
	productionTestUpdate(t, window)
	s.productionInspect("")
	ctx := <-backend.contexts
	productionTestCall(t, base)
	tester.Frame()
	if len(shown) != 1 || shown[0].ID != toastID {
		t.Fatal("repeated operation duplicated its progress notification")
	}
	oldCancel()
	if ctx.Err() != nil {
		t.Fatal("late toast action cancelled a newer request")
	}
	shown[0].OnAction()
	if ctx.Err() == nil {
		t.Fatal("current toast could not cancel the new request")
	}
	productionTestUpdate(t, window)
}

func TestProductionProgressToastRejectsStaleContext(t *testing.T) {
	for _, change := range []string{"home", "config", "batch", "branch", "manual", "selection", "generation", "view", "closed"} {
		t.Run(change, func(t *testing.T) {
			s, v, _, base, _ := productionRequestFixture(t)
			backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 1)}
			s.backend = backend
			s.productionInspect("")
			ctx := <-backend.contexts
			productionTestCall(t, base)
			var shown []ui.Toast
			tester := productionToastTester(s, &shown)
			cancel := shown[0].OnAction
			switch change {
			case "home":
				s.snapshot.Home = "another-home"
			case "config":
				s.configJSON = `{"tagEnabled":false}`
			case "batch":
				v.BatchID = "another-batch"
			case "branch":
				v.Branch = "feature/another"
			case "manual":
				v.ManualTag = "9.9.9"
			case "selection":
				v.Selection.ServiceID = "another-service"
			case "generation":
				v.Generation++
			case "view":
				s.production.views[s.production.selected] = &productionView{Selection: v.Selection}
			case "closed":
				s.production.mu.Lock()
				s.production.closed = true
				s.production.mu.Unlock()
			}
			tester.Frame()
			if len(shown) != 0 {
				t.Fatal("out-of-context request left progress visible")
			}
			cancel()
			if ctx.Err() != nil {
				t.Fatal("out-of-context toast action was still active")
			}
		})
	}
}

func TestProductionProgressToastKeepsConcurrentServicesSeparate(t *testing.T) {
	s, first, _, base, window := productionRequestFixture(t)
	backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 2)}
	s.backend = backend
	s.productionInspect("")
	firstContext := <-backend.contexts
	productionTestCall(t, base)
	second := &productionView{Selection: production.Selection{GroupID: "other-group", ServiceID: "other-service"}}
	s.production.views[productionKey(second.Selection)] = second
	s.productionRequestFor(second, application.Action{Kind: "production.inspect", ServiceID: "other-service"}, "")
	secondContext := <-backend.contexts
	productionTestCall(t, base)
	var shown []ui.Toast
	tester := productionToastTester(s, &shown)
	if len(shown) != 2 || shown[0].ID == shown[1].ID {
		t.Fatalf("concurrent services shared a progress slot: %+v", shown)
	}
	for _, toast := range shown {
		if strings.Contains(toast.Title, "示例服务：") {
			toast.OnAction()
		}
	}
	if firstContext.Err() == nil || secondContext.Err() != nil {
		t.Fatal("cancellation crossed service request boundaries")
	}
	productionTestUpdate(t, window)
	tester.Frame()
	if len(shown) != 1 || !strings.Contains(shown[0].Title, "other-service：") || first.Busy || !second.Busy {
		t.Fatal("finishing one service removed or reassigned another service's progress")
	}
	shown[0].OnAction()
	productionTestUpdate(t, window)
}

func TestProductionProgressToastLeavesPageGeometryAndFocusUnchanged(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, _, base, window := productionRequestFixture(t)
				s.production.batches, v.BatchID, v.Version = nil, "", "release/2.3.99"
				backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 1)}
				s.backend = backend
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.renderProductionPage(c, p) })
					s.renderOperationNotifications(c)
				}, width, 900)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
				if err := tester.Click("release 分支名"); err != nil {
					t.Fatal(err)
				}
				anchors := []string{"重新检查", "无差异", "2. 合并生产 Tag 到主分支", "3. 创建 release 分支", "release 分支名", "创建 release"}
				rects := make(map[string]ui.Rect)
				for _, anchor := range anchors {
					var ok bool
					rects[anchor], ok = tester.Find(anchor)
					if !ok {
						t.Fatalf("missing page anchor %s", anchor)
					}
				}
				captureProductionSummary(t, tester, fmt.Sprintf("progress-%d-dark-%t-before", width, dark))
				s.productionInspect("")
				ctx := <-backend.contexts
				productionTestCall(t, base)
				const title = "读取线上版本及远端引用"
				production.EmitProgress(ctx, production.Progress{Selection: v.Selection, Step: 1, State: "running", Message: title})
				productionTestUpdate(t, window)
				tester.Frame()
				// The native toast has a 200 ms entrance fade. Capture its settled
				// appearance rather than the first nearly transparent frame.
				time.Sleep(220 * time.Millisecond)
				tester.Frame()
				for _, anchor := range anchors {
					if got, _ := tester.Find(anchor); got != rects[anchor] {
						t.Errorf("progress moved %s: before=%+v during=%+v", anchor, rects[anchor], got)
					}
				}
				for _, text := range []string{title, "取消"} {
					r, ok := tester.Find(text)
					if !ok || r.Y < 700 || r.Y+r.H > 900 || r.X < 0 || r.X+r.W > float32(width) {
						t.Fatalf("native progress was not visible at the window bottom: %s %+v", text, r)
					}
				}
				if !tester.Focused("release 分支名") || v.Version != "release/2.3.99" {
					t.Fatal("native progress took input focus or replaced the edited release name")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("progress-%d-dark-%t-during", width, dark))
				base.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: v.Preview}}}
				productionTestUpdate(t, window)
				tester.Frame()
				for _, anchor := range anchors {
					if got, _ := tester.Find(anchor); got != rects[anchor] {
						t.Errorf("same result moved %s: before=%+v after=%+v", anchor, rects[anchor], got)
					}
				}
				if tester.HasText(title) || tester.HasText("取消") {
					t.Fatal("completed request left a bottom progress control")
				}
				captureProductionSummary(t, tester, fmt.Sprintf("progress-%d-dark-%t-after", width, dark))
			})
		}
	}
}

func TestProductionAutomaticBuildProbeIsQuietButManualRefreshShowsProgress(t *testing.T) {
	s, v, b, base, window := productionRequestFixture(t)
	var shown []ui.Toast
	tester := productionToastTester(s, &shown)
	for _, follow := range []string{"background-probe", ""} {
		s.productionRequestFor(v, application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, follow)
		productionTestCall(t, base)
		tester.Frame()
		if (len(shown) == 1) != (follow == "") {
			t.Fatalf("automatic/manual build probe progress mismatch: follow=%q shown=%+v", follow, shown)
		}
		base.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
		productionTestUpdate(t, window)
	}
}

func TestProductionToastCancelIsKeyboardReachable(t *testing.T) {
	s, _, _, base, window := productionRequestFixture(t)
	backend := &productionProgressBackend{productionTestBackend: base, contexts: make(chan context.Context, 1)}
	s.backend = backend
	s.notes[0] = "unsaved draft"
	s.productionInspect("")
	ctx := <-backend.contexts
	productionTestCall(t, base)
	tester := ui.NewTester(func(c *ui.Context) {
		setTheme(c, false)
		ui.Button(c, "当前页面操作")
		s.renderOperationNotifications(c)
	}, 640, 400)
	for i := 0; i < 8 && !tester.Focused("取消"); i++ {
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused("取消") {
		t.Fatal("native progress cancellation is not keyboard reachable")
	}
	tester.Key(0, ui.KeyEnter)
	if ctx.Err() == nil || s.notes[0] != "unsaved draft" {
		t.Fatal("keyboard cancel did not cancel its request safely")
	}
	productionTestUpdate(t, window)
	tester.Frame()
	if tester.HasText("取消") {
		t.Fatal("keyboard-cancelled request retained its progress action")
	}
}
