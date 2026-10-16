package desktop

import (
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/tools"
)

func TestTimeToolNumericCopyRoundTripAndFriendlyError(t *testing.T) {
	state := newToolPageState("")
	t.Cleanup(state.cancelWork)
	state.activeTab, state.timeZone, state.timeUnit = "日期时间", "Asia/Shanghai", "AUTO"
	s := &app{page: "tools", tools: state}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() { s.renderTimeTool(c, setTheme(c, false), state) })
	}, 980, 720)
	if err := tester.Click("复制日期"); err != nil {
		t.Fatal(err)
	}
	copied := tester.Clipboard()
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}$`).MatchString(copied) || !tester.HasText(copied) {
		t.Fatalf("current date display and copied value differ or retain Chinese literals: %q", copied)
	}
	roundTrip, err := tools.ConvertTime(copied, "AUTO", state.timeZone)
	if err != nil || roundTrip.Formatted != copied {
		t.Fatalf("automatically copied date cannot be parsed: %+v %v", roundTrip, err)
	}
	for _, timestamp := range []string{"1792045773", "1792045773001"} {
		result, err := tools.ConvertTime(timestamp, "AUTO", state.timeZone)
		if err != nil {
			t.Fatal(err)
		}
		state.timeInput, state.timeResult, state.timeHasData = timestamp, result, true
		tester.Frame()
		if !tester.HasText(result.Formatted) {
			t.Fatal("converted numeric date was reformatted differently in the UI")
		}
		if err := tester.Click("复制日期时间"); err != nil {
			t.Fatal(err)
		}
		if tester.Clipboard() != result.Formatted {
			t.Fatalf("converted copy changed seconds/millisecond precision: %q", tester.Clipboard())
		}
	}
	_, err = tools.ConvertTime("2026年02月30日 12:34:56.001", "AUTO", state.timeZone)
	if err == nil {
		t.Fatal("invalid date was accepted")
	}
	state.timeError, state.timeHasData = err.Error(), false
	tester.Frame()
	if !tester.HasText("无法识别日期时间，请输入有效日期或时间戳") {
		t.Fatal("user-facing Chinese error missing")
	}
	for _, text := range tester.Texts() {
		if strings.Contains(text, "parsing time") || strings.Contains(text, `\x`) || strings.Contains(text, "cannot parse") {
			t.Fatalf("Go parser internals leaked into the UI: %q", text)
		}
	}
}
