package markdown

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDefaultCodeBlockCopyHookPreservesNativeLayoutAndFailureFeedback(t *testing.T) {
	source := "```go\nraw()\n```\n"
	tester, _ := rvView(&source, nil)
	tester.SetLocale("en")
	if err := tester.Click("Copy code"); err != nil || tester.Clipboard() != "raw()\n" || !tester.HasText("Copied") {
		t.Fatalf("nil hook changed default code copy: %v, clipboard=%q", err, tester.Clipboard())
	}

	succeed, calls := true, 0
	options := &Options{CopyCode: func(c *ui.Context, block *CodeBlockView) bool {
		calls++
		if block.Language != "go" || block.Code() != "raw()\n" {
			t.Fatal("native code metadata was lost")
		}
		if succeed {
			c.WriteClipboard("original()\r\n")
		}
		return succeed
	}}
	tester, _ = rvView(&source, options)
	tester.SetLocale("en")
	if !tester.HasText("Code, go") || !tester.HasText("raw()") {
		t.Fatal("copy hook replaced the native code layout")
	}
	if err := tester.Click("Copy code"); err != nil || tester.Clipboard() != "original()\r\n" || !tester.HasText("Copied") {
		t.Fatalf("successful host copy lost original bytes or native feedback: %v", err)
	}
	succeed = false
	tester.SetClipboard("keep")
	if err := tester.Click("Copy code"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.Clipboard() != "keep" || tester.HasText("Copied") || calls != 2 {
		t.Fatal("failed host copy changed the clipboard or kept the old success indicator")
	}
}
