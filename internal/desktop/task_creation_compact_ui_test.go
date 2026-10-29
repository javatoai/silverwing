package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func taskCreationCompactFixture(t *testing.T, width int, dark bool, count int) (*app, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	config := domain.DefaultAppConfig()
	config.AIRequirementNamingEnabled = false
	config.DefaultBranchPrefix = "feature/{num}"
	tasks, materials := filepath.Join(root, "tasks"), filepath.Join(root, "materials")
	config.TaskRoot, config.RequirementMaterialsRoot = &tasks, &materials
	modules := []domain.ServiceModuleConfig{{ID: "api", Name: "API", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch, CustomCommands: []domain.WorkspaceCommandConfig{}}, {ID: "worker", Name: "Worker", Strategy: domain.WorkspaceIndependentClone, TagMode: domain.TagBuildCurrentBranch, CustomCommands: []domain.WorkspaceCommandConfig{}}}
	config.Groups = []domain.GroupConfig{{ID: "group", Name: "项目组", Services: []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "fixture-service", Enabled: true, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, MasterBranch: "origin/main", Bootstrap: domain.BootstrapConfig{Commands: []domain.BootstrapCommand{}, CopyRules: []domain.BootstrapCopyRule{}}, Modules: modules[:count]}}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s := &app{
		settingsUnit: settingsController{state: settingsState{configJSON: string(raw)}},
		page:         "tasks",
		dialogOpen:   true,
		dark:         dark,
		inputs:       map[string]*string{},
	}
	s.snapshot.Home, s.snapshot.ConfigJSON = filepath.Join(root, "home"), string(raw)
	s.form.mode = "task-create"
	s.initTaskCreation("基于需求创建任务", "原始名称", "https://project.feishu.cn/obt/userstory/detail/1000000004")
	form := s.taskUnit.state.taskCreation
	if form.configError != "" {
		t.Fatal(form.configError)
	}
	form.toolsLoaded, form.toolsLoading, form.toolsError = true, false, ""
	s.ensureTaskCreationService("service")
	s.focusTaskCreationService("service")
	for _, draft := range form.moduleData["service"] {
		picker := &draft.repositoryPicker
		prepareTaskRepositoryPickerIdentity(picker, "repo", "")
		picker.ensureMaps()
		picker.remotes, picker.remotesLoaded = []string{"origin"}, true
		picker.branches["origin"] = []string{"origin/main", "origin/develop"}
		picker.branchesLoaded["origin"], picker.branchesLoadedAt["origin"] = true, time.Now()
	}
	requirements := s.ensureTaskCreationRequirements()
	requirements.candidatesLoaded = true
	requirements.candidates = []taskCreationRequirementCandidate{{Title: "示例支付优化 · 完整需求标题", URL: "https://project.feishu.cn/obt/userstory/detail/1000000005"}, {Title: "扩容适配", URL: "https://project.feishu.cn/obt/userstory/detail/1000000008"}}
	t.Cleanup(func() {
		s.cancelTaskCreationReads(form, false)
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		cancelTaskRepositoryChoices(form)
	})
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderTaskCreationDialog(c, setTheme(c, dark)) })
	}, width, 800)
	return s, tester
}

func compactCreationEdit(t *testing.T, tester *ui.Tester, label, value string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type(value)
}

