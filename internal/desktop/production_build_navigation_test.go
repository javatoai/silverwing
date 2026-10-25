package desktop

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionBuildNavigationHarness struct {
	s          *app
	v, other   *productionView
	batch      production.Batch
	otherBatch production.Batch
	preview    production.Preview
	backend    *productionTestBackend
	window     *refreshTestWindow
	tester     *ui.Tester
}

func newProductionBuildNavigationHarness(t *testing.T) *productionBuildNavigationHarness {
	t.Helper()
	s, v, b, backend, window := productionRequestFixture(t)
	v.Preview.MainInRelease = true
	s.notes[0] = "unsaved navigation draft"
	var config domain.AppConfig
	if err := json.Unmarshal([]byte(s.configJSON), &config); err != nil {
		t.Fatal(err)
	}
	service := config.Groups[0].Services[0]
	service.ID, service.RepositoryID, service.DisplayName = "other-service", "other-repo", "另一个服务"
	config.Groups[0].Services = append(config.Groups[0].Services, service)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.configJSON = string(encoded)
	s.production.configScope = productionConfigScope(s.configJSON)
	choice := application.Service{ID: service.ID, GroupID: v.Selection.GroupID, Name: service.DisplayName, RepositoryID: service.RepositoryID, MasterBranch: "origin/master"}
	s.snapshot.Services = append(s.snapshot.Services, choice)
	s.snapshot.Repositories = append(s.snapshot.Repositories, application.Repository{ID: service.RepositoryID, Name: "other-repo", RootPath: "C:/synthetic/other-repo"})
	otherBatch := b
	otherBatch.ID, otherBatch.ReleaseSHA = strings.Repeat("e", 32), strings.Repeat("f", 40)
	otherBatch.Target.ServiceID, otherBatch.Target.ServiceName = service.ID, service.DisplayName
	otherBatch.Target.RepositoryID, otherBatch.Target.Repository = service.RepositoryID, "C:/synthetic/other-repo"
	otherPreview := *v.Preview
	otherPreview.Target, otherPreview.ReleaseSHA = otherBatch.Target, otherBatch.ReleaseSHA
	other := &productionView{Selection: otherBatch.Target.Selection, BatchID: otherBatch.ID, Preview: &otherPreview}
	s.production.views[productionKey(other.Selection)] = other
	s.production.choices = append(s.production.choices, productionChoice(choice, s.snapshot))
	s.production.batches = append(s.production.batches, otherBatch)
	h := &productionBuildNavigationHarness{s: s, v: v, other: other, batch: b, otherBatch: otherBatch, preview: *v.Preview, backend: backend, window: window}
	// Both navigation controls and the build button are the real production
	// renderers. Only the unrelated task page body is omitted from this harness.
	h.tester = ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			s.sidebar(c, p, true)
			if s.page == "production" {
				s.renderProductionPage(c, p)
			} else {
				ui.Box(c).Grow(1)
			}
		})
		s.renderOperationNotifications(c)
	}, 1440, 900)
	h.tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	return h
}

func (h *productionBuildNavigationHarness) click(t *testing.T, text string) {
	t.Helper()
	h.tester.Frame()
	if text == "构建 Tag" {
		productionRevealControl(t, h.tester, text)
	}
	if err := h.tester.Click(text); err != nil {
		t.Fatal(err)
	}
}

func (h *productionBuildNavigationHarness) navigate(t *testing.T, route string, roundTrip bool) {
	t.Helper()
	if route == "service" {
		h.click(t, "另一个服务")
		if h.s.page != "production" || h.s.productionView() != h.other {
			t.Fatal("service button did not leave the original target")
		}
		if roundTrip {
			h.click(t, "示例服务")
		}
	} else {
		h.click(t, "任务")
		if h.s.page != "tasks" {
			t.Fatal("sidebar button did not leave the production page")
		}
		if roundTrip {
			h.click(t, "生产发布")
		}
	}
	if roundTrip && (h.s.page != "production" || h.s.productionView() != h.v) {
		t.Fatal("navigation did not return to the original target")
	}
}

func (h *productionBuildNavigationHarness) startBuild(t *testing.T) {
	t.Helper()
	h.click(t, "构建 Tag")
	call := productionTestCall(t, h.backend)
	if call.Kind != "production.prepare-tag" || call.ServiceID != h.v.Selection.ServiceID || call.Payload["batchId"] != h.batch.ID || call.Payload["token"] != nil || call.Payload["confirmed"] != nil {
		t.Fatalf("a build click must start a fresh read of the original target: %+v", call)
	}
}

func (h *productionBuildNavigationHarness) setPlan(token string) {
	h.batch.TagPlan = &production.TagPlan{Token: token, Tag: "2.3.44", Commit: h.batch.ReleaseSHA, MainSHA: h.preview.MainSHA, Preview: h.preview}
}

func (h *productionBuildNavigationHarness) dispatchPublish(t *testing.T, token string) {
	t.Helper()
	h.setPlan(token)
	h.backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &h.batch, Preview: &h.preview}}}
	productionTestUpdate(t, h.window)
	call := productionTestCall(t, h.backend)
	if call.Kind != "production.publish" || call.Payload["batchId"] != h.batch.ID || call.Payload["token"] != token || call.Payload["confirmed"] != true || h.s.confirmationDialog != nil {
		t.Fatalf("fresh preparation did not publish exactly its own token: %+v", call)
	}
}

