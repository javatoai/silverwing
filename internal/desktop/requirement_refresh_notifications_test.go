package desktop

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestRequirementRefreshNotificationShowsImmediatelyAndEndsWithJob(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			s, backend, window := refreshTestFixture(t)
			backend.commentsError = outcome == "failure"
			var shown []ui.Toast
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() {
					for _, item := range s.requirementUnit.state.requirements {
						s.requirementListItem(c, setTheme(c, false), item, false)
					}
				})
				s.renderOperationNotifications(c)
				ui.ToastViewportBase(c, func(_ ui.Element, toasts []ui.Toast) { shown = append([]ui.Toast(nil), toasts...) })
			}, 340, 400)
			if len(shown) != 0 {
				t.Fatal("idle page shows a progress notification")
			}
			if err := tester.RightClick("B"); err != nil {
				t.Fatal(err)
			}
			if err := tester.ChooseMenuItem("刷新"); err != nil {
				t.Fatal(err)
			}
			url := s.requirementUnit.state.requirements[1].URL
			job := s.requirementUnit.state.requirementRefreshJobs.current[requirementRefreshKey(url)]
			if len(shown) != 1 || shown[0].Title != "刷新中" || shown[0].Timeout != -1 {
				t.Fatalf("right-click refresh did not show persistent progress immediately: %+v", shown)
			}
			s.startRequirementRefresh(url)
			s.page = "tasks"
			tester.Frame()
			if len(shown) != 1 || shown[0].Title != "刷新中" || s.requirementUnit.state.selectedRequirement != 0 || s.materialsUnit.state.documentContent != "unsaved draft" {
				t.Fatal("coalescing/page switch duplicated progress or changed navigation/draft")
			}
			if outcome == "cancelled" {
				s.invalidateRequirementRefreshJobs()
			} else {
				close(backend.gate)
				finishRefreshTest(t, window, job)
			}
			tester.Frame()
			for _, toast := range shown {
				if toast.Title == "刷新中" {
					t.Fatal("finished or cancelled job left a progress notification")
				}
			}
			if outcome == "success" && (len(shown) != 1 || shown[0].Type != "success" || !strings.Contains(shown[0].Title, "刷新成功")) {
				t.Fatalf("missing completion feedback: %+v", shown)
			}
			if outcome == "failure" && (len(shown) != 1 || shown[0].Type != "error") {
				t.Fatalf("missing failure feedback: %+v", shown)
			}
		})
	}
}

func TestRequirementRefreshNotificationStaysUntilAllTargetsFinish(t *testing.T) {
	s, backend, window := refreshTestFixture(t)
	s.startRequirementRefresh(s.requirementUnit.state.requirements[0].URL)
	s.startRequirementRefresh(s.requirementUnit.state.requirements[1].URL)
	var shown []ui.Toast
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderRequirementRefreshProgressNotification(c)
		ui.ToastViewportBase(c, func(_ ui.Element, toasts []ui.Toast) { shown = append([]ui.Toast(nil), toasts...) })
	}, 340, 300)
	if len(shown) != 1 || shown[0].Title != "刷新中" {
		t.Fatalf("multiple targets need one progress notification: %+v", shown)
	}
	close(backend.gate)
	for completed := 0; completed < 2; completed++ {
		var update func()
		select {
		case update = <-window.updates:
		case <-time.After(time.Second):
			t.Fatal("refresh did not queue its completion")
		}
		update()
		tester.Frame()
		want := 1 - completed
		if len(shown) != want {
			t.Fatalf("progress after %d targets: %v", completed+1, shown)
		}
	}
}

func TestRequirementRefreshNotificationVisibleInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			s, _, _ := refreshTestFixture(t)
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, dark)
				ui.Column(c).Fill().Padding(12).Gap(8).Background(p.bg).Children(func() {
					for _, item := range s.requirementUnit.state.requirements {
						s.requirementListItem(c, p, item, false)
					}
				})
				s.renderOperationNotifications(c)
			}, 600, 400)
			if err := tester.RightClick("B"); err != nil {
				t.Fatal(err)
			}
			if err := tester.ChooseMenuItem("刷新"); err != nil {
				t.Fatal(err)
			}
			// Capture the settled toast rather than its first transparent
			// entrance frame. The backend remains blocked throughout.
			time.Sleep(250 * time.Millisecond)
			tester.Frame()
			notice, found := tester.Find("刷新中")
			if !found || notice.X < 0 || notice.Y < 0 || notice.X+notice.W > 600 || notice.Y+notice.H > 400 {
				t.Fatalf("refresh feedback is missing or clipped: %+v", notice)
			}
			saveRequirementRefreshImage(t, tester, fmt.Sprintf("refresh-notice-dark-%v.png", dark))
		})
	}
}