func clickTaskCreationVisible(t *testing.T, tester *ui.Tester, label string, viewport ui.Rect) ui.Rect {
	t.Helper()
	r, found := tester.Find(label)
	if !found || r.W <= 0 || r.H <= 0 {
		t.Fatalf("visible task-creation control %q not found: rect=%+v found=%v", label, r, found)
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	if x < viewport.X || x > viewport.X+viewport.W || y < viewport.Y || y > viewport.Y+viewport.H {
		t.Fatalf("task-creation control %q center is outside its visible viewport: control=%+v viewport=%+v", label, r, viewport)
	}
	tester.ClickAt(x, y)
	tester.Frame()
	return r
}

func scrollTaskCreationRight(t *testing.T, tester *ui.Tester, viewport ui.Rect, delta float32) {
	t.Helper()
	tester.Scroll(viewport.X+viewport.W*.75, viewport.Y+viewport.H*.65, 0, delta)
	tester.Frame()
}

func TestTaskCreationCompactLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, tester := taskCreationCompactFixture(t, width, dark, 2)
				left, _ := tester.Find("任务基本信息")
				right, _ := tester.Find("任务服务与模块")
				serviceViewport, serviceViewportFound := tester.Find("任务服务列表")
				input, found := tester.Find("需求链接或搜索")
				name, _ := tester.Find("任务名称")
				footer, _ := tester.Find("检查并创建任务")
				if !found || !serviceViewportFound || serviceViewport.X < right.X || serviceViewport.X+serviceViewport.W > right.X+right.W || right.W < left.W*0.95 || right.W > left.W*1.05 || left.Y != right.Y || input.Y >= name.Y || input.X < left.X || input.X+input.W > left.X+left.W || footer.Y < left.Y+left.H || footer.Y+footer.H > 800 {
					t.Fatalf("first input/1:1/fixed footer/full-width service scroll: left=%+v right=%+v serviceScroll=%+v input=%+v name=%+v footer=%+v", left, right, serviceViewport, input, name, footer)
				}
				for _, obsolete := range []string{"当前服务配置", "服务列表", "服务设置"} {
					if _, found := tester.Find(obsolete); found {
						t.Fatalf("obsolete separate service navigation remains: %q", obsolete)
					}
				}
				_, oldPicker := tester.Find("需求候选")
				if oldPicker || tester.HasText("搜索需求标题或链接") || tester.HasText("本次任务的模块设置") || tester.HasText("条需求候选") {
					t.Fatal("duplicate requirement input or repeated module cards remain")
				}
				for _, moduleID := range s.taskUnit.state.taskCreation.moduleOrder["service"] {
					if !s.taskUnit.state.taskCreation.moduleExpanded["service"][moduleID] {
						t.Fatalf("configured modules should default to expanded in the vertical service flow: module=%q expanded=%v", moduleID, s.taskUnit.state.taskCreation.moduleExpanded["service"])
					}
				}
				first, firstFound := tester.Find("模块名称 · service:api")
				if !firstFound || first.W <= 0 || first.X < right.X || first.X+first.W > right.X+right.W {
					t.Fatalf("first configured module should be visible in the full-width service pane: rect=%+v right=%+v", first, right)
				}
			})
		}
	}
	_, single := taskCreationCompactFixture(t, 960, false, 1)
	if !single.HasText("模块名称 · service:api") {
		t.Fatalf("single module should start as the active editor: %+v", single.Texts())
	}
}

