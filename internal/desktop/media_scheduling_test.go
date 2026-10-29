package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func mediaTestReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("media worker did not complete")
		var zero T
		return zero
	}
}

func TestPDFRenderQueueCoalescesWidthsAndPrunesOffscreenPending(t *testing.T) {
	queue := newPDFRenderQueue(context.Background())
	defer queue.close()
	started, release := make(chan struct{}), make(chan struct{})
	completed := make(chan int, 16)
	var calls atomic.Int32
	queue.enqueue(0, 100, func(_ context.Context, request *pdfPageRenderRequest) {
		calls.Add(1)
		close(started)
		<-release // a native render cannot be interrupted
		queue.ack(0, request)
		completed <- 100
	})
	done := make(chan struct{})
	go func() { queue.run(); close(done) }()
	mediaTestReceive(t, started)
	for width := 101; width <= 500; width++ {
		width := width
		queue.enqueue(0, width, func(_ context.Context, request *pdfPageRenderRequest) {
			calls.Add(1)
			queue.ack(0, request)
			completed <- width
		})
	}
	queue.mu.Lock()
	if len(queue.pending) != 1 || len(queue.latest) != 1 || queue.pending[0].width != 500 || !queue.active.request.invalidated.Load() {
		t.Fatal("width churn retained obsolete jobs or failed to retire active result")
	}
	queue.mu.Unlock()
	close(release)
	if mediaTestReceive(t, completed) != 100 || mediaTestReceive(t, completed) != 500 || calls.Load() != 2 {
		t.Fatal("obsolete pending widths reached the renderer")
	}
	queue.close()
	mediaTestReceive(t, done)

	queue = newPDFRenderQueue(context.Background())
	defer queue.close()
	for page := 0; page < 100; page++ {
		queue.enqueue(page, 400, func(context.Context, *pdfPageRenderRequest) {})
	}
	queue.mu.Lock()
	bounded := len(queue.pending) <= pdfRenderQueueLimit && len(queue.latest) <= pdfRenderQueueLimit
	queue.mu.Unlock()
	if !bounded {
		t.Fatal("render admission exceeded the bounded queue")
	}
	queue.viewport(2, 3, 400)
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for page := range queue.pending {
		if page < 2 || page > 3 {
			t.Fatal("offscreen pending render survived viewport change")
		}
	}
}

func TestPDFRenderQueueRetiresCompletedCallbackAcrossWidthABA(t *testing.T) {
	queue := newPDFRenderQueue(context.Background())
	defer queue.close()
	callbacks := make(chan *pdfPageRenderRequest, 3)
	queue.enqueue(0, 100, func(_ context.Context, r *pdfPageRenderRequest) { callbacks <- r })
	done := make(chan struct{})
	go func() { queue.run(); close(done) }()
	old := mediaTestReceive(t, callbacks)
	queue.viewport(0, 0, 200)
	queue.viewport(0, 0, 100)
	queue.enqueue(0, 100, func(_ context.Context, r *pdfPageRenderRequest) { callbacks <- r })
	current := mediaTestReceive(t, callbacks)
	if old == current || !old.invalidated.Load() || current.invalidated.Load() {
		t.Fatal("completed old width was revived after resize away and back")
	}
	queue.ack(0, old)
	queue.mu.Lock()
	retained := queue.latest[0] != nil && queue.latest[0].request == current
	queue.mu.Unlock()
	if !retained {
		t.Fatal("old callback removed a newer request")
	}
	queue.close()
	mediaTestReceive(t, done)
}

