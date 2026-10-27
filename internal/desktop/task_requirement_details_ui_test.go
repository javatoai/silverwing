package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTaskRequirementDetailTitleSources(t *testing.T) {
	const link = "https://fixture.invalid/requirement/1001"
	for _, test := range []struct {
		name, metadataTitle, metadataURL, listTitle, workItemTitle, listURL, savedTitle, want string
		status                                                                                requirementMetadataState
		noLink                                                                                bool
	}{
		{name: "metadata-first", metadataTitle: "  元数据原始需求名  ", listTitle: "列表需求名", savedTitle: "已保存需求名", want: "元数据原始需求名"},
		{name: "retained-title-during-refresh", metadataTitle: "刷新前完整需求名", status: requirementMetadataLoading, want: "刷新前完整需求名"},
		{name: "list-after-stale-metadata", metadataTitle: "另一个需求名", metadataURL: link + "/other", listTitle: "当前需求名", want: "当前需求名"},
		{name: "list-before-saved-title", listTitle: "列表原始需求名", savedTitle: "已保存需求名", want: "列表原始需求名"},
		{name: "work-item-title", listTitle: " ", workItemTitle: "原始工作项名称", want: "原始工作项名称"},
		{name: "saved-offline-title", savedTitle: "  已保存原始需求名  ", status: requirementMetadataFailed, want: "已保存原始需求名"},
		{name: "unmatched-list-is-rejected", listTitle: "另一个列表需求名", listURL: link + "/other", savedTitle: "已保存需求名", want: "已保存需求名"},
		{name: "not-loaded-does-not-use-folder", status: requirementMetadataNotLoaded, want: "需求详情"},
		{name: "loading-does-not-use-folder", status: requirementMetadataLoading, want: "获取需求信息中"},
		{name: "failed-does-not-use-folder", status: requirementMetadataFailed, want: "未获取需求名称"},
		{name: "loaded-without-title", status: requirementMetadataLoaded, want: "未获取需求名称"},
		{name: "no-bound-requirement", noLink: true, metadataTitle: "旧需求名", savedTitle: "旧需求名", want: "需求详情"},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := task{id: "task", title: "1001-task-folder", requirementURL: link, requirementTitle: test.savedTitle}
			state := taskRequirementMetadataState{URL: firstNonEmpty(test.metadataURL, link), Status: test.status}
			if test.metadataTitle != "" {
				state.Metadata.Title = &test.metadataTitle
			}
			s := &app{taskRequirementMetadata: map[string]taskRequirementMetadataState{item.id: state}}
			if test.listTitle != "" || test.workItemTitle != "" {
				s.requirements = []application.Requirement{{Title: test.listTitle, URL: firstNonEmpty(test.listURL, link),
					WorkItem: domain.ParticipatedWorkItem{ID: "1001", Title: test.workItemTitle}}}
			}
			if test.noLink {
				item.requirementURL = ""
			}
			if got := s.taskRequirementDetailTitle(item); got != test.want {
				t.Fatalf("detail title=%q, want %q", got, test.want)
			}
		})
	}
}

func TestTaskRequirementRolesMoveToRequirementDetails(t *testing.T) {
	fullTitle := "  【研发需求】" + strings.Repeat("完整原始需求名称", 10) + "  "
	for _, width := range []int{360, 1080} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := taskRequirementTitleFixture(&fullTitle)
				s.configJSON, s.inputs = "{}", map[string]*string{}
				item := s.currentTask()
				state := s.taskRequirementMetadata[item.id]
				state.Metadata.Participants = domain.RequirementParticipants{
					QCOwners: []domain.RequirementPerson{{Name: "测试同事"}}, ProductManagers: []domain.RequirementPerson{{Name: "产品同事"}},
				}
				s.taskRequirementMetadata[item.id] = state
				header := ui.NewTester(func(c *ui.Context) { s.taskHeader(c, setTheme(c, dark)) }, width, 220)
				for _, removed := range []string{"复制需求编号", "测试同事", "产品同事"} {
					if header.HasText(removed) {
						t.Fatalf("task header still exposes moved content %q", removed)
					}
				}
				if err := header.Click("复制需求名称"); err != nil || header.Clipboard() != fullTitle {
					t.Fatalf("task header lost complete title copy: err=%v clipboard=%q", err, header.Clipboard())
				}
				if !header.HasText(item.requirementURL) {
					t.Fatal("task header lost the requirement URL")
				}
				if err := header.Click("复制需求链接"); err != nil || header.Clipboard() != item.requirementURL {
					t.Fatalf("task header lost complete URL copy: err=%v clipboard=%q", err, header.Clipboard())
				}
				reader := s.requirementReader(item.id, item.requirementURL)
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "## 需求正文\n\n需求真实名称与角色在此集中展示。", Complete: true})
				s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "comment", AuthorName: "需求讨论成员", CreatedAt: "2026-10-16 10:00", Content: "原有正文和评论阅读继续保留。"}}})
				t.Cleanup(s.closeRequirementReaders)
				detail := ui.NewTester(func(c *ui.Context) { s.requirementDetails(c, setTheme(c, dark)) }, width, 720)
				if !detail.HasText(strings.TrimSpace(fullTitle)) || detail.HasText(item.title) || detail.HasText(item.requirementTitle) {
					t.Fatal("requirement detail did not use the real metadata title")
				}
				for _, role := range []string{"测试：测试同事", "产品：产品同事"} {
					count := 0
					for _, text := range detail.Texts() {
						if text == role {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("role %q rendered %d times in requirement detail", role, count)
					}
				}
				captureDesktopTestFrame(t, detail, fmt.Sprintf("requirement-detail-roles-%d-dark-%t", width, dark))
				if err := detail.Click("复制需求链接"); err != nil || detail.Clipboard() != item.requirementURL {
					t.Fatalf("requirement detail lost URL copy: err=%v clipboard=%q", err, detail.Clipboard())
				}
			})
		}
	}
}

func TestTaskRequirementParticipantsRejectDifferentURL(t *testing.T) {
	fullTitle := "真实需求名"
	s := taskRequirementTitleFixture(&fullTitle)
	item := s.currentTask()
	state := s.taskRequirementMetadata[item.id]
	state.URL += "/other"
	state.Metadata.Participants.QCOwners = []domain.RequirementPerson{{Name: "其他需求的测试"}}
	s.taskRequirementMetadata[item.id] = state
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderTaskRequirementParticipants(c, setTheme(c, false), item)
	}, 600, 200)
	if tester.HasText("其他需求的测试") {
		t.Fatal("participants from another requirement URL leaked into the detail")
	}
}
