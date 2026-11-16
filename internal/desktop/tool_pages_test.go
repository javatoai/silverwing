package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDeveloperToolVisibilityKeepsOrderAndFallback(t *testing.T) {
	s := &app{}
	s.settingsUnit.state.configJSON = `{"toolVisibility":{"json":true,"dateTime":true,"textDiff":true,"killPort":true}}`
	visible := s.visibleDeveloperToolTabs()
	keys := make([]string, 0, len(visible))
	for _, tab := range visible {
		keys = append(keys, tab.key)
	}
	if want := []string{"JSON", "日期时间", "文本对比", "端口", "Base64", "二维码", "文字计数"}; !slices.Equal(keys, want) {
		t.Fatalf("old config should keep the tools in order and show new tabs by default: %v", keys)
	}
	if got := reconcileDeveloperToolTab("二维码", visible); got != "二维码" {
		t.Fatalf("visible active tab changed to %q", got)
	}
	s.settingsUnit.state.configJSON = `{"toolVisibility":{"base64":true,"qrCode":false,"textCount":false}}`
	visible = s.visibleDeveloperToolTabs()
	if got := reconcileDeveloperToolTab("二维码", visible); got != "JSON" {
		t.Fatalf("hiding trailing tabs should wrap to the first visible tab: %q", got)
	}
	if got := reconcileDeveloperToolTab("端口", visible); got != "Base64" {
		t.Fatalf("hiding a middle tab should select the next visible tab: %q", got)
	}
	if got := reconcileDeveloperToolTab("Base64", nil); got != "" {
		t.Fatalf("there is an active tab with every tool disabled: %q", got)
	}
}

func TestDeveloperToolTabsScrollWithStatusInView(t *testing.T) {
	const width = 420
	s := &app{}
	state := s.ensureToolState()
	state.activeTab = "端口"
	state.portStoppingPID = 2147483647
	statusText := s.developerToolStatus(state.activeTab)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			s.renderDeveloperToolTabs(c, setTheme(c, false), state, developerToolTabs, false)
		})
	}, width, 200)
	rail, railFound := tester.Find("研发工具标签")
	status, statusFound := tester.Find(statusText)
	last, lastFound := tester.Find("文字计数")
	if !railFound || !statusFound || !lastFound || rail.W <= 0 || last.W > 0 || status.W <= 0 || status.X < rail.X+rail.W || status.X+status.W > width {
		t.Fatalf("unexpected narrow tab layout: rail=%+v status=%+v last=%+v", rail, status, last)
	}
	x, y := rail.X+rail.W/2, rail.Y+rail.H/2
	tester.Scroll(x, y, 1000, 0)
	last, _ = tester.Find("文字计数")
	if last.W < 90 || last.X < rail.X || last.X+last.W > rail.X+rail.W {
		t.Fatalf("horizontal scrolling did not fully reveal the last tab: rail=%+v last=%+v", rail, last)
	}
	if after, _ := tester.Find(statusText); after != status {
		t.Fatalf("scrolling moved the status: before=%+v after=%+v", status, after)
	}
	tester.Scroll(x, y, -1000, 0)
	for range developerToolTabs {
		tester.Key(0, ui.KeyTab)
	}
	if last, _ := tester.Find("文字计数"); !tester.Focused("文字计数") || last.W < 90 {
		t.Fatalf("keyboard focus did not reveal the last tab: %+v", last)
	}
	tester.Key(0, ui.KeySpace)
	if state.activeTab != "文字计数" {
		t.Fatalf("keyboard did not activate focused tab: %q", state.activeTab)
	}
}

func TestDeveloperToolVisibilitySettingsSearch(t *testing.T) {
	for query, title := range map[string]string{"base64": "Base64", "qrcode": "二维码", "汉字数": "文字计数"} {
		matches := settingsSearch(query)
		if len(matches) == 0 || matches[0].title != title || matches[0].page != "tool-visibility" {
			t.Fatalf("%q did not find the %q settings switch: %v", query, title, matches)
		}
	}
}
