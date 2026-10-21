package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func productionConfirmationFixture(t *testing.T, kind string) (*app, *productionView, production.Batch) {
	t.Helper()
	s, v, b := productionFixture(t)
	s.win = &mygo.Window{}
	s.tasks = []task{{id: "selected-task", path: "selected-path", createdAt: "selected-created"}}
	s.notes[0] = "unsaved draft"
	v.Preview.BranchHeads = map[string]string{v.Branch: strings.Repeat("d", 40)}
	v.Preview.ProductionStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuSuccess}
	b.TagPlan = &production.TagPlan{Tag: "2.3.44", Token: "confirmed-token", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
	if kind == "resume" || kind == "pending" {
		b.Intent = &production.Intent{Kind: "feature", SourceBranch: v.Branch, Merge: &production.Merge{SourceSHA: strings.Repeat("d", 40), BeforeSHA: b.ReleaseSHA, TargetBranch: b.ReleaseBranch, Worktree: "retained conflict workspace"}}
	}
	if kind == "reprepare" {
		b.Intent = &production.Intent{Kind: "tag", Tag: "2.3.44", TagCommit: b.ReleaseSHA}
	}
	s.production.batches[0] = b
	switch kind {
	case "create":
		s.confirmProductionCreate(v)
	case "main":
		s.confirmProductionMain(v)
	case "feature":
		s.confirmProductionFeature(v, *v.Preview, v.Branch)
	case "sync":
		s.confirmProductionSync(v, *v.Preview)
	case "tag":
		s.confirmProductionTag(v, b)
	case "pending":
		s.confirmProductionPending(v, b, *v.Preview)
	case "resume":
		s.confirmProductionResume(v, b)
	case "reprepare":
		s.confirmProductionReprepare(v, b)
	case "adopt":
		s.confirmProductionAdopt(v, b)
	case "end":
		s.confirmProductionEnd(v, b)
	default:
		t.Fatalf("unknown fixture %q", kind)
	}
	if s.confirmationDialog == nil {
		t.Fatal("missing production confirmation")
	}
	return s, v, b
}

func TestProductionAllConfirmationsUseSummaryAndKeepOriginalPayload(t *testing.T) {
	for kind, actionKind := range map[string]string{
		"create": "production.create", "main": "production.merge-main", "feature": "production.merge", "sync": "production.merge-release", "tag": "production.publish",
		"pending": "production.rebase-pending", "resume": "production.resume", "reprepare": "production.reprepare-tag", "adopt": "production.accept-remote", "end": "production.end",
	} {
		t.Run(kind, func(t *testing.T) {
			s, v, b := productionConfirmationFixture(t, kind)
			d := s.confirmationDialog
			if !d.FoldDetail || d.DetailOpen || d.DefaultIndex != d.CancelIndex || len(d.Notices) == 0 {
				t.Fatal("missing collapsed details, visible notice, or safe default")
			}
			for _, value := range []string{b.Target.ServiceName, b.Target.Repository, b.Target.Remote} {
				found := false
				for _, field := range d.Summary {
					found = found || field.Value == value
				}
				if !found {
					t.Fatalf("summary lost confirmed identity %s", value)
				}
			}
			if !strings.Contains(d.Detail, b.Target.RepositoryID) || !strings.Contains(d.Detail, b.Target.GroupID) {
				t.Fatal("details lost original repository identity")
			}
			tester := productionIterationTester(s, 960, false)
			for _, notice := range d.Notices {
				if !tester.HasText(notice) {
					t.Fatal("essential notice is folded away")
				}
			}
			captureProductionSummary(t, tester, "confirm-action-"+kind)
			before, err := json.Marshal(d.Summary)
			if err != nil {
				t.Fatal(err)
			}
			// A later read must not rewrite either the displayed confirmation or
			// its already captured full-SHA continuation.
			fresh := *v.Preview
			fresh.MainSHA, fresh.ProductionSHA, fresh.ReleaseSHA = strings.Repeat("e", 40), strings.Repeat("f", 40), strings.Repeat("e", 40)
			v.Preview = &fresh
			after, _ := json.Marshal(d.Summary)
			if string(before) != string(after) {
				t.Fatal("live preview rewrote confirmation facts")
			}
			backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 1)}
			window := &refreshTestWindow{updates: make(chan func(), 8)}
			s.backend, s.win, s.production.window = backend, nil, window
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.closeProductionJobs(ctx); err != nil {
					t.Error(err)
				}
			})
			s.finishConfirmationDialog(d, 0)
			select {
			case action := <-backend.calls:
				if action.Kind != actionKind || action.Payload["batchId"] != b.ID || action.Payload["confirmed"] != true {
					t.Fatalf("wrong confirmation continuation: %+v", action)
				}
				if p, ok := action.Payload["preview"].(production.Preview); ok && (p.MainSHA != strings.Repeat("a", 40) || p.Target != b.Target) {
					t.Fatalf("presentation changed original preview: %+v", p)
				}
				for _, key := range []string{"sourceSha", "releaseSha", "remoteSha"} {
					if sha, ok := action.Payload[key].(string); ok && (len(sha) != 40 || sha == fresh.MainSHA || sha == fresh.ProductionSHA) {
						t.Fatalf("%s received a short or late SHA %q", key, sha)
					}
				}
				if kind == "create" && action.Payload["version"] != "2.3.44" || kind == "tag" && action.Payload["token"] != "confirmed-token" {
					t.Fatal("version or prepared token changed")
				}
			case <-time.After(time.Second):
				t.Fatal("missing confirmed request")
			}
			backend.replies <- productionTestAnswer{err: errors.New("synthetic: stop before any external work")}
			select {
			case apply := <-window.updates:
				apply()
			case <-time.After(time.Second):
				t.Fatal("missing completion")
			}
			if s.tasks[0].id != "selected-task" || s.notes[0] != "unsaved draft" {
				t.Fatal("confirmation changed task or draft")
			}
		})
	}
}

