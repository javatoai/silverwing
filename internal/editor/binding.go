package editor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type FlushAction func(func(bool))

// Binding owns the editable source between the native input widget and its
// host. The host remains authoritative for file persistence and conflict
// policy; acknowledgements are matched before external content is accepted.
type Binding struct {
	mu             sync.Mutex
	id             string
	content        string
	revision       uint64
	editing        bool
	composing      bool
	sourceMode     bool
	fallback       string
	dirty          bool
	clientRevision int64
	owner          any
	flush          FlushAction
	pendingContent *string
	onChange       func(string)
	onSave         func()
	onComposition  func(bool)
}

type BindingState struct {
	ID             string `json:"id"`
	Content        string `json:"content"`
	Revision       uint64 `json:"revision"`
	Editing        bool   `json:"editing"`
	Composing      bool   `json:"composing"`
	SourceMode     bool   `json:"sourceMode"`
	Fallback       string `json:"fallbackMessage,omitempty"`
	Dirty          bool   `json:"dirty"`
	ClientRevision int64  `json:"clientRevision"`
}

func NewBinding(id, initialContent string, onChange func(string), onSave func(), onComposition func(bool)) *Binding {
	if id == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err == nil {
			id = hex.EncodeToString(random[:])
		}
	}
	return &Binding{id: id, content: initialContent, editing: true, clientRevision: -1,
		onChange: onChange, onSave: onSave, onComposition: onComposition}
}

func (b *Binding) State() BindingState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BindingState{ID: b.id, Content: b.content, Revision: b.revision, Editing: b.editing,
		Composing: b.composing, SourceMode: b.sourceMode, Fallback: b.fallback, Dirty: b.dirty, ClientRevision: b.clientRevision}
}

func (b *Binding) Sync(value string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingContent != nil {
		if value != *b.pendingContent {
			return
		}
		b.pendingContent = nil
	}
	if b.content == value {
		return
	}
	b.content = value
	b.revision++
}

func (b *Binding) Bind(owner any, flush FlushAction) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner != owner {
		b.clientRevision = -1
	}
	b.owner, b.flush = owner, flush
}

func (b *Binding) Unbind(owner any) {
	b.mu.Lock()
	if b.owner != owner {
		b.mu.Unlock()
		return
	}
	b.owner, b.flush = nil, nil
	b.mu.Unlock()
	b.Composition(false)
}

func (b *Binding) Change(owner any, sequence int64, value string) bool {
	b.mu.Lock()
	if b.owner != owner || sequence <= b.clientRevision {
		b.mu.Unlock()
		return false
	}
	b.clientRevision = sequence
	b.mu.Unlock()
	return b.Update(value)
}

func (b *Binding) Update(value string) bool {
	if len(value) > MaxDocumentBytes {
		b.mu.Lock()
		b.fallback = "Markdown 内容超过 3 MiB（含 BOM），请缩小文档后继续编辑。"
		b.mu.Unlock()
		return false
	}
	b.mu.Lock()
	if value == b.content {
		b.mu.Unlock()
		return true
	}
	b.pendingContent = &value
	b.content = value
	b.revision++
	onChange := b.onChange
	b.mu.Unlock()
	if onChange != nil {
		onChange(value)
	}
	return true
}

func (b *Binding) Composition(value bool) {
	b.mu.Lock()
	if b.composing == value {
		b.mu.Unlock()
		return
	}
	b.composing = value
	callback := b.onComposition
	b.mu.Unlock()
	if callback != nil {
		callback(value)
	}
}

func (b *Binding) SetDirty(dirty bool) { b.mu.Lock(); b.dirty = dirty; b.mu.Unlock() }

func (b *Binding) Fallback(message string) {
	b.mu.Lock()
	b.fallback, b.sourceMode = message, true
	b.mu.Unlock()
	b.Composition(false)
}

func (b *Binding) SetEditing(value bool, after func(bool)) {
	b.mu.Lock()
	editing := b.editing
	if value || !editing {
		b.editing = value
		b.mu.Unlock()
		if after != nil {
			after(true)
		}
		return
	}
	b.mu.Unlock()
	b.Flush(func(success bool) {
		if success {
			b.mu.Lock()
			b.editing = false
			b.mu.Unlock()
			b.Composition(false)
		}
		if after != nil {
			after(success)
		}
	})
}

func (b *Binding) ShowSource(value bool, after func(bool)) {
	b.mu.Lock()
	if b.sourceMode == value {
		b.mu.Unlock()
		if after != nil {
			after(true)
		}
		return
	}
	editing := b.editing
	if !editing {
		b.sourceMode = value
		b.mu.Unlock()
		if after != nil {
			after(true)
		}
		return
	}
	b.mu.Unlock()
	b.Flush(func(success bool) {
		if success {
			b.mu.Lock()
			b.sourceMode = value
			b.mu.Unlock()
			b.Composition(false)
		}
		if after != nil {
			after(success)
		}
	})
}

func (b *Binding) Save() {
	b.Flush(func(success bool) {
		if success {
			b.mu.Lock()
			onSave := b.onSave
			b.mu.Unlock()
			if onSave != nil {
				onSave()
			}
		}
	})
}

func (b *Binding) Flush(done func(bool)) {
	if done == nil {
		done = func(bool) {}
	}
	b.mu.Lock()
	if b.composing {
		b.mu.Unlock()
		done(false)
		return
	}
	flush := b.flush
	b.mu.Unlock()
	completed := func(success bool) {
		b.mu.Lock()
		stillCommitted := !b.composing
		b.mu.Unlock()
		done(success && stillCommitted)
	}
	if flush != nil {
		flush(completed)
	} else {
		completed(true)
	}
}

// EditorRegistry drains every unique live binding before a host persists its
// latest documents or closes a window, matching NativeMarkdownEditorRegistry.
type EditorRegistry struct {
	mu       sync.Mutex
	bindings map[any]*Binding
}

func NewEditorRegistry() *EditorRegistry { return &EditorRegistry{bindings: make(map[any]*Binding)} }
func (r *EditorRegistry) Register(owner any, binding *Binding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner != nil && binding != nil {
		r.bindings[owner] = binding
	}
}
func (r *EditorRegistry) Remove(owner any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bindings, owner)
}

func (r *EditorRegistry) Flush(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	unique := make([]*Binding, 0, len(r.bindings))
	seen := map[*Binding]bool{}
	for _, binding := range r.bindings {
		if !seen[binding] {
			seen[binding] = true
			unique = append(unique, binding)
		}
	}
	r.mu.Unlock()
	for _, binding := range unique {
		result := make(chan bool, 1)
		binding.Flush(func(success bool) {
			select {
			case result <- success:
			default:
			}
		})
		timer := time.NewTimer(3 * time.Second)
		select {
		case success := <-result:
			timer.Stop()
			if !success {
				return false
			}
		case <-timer.C:
			return false
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
	return true
}
