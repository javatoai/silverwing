package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/tools"
)

func newTextUtilityTester(t *testing.T, tab string, width int) (*app, *toolPageState, *ui.Tester) {
	t.Helper()
	state := &toolPageState{activeTab: tab}
	s := &app{page: "tools", toolsUnit: toolsController{state: toolsState{tools: state}}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderDeveloperTools(c) })
		s.syncDeveloperToolJobVisibility()
	}, width, 600)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.toolsUnit.closeDeveloperToolJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, state, tester
}

func waitTextUtility(t *testing.T, s *app, tester *ui.Tester) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.toolsUnit.state.toolJobs.lifecycle.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}

func TestBase64UtilityRoundTripCopyAndInvalidation(t *testing.T) {
	s, state, tester := newTextUtilityTester(t, "Base64", 680)
	b := &state.base64Tool
	const input = "汉\r\n😀"
	b.input.Buffer.Set(input)
	tester.Frame()
	click := func(label string) {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("URL-safe")
	click("编码")
	waitTextUtility(t, s, tester)
	want, err := tools.EncodeBase64(input, true, true)
	if err != nil || b.result != want || !b.output.ReadOnly {
		t.Fatalf("encoded = %q, want %q, err = %v", b.result, want, err)
	}
	click("结果用作输入")
	if b.input.Buffer.OriginalText() != want || b.result != "" {
		t.Fatal("reusing the result did not replace input and invalidate output")
	}
	click("解码")
	waitTextUtility(t, s, tester)
	click("复制结果")
	if got := tester.Clipboard(); got != input {
		t.Fatalf("decoded clipboard = %q, want %q", got, input)
	}
	b.input.Buffer.Set("$broken")
	tester.Frame()
	click("解码")
	waitTextUtility(t, s, tester)
	if b.result != "" || b.output.Buffer.Len() != 0 || !tester.HasText("无效的 Base64，请检查字符和末尾填充") {
		t.Fatalf("invalid input left stale output or no error: %q, %q", b.result, b.err)
	}
}

func TestBase64UtilityWideEditorsAcceptNativePaste(t *testing.T) {
	_, state, tester := newTextUtilityTester(t, "Base64", 960)
	for _, name := range []string{"Base64 输入", "Base64 结果"} {
		if rect, found := tester.Find(name); !found || rect.W <= 0 || rect.H <= 0 {
			t.Fatalf("%s is not laid out: %+v", name, rect)
		}
	}
	if err := tester.Click("Base64 输入"); err != nil {
		t.Fatal(err)
	}
	const source = "中文😀\r\n"
	tester.SetClipboard(source)
	tester.Key(ui.Cmd, ui.KeyV)
	if got := state.base64Tool.input.Buffer.OriginalText(); got != source {
		t.Fatalf("pasted %q, want %q", got, source)
	}
}

func TestTextCountUtilityCoalescesEditsAndClears(t *testing.T) {
	s, state, tester := newTextUtilityTester(t, "文字计数", 960)
	count := &state.textCountTool
	count.input.Buffer.Set("旧统计")
	tester.Frame()
	tester.Advance(textCountDelay / 2)
	source := "汉 e\u0301👩🏽‍💻\r\n"
	count.input.Buffer.Set(source)
	tester.Frame()
	tester.Advance(textCountDelay / 2)
	if count.work.lease != nil || !count.dirty {
		t.Fatal("an older input started a count before the latest debounce elapsed")
	}
	tester.Advance(textCountDelay / 2)
	waitTextUtility(t, s, tester)
	want := tools.TextCounts{Characters: 5, NonWhitespace: 3, Han: 1, Lines: 2, Bytes: len(source)}
	if count.result != want || count.dirty {
		t.Fatalf("count = %+v, want %+v", count.result, want)
	}
	if err := tester.Click("清空"); err != nil {
		t.Fatal(err)
	}
	if count.result != (tools.TextCounts{}) || count.dirty || count.input.Buffer.Len() != 0 {
		t.Fatal("clearing the editor did not reset all metrics")
	}
}
