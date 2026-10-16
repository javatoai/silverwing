package desktop

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsAboutReleaseButtonLayout(t *testing.T) {
	for _, width := range []int{960, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := &app{}
				s.snapshot.Home = filepath.Join(t.TempDir(), "synthetic-home")
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(12).Background(p.bg).Children(func() { s.settingsAbout(c, p) })
				}, width, 720)
				row, rowFound := tester.Find("GitHub 仓库操作")
				_, gitlabFound := tester.Find("GitLab 仓库操作")
				release, releaseFound := tester.Find("版本发布")
				open, _ := tester.Find("打开仓库")
				copy, _ := tester.Find("复制地址")
				if gitlabFound || tester.HasText("GitLab") {
					t.Fatal("System About still exposes the removed GitLab repository")
				}
				if !rowFound || !releaseFound || release.W < 90 || release.W > 140 || release.H != 34 || release.X < row.X || release.X+release.W > row.X+row.W || release.Y < row.Y || release.Y+release.H > row.Y+row.H || row.X+row.W > float32(width) {
					t.Fatalf("content-sized release action must belong to GitHub's safe action group: row=%+v release=%+v", row, release)
				}
				if width == 960 && (release.Y != open.Y || release.Y != copy.Y || release.X <= copy.X+copy.W) {
					t.Fatal("wide release action must sit beside open/copy")
				}
				if width == 360 && release.Y <= copy.Y {
					t.Fatal("narrow action group must wrap rather than overflow")
				}
				if tester.HasText("GitHub Releases") || icons["launch"] == nil {
					t.Fatal("old bottom strip remains or the existing external-link icon is unavailable")
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("about-%d-dark-%t", width, dark))
				// Only the adjacent copy action is clicked. The release action keeps
				// its original Shell.OpenExternal URL and is never opened by this test.
				if err := tester.Click("复制地址"); err != nil {
					t.Fatal(err)
				}
				if tester.Clipboard() != "https://github.com/javatoai/silverwing" {
					t.Fatal("moving the release button changed the repository copy action")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused("版本发布") {
					t.Fatal("the native release action lost keyboard focus order")
				}
			})
		}
	}
}