func TestNativeImagePoolSharesFlightFailureAndIndependentSubscribers(t *testing.T) {
	pool := newNativeImagePool()
	defer pool.close()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 4)))
	load := func(ctx context.Context) (*ui.Bitmap, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return bitmap, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan struct{}, 1)
	unsubscribe, err := pool.subscribe(firstCtx, "verified-resource-v1", load, func(*ui.Bitmap, error) { firstDone <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	mediaTestReceive(t, started)
	secondDone := make(chan *ui.Bitmap, 1)
	_, err = pool.subscribe(context.Background(), "verified-resource-v1", load, func(value *ui.Bitmap, err error) {
		if err != nil {
			t.Error(err)
		}
		secondDone <- value
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelFirst()
	unsubscribe()
	close(release)
	if mediaTestReceive(t, secondDone) != bitmap || calls.Load() != 1 {
		t.Fatal("shared load duplicated or one unsubscribe cancelled the other subscriber")
	}
	select {
	case <-firstDone:
		t.Fatal("unsubscribed node received completion")
	default:
	}
	thirdDone := make(chan *ui.Bitmap, 1)
	_, err = pool.subscribe(context.Background(), "verified-resource-v1", load, func(value *ui.Bitmap, _ error) { thirdDone <- value })
	if err != nil || mediaTestReceive(t, thirdDone) != bitmap || calls.Load() != 1 {
		t.Fatal("immutable bitmap was not reused within its scope")
	}
	pool.close()
	if err := pool.wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	pool = newNativeImagePool()
	defer pool.close()
	failure := errors.New("decode failed")
	ready, unblock := make(chan struct{}), make(chan struct{})
	var failedLoads atomic.Int32
	bad := func(context.Context) (*ui.Bitmap, error) {
		if failedLoads.Add(1) == 1 {
			close(ready)
			<-unblock
		}
		return nil, failure
	}
	errorsSeen := make(chan error, 3)
	_, _ = pool.subscribe(context.Background(), "same", bad, func(_ *ui.Bitmap, e error) { errorsSeen <- e })
	mediaTestReceive(t, ready)
	_, _ = pool.subscribe(context.Background(), "same", bad, func(_ *ui.Bitmap, e error) { errorsSeen <- e })
	close(unblock)
	if !errors.Is(mediaTestReceive(t, errorsSeen), failure) || !errors.Is(mediaTestReceive(t, errorsSeen), failure) || failedLoads.Load() != 1 {
		t.Fatal("shared failure lost a subscriber or duplicated the flight")
	}
	_, _ = pool.subscribe(context.Background(), "same", bad, func(_ *ui.Bitmap, e error) { errorsSeen <- e })
	if !errors.Is(mediaTestReceive(t, errorsSeen), failure) || failedLoads.Load() != 2 {
		t.Fatal("failed resource could not be retried")
	}
}

func TestNativeImagePoolBoundsLoadingAndRetiresScope(t *testing.T) {
	pool := newNativeImagePool()
	started := make(chan struct{}, nativeImagePoolWorkers)
	var active, maxActive, calls atomic.Int32
	load := func(ctx context.Context) (*ui.Bitmap, error) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for previous := maxActive.Load(); n > previous && !maxActive.CompareAndSwap(previous, n); previous = maxActive.Load() {
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	var callbacks atomic.Int32
	for _, key := range []string{"a", "b", "c", "d"} {
		_, err := pool.subscribe(context.Background(), key, load, func(*ui.Bitmap, error) { callbacks.Add(1) })
		if err != nil {
			t.Fatal(err)
		}
	}
	mediaTestReceive(t, started)
	mediaTestReceive(t, started)
	pool.close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.wait(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != nativeImagePoolWorkers || maxActive.Load() > nativeImagePoolWorkers || callbacks.Load() != 0 {
		t.Fatal("pool exceeded worker budget or published retired scope")
	}
	if _, err := pool.subscribe(context.Background(), "new", load, func(*ui.Bitmap, error) {}); !errors.Is(err, context.Canceled) {
		t.Fatal("closed scope admitted a new image")
	}
}

func TestCommentImageContentVersionsSharePixelsInsideReaderScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "requirement-comment-images", "account-and-item", "assets")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("verified immutable fixture")
	digest := sha256.Sum256(data)
	version := hex.EncodeToString(digest[:])
	paths := []string{filepath.Join(root, version+"-0123456789ab.png"), filepath.Join(root, version+".png")}
	for _, path := range paths {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := &markdownEditorState{commentReading: true}
	first, err := markdownImageResourceKey(state, "https://example.test/image", paths[0], false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := markdownImageResourceKey(state, "https://example.test/image", paths[1], false)
	if err != nil || first != second {
		t.Fatal("comment-owned paths for the same content version did not share a resource identity")
	}
	different, _ := markdownImageResourceKey(state, "https://example.test/other", paths[1], false)
	if first == different {
		t.Fatal("resource identity discarded its validated source URL")
	}
	private := &markdownEditorState{}
	localFirst, _ := markdownImageResourceKey(private, "same", paths[0], false)
	localSecond, _ := markdownImageResourceKey(private, "same", paths[1], false)
	if localFirst == localSecond {
		t.Fatal("ordinary local images trusted a filename as content identity")
	}
	pool, otherScope := newNativeImagePool(), newNativeImagePool()
	defer pool.close()
	defer otherScope.close()
	var loads atomic.Int32
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 16)))
	load := func(context.Context) (*ui.Bitmap, error) { loads.Add(1); return bitmap, nil }
	results := make(chan *ui.Bitmap, 3)
	for _, key := range []string{first, second} {
		_, err := pool.subscribe(context.Background(), key, load, func(value *ui.Bitmap, _ error) { results <- value })
		if err != nil {
			t.Fatal(err)
		}
		if mediaTestReceive(t, results) != bitmap {
			t.Fatal("shared image changed")
		}
	}
	_, err = otherScope.subscribe(context.Background(), first, load, func(value *ui.Bitmap, _ error) { results <- value })
	if err != nil || mediaTestReceive(t, results) != bitmap || loads.Load() != 2 {
		t.Fatal("same URL crossed a reader scope or duplicate content was decoded twice")
	}
}

func TestMarkdownPreviewFeatureCloseCancelsAndSettlesPool(t *testing.T) {
	s := &app{}
	pool := s.newMarkdownImagePool()
	started := make(chan struct{})
	_, err := pool.subscribe(context.Background(), "pending", func(ctx context.Context) (*ui.Bitmap, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }, func(*ui.Bitmap, error) { t.Error("closed feature published a result") })
	if err != nil {
		t.Fatal(err)
	}
	mediaTestReceive(t, started)
	s.materialsUnit.stopMarkdownPreviewJobs()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.materialsUnit.closeMarkdownPreviewJobs(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pool.done:
	default:
		t.Fatal("feature drain completed before image workers settled")
	}
}

func TestRequirementCommentImageBatchKeepsIndependentTokensAndScopeCancellation(t *testing.T) {
	s := &app{}
	reader := s.requirementReader("", "https://project.feishu.cn/test/story/detail/123")
	resources := []application.RequirementCommentResource{{CommentID: "first", Generation: "g1", URL: "https://example.test/same.png"}, {CommentID: "second", Generation: "g2", URL: "https://example.test/same.png"}}
	s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "first", Content: "first"}, {ID: "second", Content: "second"}}, CommentResources: resources})
	batch := reader.commentImageBatch
	if batch == nil || reader.commentMarkdown["id:first"].imagePool != reader.commentMarkdown["id:second"].imagePool {
		t.Fatal("comments did not share their account-bound image pool")
	}
	requests := make([]*requirementContentJob, 0, 2)
	for _, resource := range resources {
		key := commentResourceKey(resource)
		payload := s.requirementReaderPayload(reader, commentImageRequestKind(key), false)
		payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"], payload["commentImageToken"] = "comment", resource.CommentID, resource.Generation, resource.URL, key
		action := application.Action{Kind: "requirements.attachment", Payload: payload}
		request, err := s.beginRequirementContentJob(action)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		batch.pending[key] = true
	}
	if requests[0].key == requests[1].key || !s.requirementContentJobRelevant(requests[0]) || !s.requirementContentJobRelevant(requests[1]) {
		t.Fatal("one comment replaced another's token")
	}
	requests[0].cancel()
	if requests[1].ctx.Err() != nil {
		t.Fatal("one child cancellation retired another comment")
	}
	s.retireRequirementCommentImages(reader)
	if batch.ctx.Err() == nil || requests[1].ctx.Err() == nil || reader.commentResourceLoading != "" || s.requirementUnit.requirementReaderForAction(requests[1].action) != nil {
		t.Fatal("retired batch kept a live child or applicable callback")
	}
	state := s.requirementUnit.state.requirementContentJobs
	state.mu.Lock()
	for _, request := range requests {
		delete(state.running, request)
		close(request.done)
	}
	state.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.requirementUnit.closeRequirementCommentImageBatches(ctx); err != nil {
		t.Fatal(err)
	}
	s.requirementUnit.closeRequirementReaders()
}

