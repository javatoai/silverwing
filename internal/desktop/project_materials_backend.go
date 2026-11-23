package desktop

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync/atomic"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

// projectMaterialsBackend confines the shared materials browser to its own
// repository. The browser's local selection identity is never sent as a task ID.
type projectMaterialsBackend struct {
	backend  application.Backend
	scope    atomic.Pointer[workspace.MaterialsGitScope]
	lifetime context.Context
	checkout chan struct{}
}

var _ application.Backend = (*projectMaterialsBackend)(nil)
var _ application.RichClipboardBackend = (*projectMaterialsBackend)(nil)

func newProjectMaterialsBackend(backend application.Backend, scope workspace.MaterialsGitScope, lifetime context.Context, checkout chan struct{}) *projectMaterialsBackend {
	if checkout == nil {
		checkout = make(chan struct{}, 1)
	}
	b := &projectMaterialsBackend{backend: backend, lifetime: lifetime, checkout: checkout}
	b.setScope(scope)
	return b
}

func (b *projectMaterialsBackend) setScope(scope workspace.MaterialsGitScope) {
	if current := b.scope.Load(); current == nil || *current != scope {
		b.scope.Store(&scope)
	}
}

func (*projectMaterialsBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("项目资料面板不读取任务快照")
}

func (b *projectMaterialsBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	if b.backend == nil {
		return application.ActionResult{}, errors.New("项目资料后端不可用")
	}
	externalLink := action.Kind == "system.open" && systemOpenJobKey(action) == "link"
	switch {
	case action.Kind == "system.open-with":
		action.Kind = "document.open-with"
	case action.Kind == "system.open" && !externalLink:
		action.Kind = "document.open"
	case !externalLink && !strings.HasPrefix(action.Kind, "document."):
		return application.ActionResult{}, errors.New("项目资料不支持此操作")
	}
	action.TaskID = ""
	action.Payload = maps.Clone(action.Payload)
	if externalLink {
		// External links use the application's URI validation, independently of
		// repository file opens.
		delete(action.Payload, application.ProjectInstructionsScopePayloadKey)
	} else {
		if action.Payload == nil {
			action.Payload = make(map[string]any, 1)
		}
		action.Payload[application.ProjectInstructionsScopePayloadKey] = *b.scope.Load()
	}
	if action.Kind == "document.draft" || action.Kind == "document.draft-discard" {
		// Draft storage can finish after its pane retires. It has no checkout
		// writes and uses the captured scope for its cache identity.
		return b.backend.Dispatch(ctx, action)
	}
	var result application.ActionResult
	dispatch := func(ctx context.Context) error {
		var err error
		result, err = b.backend.Dispatch(ctx, action)
		return err
	}
	var err error
	if externalLink {
		err = b.withLiveContext(ctx, dispatch)
	} else {
		err = b.withCheckout(ctx, dispatch)
	}
	return result, err
}

func (b *projectMaterialsBackend) withCheckout(ctx context.Context, work func(context.Context) error) error {
	return b.withLiveContext(ctx, func(ctx context.Context) error {
		select {
		case b.checkout <- struct{}{}:
			defer func() { <-b.checkout }()
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.lifetime.Err(); err != nil {
			return err
		}
		return work(ctx)
	})
}

func (b *projectMaterialsBackend) withLiveContext(ctx context.Context, work func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.lifetime.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.lifetime, cancel)
	defer stop()
	if err := b.lifetime.Err(); err != nil {
		return err
	}
	return work(ctx)
}

func (b *projectMaterialsBackend) ReadRichClipboard() (application.RichClipboardContent, error) {
	if clipboard, ok := b.backend.(application.RichClipboardBackend); ok {
		return clipboard.ReadRichClipboard()
	}
	return application.RichClipboardContent{}, application.ErrRichClipboardUnsupported
}

func (b *projectMaterialsBackend) WriteRichClipboard(content application.RichClipboardContent) error {
	if clipboard, ok := b.backend.(application.RichClipboardBackend); ok {
		return clipboard.WriteRichClipboard(content)
	}
	return application.ErrRichClipboardUnsupported
}
