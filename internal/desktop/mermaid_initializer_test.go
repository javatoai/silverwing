package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/editor"
)

type mermaidInitializerLoadResult struct {
	renderer *editor.MermaidRenderer
	message  string
}

type mermaidWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *mermaidWaitingContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func startMermaidInitializerLoad(owner *mermaidInitializer, ctx context.Context) <-chan mermaidInitializerLoadResult {
	done := make(chan mermaidInitializerLoadResult, 1)
	go func() { renderer, message := owner.load(ctx); done <- mermaidInitializerLoadResult{renderer, message} }()
	return done
}

func awaitMermaidInitializerLoad(t *testing.T, done <-chan mermaidInitializerLoadResult) mermaidInitializerLoadResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("Mermaid initialization subscriber did not settle")
		return mermaidInitializerLoadResult{}
	}
}

func waitMermaidInitializerConsumers(t *testing.T, owner *mermaidInitializer, count int) *mermaidInitializationRequest {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		owner.mu.Lock()
		request := owner.inflight
		actual := 0
		if request != nil {
			actual = request.consumers
		}
		owner.mu.Unlock()
		if actual == count {
			return request
		}
		select {
		case <-deadline:
			t.Fatalf("Mermaid subscribers=%d, want %d", actual, count)
		case <-time.After(time.Millisecond):
		}
	}
}

func mermaidInitializerFixtureScope(t *testing.T) mermaidInitializationScope {
	t.Helper()
	platform, err := editor.NativeMermaidPlatform()
	if err != nil {
		t.Fatal(err)
	}
	return mermaidInitializationScope{version: editor.MermaidRendererVersion, platform: platform, resources: t.TempDir(), cache: t.TempDir()}
}

func mermaidInitializerFixture(scope mermaidInitializationScope, install func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error)) *mermaidInitializer {
	return newMermaidInitializerWithDependencies(func(context.Context) (mermaidInitializationScope, error) { return scope, nil }, install)
}

