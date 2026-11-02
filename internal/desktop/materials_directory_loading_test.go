package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestMaterialsDirectoryLoadingAcrossTaskSwitch(t *testing.T) {
	root := t.TempDir()
	s := newApp(&annualMaterialsUIBackend{}, Options{Page: "tasks", Tab: 1})
	s.loading = false
	s.taskUnit.state.tasks = []task{
		{id: "first", path: filepath.Join(root, "first"), materialsPath: filepath.Join(root, "first", "materials"), materialsStatus: "READY"},
		{id: "second", path: filepath.Join(root, "second"), materialsPath: filepath.Join(root, "second", "materials"), materialsStatus: "READY"},
	}
	s.materialsUnit.state.documentsLoaded = true
	s.materialsUnit.replaceDocuments([]application.Document{{Name: "研发", Path: filepath.Join(s.taskUnit.currentTask().materialsPath, "研发"), Kind: "directory"}})
	tester := ui.NewTester(func(c *ui.Context) { s.documentTree(c, setTheme(c, false), 248) }, 248, 400)
	if !tester.HasText("研发") {
		t.Fatal("initial task contents missing")
	}
	if !s.selectTaskIndex(1) {
		t.Fatalf("switch failed: %s", s.lastError)
	}
	tester.Frame()
	if !tester.HasText("正在加载资料目录…") || !tester.HasText("加载中") || tester.HasText("(0)") || tester.HasText("此任务资料目录为空。") || tester.HasText("研发") {
		t.Fatal("new task must show loading before its first request, without the old tree or an empty-directory claim")
	}
	s.materialsUnit.state.documentsRequested = true
	tester.Frame()
	if !tester.HasText("正在加载资料目录…") || tester.HasText("此任务资料目录为空。") {
		t.Fatal("in-flight directory query was displayed as empty")
	}
	// A late response for the previous task cannot settle the current task.
	s.applyMaterialsActionResult(application.Action{Kind: "document.list", TaskID: "first"}, application.ActionResult{Data: []application.Document{}})
	tester.Frame()
	if !tester.HasText("正在加载资料目录…") {
		t.Fatal("stale task response dismissed the current loading state")
	}
	s.applyMaterialsActionResult(application.Action{Kind: "document.list", TaskID: "second"}, application.ActionResult{Data: []application.Document{}})
	tester.Frame()
	if !tester.HasText("此任务资料目录为空。") || tester.HasText("正在加载资料目录…") {
		t.Fatal("completed empty directory did not replace loading")
	}
	// Refresh an already empty directory: it is pending again until settled.
	s.materialsUnit.state.documentsRequested = true
	tester.Frame()
	if !tester.HasText("正在加载资料目录…") || tester.HasText("此任务资料目录为空。") {
		t.Fatal("refreshing an empty result still claimed the directory was empty")
	}
	s.applyMaterialsActionResult(application.Action{Kind: "document.list", TaskID: "second"}, application.ActionResult{Data: []application.Document{{Name: "temp", Path: filepath.Join(s.taskUnit.currentTask().materialsPath, "temp"), Kind: "directory"}}})
	tester.Frame()
	if !tester.HasText("temp") || tester.HasText("正在加载资料目录…") || tester.HasText("此任务资料目录为空。") {
		t.Fatal("loaded contents did not replace the pending placeholder")
	}
	// An explicit refresh keeps usable rows in place.
	s.materialsUnit.state.documentsLoaded = false
	s.materialsUnit.state.documentsRequested = true
	tester.Frame()
	if !tester.HasText("temp") || tester.HasText("正在加载资料目录…") {
		t.Fatal("refresh hid existing directory contents")
	}
}