func TestTaskCreationCompactRequirementInput(t *testing.T) {
	s, tester := taskCreationCompactFixture(t, 1100, false, 1)
	form := s.taskUnit.state.taskCreation
	compactCreationEdit(t, tester, "任务名称", "手工任务名")
	compactCreationEdit(t, tester, "任务分支输入", "feature/manual")
	compactCreationEdit(t, tester, "需求链接或搜索", "支付")
	if !form.requirements.inputIsQuery || form.draft.RequirementLink == "支付" || tester.HasText("扩容适配") {
		t.Fatal("title query leaked into a URL or failed to filter candidates")
	}
	if _, err := s.taskCreationPayload(); err == nil {
		t.Fatal("unselected title query must not reach preflight payload")
	}
	s.beginTaskCreationPreflight()
	if form.preview != nil || !strings.Contains(form.inlineError, "请选择匹配需求") {
		t.Fatal("preflight did not stop an unselected search query")
	}
	tester.Key(0, ui.KeyEnter)
	want := "https://project.feishu.cn/obt/userstory/detail/1000000005"
	if form.draft.RequirementLink != want || form.requirements.query != want || form.requirements.inputIsQuery || form.requirements.selectedTitle != "示例支付优化 · 完整需求标题" {
		t.Fatalf("keyboard choice must show the complete selected URL: %+v", form.requirements)
	}
	if form.draft.TaskName != "手工任务名" || form.draft.Branch != "feature/manual" {
		t.Fatal("candidate selection overwrote manually edited name/branch")
	}
	custom := "https://example.invalid/requirements/custom?value=full"
	compactCreationEdit(t, tester, "需求链接或搜索", custom)
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	payload, err := s.taskCreationPayload()
	if err != nil || payload["requirementLink"] != custom || form.requirements.inputIsQuery {
		t.Fatalf("custom URL did not reach the existing request intact: %+v %v", payload, err)
	}
	compactCreationEdit(t, tester, "需求链接或搜索", "")
	tester.Key(0, ui.KeyTab)
	for range 3 {
		tester.Frame()
	}
	payload, err = s.taskCreationPayload()
	if err != nil || payload["requirementLink"] != "" || form.requirements.query != "" || form.draft.TaskName != "手工任务名" || form.draft.Branch != "feature/manual" {
		t.Fatalf("clear/blur/redraw refilled the requirement or user draft: %+v %v", payload, err)
	}
	if err := tester.Click("刷新需求候选"); err != nil {
		t.Fatal(err)
	}
	if form.requirements.candidatesError == "" || !tester.HasText("重试读取需求候选") || form.requirements.query != "" {
		t.Fatal("refresh must route to the existing read/error-retry path without filling the input")
	}
	if err := tester.Click("重试读取需求候选"); err != nil {
		t.Fatal(err)
	}
}

func TestTaskCreationCompactModuleDraftAndPayload(t *testing.T) {
	s, tester := taskCreationCompactFixture(t, 1100, false, 2)
	form := s.taskUnit.state.taskCreation
	serviceViewport, _ := tester.Find("任务服务列表")
	compactCreationEdit(t, tester, "模块名称 · service:api", "API-edited")
	compactCreationEdit(t, tester, "模块目标分支 · service:api", "feature/custom-api")
	draft := form.moduleData["service"]["api"]
	clickTaskCreationVisible(t, tester, "折叠模块：service:api", serviceViewport)
	if form.moduleExpanded["service"]["api"] || draft.Name != "API-edited" || draft.TargetBranch != "feature/custom-api" || !draft.TargetEdited || !form.moduleExpanded["service"]["worker"] {
		t.Fatal("folding one module should preserve its draft and leave the other module expanded")
	}
	clickTaskCreationVisible(t, tester, "展开模块：service:api", serviceViewport)
	form.preview = &taskCreationPreview{signature: s.taskCreationSignature()}
	form.phase = "confirm"
	tester.Frame()
	scrollTaskCreationRight(t, tester, serviceViewport, 10000)
	clickTaskCreationVisible(t, tester, "添加临时模块", serviceViewport)
	order := form.moduleOrder["service"]
	newID := order[len(order)-1]
	newDraft := form.moduleData["service"][newID]
	if !form.moduleExpanded["service"][newID] || !tester.Focused("模块名称 · service:"+newID) || len(order) != 3 || form.preview != nil || form.phase != "editing" {
		t.Fatalf("temporary module did not append, expand/focus, or invalidate preflight: expanded=%v id=%q focus=%t count=%d preview=%+v phase=%q texts=%+v", form.moduleExpanded["service"], newID, tester.Focused("模块名称 · service:"+newID), len(order), form.preview, form.phase, tester.Texts())
	}
	nameBox, _ := tester.Find("模块名称 · service:" + newID)
	if nameBox.Y < serviceViewport.Y || nameBox.Y+nameBox.H > serviceViewport.Y+serviceViewport.H {
		t.Fatalf("new module focus was not scrolled into view: input=%+v viewport=%+v", nameBox, serviceViewport)
	}
	payload, err := s.taskCreationPayload()
	if err != nil {
		t.Fatal(err)
	}
	selections := payload["serviceSelections"].([]workspace.TaskServiceSelection)
	if len(selections) != 1 || len(selections[0].Modules) != 3 || selections[0].Modules[0].Name != "API-edited" || selections[0].Modules[0].TargetBranch != "feature/custom-api" || selections[0].Modules[0].Strategy != domain.WorkspaceStandardWorktree || selections[0].Modules[2].Source != domain.TaskModuleTemporary {
		t.Fatalf("visible module drafts diverged from preflight selection: %+v", selections)
	}
	clickTaskCreationVisible(t, tester, "删除模块 "+newDraft.Name, serviceViewport)
	if form.moduleData["service"][newID] != nil || len(form.moduleOrder["service"]) != 2 {
		t.Fatal("temporary module delete did not preserve module count/selection")
	}
	s.toggleTaskCreationService("service")
	s.toggleTaskCreationService("service")
	if form.moduleData["service"]["api"] != draft || draft.TargetBranch != "feature/custom-api" {
		t.Fatal("service deselect/reselect discarded edited module drafts")
	}
}

