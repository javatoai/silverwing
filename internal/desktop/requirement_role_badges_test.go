package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestRequirementRoleBadgesSummariesAndCompleteNames(t *testing.T) {
	email := " tester@example.invalid "
	badges := requirementRoleBadges(
		[]domain.RequirementPerson{{Name: " 甲 "}, {Name: "乙"}, {Name: "丙"}, {Name: " "}},
		[]domain.RequirementPerson{{Email: &email}}, nil,
	)
	if len(badges) != 2 || badges[0].summary != "研发 3 人" || badges[0].fullText != "研发：甲、乙、丙" ||
		badges[1].summary != "测试：tester@example.invalid" || badges[1].fullText != "测试：tester@example.invalid" {
		t.Fatalf("summary or tooltip lost roles/names: %+v", badges)
	}
	if len(requirementRoleBadges(nil, []domain.RequirementPerson{{Name: " "}}, nil)) != 0 {
		t.Fatal("empty people produced a role badge")
	}
}

func TestRequirementRoleBadgesSharedHeadersAndNarrowBounds(t *testing.T) {
	longName := strings.Repeat("很长的人员姓名", 24)
	for _, example := range []struct {
		name string
		work domain.ParticipatedWorkItem
		want []string
	}{
		{"single", domain.ParticipatedWorkItem{
			Developers: []domain.RequirementPerson{{Name: "张三"}}, QCOwners: []domain.RequirementPerson{{Name: "李四"}}, ProductManagers: []domain.RequirementPerson{{Name: "王五"}},
		}, []string{"研发：张三", "测试：李四", "产品：王五"}},
		{"multiple-and-missing", domain.ParticipatedWorkItem{
			Developers: []domain.RequirementPerson{{Name: "甲"}, {Name: "乙"}}, ProductManagers: []domain.RequirementPerson{{Name: "丙"}, {Name: "丁"}, {Name: "戊"}},
		}, []string{"研发：甲、乙", "产品 3 人"}},
		{"long", domain.ParticipatedWorkItem{
			Developers: []domain.RequirementPerson{{Name: longName}}, QCOwners: []domain.RequirementPerson{{Name: "测试同事"}},
		}, []string{"研发：" + longName, "测试：测试同事"}},
		{"empty", domain.ParticipatedWorkItem{}, nil},
	} {
		for _, width := range []int{140, 360, 1080} {
			for _, dark := range []bool{false, true} {
				for _, host := range []string{"task", "requirement"} {
					t.Run(fmt.Sprintf("%s/%s/%d/dark-%t", example.name, host, width, dark), func(t *testing.T) {
						s := taskRequirementTitleFixture(nil)
						item := s.taskUnit.currentTask()
						metadata := s.requirementUnit.state.taskRequirementMetadata[item.id]
						metadata.Metadata.WorkItem = &example.work
						s.requirementUnit.state.taskRequirementMetadata[item.id] = metadata
						tester := ui.NewTester(func(c *ui.Context) {
							p := setTheme(c, dark)
							ui.Column(c).Fill().Padding(8).Gap(6).Children(func() {
								if host == "task" {
									s.renderTaskRequirementParticipants(c, p, item)
								} else {
									s.renderRequirementMetadataSummary(c, p, application.Requirement{WorkItem: example.work})
								}
								label(c, "后续正文", 12, p.text)
							})
						}, width, 240)
						last := ui.Rect{}
						for _, summary := range example.want {
							bounds, ok := tester.Find(summary)
							if !ok || bounds.W <= 0 || bounds.X < 7 || bounds.X+bounds.W > float32(width-7) || bounds.Y < last.Y || bounds.Y == last.Y && bounds.X <= last.X {
								t.Fatalf("role order or bounds invalid: %q %+v after %+v", summary, bounds, last)
							}
							last = bounds
						}
						if example.name == "single" && width == 140 {
							first, _ := tester.Find(example.want[0])
							if last.Y <= first.Y {
								t.Fatal("narrow header did not wrap role groups")
							}
						}
						if example.name == "empty" {
							body, ok := tester.Find("后续正文")
							if !ok || body.Y > 9 || len(tester.Texts()) != 1 {
								t.Fatal("missing roles left an empty metadata row")
							}
						}
					})
				}
			}
		}
	}
}
