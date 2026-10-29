package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo"
)

const mermaidScopeResolutionSlots = 2

var errMermaidInitializerClosed = errors.New("Mermaid 初始化服务已关闭。")

type mermaidInitializationScope struct {
	version   string
	platform  editor.MermaidPlatform
	resources string
	cache     string
}

type mermaidInitializedRenderer struct {
	scope    mermaidInitializationScope
	renderer *editor.MermaidRenderer
}

type mermaidScopeResolution struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	scope  mermaidInitializationScope
	err    error
}

type mermaidInitializationRequest struct {
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	scope     mermaidInitializationScope
	consumers int
	finished  bool
	delivered bool
	renderer  *editor.MermaidRenderer
	err       error
}

// Each app owns one initializer. It retains one ready renderer (whose existing
// cache is capped at 32 items/32 MiB and two renders), plus at most one install
// and two scope resolutions. Different scopes wait without accumulating keys.
// Evicted renderers are not retained here; render jobs may finish using their
// captured pointer, and document state must not retain automatic renderers.
type mermaidInitializer struct {
	mu           sync.Mutex
	closed       bool
	closing      chan struct{}
	resolveSlots chan struct{}
	resolving    map[*mermaidScopeResolution]struct{}
	ready        *mermaidInitializedRenderer
	inflight     *mermaidInitializationRequest
	resolveScope func(context.Context) (mermaidInitializationScope, error)
	install      func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error)
}

func newMermaidInitializer() *mermaidInitializer {
	return newMermaidInitializerWithDependencies(resolveMermaidInitializationScope, installPackagedMermaidRenderer)
}

func newMermaidInitializerWithDependencies(resolve func(context.Context) (mermaidInitializationScope, error),
	install func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error)) *mermaidInitializer {
	return &mermaidInitializer{closing: make(chan struct{}), resolveSlots: make(chan struct{}, mermaidScopeResolutionSlots),
		resolving: make(map[*mermaidScopeResolution]struct{}), resolveScope: resolve, install: install}
}

// mermaidInitializerFor is called on the UI thread before starting a render
// job. Resource discovery and installation happen only inside owner.load in
// that background job, never while obtaining this owner.
func (s *app) mermaidInitializerFor() *mermaidInitializer {
	if s.mermaidInitializer == nil {
		s.mermaidInitializer = newMermaidInitializer()
	}
	return s.mermaidInitializer
}

func (i *mermaidInitializer) load(ctx context.Context) (*editor.MermaidRenderer, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if i == nil {
		return nil, errMermaidInitializerClosed.Error()
	}
	scope, err := i.scope(ctx)
	if err != nil {
		return nil, mermaidInitializationMessage(err)
	}
	renderer, err := i.loadScope(ctx, scope)
	if err != nil {
		return nil, mermaidInitializationMessage(err)
	}
	return renderer, ""
}

// PathResources may resolve symlinks and PathCache may create its directory.
// Resolve them in a bounded job too, so cancellation never waits for filesystem
// calls in either discovery or the native installer's atomic stage.
func (i *mermaidInitializer) scope(ctx context.Context) (mermaidInitializationScope, error) {
	if err := ctx.Err(); err != nil {
		return mermaidInitializationScope{}, err
	}
	select {
	case <-ctx.Done():
		return mermaidInitializationScope{}, ctx.Err()
	case <-i.closing:
		return mermaidInitializationScope{}, errMermaidInitializerClosed
	case i.resolveSlots <- struct{}{}:
	}
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		<-i.resolveSlots
		return mermaidInitializationScope{}, errMermaidInitializerClosed
	}
	child, cancel := context.WithCancel(ctx)
	request := &mermaidScopeResolution{ctx: child, cancel: cancel, done: make(chan struct{})}
	i.resolving[request] = struct{}{}
	i.mu.Unlock()
	go func() {
		defer cancel()
		defer func() { <-i.resolveSlots }()
		request.scope, request.err = i.resolveScope(child)
		if child.Err() != nil {
			request.err = child.Err()
		}
		i.mu.Lock()
		delete(i.resolving, request)
		close(request.done)
		i.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		cancel()
		return mermaidInitializationScope{}, ctx.Err()
	case <-i.closing:
		cancel()
		return mermaidInitializationScope{}, errMermaidInitializerClosed
	case <-request.done:
		if err := ctx.Err(); err != nil {
			return mermaidInitializationScope{}, err
		}
		return request.scope, request.err
	}
}

func (i *mermaidInitializer) loadScope(ctx context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i.mu.Lock()
		if i.closed {
			i.mu.Unlock()
			return nil, errMermaidInitializerClosed
		}
		if i.ready != nil && i.ready.scope == scope {
			renderer := i.ready.renderer
			i.mu.Unlock()
			return renderer, nil
		}
		request := i.inflight
		if request != nil && (request.scope != scope || request.ctx.Err() != nil) {
			i.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-i.closing:
				return nil, errMermaidInitializerClosed
			case <-request.done:
				continue
			}
		}
		if request == nil {
			shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
			request = &mermaidInitializationRequest{ctx: shared, cancel: cancel, done: make(chan struct{}), scope: scope}
			i.inflight = request
			go i.runInstall(request)
		}
		request.consumers++
		i.mu.Unlock()
		return i.wait(ctx, request)
	}
}