func TestMermaidInitializerConcurrentReadersShareOneNativeInstallAndRenderer(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	resourcePath := filepath.Join(scope.resources, "native-mermaid", scope.platform.Key, scope.platform.ExecutableName)
	if err := os.MkdirAll(filepath.Dir(resourcePath), 0700); err != nil {
		t.Fatal(err)
	}
	resource := []byte("isolated packaged native fixture; never executed")
	if err := os.WriteFile(resourcePath, resource, 0600); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var calls atomic.Int32
	owner := mermaidInitializerFixture(scope, func(ctx context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		calls.Add(1)
		<-gate
		return installPackagedMermaidRenderer(ctx, scope)
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	const readers = 16
	done := make([]<-chan mermaidInitializerLoadResult, readers)
	for i := range done {
		done[i] = startMermaidInitializerLoad(owner, context.Background())
	}
	waitMermaidInitializerConsumers(t, owner, readers)
	release.Do(func() { close(gate) })
	var shared *editor.MermaidRenderer
	for _, completion := range done {
		result := awaitMermaidInitializerLoad(t, completion)
		if result.message != "" || result.renderer == nil {
			t.Fatalf("initialization failed: %+v", result)
		}
		if shared == nil {
			shared = result.renderer
		} else if result.renderer != shared {
			t.Fatal("each diagram received a separate renderer/cache")
		}
	}
	expected := filepath.Join(scope.cache, "cache", "native-mermaid", editor.MermaidRendererVersion, scope.platform.Key, scope.platform.ExecutableName)
	data, err := os.ReadFile(expected)
	if err != nil || !bytes.Equal(data, resource) || shared.Executable != expected {
		t.Fatalf("versioned native install changed: path=%q data=%q err=%v", shared.Executable, data, err)
	}
	temporaries, err := filepath.Glob(filepath.Join(filepath.Dir(expected), "mmdr-*.tmp"))
	if err != nil || len(temporaries) != 0 {
		t.Fatalf("atomic install left temporary files: %v %v", temporaries, err)
	}
	warm, message := owner.load(context.Background())
	if warm != shared || message != "" || calls.Load() != 1 {
		t.Fatalf("warm initialization repeated install or renderer allocation: calls=%d", calls.Load())
	}
}

func TestMermaidInitializerCancelOneSubscriberKeepsOtherInstallAlive(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var calls atomic.Int32
	owner := mermaidInitializerFixture(scope, func(ctx context.Context, _ mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		calls.Add(1)
		select {
		case <-gate:
			return editor.NewMermaidRenderer("fixture", nil), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := startMermaidInitializerLoad(owner, ctx)
	second := startMermaidInitializerLoad(owner, context.Background())
	request := waitMermaidInitializerConsumers(t, owner, 2)
	cancel()
	result := awaitMermaidInitializerLoad(t, first)
	if result.renderer != nil || !strings.Contains(result.message, "取消") {
		t.Fatalf("canceled subscriber received a renderer: %+v", result)
	}
	if err := request.ctx.Err(); err != nil {
		t.Fatalf("one subscriber canceled shared install: %v", err)
	}
	release.Do(func() { close(gate) })
	result = awaitMermaidInitializerLoad(t, second)
	if result.renderer == nil || result.message != "" || calls.Load() != 1 {
		t.Fatalf("remaining subscriber lost shared install: %+v calls=%d", result, calls.Load())
	}
}

func TestMermaidInitializerLastCancellationReturnsBeforeNativeStageAndDoesNotPublish(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var calls atomic.Int32
	owner := mermaidInitializerFixture(scope, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		if calls.Add(1) == 1 {
			<-gate
		} // Model an already-entered, noninterruptible native I/O stage.
		return editor.NewMermaidRenderer("fixture", nil), nil
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	first := startMermaidInitializerLoad(owner, ctx)
	request := waitMermaidInitializerConsumers(t, owner, 1)
	cancel()
	result := awaitMermaidInitializerLoad(t, first)
	if result.renderer != nil || !strings.Contains(result.message, "取消") {
		t.Fatalf("last canceled subscriber received result: %+v", result)
	}
	select {
	case <-request.done:
		t.Fatal("fixture did not hold its noninterruptible stage")
	default:
	}
	if request.ctx.Err() == nil {
		t.Fatal("last subscriber did not retire installation")
	}
	release.Do(func() { close(gate) })
	select {
	case <-request.done:
	case <-time.After(5 * time.Second):
		t.Fatal("retired installation did not settle")
	}
	owner.mu.Lock()
	published := owner.ready != nil || request.renderer != nil
	owner.mu.Unlock()
	if published {
		t.Fatal("canceled installation published a renderer after native I/O completed")
	}
	renderer, message := owner.load(context.Background())
	if renderer == nil || message != "" || calls.Load() != 2 {
		t.Fatalf("canceled attempt poisoned retry: message=%q calls=%d", message, calls.Load())
	}
}

func TestMermaidInitializerScopesIncludeVersionPlatformResourcesCacheAndEvict(t *testing.T) {
	base := mermaidInitializerFixtureScope(t)
	var calls atomic.Int32
	owner := mermaidInitializerFixture(base, func(_ context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		return editor.NewMermaidRenderer(fmt.Sprintf("fixture-%d", calls.Add(1)), nil), nil
	})
	defer owner.close(context.Background())
	first, err := owner.loadScope(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if warm, _ := owner.loadScope(context.Background(), base); warm != first {
		t.Fatal("identical scope missed ready renderer")
	}
	variants := []mermaidInitializationScope{base, base, base, base}
	variants[0].version += "-fixture-new"
	variants[1].platform, err = editor.MermaidPlatformFor("linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if variants[1].platform == base.platform {
		variants[1].platform, _ = editor.MermaidPlatformFor("windows", "amd64")
	}
	variants[2].resources = filepath.Join(base.resources, "another-build")
	variants[3].cache = filepath.Join(base.cache, "another-home")
	for _, scope := range variants {
		changed, err := owner.loadScope(context.Background(), scope)
		if err != nil || changed == first {
			t.Fatalf("changed full scope reused old renderer: %+v %v", scope, err)
		}
		if warm, _ := owner.loadScope(context.Background(), scope); warm != changed {
			t.Fatal("new scope did not become sole ready entry")
		}
		prior := calls.Load()
		first, err = owner.loadScope(context.Background(), base)
		if err != nil || calls.Load() != prior+1 {
			t.Fatal("initializer retained an evicted scope/key map")
		}
		owner.mu.Lock()
		bounded := owner.ready != nil && owner.ready.scope == base && owner.inflight == nil && len(owner.resolving) == 0
		owner.mu.Unlock()
		if !bounded {
			t.Fatal("scope change exceeded one-ready/one-inflight ownership")
		}
	}
	if calls.Load() != 9 {
		t.Fatalf("unexpected install/cache count: %d", calls.Load())
	}
}

func TestMermaidInitializerDifferentScopesWaitForOneInstallSlot(t *testing.T) {
	base := mermaidInitializerFixtureScope(t)
	other := base
	other.cache = filepath.Join(base.cache, "other")
	entered, gate := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var active, maximum, calls atomic.Int32
	owner := mermaidInitializerFixture(base, func(_ context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		now := active.Add(1)
		defer active.Add(-1)
		for prior := maximum.Load(); now > prior && !maximum.CompareAndSwap(prior, now); prior = maximum.Load() {
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-gate
		}
		return editor.NewMermaidRenderer(scope.cache, nil), nil
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	first := startMermaidInitializerLoad(owner, context.Background())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first install did not enter")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := &mermaidWaitingContext{Context: ctx, waiting: make(chan struct{})}
	second := make(chan error, 1)
	go func() { _, err := owner.loadScope(waiting, other); second <- err }()
	select {
	case <-waiting.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("different scope did not wait for the active install")
	}
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting scope ignored cancellation")
	}
	if calls.Load() != 1 {
		t.Fatal("different scope started another native install")
	}
	release.Do(func() { close(gate) })
	if result := awaitMermaidInitializerLoad(t, first); result.message != "" {
		t.Fatal(result.message)
	}
	if _, err := owner.loadScope(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || maximum.Load() != 1 {
		t.Fatalf("install concurrency exceeded one: calls=%d max=%d", calls.Load(), maximum.Load())
	}
}

func TestMermaidInitializerFailuresAreReportedAndNextRequestRetries(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	var calls atomic.Int32
	owner := mermaidInitializerFixture(scope, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("fixture atomic install failed")
		}
		return editor.NewMermaidRenderer("fixture", nil), nil
	})
	defer owner.close(context.Background())
	if renderer, message := owner.load(context.Background()); renderer != nil || message != "fixture atomic install failed" {
		t.Fatalf("installation failure lost: renderer=%v message=%q", renderer, message)
	}
	renderer, message := owner.load(context.Background())
	if renderer == nil || message != "" || calls.Load() != 2 {
		t.Fatalf("failed attempt was cached or retry failed: calls=%d message=%q", calls.Load(), message)
	}
	if warm, message := owner.load(context.Background()); warm != renderer || message != "" || calls.Load() != 2 {
		t.Fatal("successful retry did not replace failed initialization")
	}
	var resolutions atomic.Int32
	paths := newMermaidInitializerWithDependencies(func(context.Context) (mermaidInitializationScope, error) {
		if resolutions.Add(1) == 1 {
			return mermaidInitializationScope{}, errors.New("fixture resource path unavailable")
		}
		return scope, nil
	}, owner.install)
	defer paths.close(context.Background())
	if _, message := paths.load(context.Background()); message != "fixture resource path unavailable" {
		t.Fatal("resource path error was hidden")
	}
	if result, message := paths.load(context.Background()); result == nil || message != "" {
		t.Fatalf("resource path failure prevented retry: %q", message)
	}
}

func TestMermaidInitializerCanceledScopeResolutionIsBoundedAndDoesNotBlockCaller(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	entered := make(chan struct{}, mermaidScopeResolutionSlots)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var resolutions, installs atomic.Int32
	owner := newMermaidInitializerWithDependencies(func(context.Context) (mermaidInitializationScope, error) {
		resolutions.Add(1)
		entered <- struct{}{}
		<-gate
		return scope, nil
	}, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		installs.Add(1)
		return editor.NewMermaidRenderer("fixture", nil), nil
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const readers = 20
	done := make([]<-chan mermaidInitializerLoadResult, readers)
	for index := range done {
		done[index] = startMermaidInitializerLoad(owner, ctx)
	}
	for index := 0; index < mermaidScopeResolutionSlots; index++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("bounded scope resolution did not start")
		}
	}
	cancel()
	for _, completion := range done {
		result := awaitMermaidInitializerLoad(t, completion)
		if result.renderer != nil || !strings.Contains(result.message, "取消") {
			t.Fatalf("canceled path resolution returned result: %+v", result)
		}
	}
	if resolutions.Load() != mermaidScopeResolutionSlots || installs.Load() != 0 {
		t.Fatalf("canceled path lookup accumulated jobs: resolutions=%d installs=%d", resolutions.Load(), installs.Load())
	}
	deadline, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if err := owner.close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked native path lookup claimed closed: %v", err)
	}
	release.Do(func() { close(gate) })
	if err := owner.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if owner.ready != nil || owner.inflight != nil || installs.Load() != 0 {
		t.Fatal("closed/canceled resolution published or installed")
	}
}

type gatedMermaidResource struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
	data    io.Reader
}

func (r *gatedMermaidResource) Read(data []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.data.Read(data)
}

func TestMermaidInitializerCloseRejectsPublicationAndJoinsAtomicInstall(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	resource := &gatedMermaidResource{entered: make(chan struct{}), release: gate, data: strings.NewReader("bounded native fixture")}
	owner := mermaidInitializerFixture(scope, func(_ context.Context, scope mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		path, err := editor.InstallMermaidExecutable(resource, scope.cache, scope.platform)
		if err != nil {
			return nil, err
		}
		return editor.NewMermaidRenderer(path, nil), nil
	})
	defer func() { release.Do(func() { close(gate) }); _ = owner.close(context.Background()) }()
	done := startMermaidInitializerLoad(owner, context.Background())
	select {
	case <-resource.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native installer did not start")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := owner.close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("noninterruptible installer did not report incomplete join: %v", err)
	}
	if result := awaitMermaidInitializerLoad(t, done); result.renderer != nil || !strings.Contains(result.message, "关闭") {
		t.Fatalf("closed owner published a renderer: %+v", result)
	}
	if result, message := owner.load(context.Background()); result != nil || !strings.Contains(message, "关闭") {
		t.Fatal("closed owner accepted another initialization")
	}
	other := mermaidInitializerFixture(scope, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		return editor.NewMermaidRenderer("other-app", nil), nil
	})
	defer other.close(context.Background())
	if renderer, message := other.load(context.Background()); renderer == nil || message != "" {
		t.Fatal("closing one app closed another app's initializer")
	}
	release.Do(func() { close(gate) })
	if err := owner.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	published := owner.ready != nil || owner.inflight != nil
	owner.mu.Unlock()
	if published {
		t.Fatal("atomic stage published after owner closed")
	}
	target := filepath.Join(scope.cache, "cache", "native-mermaid", scope.version, scope.platform.Key, scope.platform.ExecutableName)
	if data, err := os.ReadFile(target); err != nil || string(data) != "bounded native fixture" {
		t.Fatalf("entered native stage lost atomic installation: data=%q err=%v", data, err)
	}
	if temporary, _ := filepath.Glob(filepath.Join(filepath.Dir(target), "mmdr-*.tmp")); len(temporary) != 0 {
		t.Fatalf("atomic installer left temporary files: %v", temporary)
	}
}

func TestMermaidPackagedInitializerKeepsPlatformVersionAndExecutableLimits(t *testing.T) {
	for _, mode := range []string{"missing", "platform", "version", "empty", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			scope := mermaidInitializerFixtureScope(t)
			path := filepath.Join(scope.resources, "native-mermaid", scope.platform.Key, scope.platform.ExecutableName)
			if mode != "missing" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "oversized" {
					err = file.Truncate(editor.MaxMermaidExecutableBytes + 1)
				}
				if closeErr := file.Close(); err != nil || closeErr != nil {
					t.Fatalf("fixture file: %v %v", err, closeErr)
				}
			}
			if mode == "platform" {
				scope.platform.ExecutableName = "../unexpected"
			}
			if mode == "version" {
				scope.version = "unverified-version"
			}
			renderer, err := installPackagedMermaidRenderer(context.Background(), scope)
			if renderer != nil || err == nil {
				t.Fatalf("invalid packaged resource accepted: mode=%s renderer=%v err=%v", mode, renderer, err)
			}
			if mode == "missing" && !strings.Contains(err.Error(), "缺少离线 Mermaid 渲染器："+path) {
				t.Fatalf("missing-resource feedback changed: %v", err)
			}
			target := filepath.Join(scope.cache, "cache", "native-mermaid", editor.MermaidRendererVersion, scope.platform.Key, filepath.Base(scope.platform.ExecutableName))
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed install left an executable: %s %v", target, err)
			}
		})
	}
}

func TestMermaidInitializerContextCompatibilityUsesOwnerWithoutAnotherCache(t *testing.T) {
	scope := mermaidInitializerFixtureScope(t)
	var calls atomic.Int32
	owner := mermaidInitializerFixture(scope, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		calls.Add(1)
		return editor.NewMermaidRenderer("fixture", nil), nil
	})
	defer owner.close(context.Background())
	ctx := withMermaidInitializer(context.Background(), owner)
	first, message := loadMermaidRendererContext(ctx)
	second, nextMessage := loadMermaidRendererContext(ctx)
	if first == nil || first != second || message != "" || nextMessage != "" || calls.Load() != 1 {
		t.Fatal("compatibility context did not reuse its app owner")
	}
}