func commentImageReaderFixture(t *testing.T, s *app, taskID, id string) *requirementReaderState {
	t.Helper()
	link := "https://project.feishu.cn/space/userstory/detail/" + id
	if taskID != "" {
		root := t.TempDir()
		s.taskUnit.state.tasks = append(s.taskUnit.state.tasks, task{id: taskID, path: root, materialsPath: root, createdAt: id, requirementURL: link})
		s.taskUnit.state.taskCatalog = nil
	}
	path := filepath.Join(t.TempDir(), "image.png")
	writeMarkdownImageFixture(t, path, 16, 12)
	reader := s.requirementReader(taskID, link)
	s.updateRequirementReaderBody(reader, application.RequirementContent{Body: "retained body", Generation: "body-v1"})
	s.updateRequirementReaderComments(reader, application.RequirementComments{FetchedAt: 1,
		Comments: []domain.RequirementComment{{ID: "first", Content: "![first](https://example.test/first.png)"}, {ID: "second", Content: "![second](https://example.test/second.png)"}},
		CommentResources: []application.RequirementCommentResource{
			{CommentID: "first", Generation: "first-v1", URL: "https://example.test/first.png", Path: path},
			{CommentID: "second", Generation: "second-v1", URL: "https://example.test/second.png", Path: path}}})
	return reader
}

