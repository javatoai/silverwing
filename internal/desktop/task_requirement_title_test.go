package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func taskRequirementTitleTester(s *app, width int, dark, reference bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Padding(16).Gap(6).Children(func() {
			if reference {
				// The original static title row is the geometry reference.
				title := strings.TrimSpace(*s.requirementUnit.state.taskRequirementMetadata[s.taskUnit.currentTask().id].Metadata.Title)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					label(c, title, 14, p.muted).FontWeight(500).Grow(1).SingleLine()
				})
			} else {
				s.renderTaskRequirementMetadata(c, p, s.taskUnit.currentTask())
			}
			label(c, "后续内容", 12, p.text)
		})
	}, width, 220)
}

func taskRequirementTitleFixture(title *string) *app {
	item := task{id: "fixture-task", title: "任务短名", requirementTitle: "不能代替原需求标题的任务缓存短名", requirementURL: "https://fixture.invalid/requirement/1"}
	return &app{
		taskUnit: taskController{state: taskState{tasks: []task{item}}},
		requirementUnit: requirementController{state: requirementState{taskRequirementMetadata: map[string]taskRequirementMetadataState{
			item.id: {URL: item.requirementURL, Status: requirementMetadataLoaded, Metadata: requirementMetadata{Title: title}},
		}}},
	}
}

func TestTaskRequirementTitleCopiesCompleteOriginal(t *testing.T) {
	full := "  【研发-技改-S】TNS 服务多节点扩容适配，保障消息消费及共享业务数据的并发安全 · " + strings.Repeat("完整长中文需求名称不会因界面省略而截断，", 18) + "结束  "
	s := taskRequirementTitleFixture(&full)
	tester := taskRequirementTitleTester(s, 360, false, false)
	tester.SetClipboard("untouched")
	if err := tester.Click("复制需求名称"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != full || s.lastMessage != "已复制需求名称" {
		t.Fatalf("title copy used truncated/short text or lost feedback: clipboard length=%d feedback=%q", len(tester.Clipboard()), s.lastMessage)
	}
	keyboard := taskRequirementTitleTester(s, 360, false, false)
	keyboard.SetClipboard("keyboard-untouched")
	keyboard.Key(0, ui.KeyTab)
	if !keyboard.Focused("复制需求名称") {
		t.Fatal("title copy cannot receive keyboard focus")
	}
	keyboard.Key(0, ui.KeyEnter)
	if keyboard.Clipboard() != full {
		t.Fatal("Enter did not copy the complete original title")
	}
}

func TestTaskRequirementTitlePreservesStaticLayout(t *testing.T) {
	full := "【研发-技改-S】" + strings.Repeat("完整中文需求标题", 24)
	for _, width := range []int{360, 1000} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := taskRequirementTitleFixture(&full)
				actual, reference := taskRequirementTitleTester(s, width, dark, false), taskRequirementTitleTester(s, width, dark, true)
				for _, label := range []string{full, "后续内容"} {
					got, ok := actual.Find(label)
					want, wantOK := reference.Find(label)
					if !ok || !wantOK || math.Abs(float64(got.X-want.X))+math.Abs(float64(got.Y-want.Y))+math.Abs(float64(got.W-want.W))+math.Abs(float64(got.H-want.H)) > .1 {
						t.Fatalf("copy affordance changed static title layout: got=%+v want=%+v", got, want)
					}
				}
			})
		}
	}
}

func TestTaskRequirementTitleEmptyAndPlaceholderCannotCopy(t *testing.T) {
	for _, status := range []requirementMetadataState{requirementMetadataNotLoaded, requirementMetadataLoading, requirementMetadataFailed} {
		t.Run(string(status), func(t *testing.T) {
			blank := "  "
			s := taskRequirementTitleFixture(&blank)
			state := s.requirementUnit.state.taskRequirementMetadata["fixture-task"]
			state.Status = status
			s.requirementUnit.state.taskRequirementMetadata["fixture-task"] = state
			tester := taskRequirementTitleTester(s, 360, false, false)
			tester.SetClipboard("unchanged")
			if _, ok := tester.Find("复制需求名称"); ok {
				_ = tester.Click("复制需求名称")
			}
			tester.Key(0, ui.KeyTab)
			tester.Key(0, ui.KeyEnter)
			if tester.Clipboard() != "unchanged" || s.lastMessage != "" {
				t.Fatal("an empty title/loading/error placeholder copied a short task name")
			}
		})
	}
	full := "另一个 URL 的旧标题"
	s := taskRequirementTitleFixture(&full)
	s.taskUnit.state.tasks[0].requirementURL = "https://fixture.invalid/requirement/2"
	tester := taskRequirementTitleTester(s, 360, false, false)
	if _, ok := tester.Find("复制需求名称"); ok {
		t.Fatal("stale metadata for another requirement URL became copyable")
	}
}

func TestTaskRequirementTitleTooltip(t *testing.T) {
	full := "【研发-技改-S】完整的原始需求名称"
	s := taskRequirementTitleFixture(&full)
	tester := taskRequirementTitleTester(s, 600, false, false)
	r, ok := tester.Find("复制需求名称")
	if !ok {
		t.Fatal("title action missing")
	}
	tester.Move(r.X+r.W/2, r.Y+r.H/2)
	time.Sleep(650 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("点击复制需求名称") || !tester.HasText(full) {
		t.Fatal("hover did not expose the copy instruction and complete name")
	}
}
