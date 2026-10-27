package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTaskCreationDialogFixedGeometry(t *testing.T) {
	for _, size := range [][2]int{{960, 760}, {1440, 900}} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d-dark-%t", size[0], size[1], dark), func(t *testing.T) {
				s, tester := taskCreationLeftFixture(t, size[0], dark)
				form := s.taskCreation
				for i := 0; i < 24; i++ {
					service := form.config.Groups[0].Services[0]
					service.ID, service.DisplayName = fmt.Sprintf("synthetic-%02d", i), fmt.Sprintf("合成服务 %02d", i)
					form.config.Groups[0].Services = append(form.config.Groups[0].Services, service)
				}
				form.config.Groups = append(form.config.Groups, domain.GroupConfig{ID: "empty-group", Name: "合成空组", Services: []domain.GroupServiceConfig{}})
				tester.SetSize(size[0], size[1])
				tester.Frame()
				panel, panelFound := tester.Find("创建任务表单")
				footer, _ := tester.Find("检查并创建任务")
				header, _ := tester.Find(form.title)
				left, _ := tester.Find("任务基本信息")
				right, _ := tester.Find("任务服务与模块")
				serviceViewport, _ := tester.Find("任务服务列表")
				if !panelFound || serviceViewport.H <= 0 || serviceViewport.X < right.X || serviceViewport.X+serviceViewport.W > right.X+right.W || panel.H != min(float32(900), float32(size[1]-24)) || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(size[0]) || panel.Y+panel.H > float32(size[1]) || footer.Y+footer.H > panel.Y+panel.H || header.Y < panel.Y || footer.Y < left.Y+left.H || left.Y != right.Y || right.W < left.W*0.95 || right.W > left.W*1.05 {
					t.Fatalf("fixed-height panel/header/footer/1:1 and service scroll escaped viewport: panel=%+v header=%+v footer=%+v left=%+v right=%+v services=%+v", panel, header, footer, left, right, serviceViewport)
				}
				assertStable := func() {
					t.Helper()
					tester.Frame()
					actual, _ := tester.Find("创建任务表单")
					actualFooter, foundFooter := tester.Find("检查并创建任务")
					if !foundFooter {
						actualFooter, _ = tester.Find("创建任务")
					}
					actualHeader, _ := tester.Find(form.title)
					actualLeft, _ := tester.Find("任务基本信息")
					actualRight, _ := tester.Find("任务服务与模块")
					if actual != panel || actualFooter.Y != footer.Y || actualFooter.H != footer.H || actualFooter.X+actualFooter.W != footer.X+footer.W || actualHeader != header || actualLeft != left || actualRight != right {
						t.Fatalf("content changed panel/footer geometry: panel=%+v footer=%+v header=%+v left=%+v right=%+v", actual, actualFooter, actualHeader, actualLeft, actualRight)
					}
				}
				for _, query := range []string{"合成-不存在", "合成-一组甲", "合成-一组", ""} {
					form.serviceSearch = query
					assertStable()
				}
				s.toggleTaskCreationService("service")
				assertStable()
				s.toggleTaskCreationService("service")
				assertStable()
				s.setTaskCreationGroup("empty-group")
				assertStable()
				s.setTaskCreationGroup(form.config.Groups[0].ID)
				assertStable()
				form.aiError = strings.Repeat("合成长提示自然滚动。", 80)
				assertStable()
				beforeRight, _ := tester.Find("任务服务与模块")
				tester.Scroll(left.X+left.W/2, left.Y+left.H/2, 0, 10000)
				assertStable()
				afterRight, _ := tester.Find("任务服务与模块")
				if beforeRight != afterRight {
					t.Fatal("left scrolling moved the independent right pane")
				}
				beforeLeft, _ := tester.Find("任务基本信息")
				beforeListScroll := form.serviceListScroll[form.groupID].Y
				tester.Scroll(serviceViewport.X+serviceViewport.W*.75, serviceViewport.Y+serviceViewport.H*.65, 0, 10000)
				assertStable()
				afterLeft, _ := tester.Find("任务基本信息")
				if beforeLeft != afterLeft || form.serviceListScroll[form.groupID].Y <= beforeListScroll {
					t.Fatalf("right service scrolling should move only its list: leftBefore=%+v leftAfter=%+v scrollBefore=%v scrollAfter=%+v", beforeLeft, afterLeft, beforeListScroll, form.serviceListScroll[form.groupID].Y)
				}
				form.aiError = ""
				tester.Scroll(left.X+left.W/2, left.Y+left.H/2, 0, -10000)
				tester.Frame()
				captureTaskCreationLeft(t, tester, fmt.Sprintf("fixed-dialog-%dx%d-dark-%t", size[0], size[1], dark))
			})
		}
	}
}

func TestTaskCreationDialogToolCheckboxes(t *testing.T) {
	s, tester := taskCreationLeftFixture(t, 960, false)
	form := s.taskCreation
	heading, _ := tester.Find("创建后打开工作区工具")
	codex, _ := tester.Find("Codex")
	cursor, _ := tester.Find("Cursor")
	if heading.H > 20 || codex.Y < heading.Y+heading.H || cursor.Y != codex.Y || codex.X != heading.X {
		t.Fatal("tools must be native checkboxes on a separate row below a single-line heading")
	}
	if err := tester.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if !form.selectedToolIDs["codex"] || !form.selectedToolIDs["cursor"] {
		t.Fatal("checkbox click must retain multi-select semantics")
	}
	tester.Key(0, ui.KeySpace)
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("Cursor") {
		t.Fatal("checkboxes must retain native keyboard tab order")
	}
	tester.Key(0, ui.KeySpace)
	if len(s.taskCreationSelectedToolIDs()) != 0 {
		t.Fatal("keyboard checkbox changes must permit no selected tools")
	}
	form.workspaceTools = append(form.workspaceTools, taskCreationWorkspaceToolOption{ID: "custom", DisplayName: "合成工具", Available: false, Reason: "尚未配置"})
	form.config.DefaultWorkspaceToolIDs = append(form.config.DefaultWorkspaceToolIDs, "custom")
	tester.Frame()
	if err := tester.Click("合成工具"); err != nil {
		t.Fatal(err)
	}
	if form.selectedToolIDs["custom"] || !tester.HasText("合成工具（不可用）") {
		t.Fatal("dynamic unavailable checkbox was removed or enabled")
	}
	s.busy = true
	tester.Frame()
	if err := tester.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if form.selectedToolIDs["codex"] {
		t.Fatal("busy checkbox accepted input")
	}
}

