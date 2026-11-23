package desktop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/tools"
)

func TestJSONDiffToolSharesInputsAndCopiesStructuredResult(t *testing.T) {
	s, state, tester := diffToolbarFixture(t, 850, false)
	const left = `{"data":{"timestamp":1,"name":"before","requestId":"a"},"meta":{"timestamp":5}}`
	const right = `{"meta":{"timestamp":7},"data":{"name":"after","timestamp":2,"requestId":"b"}}`
	const ignore = `requestId, $.data.timestamp`
	state.diffLeft, state.diffRight = left, right
	state.diffWrap = false
	state.diffOptions.IgnoreCase = true
	tester.Frame()
	click := func(name string) {
		t.Helper()
		if err := tester.Click(name); err != nil {
			t.Fatal(err)
		}
	}
	click("JSON 模式")
	if !state.diffJSONMode || state.diffHasData || tester.HasText("文本对比操作栏") {
		t.Fatal("switching mode did not clear the prior text diff")
	}
	if state.diffLeft != left || state.diffRight != right || !tester.HasText("左侧 JSON输入") || !tester.HasText("右侧 JSON输入") {
		t.Fatal("JSON mode did not reuse the two source drafts")
	}
	for _, history := range state.diffInputHistory {
		if history.decorations != nil {
			t.Fatal("JSON mode retained text diff decorations")
		}
	}
	click("左侧 JSON输入")
	tester.Key(ui.Cmd, ui.KeyF)
	if !state.diffInputHistory[0].search.open || !tester.HasText("查找文本") {
		t.Fatal("JSON mode lost the native editor find shortcut")
	}
	tester.Key(0, ui.KeyEscape)
	if state.diffInputHistory[0].search.open {
		t.Fatal("Escape did not close the JSON input search")
	}
	before := state.diffRevision
	click("忽略字段输入")
	tester.Type(ignore)
	if state.diffJSONIgnore != ignore || state.diffRevision <= before {
		t.Fatalf("ignore edit did not invalidate the comparison: %q, revision %d", state.diffJSONIgnore, state.diffRevision)
	}
	backend := &diffToolbarCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend = backend
	t.Cleanup(func() { s.cancelDeveloperToolJob("tool.diff") })
	click("比较")
	var action application.Action
	select {
	case action = <-backend.actions:
	case <-time.After(time.Second):
		t.Fatal("JSON compare did not reach the backend")
	}
	if action.Kind != "tool.diff" || action.Payload["mode"] != application.JSONDiffMode || action.Payload["ignore"] != ignore ||
		action.Payload["left"] != left || action.Payload["right"] != right || action.Payload["options"] != nil {
		t.Fatalf("unexpected JSON compare payload: %+v", action)
	}
	result, err := tools.CompareJSON(context.Background(), left, right, ignore)
	if err != nil {
		t.Fatal(err)
	}
	s.cancelDeveloperToolJob("tool.diff")
	state.handleResult(action, application.ActionResult{Data: result})
	tester.Frame()
	for _, value := range []string{"$.data.name", "$.meta.timestamp", `"before"`, `"after"`} {
		if !tester.HasText(value) {
			t.Fatalf("JSON result does not show %q", value)
		}
	}
	if _, found := tester.Find("$.data.timestamp"); found {
		t.Fatal("exactly ignored path appeared in the result")
	}
	click("复制差异")
	const want = "- $.data.name = \"before\"\n+ $.data.name = \"after\"\n- $.meta.timestamp = 5\n+ $.meta.timestamp = 7\n"
	if got := tester.Clipboard(); got != want {
		t.Fatalf("copied diff = %q, want %q", got, want)
	}
	click("文本模式")
	if state.diffJSONMode || state.diffHasData || !state.diffOptions.IgnoreCase || state.diffJSONIgnore != ignore ||
		state.diffLeft != left || state.diffRight != right || !tester.HasText("左侧文本输入") || tester.HasText("JSON 对比操作栏") {
		t.Fatal("returning to text mode did not retain the original tooling and drafts")
	}
	if state.diffInputHistory[0].view.SoftWrap || state.diffInputHistory[1].view.SoftWrap {
		t.Fatal("returning to text mode did not restore the text wrap preference")
	}
}

func TestJSONDiffToolLayoutAndVirtualResults(t *testing.T) {
	_, state, tester := diffToolbarFixture(t, 360, false)
	state.diffJSONMode = true
	state.diffJSONResult.Changed = 800
	for index := range state.diffJSONResult.Changed {
		state.diffJSONResult.Entries = append(state.diffJSONResult.Entries, tools.JSONDiffEntry{
			Path: fmt.Sprintf("$[%d]", index), Kind: tools.JSONDiffChanged, Left: "null", Right: "false",
		})
	}
	for _, width := range []int{360, 1100} {
		tester.SetSize(width, 640)
		for _, name := range []string{"文本模式", "JSON 模式", "比较", "复制差异", "忽略字段输入", "左侧 JSON输入", "右侧 JSON输入", "变更", "路径", "旧值", "新值", "$[0]", "null", "false"} {
			bounds, visible := tester.Find(name)
			if !visible || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.X+bounds.W > float32(width)+.5 || bounds.Y < 0 || bounds.Y+bounds.H > 640 {
				t.Fatalf("width %d clips %q: %+v", width, name, bounds)
			}
		}
		if tester.HasText("$[799]") {
			t.Fatal("virtual result list built offscreen rows")
		}
		captureDesktopTestFrame(t, tester, fmt.Sprintf("json-diff-%d", width))
	}
	state.diffJSONResult = tools.JSONDiffResult{}
	tester.Frame()
	if !tester.HasText("没有结构差异") || tester.HasText("$[0]") {
		t.Fatal("an equal result still shows prior differences")
	}
	const original = "clipboard before clicking disabled copy"
	tester.SetClipboard(original)
	_ = tester.Click("复制差异")
	if tester.Clipboard() != original {
		t.Fatal("empty comparison changed the clipboard")
	}
}
