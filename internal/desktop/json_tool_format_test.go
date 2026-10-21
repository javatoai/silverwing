package desktop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type jsonFormatButtonBackend struct{ actions chan application.Action }

func (*jsonFormatButtonBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *jsonFormatButtonBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	select {
	case b.actions <- action:
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
	// Headless tests deliver the application result on the UI thread below.
	<-ctx.Done()
	return application.ActionResult{}, ctx.Err()
}

func TestJSONToolFormatButtonRetainsCommaAndEditorHistory(t *testing.T) {
	for _, width := range []int{360, 960} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				const source = "{\n  \"a\": \"a\",\n}"
				const want = "{\n    \"a\": \"a\",\n}"
				backend := &jsonFormatButtonBackend{actions: make(chan application.Action, 1)}
				s := &app{backend: backend, page: "tools", toolTab: "JSON", json: source, inputs: map[string]*string{}}
				state := s.ensureToolState()
				state.activeTab = "JSON"
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if err := s.closeDeveloperToolJobs(ctx); err != nil {
						t.Error(err)
					}
					state.cancelWork()
				})
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Padding(10).Children(func() { s.renderJSONTool(c, setTheme(c, dark), state) })
				}, width, 480)
				state.jsonEditor.setPendingSourceSelection(5, 5)
				tester.Frame()
				before, _ := tester.Find("JSON 源码输入")
				selection := state.jsonEditor.lastSelection
				if err := tester.Click("格式化"); err != nil {
					t.Fatal(err)
				}
				var action application.Action
				select {
				case action = <-backend.actions:
				case <-time.After(time.Second):
					t.Fatal("format button did not dispatch")
				}
				if action.Kind != "tool.json" || action.Text != source || payloadString(action.Payload, "operation") != "format" {
					t.Fatalf("format button changed its request: %+v", action)
				}
				result, err := (&application.App{}).Dispatch(context.Background(), action)
				if err != nil {
					t.Fatal(err)
				}
				s.finishDeveloperToolJob(s.toolJobs.current["tool.json"], result, nil)
				tester.Frame()
				after, _ := tester.Find("JSON 源码输入")
				if state.jsonSource != want || !state.jsonValid || before != after ||
					state.jsonEditor.lastSelection.selectionStart != selection.selectionStart ||
					state.jsonEditor.lastSelection.selectionEnd != selection.selectionEnd {
					t.Fatalf("format changed comma, geometry or caret: source=%q before=%+v after=%+v", state.jsonSource, before, after)
				}
				if err := tester.Click("撤销"); err != nil {
					t.Fatal(err)
				}
				state.validateJSON(state.jsonSource)
				if state.jsonSource != source {
					t.Fatal("undo did not restore the exact original trailing-comma input")
				}
				tester.Frame()
				if err := tester.Click("重做"); err != nil {
					t.Fatal(err)
				}
				state.validateJSON(state.jsonSource)
				if state.jsonSource != want || !state.jsonValid {
					t.Fatal("redo did not restore the formatted trailing-comma input")
				}
			})
		}
	}
}
