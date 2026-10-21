package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestRequirementRefreshMenuKeyboardAndLayouts(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{160, 240, 340} {
			t.Run(fmt.Sprintf("dark-%v-width-%d", dark, width), func(t *testing.T) {
				s, _, _ := refreshTestFixture(t)
				s.dark = dark
				s.requirements[0].Title = "较长的需求名称：支付渠道及多节点扩容适配"
				s.requirements[1].Title = "右键目标：需求状态及角色同步"
				s.requirements[0].Status = "技术设计中"
				s.requirements[1].Status = "待测试"
				tester := ui.NewTester(func(c *ui.Context) {
					ui.MenuScope(c, "refresh-layout")
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(8).Gap(5).Background(p.bg).Children(func() {
						for _, item := range s.requirements {
							s.requirementListItem(c, p, item, false)
						}
					})
					ui.ThemedMenus(c, "refresh-layout")
				}, width, 260)
				for _, item := range s.requirements {
					r, ok := tester.Find(item.Title)
					if !ok || r.X < 0 || r.X+r.W > float32(width)+1 || r.H < 35 {
						t.Fatalf("card overflow at %d: %+v", width, r)
					}
				}
				tester.Key(0, ui.KeyTab)
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(s.requirements[1].Title) {
					t.Fatal("requirement card cannot receive keyboard focus")
				}
				tester.Key(ui.Shift, ui.KeyF10)
				if !tester.HasText("刷新") {
					t.Fatal("keyboard context menu missing")
				}
				saveRequirementRefreshImage(t, tester, fmt.Sprintf("menu-dark-%v-%d.png", dark, width))
				tester.Key(0, ui.KeyEscape)
				if tester.HasText("Menu") || len(s.requirementRefreshJobs.current) != 0 || s.selectedRequirement != 0 || s.documentContent != "unsaved draft" {
					t.Fatalf("Esc/menu state: menu=%v jobs=%d selected=%d draft=%q", tester.HasText("Menu"), len(s.requirementRefreshJobs.current), s.selectedRequirement, s.documentContent)
				}
			})
		}
	}
}

func TestRequirementPrimaryCreateButtonStatesAndBehavior(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			s, _, _ := refreshTestFixture(t)
			s.dark = dark
			item := s.requirements[0]
			r := s.requirementReader("", item.URL)
			s.updateRequirementReaderBody(r, application.RequirementContent{Body: "正文", Generation: "cached"})
			s.updateRequirementReaderComments(r, application.RequirementComments{})
			tester := ui.NewTester(func(c *ui.Context) { s.requirementDetail(c, setTheme(c, dark), item) }, 800, 400)
			button, ok := tester.Find("基于需求创建任务")
			if !ok || button.W != 34 || button.H != 34 {
				t.Fatalf("create hit area changed: %+v", button)
			}
			x, y := int(button.X+9), int(button.Y+9)
			normal := tester.Image().RGBAAt(x, y)
			if normal.R != 0x35 || normal.G != 0x6a || normal.B != 0xe6 {
				t.Fatalf("primary background not solid blue: %+v", normal)
			}
			tester.Move(button.X+17, button.Y+17)
			hover := tester.Image().RGBAAt(x, y)
			if hover == normal {
				t.Fatal("missing hover feedback")
			}
			tester.Press(button.X+17, button.Y+17)
			pressed := tester.Image().RGBAAt(x, y)
			if pressed == hover {
				t.Fatal("missing pressed feedback")
			}
			saveRequirementRefreshImage(t, tester, fmt.Sprintf("create-pressed-dark-%v.png", dark))
			tester.Release(button.X+17, button.Y+17)
			if !s.dialogOpen || s.form.kind != "task.create" || s.form.title != "基于需求创建任务" {
				t.Fatal("primary create behavior changed")
			}
			if !tester.Focused("基于需求创建任务") {
				t.Fatal("primary button lost keyboard focus")
			}
			saveRequirementRefreshImage(t, tester, fmt.Sprintf("create-focused-dark-%v.png", dark))
		})
	}
}

func saveRequirementRefreshImage(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	directory := os.Getenv("SILVERWING_REFRESH_CAPTURE")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, tester.Image())
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
