//go:build !js

package e2e

import (
	"slices"
	"sync"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/internal/wait"
)

// clipboardChanges records the text the clipboard held at each change a
// listener heard.
type clipboardChanges struct {
	mu    sync.Mutex
	texts []string
}

func (c *clipboardChanges) listen() {
	text := mygo.Clipboard.ReadText()
	c.mu.Lock()
	c.texts = append(c.texts, text)
	c.mu.Unlock()
}

func (c *clipboardChanges) heard(text string) func() bool {
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return slices.Contains(c.texts, text)
	}
}

func (c *clipboardChanges) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.texts)
}

func TestClipboardChangeNotifications(t *testing.T) {
	preserveClipboard(t)
	var first, second clipboardChanges
	off := mygo.Clipboard.OnChanged(first.listen)
	t.Cleanup(off)
	mygo.Clipboard.WriteText("clipboard observation 日本語")
	wait.Until(t, "a change with the text written", first.heard("clipboard observation 日本語"))
	mygo.Clipboard.Clear()
	wait.Until(t, "a change for the cleared clipboard", first.heard(""))

	// A listener that stays tells when a change was delivered: once it heard
	// the next one, the listener removed before could have heard it too.
	t.Cleanup(mygo.Clipboard.OnChanged(second.listen))
	off()
	off()
	heard := first.count()
	mygo.Clipboard.WriteText("after unsubscribe")
	wait.Until(t, "the remaining listener to hear the change", second.heard("after unsubscribe"))
	if n := first.count(); n != heard {
		t.Fatalf("a removed listener heard %d more changes", n-heard)
	}
}