func TestProductionContentRecheckOnlyReadsAndKeepsManualBranch(t *testing.T) {
	s, v, b := productionFixture(t)
	v.Version = "release/2.3.90"
	s.tasks = []task{{id: "selected-task"}}
	s.notes[0] = "unsaved draft"
	backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 1)}
	window := &refreshTestWindow{updates: make(chan func(), 8)}
	s.backend, s.production.window = backend, window
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeProductionJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	tester := productionIterationTester(s, 960, false)
	if err := tester.Click("重新检查"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-backend.calls:
		if action.Kind != "production.inspect" || action.Payload["confirmed"] != nil || action.Payload["batchId"] != b.ID {
			t.Fatalf("check dispatched write or wrong target: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("missing read request")
	}
	fresh := *v.Preview
	fresh.SuggestedVersion = "2.3.91"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: &fresh, Batches: []production.Batch{b}}}}
	select {
	case apply := <-window.updates:
		apply()
	case <-time.After(time.Second):
		t.Fatal("missing check completion")
	}
	if v.Version != "release/2.3.90" || s.tasks[0].id != "selected-task" || s.notes[0] != "unsaved draft" {
		t.Fatal("recheck changed manual branch, task or draft")
	}
	select {
	case action := <-backend.calls:
		t.Fatalf("plain recheck chained another operation: %+v", action)
	default:
	}
}

func TestProductionConfirmationRejectsChangedContextAndOldDialogs(t *testing.T) {
	for _, changed := range []string{"none", "page", "service", "batch", "manual", "home", "config", "unrelated-config", "selection", "replaced"} {
		t.Run(changed, func(t *testing.T) {
			s, v, _ := productionFixture(t)
			s.win = &mygo.Window{}
			called := 0
			s.confirmProduction(v, v.Preview.Target, productionConfirmationContent{Title: "synthetic", Detail: "full detail"}, func() { called++ })
			d := s.confirmationDialog
			switch changed {
			case "page":
				s.page = "tasks"
			case "service":
				s.production.selected = "other"
			case "batch":
				v.BatchID = "another"
			case "selection":
				v.Selection.ServiceID = "another"
			case "manual":
				v.ManualTag = "another"
			case "home":
				s.snapshot.Home = "another"
			case "config", "unrelated-config":
				var c domain.AppConfig
				if err := json.Unmarshal([]byte(s.configJSON), &c); err != nil {
					t.Fatal(err)
				}
				value := "another"
				if changed == "config" {
					c.GitExecutablePath = &value
				} else {
					c.TaskRoot = &value
				}
				data, _ := json.Marshal(c)
				s.configJSON = string(data)
			case "replaced":
				s.cancelConfirmationDialog()
				s.confirmNative("new dialog", "new message", "new detail", nil)
			}
			s.finishConfirmationDialog(d, 0)
			want := 0
			if changed == "none" || changed == "unrelated-config" {
				want = 1
			}
			if called != want {
				t.Fatalf("stale continuation accepted: %d, want %d", called, want)
			}
			if changed == "replaced" && s.confirmationDialog == nil {
				t.Fatal("late callback dismissed replacement dialog")
			}
		})
	}
}

func TestProductionConfirmationLongPathAndCancelDefaults(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s, v, b := productionConfirmationFixture(t, "main")
		s.cancelConfirmationDialog()
		v.Preview.Target.Repository = "C:/synthetic/" + strings.Repeat("long-service-directory/", 12)
		b.Target = v.Preview.Target
		s.confirmProductionMain(v)
		d := s.confirmationDialog
		tester := productionIterationTester(s, 960, dark)
		if !tester.Focused("取消") {
			t.Fatal("confirmation did not default focus to Cancel")
		}
		path, found := tester.Find(b.Target.Repository)
		if !found || path.W > 440 || path.H < 30 || path.X+path.W > 960 {
			t.Fatalf("long path is not wrapped inside summary: %+v", path)
		}
		for _, notice := range d.Notices {
			if !tester.HasText(notice) {
				t.Fatal("essential notice hidden in details")
			}
		}
		captureProductionSummary(t, tester, fmt.Sprintf("confirm-long-path-dark-%t", dark))
		tester.Key(0, ui.KeyEnter)
		if s.confirmationDialog != nil || v.Busy {
			t.Fatal("Enter on default Cancel continued production action")
		}
		s.confirmProductionMain(v)
		tester.Frame()
		if s.confirmationDialog.DetailOpen {
			t.Fatal("new dialog retained previous disclosure state")
		}
		s.configJSON = `{"tagEnabled":false}`
		tester.Frame()
		if s.confirmationDialog != nil {
			t.Fatal("context change did not dismiss pending confirmation")
		}
	}
}
