package desktop

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestMaterialsPreviewFallbackExplicitClickAndBusy(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				calls, busy := 0, false
				tester := ui.NewTester(func(c *ui.Context) {
					materialsPreviewFallback(c, setTheme(c, dark), "中文 名称.unknown", "此格式暂不支持预览", busy, func() { calls++ })
				}, width, 650)
				if calls != 0 || !tester.HasText("中文 名称.unknown") {
					t.Fatal("render opened a file or omitted its name")
				}
				if err := tester.Click("使用系统应用打开"); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatal("explicit click did not open exactly once")
				}
				busy = true
				tester.Frame()
				_ = tester.Click("使用系统应用打开")
				if calls != 1 {
					t.Fatal("busy control launched again")
				}
			})
		}
	}
}

func TestMaterialsExternalOpenCapturesIdentityAndRejectsDuplicate(t *testing.T) {
	s, _, _ := materialsTextPreviewFixture(t, "中文 资料.unknown", []byte{0, 1})
	s.snapshot.Home = t.TempDir()
	s.taskUnit.state.tasks[0].createdAt = "original"
	action := application.Action{Kind: "document.open", TaskID: s.taskUnit.state.tasks[0].id, Path: s.materialsUnit.state.documentPath}
	request, err := s.beginSystemOpenJob(action)
	if err != nil || request == nil {
		t.Fatalf("begin: %v", err)
	}
	defer s.completeSystemOpenJob(request)
	if request.action.Payload["draftTaskPath"] != s.taskUnit.state.tasks[0].path || request.action.Payload["materialsHome"] != s.snapshot.Home || request.action.Payload["desktopRenameTaskCreatedAt"] != "original" {
		t.Fatal("missing backend identity guards")
	}
	if duplicate, err := s.beginSystemOpenJob(action); err != nil || duplicate != nil {
		t.Fatal("duplicate launched")
	}
	if !s.systemOpenJobRelevant(request) {
		t.Fatal("current open rejected")
	}
	s.settingsUnit.state.configJSON = `{"changed":true}`
	if s.systemOpenJobRelevant(request) {
		t.Fatal("late result applied after configuration change")
	}
	s.settingsUnit.state.configJSON = ""
	s.taskUnit.state.tasks[0].materialsPath = filepath.Join(t.TempDir(), "changed")
	if s.systemOpenJobRelevant(request) {
		t.Fatal("late result applied to new materials directory")
	}
}

func TestFailedMediaUsesSharedMaterialsFallback(t *testing.T) {
	for _, kind := range []string{"image", "pdf"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := materialsTextPreviewFixture(t, "broken."+kind, []byte{0, 1})
			s.materialsUnit.state.nativeMedia = &nativeMediaState{kind: kind, path: s.materialsUnit.state.documentPath, previewError: "文件损坏"}
			tester := ui.NewTester(func(c *ui.Context) {
				s.renderNativeMedia(c, setTheme(c, false), ui.Rect{W: 960, H: 650})
			}, 960, 650)
			if !tester.HasText("使用系统应用打开") || !tester.HasText("无法预览此文件：文件损坏") {
				t.Fatal("media failure omitted shared fallback")
			}
			if s.systemOpenJobs != nil {
				t.Fatal("render started an external open")
			}
		})
	}
}
