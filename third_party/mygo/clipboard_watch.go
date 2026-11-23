package mygo

// clipboardChangeWatch is the one native watch the listeners of
// Clipboard.OnChanged share. All but listeners is main-thread only.
type clipboardChangeWatch struct {
	listeners             listeners[func()]
	stop                  func()
	generation            uint64
	dirty, queued, firing bool
	// hooked tells that the clipboard's operations and the app's quit
	// reach the watch: only apps that observe the clipboard link it.
	hooked bool
}

var clipboardChanges clipboardChangeWatch

// OnChanged is called on the main thread after the system clipboard
// changes, through this app or another one. It does not report the
// contents the clipboard held when the listener was added, and reads none
// of them: use Read or Formats in fn. Changes close together may be
// coalesced; a call is a prompt to read what the clipboard holds now, not
// a history of copies, and the contents may change again before they are
// read.
//
// Windows and Linux use native notifications. macOS checks the pasteboard's
// change count every 250 ms, only while a listener is registered. On Linux
// observation depends on the desktop exposing clipboard ownership changes.
// Where the system clipboard cannot be observed, as in browsers, fn is
// never called.
//
// off removes the listener, and the last one to go stops the native
// observation. A listener removed while a change is delivered may still
// hear that one. Listeners may read and write the clipboard; changes
// during a call are delivered after it returns.
func (ClipboardModule) OnChanged(fn func()) (off func()) {
	w := &clipboardChanges
	remove := w.listeners.add(fn, false)
	w.update()
	return func() {
		remove()
		w.update()
	}
}

// update brings the native watch in line with the listeners, from any
// goroutine: at once on the main thread, so that a listener hears what the
// code after OnChanged copies, and once the app is ready before that.
func (w *clipboardChangeWatch) update() {
	if !App.IsReady() {
		App.WhenReady(w.sync)
		return
	}
	onMain(w.sync)
}

// sync starts the native watch for the first listener and stops it after
// the last one. Neither touches what the clipboard holds, so a clipboard
// provider may add and remove listeners too.
func (w *clipboardChangeWatch) sync() {
	if clipboardStopped {
		return
	}
	if !w.hooked {
		w.hooked = true
		afterClipboardOperations = w.schedule
		App.OnQuit(w.close)
	}
	switch n := w.listeners.len(); {
	case n == 0 && w.stop != nil:
		w.halt()
	case n != 0 && w.stop == nil:
		w.generation++
		generation := w.generation
		// A backend that cannot observe the clipboard leaves the listeners
		// uncalled.
		stop, err := backend().Clipboard().WatchChanges(func() {
			if generation == w.generation {
				w.dirty = true
				w.schedule()
			}
		})
		if err == nil {
			w.stop = stop
		}
	}
}

func (w *clipboardChangeWatch) schedule() {
	if !w.dirty || w.queued || w.firing || clipboardOperations != 0 || clipboardStopped || w.stop == nil {
		return
	}
	w.queued = true
	generation := w.generation
	postMain(func() {
		if generation != w.generation {
			return
		}
		w.queued = false
		if clipboardOperations != 0 || w.firing || clipboardStopped {
			return // the operation/callback finishing schedules the pending change
		}
		w.dirty, w.firing = false, true
		defer func() { w.firing = false; w.schedule() }()
		for _, fn := range w.listeners.snapshot() {
			if generation != w.generation {
				return // a listener removed the last ones, or the app quit
			}
			fn()
		}
	})
}

// halt stops the native watch and drops the change it had yet to deliver.
func (w *clipboardChangeWatch) halt() {
	w.generation++
	w.dirty, w.queued = false, false
	if w.stop != nil {
		stop := w.stop
		w.stop = nil
		stop()
	}
}

// close ends the observation when the app quits.
func (w *clipboardChangeWatch) close() {
	w.listeners.clear()
	w.halt()
}