func commentImageLifecycleFixture(t *testing.T) *app {
	t.Helper()
	s := newApp(nil, Options{Page: "requirements", CapturePath: "isolated-comment-image-lifecycle"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	t.Cleanup(func() {
		for _, reader := range s.requirementUnit.state.requirementReaders {
			s.retireRequirementCommentImages(reader)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.requirementUnit.closeRequirementCommentImageBatches(ctx); err != nil {
			t.Error(err)
		}
		s.requirementUnit.closeRequirementReaders()
		if err := s.materialsUnit.closeMarkdownPreviewJobs(ctx); err != nil {
			t.Error(err)
		}
		if failures := s.materialsUnit.state.markdownAutosave.Close(ctx); len(failures) != 0 {
			t.Errorf("readonly fixture left pending saves: %v", failures)
		}
	})
	return s
}

func commentImagePendingJob(t *testing.T, s *app, reader *requirementReaderState, resource application.RequirementCommentResource) *requirementContentJob {
	t.Helper()
	key := commentResourceKey(resource)
	payload := s.requirementReaderPayload(reader, commentImageRequestKind(key), false)
	payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"], payload["commentImageToken"] = "comment", resource.CommentID, resource.Generation, resource.URL, key
	request, err := s.beginRequirementContentJob(application.Action{Kind: "requirements.attachment", TaskID: reader.taskID, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	reader.commentImageBatch.pending[key] = true
	reader.commentResourceLoading = key
	// These tests drive the production cancellation/token path without starting
	// an attachment backend; byte-operation load/join has its own fake CLI tests.
	t.Cleanup(func() {
		request.cancel()
		state := s.requirementUnit.state.requirementContentJobs
		delete(state.requests, request.key)
		state.mu.Lock()
		delete(state.running, request)
		close(request.done)
		state.mu.Unlock()
	})
	return request
}

func commentImageBlockingDecode(t *testing.T, batch *requirementCommentImageBatch, subscriber context.Context) (<-chan struct{}, func(), context.CancelFunc) {
	t.Helper()
	started, cancelled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(cleanup) }) }
	t.Cleanup(release)
	unsubscribe, err := batch.images.subscribe(subscriber, "blocking-decode", func(ctx context.Context) (*ui.Bitmap, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-cleanup
		return nil, ctx.Err()
	}, func(*ui.Bitmap, error) { t.Error("retired decoder published a result") })
	if err != nil {
		t.Fatal(err)
	}
	mediaTestReceive(t, started)
	return cancelled, release, unsubscribe
}

func commentImageAssertRetiring(t *testing.T, batch *requirementCommentImageBatch) {
	t.Helper()
	if batch.ctx.Err() == nil || len(batch.pending) != 0 {
		t.Fatal("reader retirement retained its owner or pending subscribers")
	}
	select {
	case <-batch.done:
		t.Fatal("retirement completed before the decoder cleanup point")
	default:
	}
	batch.owner.mu.Lock()
	_, registered := batch.owner.running[batch]
	batch.owner.mu.Unlock()
	batch.images.mu.Lock()
	empty := batch.images.closed && batch.images.bytes == 0 && len(batch.images.entries) == 0 && batch.images.order.Len() == 0
	batch.images.mu.Unlock()
	if !registered || !empty {
		t.Fatal("retirement lost its cleanup registration or retained the bitmap cache")
	}
}

func commentImageAssertSettled(t *testing.T, batch *requirementCommentImageBatch) {
	t.Helper()
	mediaTestReceive(t, batch.done)
	batch.owner.mu.Lock()
	_, registered := batch.owner.running[batch]
	batch.owner.mu.Unlock()
	batch.images.mu.Lock()
	workers := batch.images.workers
	batch.images.mu.Unlock()
	if registered || workers != 0 {
		t.Fatal("retired batch kept its registration or decoding workers")
	}
}

func TestRequirementCommentImageReaderLeaveRetiresOwnerAndReenters(t *testing.T) {
	s := commentImageLifecycleFixture(t)
	reader := commentImageReaderFixture(t, s, "", "101")
	batch := reader.commentImageBatch
	owner := reader.commentMarkdown["id:first"]
	resource := reader.comments.CommentResources[0]
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 12)))
	resourceKey, err := markdownImageResourceKey(owner, resource.URL, resource.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	warmed := make(chan struct{}, 1)
	_, err = batch.images.subscribe(batch.ctx, resourceKey, func(context.Context) (*ui.Bitmap, error) { return bitmap, nil }, func(*ui.Bitmap, error) { warmed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	mediaTestReceive(t, warmed)
	// Keep an actual pool subscriber independent from the pool owner, as an
	// inline node does. Retirement must cancel both contexts; closing only the
	// shared pool would not retire a queued native-window subscriber callback.
	pixelContext, cancelPixel := context.WithCancel(context.Background())
	t.Cleanup(cancelPixel)
	cancelled, release, unsubscribe := commentImageBlockingDecode(t, batch, pixelContext)
	oldImage := &markdownInlineImageState{key: "loading-node", loading: true, cancel: func() { cancelPixel(); unsubscribe() }}
	owner.inlineImages["loading-node"] = oldImage
	first := commentImagePendingJob(t, s, reader, resource)
	peerResource := reader.comments.CommentResources[1]
	peer := commentImagePendingJob(t, s, reader, peerResource)
	// Exercise the production attachment completion consumer with a held
	// result. This headless queue does not verify MyGO native window posting.
	window := &refreshTestWindow{updates: make(chan func(), 1)}
	lateResource := peerResource
	lateResource.Path = filepath.Join(t.TempDir(), "retired-result.png")
	window.Update(func() { s.finishRequirementContentJob(peer, application.ActionResult{Data: lateResource}, nil) })
	oldCallback := mediaTestReceive(t, window.updates)
	jobs := s.requirementUnit.state.requirementContentJobs
	jobs.entered, jobs.visible = reader, requirementReaderKey(reader.taskID, reader.url)
	s.requirementUnit.cancelRequirementContentJob(first)
	s.observeRequirementContentJobs()
	if batch.ctx.Err() != nil || peer.ctx.Err() != nil {
		t.Fatal("cancelling one subscriber retired its visible peer or owner")
	}
	jobs.visible = ""
	s.observeRequirementContentJobs()
	mediaTestReceive(t, cancelled)
	if reader.commentImageBatch != nil || peer.ctx.Err() == nil || reader.commentResourceLoading != "" || len(reader.requestIDs) != 0 {
		t.Fatal("leaving the reader kept a batch, child, pending marker or token")
	}
	commentImageAssertRetiring(t, batch)
	if pixelContext.Err() == nil || oldImage.cancel != nil {
		t.Fatal("reader retirement retained the inline subscriber context")
	}
	oldCallback()
	if reader.comments.CommentResources[1].Path != peerResource.Path {
		t.Fatal("a queued attachment callback applied after leaving the reader")
	}
	release()
	commentImageAssertSettled(t, batch)
	next := s.ensureRequirementCommentImageBatch(reader)
	if next == batch || next.images.id == batch.images.id || next.ctx.Err() != nil {
		t.Fatal("reentry reused the closed owner or pool")
	}
	for _, state := range reader.commentMarkdown {
		if state.imagePool != next.images || state.closed {
			t.Fatal("reentry did not rebind each retained comment")
		}
	}
	request := commentImagePendingJob(t, s, reader, peerResource)
	if payloadString(request.action.Payload, "readerRequestID") == payloadString(peer.action.Payload, "readerRequestID") || !s.requirementContentJobRelevant(request) || s.requirementContentJobRelevant(peer) {
		t.Fatal("reentry did not replace the attachment generation")
	}
	oldCallback()
	if reader.comments.CommentResources[1].Path != peerResource.Path || !s.requirementContentJobRelevant(request) {
		t.Fatal("retired callback replaced the fresh attachment or consumed its token")
	}
	freshResource := peerResource
	freshResource.Path = filepath.Join(t.TempDir(), "fresh-result.png")
	writeMarkdownImageFixture(t, freshResource.Path, 16, 12)
	freshKey, err := markdownImageResourceKey(owner, freshResource.URL, freshResource.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	type decodedImage struct {
		bitmap *ui.Bitmap
		err    error
	}
	decoded := make(chan decodedImage, 1)
	_, err = next.images.subscribe(request.ctx, freshKey, func(ctx context.Context) (*ui.Bitmap, error) {
		return decodeNativeImagePreview(ctx, freshResource.Path, 1200)
	}, func(bitmap *ui.Bitmap, err error) { decoded <- decodedImage{bitmap, err} })
	if err != nil {
		t.Fatal(err)
	}
	current := mediaTestReceive(t, decoded)
	if current.err != nil || current.bitmap == nil {
		t.Fatalf("fresh pool PNG decode: %v", current.err)
	}
	width, height := current.bitmap.Size()
	if width != 16 || height != 12 {
		t.Fatal("fresh pool decoded the wrong PNG dimensions")
	}
	window.Update(func() { s.finishRequirementContentJob(request, application.ActionResult{Data: freshResource}, nil) })
	mediaTestReceive(t, window.updates)()
	oldCallback()
	if reader.comments.CommentResources[1].Path != freshResource.Path || reader.commentResourceLoading != "" || len(next.pending) != 0 {
		t.Fatal("fresh attachment did not complete independently from the retired callback")
	}
	if reader.body.Body != "retained body" || reader.comments.Comments[0].Content != "![first](https://example.test/first.png)" {
		t.Fatal("retirement discarded retained reader content")
	}
}

func TestRequirementCommentImageTaskIdentityDeletionRetiresOwner(t *testing.T) {
	s := commentImageLifecycleFixture(t)
	reader := commentImageReaderFixture(t, s, "task-a", "101")
	peer := commentImageReaderFixture(t, s, "task-b", "102")
	batch, peerBatch := reader.commentImageBatch, peer.commentImageBatch
	markdown := reader.commentMarkdown["id:first"]
	cancelled, release, _ := commentImageBlockingDecode(t, batch, batch.ctx)
	request := commentImagePendingJob(t, s, reader, reader.comments.CommentResources[0])
	jobs := s.requirementUnit.state.requirementContentJobs
	jobs.entered, jobs.visible = reader, requirementReaderKey(reader.taskID, reader.url)
	s.closeTaskRequirementIdentityReaders("task-a")
	mediaTestReceive(t, cancelled)
	if reader.commentImageBatch != nil || jobs.entered != nil || jobs.visible != "" || request.ctx.Err() == nil || s.requirementUnit.requirementReaderForAction(request.action) != nil {
		t.Fatal("task retirement retained its reader batch, navigation or request")
	}
	if s.requirementUnit.state.requirementReaders[requirementReaderKey(reader.taskID, reader.url)] != nil || !markdown.closed || len(reader.commentMarkdown) != 0 {
		t.Fatal("task identity retirement retained the deleted reader")
	}
	if peer.commentImageBatch != peerBatch || peerBatch.ctx.Err() != nil || peer.commentMarkdown["id:first"].closed {
		t.Fatal("task retirement cancelled another reader")
	}
	commentImageAssertRetiring(t, batch)
	release()
	commentImageAssertSettled(t, batch)
}

func TestRequirementCommentImageSameFrameReaderSwitchRetiresWithoutChild(t *testing.T) {
	s := commentImageLifecycleFixture(t)
	first := commentImageReaderFixture(t, s, "", "101")
	second := commentImageReaderFixture(t, s, "", "102")
	batch := first.commentImageBatch
	selected := first
	tester := ui.NewTester(func(c *ui.Context) {
		s.ensureRequirementContentJobs().visible = ""
		s.renderRequirementContent(c, setTheme(c, false), selected.taskID, selected.url)
		s.observeRequirementContentJobs()
	}, 640, 480)
	if s.requirementUnit.state.requirementContentJobs.entered != first || len(s.requirementUnit.state.requirementContentJobs.requests) != 0 {
		t.Fatal("fixture did not enter a reader without active children")
	}
	selected = second
	tester.Frame()
	if first.commentImageBatch != nil || batch.ctx.Err() == nil || s.requirementUnit.state.requirementContentJobs.entered != second || second.commentImageBatch.ctx.Err() != nil {
		t.Fatal("same-frame reader switch orphaned the previous batch")
	}
	mediaTestReceive(t, batch.done)
}

func TestMermaidInitializationBridgeReturnsBeforeResourceIO(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	owner := newMermaidInitializerWithDependencies(func(ctx context.Context) (mermaidInitializationScope, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return mermaidInitializationScope{}, nil
		case <-ctx.Done():
			return mermaidInitializationScope{}, ctx.Err()
		}
	}, func(context.Context, mermaidInitializationScope) (*editor.MermaidRenderer, error) {
		t.Error("cancelled initialization installed resources")
		return nil, context.Canceled
	})
	s := &app{mermaidInitializer: owner}
	state := newReadonlyMarkdownState("mermaid", "```mermaid\ngraph LR; A-->B\n```", nil)
	diagram := &markdownDiagramState{}
	state.diagrams[1] = diagram
	returned := make(chan struct{})
	go func() { s.renderMermaidDiagram(state, 1, "graph LR; A-->B", "key", diagram); close(returned) }()
	mediaTestReceive(t, returned)
	mediaTestReceive(t, started)
	closeMarkdownPreviewState(state)
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := owner.close(ctx); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNativeImagePoolDuplicateSubscribe(b *testing.B) {
	pool := newNativeImagePool()
	defer pool.close()
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 64, 64)))
	var loads atomic.Int32
	load := func(context.Context) (*ui.Bitmap, error) { loads.Add(1); return bitmap, nil }
	done := make(chan struct{}, 1)
	_, _ = pool.subscribe(context.Background(), "same", load, func(*ui.Bitmap, error) { done <- struct{}{} })
	<-done
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := pool.subscribe(context.Background(), "same", load, func(*ui.Bitmap, error) { done <- struct{}{} })
		if err != nil {
			b.Fatal(err)
		}
		<-done
	}
	if loads.Load() != 1 {
		b.Fatal("duplicate image was decoded again")
	}
}
