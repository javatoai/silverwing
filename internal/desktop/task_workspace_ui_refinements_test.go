package desktop

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTaskDetailsOmitMoreMenu(t *testing.T) {
	for _, test := range []struct {
		width, offset int
		dark          bool
	}{{1100, 12, false}, {1100, 12, true}, {360, 4, false}, {360, 4, true}, {1000, 750, false}} {
		t.Run(fmt.Sprintf("width-%d-offset-%d-dark-%t", test.width, test.offset, test.dark), func(t *testing.T) {
			s := &app{configJSON: "{}", inputs: map[string]*string{}, tasks: []task{{id: "synthetic-task", title: "合成任务", path: filepath.Join(t.TempDir(), "task")}}}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, test.dark)
				s.beginOverflowMenuFrame()
				ui.Row(c).Fill().Children(func() {
					ui.Box(c).Width(float32(test.offset)).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() { s.taskDetails(c, p) })
				})
				s.endOverflowMenuFrame()
			}, test.width, 780)
			for _, removed := range []string{"任务更多操作", "在 Codex 中打开", "Codex 配置", "Codex 会话关联", "归档任务", "恢复任务", "恢复归档", "删除任务"} {
				if tester.HasText(removed) {
					t.Fatalf("task detail toolbar still contains %q", removed)
				}
			}
		})
	}
}

func TestTaskMoreLeavesOtherMenusAlignedRight(t *testing.T) {
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Row(c).Padding(40, 400).Children(func() {
			s.overflowMenu(c, p, "other", "其它更多操作", false, func(m *overflowMenuBuilder) { m.Item("item", "普通菜单项").Chosen() })
		})
	}, 1000, 600)
	trigger, _ := tester.Find("其它更多操作")
	if err := tester.Click("其它更多操作"); err != nil {
		t.Fatal(err)
	}
	item, ok := tester.Find("普通菜单项")
	if !ok || math.Abs(float64(item.X+item.W-trigger.X-trigger.W+6)) > 1 {
		t.Fatalf("default end-aligned menu was reversed: trigger=%+v item=%+v", trigger, item)
	}
}

func appendServiceToggleFixture(t *testing.T, width int, dark bool) (*app, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	config := domain.DefaultAppConfig()
	for _, id := range []string{"existing", "alpha", "beta"} {
		service := domain.GroupServiceConfig{ID: id, RepositoryID: "repo-" + id, DisplayName: map[string]string{"existing": "已有服务", "alpha": "合成服务甲", "beta": "合成服务乙"}[id], Enabled: true, DevelopmentTool: domain.DevelopmentToolIntelliJIdea, MasterBranch: "origin/main", Bootstrap: domain.BootstrapConfig{Commands: []domain.BootstrapCommand{}, CopyRules: []domain.BootstrapCopyRule{}}, Modules: []domain.ServiceModuleConfig{{ID: "api", Name: "API", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch, CustomCommands: []domain.WorkspaceCommandConfig{}}}}
		if len(config.Groups) == 0 {
			config.Groups = []domain.GroupConfig{{ID: "synthetic-group", Name: "合成项目组"}}
		}
		config.Groups[0].Services = append(config.Groups[0].Services, service)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s := &app{inputs: map[string]*string{}, configJSON: string(raw), tasks: []task{{id: "task", title: "合成任务", groupID: config.Groups[0].ID, path: filepath.Join(root, "task"), branch: "feature/synthetic", updated: "fixed-version", serviceIDs: []string{"existing"}}}}
	s.snapshot.ConfigJSON, s.snapshot.Home = string(raw), filepath.Join(root, "home")
	s.openTaskAppendServices(s.currentTask())
	state := s.workspaceAppend
	if state == nil {
		t.Fatal(s.lastError)
	}
	for _, id := range []string{"alpha", "beta"} {
		s.toggleTaskAppendService(id)
		for _, draft := range state.moduleData[id] {
			picker := &draft.repositoryPicker
			prepareTaskRepositoryPickerIdentity(picker, "repo-"+id, "")
			picker.ensureMaps()
			picker.remotes, picker.remotesLoaded = []string{"origin"}, true
			picker.branches["origin"] = []string{"origin/main"}
			picker.branchesLoaded["origin"], picker.branchesLoadedAt["origin"] = true, time.Now()
		}
		s.toggleTaskAppendService(id)
	}
	t.Cleanup(func() { cancelTaskAppendRepositoryChoices(state) })
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderTaskAppendDialog(c, setTheme(c, dark)) })
	}, width, 800)
	return s, tester
}