func TestTaskCreationDialogDismissesDirectly(t *testing.T) {
	for _, way := range []string{"cancel", "close", "backdrop", "escape"} {
		t.Run(way, func(t *testing.T) {
			s, tester := taskCreationLeftFixture(t, 960, false)
			old := s.taskCreation
			readCtx, readCancel := context.WithCancel(context.Background())
			old.readRequest = &taskCreationReadRequest{kind: "workspace.tools-options", cancel: readCancel}
			preCtx, preCancel := context.WithCancel(context.Background())
			request := &taskCreationPreflightRequest{form: old, token: old.token, ctx: preCtx, cancel: preCancel}
			old.preflight = request
			aiCtx, aiCancel := context.WithCancel(context.Background())
			old.aiNaming.request = &taskCreationAINamingRequest{ctx: aiCtx, cancel: aiCancel}
			candidateCtx, candidateCancel := context.WithCancel(context.Background())
			old.requirements.candidateRequest = &taskCreationRequirementRequest{ctx: candidateCtx, cancel: candidateCancel}
			old.previewScheduleTimer = time.AfterFunc(time.Hour, func() {})
			old.pendingPreflight = true
			switch way {
			case "cancel":
				if err := tester.Click("取消"); err != nil {
					t.Fatal(err)
				}
			case "close":
				s.requestTaskCreationDismiss()
				tester.Frame()
			case "backdrop":
				tester.ClickAt(2, 2)
			case "escape":
				tester.Key(0, ui.KeyEscape)
			}
			if s.taskCreation != nil || s.dialogOpen || s.form.mode != "" || tester.HasText("放弃创建任务") || readCtx.Err() == nil || preCtx.Err() == nil || aiCtx.Err() == nil || candidateCtx.Err() == nil || old.previewScheduleTimer != nil {
				t.Fatal("closing must discard directly and retire only the old form's work")
			}
			s.dialogOpen, s.form.mode = true, "task-create"
			s.initTaskCreation("新建合成任务", "全新需求标题", "https://example.invalid/new-requirement")
			fresh := s.taskCreation
			s.lastMessage = "new-form-feedback"
			for _, kind := range []string{"task.create", "task.create-preview", "workspace.tools-options", "requirements.ai-name"} {
				action := application.Action{Kind: kind, Payload: map[string]any{"taskCreateToken": old.token}}
				s.taskCreationActionResult(action, application.ActionResult{Message: "late-success"})
				s.taskCreationActionError(action, errors.New("late-error"))
			}
			s.finishTaskCreationPreflight(request, application.ActionResult{}, nil)
			tester.Frame()
			if s.taskCreation != fresh || fresh.token == old.token || fresh.draft.TaskName != "全新需求标题" || s.lastMessage != "new-form-feedback" || !s.dialogOpen || tester.HasText("放弃创建任务") {
				t.Fatal("retired callbacks reopened or polluted the new form")
			}
		})
	}
}

func TestTaskCreationDialogClosePreservesOtherLifecycles(t *testing.T) {
	s, tester := taskCreationLeftFixture(t, 960, false)
	form := s.taskCreation
	form.preview = &taskCreationPreview{signature: s.taskCreationSignature()}
	form.phase = "creating"
	form.progressGeneration++
	form.creationProgress = newTaskCreationProgressState(s, form, form.progressGeneration)
	s.busy, s.operationKind = true, "task.create"
	operationCtx, operationCancel := context.WithCancel(context.Background())
	defer operationCancel()
	s.cancelOperation = operationCancel
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if s.taskCreation != form || !s.dialogOpen || !s.busy || s.operationKind != "task.create" || operationCtx.Err() != nil || !tester.HasText("中断创建") {
		t.Fatal("Escape must keep the busy creation form open and expose its explicit interrupt action")
	}
	if err := tester.Click("中断创建"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if s.taskCreation != form || !s.dialogOpen || !s.busy || !s.operationCancelling || operationCtx.Err() == nil {
		t.Fatal("interrupt must cancel the operation while keeping the form open until its result arrives")
	}
	documentApp, draft := taskLocalNoteFixture(t)
	loadTaskLocalNote(documentApp, draft, "已保存", true)
	draft.view.Buffer.Set("未保存的本地说明")
	if documentApp.documentCloseReady() == nil {
		t.Fatal("fixture must begin with the existing document close gate")
	}
	documentApp.taskCreation = &taskCreationState{token: "temporary-form", phase: "editing"}
	documentApp.dialogOpen, documentApp.form.mode = true, "task-create"
	documentApp.requestTaskCreationDismiss()
	if documentApp.documentCloseReady() == nil || draft.text() != "未保存的本地说明" || !draft.dirty() {
		t.Fatal("form dismissal bypassed the unrelated unsaved-document close gate")
	}
}