func TestTaskCreationTemporaryModuleDeletionKeepsOneModule(t *testing.T) {
	s, tester := taskCreationCompactFixture(t, 960, false, 1)
	form := s.taskUnit.state.taskCreation
	serviceViewport, _ := tester.Find("任务服务列表")
	if got := len(form.moduleOrder["service"]); got != 1 {
		t.Fatalf("fixture must start with one configured module, got %d", got)
	}
	scrollTaskCreationRight(t, tester, serviceViewport, 10000)
	clickTaskCreationVisible(t, tester, "添加临时模块", serviceViewport)
	order := form.moduleOrder["service"]
	if len(order) != 2 {
		t.Fatalf("temporary module was not appended: %v", order)
	}
	temporaryID := order[1]
	draft := form.moduleData["service"][temporaryID]
	clickTaskCreationVisible(t, tester, "删除模块 "+draft.Name, serviceViewport)
	if len(form.moduleOrder["service"]) != 1 || form.moduleOrder["service"][0] != "api" || form.moduleData["service"]["api"] == nil {
		t.Fatalf("deleting the only temporary module must preserve the configured module: order=%v data=%v", form.moduleOrder["service"], form.moduleData["service"])
	}
}

func TestTaskCreationCompactRequirementRetiresLateCandidates(t *testing.T) {
	s, _ := taskCreationCompactFixture(t, 1100, false, 1)
	form, state := s.taskUnit.state.taskCreation, s.taskUnit.state.taskCreation.requirements
	for _, scope := range []string{"home", "project", "account"} {
		ctx, cancel := context.WithCancel(context.Background())
		request := &taskCreationRequirementRequest{form: form, token: form.token, ctx: ctx, cancel: cancel, home: s.snapshot.Home, groupID: form.groupID, config: taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config), generation: state.candidateGeneration}
		state.candidateRequest, state.candidatesLoading = request, true
		if scope == "home" {
			s.snapshot.Home = filepath.Join(t.TempDir(), "other-home")
		} else if scope == "project" {
			form.config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "other-project"}}
		} else {
			state.scope.listIdentity.account = "previous-account"
		}
		s.ensureTaskCreationRequirements()
		s.finishTaskCreationRequirementCandidates(request, application.ActionResult{Data: map[string]any{"candidates": []application.RequirementCandidate{{Title: "迟到候选", URL: "https://fixture.invalid/late"}}}}, nil)
		if ctx.Err() == nil || state.candidateRequest == request || len(state.candidates) != 0 {
			t.Fatalf("%s scope change accepted late candidates", scope)
		}
	}
	for _, value := range []string{"标题文字", "https://", "not a URL"} {
		if _, valid := taskCreationRequirementInputLink(value); valid {
			t.Fatalf("query %q accepted as request URL", value)
		}
	}
	for _, value := range []string{"", " 1000000004 ", "https://example.invalid/full?x=1"} {
		if got, valid := taskCreationRequirementInputLink(value); !valid || got != strings.TrimSpace(value) {
			t.Fatalf("valid direct input rejected: %q", value)
		}
	}
}