func TestTaskAppendServiceRowTogglesOnce(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, tester := appendServiceToggleFixture(t, width, dark)
				before := append([]task(nil), s.tasks...)
				state := s.workspaceAppend
				if _, exists := tester.Find("已有服务"); exists {
					t.Fatal("existing task service appeared in add-only candidates")
				}
				row, found := tester.Find("合成服务甲")
				if !found {
					t.Fatal("synthetic service row missing")
				}
				// Text, icon and blank space are the same button and toggle once.
				tester.ClickAt(row.X+52, row.Y+18)
				if !state.selectedServices["alpha"] || state.activeServiceID != "alpha" {
					t.Fatal("text click did not select exactly once")
				}
				draft := state.moduleData["alpha"]["api"]
				draft.TargetBranch, draft.LastTargetBranch, draft.TargetEdited = "feature/manual-alpha", "feature/manual-alpha", true
				row, _ = tester.Find("合成服务甲")
				tester.ClickAt(row.X+19, row.Y+25)
				if state.selectedServices["alpha"] || state.activeServiceID != "" || !tester.HasText("选择要配置的服务") {
					t.Fatal("icon click did not deselect exactly once and empty the config pane")
				}
				row, _ = tester.Find("合成服务甲")
				tester.ClickAt(row.X+row.W-8, row.Y+row.H-6)
				if !state.selectedServices["alpha"] || state.moduleData["alpha"]["api"] != draft || draft.TargetBranch != "feature/manual-alpha" {
					t.Fatal("blank-space click did not reselect once with the retained draft")
				}
				tester.Key(0, ui.KeyEnter)
				if state.selectedServices["alpha"] {
					t.Fatal("Enter did not use the same toggle")
				}
				tester.Key(0, ui.KeySpace)
				if !state.selectedServices["alpha"] {
					t.Fatal("Space did not use the same toggle")
				}
				if err := tester.Click("合成服务乙"); err != nil {
					t.Fatal(err)
				}
				if !state.selectedServices["beta"] || state.activeServiceID != "beta" {
					t.Fatal("another row did not select and activate")
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("append-selected-%d-dark-%t", width, dark))
				if err := tester.Click("合成服务乙"); err != nil {
					t.Fatal(err)
				}
				payload, _, err := s.taskAppendRequest()
				if err != nil {
					t.Fatal(err)
				}
				selections := payload["serviceSelections"].([]workspace.TaskServiceSelection)
				if state.selectedServices["beta"] || state.activeServiceID != "alpha" || !reflect.DeepEqual(payload["serviceIds"], []string{"alpha"}) || len(selections) != 1 || selections[0].ServiceID != "alpha" || selections[0].Modules[0].TargetBranch != "feature/manual-alpha" {
					t.Fatal("deselected service leaked into payload or remaining draft/focus was lost")
				}
				if err := tester.Click("合成服务甲"); err != nil {
					t.Fatal(err)
				}
				if len(state.selectedServices) != 0 || state.activeServiceID != "" || !tester.HasText("选择要配置的服务") {
					t.Fatal("all deselected did not leave the normal empty pane")
				}
				if err := tester.Click("检查并添加"); err != nil {
					t.Fatal(err)
				}
				if state.pending || state.inlineError != "" {
					t.Fatal("empty-selection footer must be disabled, not dispatch a preview")
				}
				if _, _, err := s.taskAppendRequest(); err == nil || !reflect.DeepEqual(before, s.tasks) {
					t.Fatal("empty selection produced a payload or changed real task services")
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("append-empty-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestTaskAppendServiceRowSearchAndBusy(t *testing.T) {
	s, tester := appendServiceToggleFixture(t, 960, false)
	state := s.workspaceAppend
	if err := tester.Click("合成服务甲"); err != nil {
		t.Fatal(err)
	}
	state.serviceQuery = "合成服务乙"
	tester.Frame()
	if !state.selectedServices["alpha"] {
		t.Fatal("filtering lost the hidden selected service")
	}
	if err := tester.Click("合成服务乙"); err != nil {
		t.Fatal(err)
	}
	s.busy = true
	tester.Frame()
	if err := tester.Click("合成服务乙"); err != nil {
		t.Fatal(err)
	}
	if !state.selectedServices["alpha"] || !state.selectedServices["beta"] {
		t.Fatal("busy row accepted mouse input")
	}
	s.busy = false
	state.pending = true
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	if !state.selectedServices["beta"] {
		t.Fatal("pending row accepted keyboard input")
	}
	state.pending = false
	state.serviceQuery = ""
	tester.Frame()
	if err := tester.Click("合成服务乙"); err != nil {
		t.Fatal(err)
	}
	if state.activeServiceID != "alpha" || !state.selectedServices["alpha"] {
		t.Fatal("clearing search/deselecting did not restore the other selected draft")
	}
}