func (i *mermaidInitializer) runInstall(request *mermaidInitializationRequest) {
	renderer, err := i.install(request.ctx, request.scope)
	if request.ctx.Err() != nil {
		renderer, err = nil, request.ctx.Err()
	} else if renderer == nil && err == nil {
		err = errors.New("离线 Mermaid 初始化未返回渲染器。")
	}
	i.mu.Lock()
	if i.closed {
		renderer, err = nil, errMermaidInitializerClosed
	} else if request.consumers == 0 || request.ctx.Err() != nil {
		renderer, err = nil, context.Canceled
	} else if err == nil {
		i.ready = &mermaidInitializedRenderer{scope: request.scope, renderer: renderer}
	}
	request.renderer, request.err, request.finished = renderer, err, true
	if i.inflight == request {
		i.inflight = nil
	}
	close(request.done)
	i.mu.Unlock()
	request.cancel()
}

func (i *mermaidInitializer) wait(ctx context.Context, request *mermaidInitializationRequest) (*editor.MermaidRenderer, error) {
	defer func() {
		i.mu.Lock()
		request.consumers--
		if request.consumers == 0 && !request.finished {
			request.cancel()
		} else if request.consumers == 0 && !request.delivered && ctx.Err() != nil &&
			i.ready != nil && i.ready.scope == request.scope && i.ready.renderer == request.renderer {
			// Cancellation can race the install's final lock. Once all canceled
			// waiters retire, do not retain an otherwise unconsumed result.
			i.ready = nil
		}
		i.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-i.closing:
		return nil, errMermaidInitializerClosed
	case <-request.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i.mu.Lock()
		defer i.mu.Unlock()
		if i.closed {
			return nil, errMermaidInitializerClosed
		}
		request.delivered = request.err == nil && request.renderer != nil
		return request.renderer, request.err
	}
}

// close prevents new subscriptions and publication, then joins the bounded
// native I/O already entered. A canceled caller never has to wait for that I/O;
// shutdown supplies its own deadline and reports an incomplete join explicitly.
func (i *mermaidInitializer) close(ctx context.Context) error {
	if i == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	i.mu.Lock()
	if !i.closed {
		i.closed = true
		close(i.closing)
	}
	i.ready = nil
	resolutions := make([]*mermaidScopeResolution, 0, len(i.resolving))
	for request := range i.resolving {
		request.cancel()
		resolutions = append(resolutions, request)
	}
	installation := i.inflight
	if installation != nil {
		installation.cancel()
	}
	i.mu.Unlock()
	for _, request := range resolutions {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待 Mermaid 初始化清理：%w", ctx.Err())
		}
	}
	if installation != nil {
		select {
		case <-installation.done:
		case <-ctx.Done():
			return fmt.Errorf("等待 Mermaid 初始化清理：%w", ctx.Err())
		}
	}
	return nil
}

func resolveMermaidInitializationScope(ctx context.Context) (mermaidInitializationScope, error) {
	if err := ctx.Err(); err != nil {
		return mermaidInitializationScope{}, err
	}
	platform, err := editor.NativeMermaidPlatform()
	if err != nil {
		return mermaidInitializationScope{}, err
	}
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return mermaidInitializationScope{}, err
	}
	if err := ctx.Err(); err != nil {
		return mermaidInitializationScope{}, err
	}
	cache, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return mermaidInitializationScope{}, err
	}
	resources, err = filepath.Abs(resources)
	if err != nil {
		return mermaidInitializationScope{}, err
	}
	cache, err = filepath.Abs(cache)
	if err != nil {
		return mermaidInitializationScope{}, err
	}
	return mermaidInitializationScope{version: editor.MermaidRendererVersion, platform: platform,
		resources: filepath.Clean(resources), cache: filepath.Clean(cache)}, ctx.Err()
}

func installPackagedMermaidRenderer(ctx context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	allowed, err := editor.MermaidPlatformForRuntimeKey(scope.platform.Key)
	if err != nil || allowed != scope.platform || scope.version != editor.MermaidRendererVersion {
		return nil, errors.New("Mermaid 平台资源或版本不合法")
	}
	path := filepath.Join(scope.resources, "native-mermaid", scope.platform.Key, scope.platform.ExecutableName)
	resource, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("缺少离线 Mermaid 渲染器：%s", path)
	}
	defer resource.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The existing installer bounds input to 24 MiB and atomically renames a
	// version/platform-specific temporary file. This stage cannot be interrupted
	// safely; the request checks cancellation again before publishing its result.
	executable, err := editor.InstallMermaidExecutable(resource, scope.cache, scope.platform)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return editor.NewMermaidRenderer(executable, nil), nil
}

func mermaidInitializationMessage(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Mermaid 初始化已取消。"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Mermaid 初始化等待已超时。"
	}
	return err.Error()
}

type mermaidInitializerContextKey struct{}

func withMermaidInitializer(ctx context.Context, owner *mermaidInitializer) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, mermaidInitializerContextKey{}, owner)
}

// Compatibility entry; production captures the app owner on the UI thread and
// calls owner.load in its background job. An unbound caller gets a one-shot
// adapter, not another process-wide cache or a second persistent renderer pool.
func loadMermaidRendererContext(ctx context.Context) (*editor.MermaidRenderer, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if owner, _ := ctx.Value(mermaidInitializerContextKey{}).(*mermaidInitializer); owner != nil {
		return owner.load(ctx)
	}
	owner := newMermaidInitializer()
	defer owner.close(ctx)
	return owner.load(ctx)
}

func loadMermaidRenderer() (*editor.MermaidRenderer, string) {
	return loadMermaidRendererContext(context.Background())
}