func (h *productionBuildNavigationHarness) finishPublish(t *testing.T) {
	t.Helper()
	plan := *h.batch.TagPlan
	h.batch.Tags = append(h.batch.Tags, production.Tag{Name: plan.Tag, Commit: plan.Commit, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}})
	h.batch.TagPlan = nil
	h.batch.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	h.backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &h.batch, Preview: &h.preview}}}
	productionTestUpdate(t, h.window)
	h.tester.Frame()
	saved, other := h.s.productionBatch(h.v), h.s.productionBatch(h.other)
	if h.v.Busy || h.v.request != nil || len(h.backend.calls) != 0 || saved == nil || saved.ID != h.batch.ID || saved.Target != h.batch.Target || len(saved.Tags) != 1 || saved.Tags[0].Name != plan.Tag || saved.Tags[0].Commit != plan.Commit {
		t.Fatal("publication was lost, replayed, or saved to the wrong batch")
	}
	if other == nil || other.ID != h.otherBatch.ID || other.Target != h.otherBatch.Target || other.ReleaseSHA != h.otherBatch.ReleaseSHA || len(other.Tags) != 0 || h.other.Busy || h.s.notes[0] != "unsaved navigation draft" {
		t.Fatal("publication changed another service or the task draft")
	}
}

func (h *productionBuildNavigationHarness) queuedResult(t *testing.T) func() {
	t.Helper()
	h.backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &h.batch, Preview: &h.preview}}}
	select {
	case apply := <-h.window.updates:
		return apply
	case <-time.After(time.Second):
		t.Fatal("missing queued build result")
		return nil
	}
}

func TestProductionBuildNavigationPermanentlyRevokesContinuation(t *testing.T) {
	for _, route := range []string{"service", "page"} {
		for _, navigation := range []string{"away", "round-trip", "queued-round-trip"} {
			t.Run("prepare"+"/"+route+"/"+navigation, func(t *testing.T) {
				h := newProductionBuildNavigationHarness(t)
				h.startBuild(t)
				h.setPlan("old-navigation-token")
				generation := h.v.Generation
				var apply func()
				if navigation == "queued-round-trip" {
					apply = h.queuedResult(t)
				}
				h.navigate(t, route, navigation != "away")
				if apply == nil {
					apply = h.queuedResult(t)
				}
				apply()
				if h.v.Busy || h.v.request != nil || h.v.Generation != generation || !h.v.PreparationInvalid || len(h.backend.calls) != 0 || h.s.confirmationDialog != nil {
					t.Fatal("navigation left a busy request or continued an old build without a new click")
				}
				if navigation != "away" {
					h.startBuild(t)
					h.dispatchPublish(t, "new-click-token")
					h.finishPublish(t)
				}
			})
		}
	}
}

func TestProductionBuildSameTargetAndDuplicateClicksKeepContinuation(t *testing.T) {
	for _, target := range []string{"no-navigation", "示例服务", "生产发布"} {
		t.Run(target, func(t *testing.T) {
			h := newProductionBuildNavigationHarness(t)
			h.startBuild(t)
			worker, generation := h.v.request, h.v.Generation
			h.click(t, "构建 Tag")
			if target != "no-navigation" {
				h.click(t, target)
			}
			if !h.v.Busy || h.v.request != worker || h.v.Generation != generation || len(h.backend.calls) != 0 {
				t.Fatal("duplicate or current-target click replaced the running build")
			}
			h.dispatchPublish(t, "same-target-token")
			h.finishPublish(t)
		})
	}
}

func TestProductionDispatchedPublishSurvivesNavigation(t *testing.T) {
	for _, route := range []string{"service", "page"} {
		for _, roundTrip := range []bool{false, true} {
			name := route + "/away"
			if roundTrip {
				name = route + "/round-trip"
			}
			t.Run(name, func(t *testing.T) {
				h := newProductionBuildNavigationHarness(t)
				h.startBuild(t)
				h.dispatchPublish(t, "already-dispatched-token")
				worker, generation := h.v.request, h.v.Generation
				h.navigate(t, route, roundTrip)
				if !h.v.Busy || h.v.request != worker || h.v.Generation != generation || worker.canceled.Load() || !h.s.productionProgressCurrent(h.s.production, h.v, worker) || !h.tester.HasText("取消") {
					t.Fatal("navigation canceled or detached an already-dispatched publication")
				}
				h.finishPublish(t)
			})
		}
	}
}

func TestProductionBuildCancelButtonStopsQueuedContinuation(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		h := newProductionBuildNavigationHarness(t)
		h.startBuild(t)
		h.setPlan("queued-canceled-token")
		worker := h.v.request
		apply := h.queuedResult(t)
		// Use the native toast action, after success was queued and before
		// applying it, rather than invoking a saved cancellation closure.
		h.click(t, "取消")
		if !worker.canceled.Load() {
			t.Fatal("native cancel button did not cancel the current request")
		}
		apply()
		if h.v.Busy || h.v.request != nil || h.v.Error == "" || len(h.backend.calls) != 0 || h.s.confirmationDialog != nil {
			t.Fatal("canceled queued success continued into another build request")
		}
	})
}
