package desktop

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

const taskCreationLeftTitle = "【合成需求】多仓库任务创建时完整显示需求名称，保留手工名称与分支，并验证中文长标题在窄栏中自然换行"
const taskCreationLeftError = "AI 命名失败，可继续手工填写：示例命名服务暂不可用，请检查配置后重试；已经填写的任务名称和分支会保留。"

func taskCreationLeftFixture(t *testing.T, width int, dark bool) (*app, *ui.Tester) {
	t.Helper()
	s, tester := taskCreationCompactFixture(t, width, dark, 1)
	form := s.taskCreation
	s.selectTaskCreationRequirementCandidate(taskCreationRequirementCandidate{
		URL: "https://project.feishu.cn/synthetic/userstory/detail/1000000091", Title: taskCreationLeftTitle,
	})
	form.config.AIRequirementNamingEnabled = true
	form.workspaceTools = []taskCreationWorkspaceToolOption{
		{ID: "codex", DisplayName: "Codex", Description: "示例命令工具", Available: true},
		{ID: "cursor", DisplayName: "Cursor", Description: "示例编辑器", Available: true},
	}
	form.selectedToolIDs = map[string]bool{"cursor": true}
	form.draft.MetadataHint = nil
	tester.Frame()
	// Metadata reads have no backend in this isolated UI fixture. The selected
	// candidate remains the known title, while the input remains the URL.
	form.draft.MetadataHint = nil
	tester.Frame()
	return s, tester
}

func TestTaskCreationLeftLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, tester := taskCreationLeftFixture(t, width, dark)
				form := s.taskCreation
				form.aiError = taskCreationLeftError
				tester.Frame()
				left, _ := tester.Find("任务基本信息")
				right, _ := tester.Find("任务服务与模块")
				input, _ := tester.Find("需求链接或搜索")
				title, titleFound := tester.Find(taskCreationLeftTitle)
				nameLabel, _ := tester.Find("任务名称 / 文件夹名")
				generate, generateFound := tester.Find("生成任务名和分支")
				name, _ := tester.Find("任务名称")
				failure, failureFound := tester.Find(taskCreationLeftError)
				branch, _ := tester.Find("任务分支输入")
				toolsLabel, _ := tester.Find("创建后打开工作区工具")
				codex, _ := tester.Find("Codex")
				cursor, _ := tester.Find("Cursor")
				footer, _ := tester.Find("检查并创建任务")
				if !titleFound || title.Y < input.Y+input.H || title.Y+title.H > nameLabel.Y || title.W > left.W || title.H < 20 {
					t.Fatalf("full muted title must wrap below the sole URL input: input=%+v title=%+v label=%+v", input, title, nameLabel)
				}
				if !generateFound || generate.W != 28 || generate.H != 28 || generate.X < nameLabel.X+nameLabel.W || generate.X > nameLabel.X+nameLabel.W+8 || generate.Y+generate.H > name.Y {
					t.Fatalf("generation icon must sit beside the name label: label=%+v generate=%+v name=%+v", nameLabel, generate, name)
				}
				if !failureFound || failure.Y < name.Y+name.H || failure.Y+failure.H > branch.Y || failure.W > left.W || failure.H < 20 {
					t.Fatalf("long generation failure must own wrapping rows: failure=%+v name=%+v branch=%+v", failure, name, branch)
				}
				if toolsLabel.Y < branch.Y+branch.H || codex.Y < toolsLabel.Y+toolsLabel.H || codex.Y != cursor.Y || math.Abs(float64(codex.X-toolsLabel.X)) > 1 {
					t.Fatalf("workspace tools should be checkboxes on a separate row: label=%+v codex=%+v cursor=%+v", toolsLabel, codex, cursor)
				}
				if right.W < left.W*0.95 || right.W > left.W*1.05 || left.Y != right.Y || footer.Y < left.Y+left.H || footer.Y+footer.H > 800 {
					t.Fatalf("1:1 independent columns and fixed footer changed: left=%+v right=%+v footer=%+v", left, right, footer)
				}
				if tester.HasText("生成名称") || tester.HasText("预热") || form.requirements.query != form.draft.RequirementLink || form.requirements.query == taskCreationLeftTitle {
					t.Fatal("old action/prewarm copy remains, or the title replaced the URL")
				}
				captureTaskCreationLeft(t, tester, fmt.Sprintf("left-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestTaskCreationLeftTitleAndTools(t *testing.T) {
	s, tester := taskCreationLeftFixture(t, 960, false)
	form := s.taskCreation
	link := form.draft.RequirementLink
	if err := tester.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if !form.selectedToolIDs["codex"] || !form.selectedToolIDs["cursor"] {
		t.Fatal("tool toggles discarded the initial selection")
	}
	if err := tester.Click("Cursor"); err != nil {
		t.Fatal(err)
	}
	if form.selectedToolIDs["cursor"] || form.requirements.query != link {
		t.Fatal("tool deselection or URL preservation failed")
	}
	form.workspaceTools = append(form.workspaceTools, taskCreationWorkspaceToolOption{ID: "custom", DisplayName: "自定义工具", Available: false, Reason: "示例工具未安装"})
	tester.Frame()
	if err := tester.Click("自定义工具"); err != nil {
		t.Fatal(err)
	}
	if form.selectedToolIDs["custom"] || !tester.HasText("自定义工具（不可用）") {
		t.Fatal("dynamic unavailable option was removed or enabled")
	}
	s.busy = true
	tester.Frame()
	if err := tester.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if !form.selectedToolIDs["codex"] {
		t.Fatal("busy tool toggle should remain disabled")
	}
	s.busy = false
	form.requirements.inputIsQuery = true
	form.requirements.query = "搜索合成需求"
	tester.Frame()
	if tester.HasText(taskCreationLeftTitle) {
		t.Fatal("a search query must not display the previous selected title as its result")
	}
	form.requirements.inputIsQuery = false
	form.requirements.query = link
	form.requirements.selectedTitle = ""
	form.draft.RequirementTitle = nil
	tester.Frame()
	if tester.HasText(taskCreationLeftTitle) || tester.HasText("未读取到需求标题") {
		t.Fatal("missing title fabricated a selected-name hint")
	}
	metadataTitle := "合成元数据返回的完整标题"
	form.draft.RequirementTitle = &metadataTitle
	tester.Frame()
	if !tester.HasText(metadataTitle) || form.requirements.query != link {
		t.Fatal("metadata title must appear below, without replacing, the URL")
	}
}

func TestTaskCreationLeftGenerateInteraction(t *testing.T) {
	s, tester := taskCreationLeftFixture(t, 960, false)
	form := s.taskCreation
	compactCreationEdit(t, tester, "任务名称", "手工合成名称")
	compactCreationEdit(t, tester, "任务分支输入", "feature/manual_fixture")
	generate, _ := tester.Find("生成任务名和分支")
	labelCount := func() int {
		count := 0
		for _, text := range tester.Texts() {
			if text == "生成任务名和分支" {
				count++
			}
		}
		return count
	}
	beforeTooltip := labelCount()
	tester.Move(generate.X+generate.W/2, generate.Y+generate.H/2)
	time.Sleep(650 * time.Millisecond)
	tester.Frame()
	if labelCount() <= beforeTooltip {
		t.Fatal("hover must render the generation tooltip in addition to its accessible label")
	}
	if err := tester.Click("生成任务名和分支"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(form.aiError, "AI 命名服务不可用") || form.draft.TaskName != "手工合成名称" || form.draft.Branch != "feature/manual_fixture" {
		t.Fatal("compact action did not use the existing request/error path or preserve manual fields")
	}
	form.aiError = ""
	if !tester.Focused("生成任务名和分支") {
		t.Fatal("generation action did not retain keyboard focus")
	}
	tester.Key(0, ui.KeyEnter)
	if !strings.Contains(form.aiError, "AI 命名服务不可用") {
		t.Fatal("generation action is not keyboard operable")
	}
	form.aiError, form.aiPending = "", true
	tester.Frame()
	if err := tester.Click("生成任务名和分支"); err != nil {
		t.Fatal(err)
	}
	if form.aiError != "" || !form.aiPending || !tester.HasText("正在获取命名建议") {
		t.Fatal("pending action was enabled or lost the generation feedback")
	}
	form.aiPending, form.aiEligible = false, false
	tester.Frame()
	if _, exists := tester.Find("生成任务名和分支"); exists {
		t.Fatal("ineligible generation retained an empty icon/action slot")
	}
}

func captureTaskCreationLeft(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("SILVERWING_TASK_CREATE_LEFT_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, tester.Image())
	if closeErr := file.Close(); encodeErr != nil || closeErr != nil {
		t.Fatalf("capture: %v / %v", encodeErr, closeErr)
	}
}
