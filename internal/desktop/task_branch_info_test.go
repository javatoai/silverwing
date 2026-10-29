package desktop

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestTaskBranchInfoCopyOptions(t *testing.T) {
	fixture := task{requirementURL: " https://fixture.invalid/requirement ", workspaces: []application.Workspace{{Name: "api", Branch: "feature/api"}, {Name: "job", Branch: "feature/job"}}}
	options := taskBranchInfoCopyOptions(fixture)
	wantKeys := []string{"services", "branches"}
	if len(options) != len(wantKeys) {
		t.Fatalf("removed link cards remain: %+v", options)
	}
	for index, want := range []string{"api\njob", "api：feature/api\njob：feature/job"} {
		if options[index].Key != wantKeys[index] || options[index].Text != want {
			t.Fatalf("option %d: %+v", index, options[index])
		}
	}
	fixture.requirementURL = "  "
	options = taskBranchInfoCopyOptions(fixture)
	if len(options) != 2 || options[0].Text != "api\njob" || options[1].Text != "api：feature/api\njob：feature/job" {
		t.Fatalf("blank link changed the remaining cards: %+v", options)
	}
}

func TestTaskBranchInfoMatrixPreviewAndCopy(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				fixture := task{id: "task", path: t.TempDir(), createdAt: "created", requirementURL: "https://fixture.invalid/requirement/" + strings.Repeat("long-url/", 30), workspaces: []application.Workspace{{Name: "api", Branch: "feature/" + strings.Repeat("long-branch/", 40)}, {Name: "job", Branch: "feature/job"}}}
				s := &app{
					taskUnit: taskController{state: taskState{tasks: []task{fixture}}},
					page:     "tasks",
				}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					if ui.Button(c, "打开分支信息").Clicked() {
						s.openTaskBranchInfo(s.taskUnit.currentTask())
					}
					s.renderTaskBranchInfoDialog(c, p)
				}, width, 780)
				if err := tester.Click("打开分支信息"); err != nil {
					t.Fatal(err)
				}
				var cells [2]ui.Rect
				options := taskBranchInfoCopyOptions(fixture)
				for index, option := range options {
					cell, found := tester.Find("分支信息区域 · " + option.Key)
					preview, previewFound := tester.Find("分支信息预览 · " + option.Key)
					copy, copyFound := tester.Find("复制" + option.Label)
					if !found || !previewFound || !copyFound || preview.H < 140 || cell.X < 0 || cell.Y < 0 || cell.X+cell.W > float32(width) || cell.Y+cell.H > 780 || copy.X+copy.W > cell.X+cell.W || copy.Y < cell.Y {
						t.Fatalf("matrix cell/preview/copy clipped: cell=%+v preview=%+v copy=%+v", cell, preview, copy)
					}
					cells[index] = cell
					if !tester.HasText(option.Text) {
						t.Fatalf("%s preview differs from complete copy source", option.Key)
					}
					if err := tester.Click("复制" + option.Label); err != nil {
						t.Fatal(err)
					}
					if tester.Clipboard() != option.Text {
						t.Fatalf("%s copy changed/truncated the shown content", option.Key)
					}
				}
				for _, cell := range cells {
					if math.Abs(float64(cell.W-cells[0].W)) > .5 || math.Abs(float64(cell.H-cells[0].H)) > .5 || math.Abs(float64(cell.Y-cells[0].Y)) > .5 {
						t.Fatalf("cards must use equal columns in one row: %+v", cells)
					}
				}
				if cells[1].X <= cells[0].X+cells[0].W || tester.HasText("服务名称 · 含需求链接") || tester.HasText("服务与分支 · 含需求链接") || tester.HasText("复制内容") {
					t.Fatalf("remaining cards are not arranged in one row, or removed cards remain: %+v", cells)
				}
				if directory := os.Getenv("SILVERWING_TAG_EVIDENCE_DIR"); directory != "" {
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("branch-info-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tester.Image())
					if closeErr := file.Close(); err != nil || closeErr != nil {
						t.Fatalf("capture failed: %v / %v", err, closeErr)
					}
				}
				tester.Key(0, ui.KeyEscape)
				if s.taskUnit.state.taskBranchInfo != nil && s.taskUnit.state.taskBranchInfo.Open || !tester.Focused("打开分支信息") {
					t.Fatal("Escape no longer closes and restores the originating focus")
				}
			})
		}
	}
}

func TestTaskBranchInfoMatrixEmptyData(t *testing.T) {
	for _, fixture := range []task{{id: "empty", path: t.TempDir()}, {id: "empty-branch", path: t.TempDir(), workspaces: []application.Workspace{{Name: "api"}}}} {
		s := &app{
			taskUnit: taskController{state: taskState{tasks: []task{fixture}}},
			page:     "tasks",
		}
		s.openTaskBranchInfo(fixture)
		tester := ui.NewTester(func(c *ui.Context) { s.renderTaskBranchInfoDialog(c, setTheme(c, false)) }, 960, 780)
		for _, option := range taskBranchInfoCopyOptions(fixture) {
			tester.SetClipboard("unchanged")
			if err := tester.Click("复制" + option.Label); err != nil {
				t.Fatal(err)
			}
			want := option.Text
			if tester.Clipboard() != want || strings.Contains(option.Text, "未命名") || strings.Contains(option.Text, "unknown") {
				t.Fatal("missing URL/branch/services changed the existing copy contract")
			}
		}
		if err := tester.Click("关闭"); err != nil || s.taskUnit.state.taskBranchInfo != nil && s.taskUnit.state.taskBranchInfo.Open {
			t.Fatalf("close button failed: %v", err)
		}
	}
}

func TestTaskBranchInfoIdentity(t *testing.T) {
	state := &taskBranchInfoState{TaskID: "task", TaskPath: "fixture/task", TaskCreatedAt: "created", Open: true}
	fixture := task{id: state.TaskID, path: state.TaskPath, createdAt: state.TaskCreatedAt}
	if !taskBranchInfoMatches(state, fixture, "tasks", 0) || !taskBranchInfoMatches(state, fixture, "archive", 0) {
		t.Fatal("current task should match")
	}
	if taskBranchInfoMatches(state, fixture, "settings", 0) || taskBranchInfoMatches(state, fixture, "tasks", 1) || taskBranchInfoMatches(nil, fixture, "tasks", 0) {
		t.Fatal("closed or hidden detail should not match")
	}
	for _, other := range []task{{id: "other", path: fixture.path, createdAt: fixture.createdAt}, {id: fixture.id, path: "other", createdAt: fixture.createdAt}, {id: fixture.id, path: fixture.path, createdAt: "recreated"}} {
		if taskBranchInfoMatches(state, other, "tasks", 0) {
			t.Fatalf("stale identity matched: %+v", other)
		}
	}
	state.Open = false
	if taskBranchInfoMatches(state, fixture, "tasks", 0) {
		t.Fatal("closed dialog matched")
	}
}
