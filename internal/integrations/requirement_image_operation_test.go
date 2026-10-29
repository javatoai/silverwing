package integrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type requirementImageByteAnswer struct {
	data []byte
	err  error
}

func imageOperationFixture(t *testing.T) (context.Context, *requirementImageOperation) {
	t.Helper()
	parent, cancel := context.WithCancel(context.Background())
	ctx := WithRequirementImageReadOperation(parent)
	t.Cleanup(func() {
		cancel()
		wait, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := CloseRequirementImageReadOperation(wait, ctx); err != nil {
			t.Errorf("image batch cleanup: %v", err)
		}
	})
	return ctx, requirementImageReadOperation(ctx)
}

func TestRequirementImageOperationSharesDownloadWithIndependentCancellation(t *testing.T) {
	ctx, operation := imageOperationFixture(t)
	first, cancel := context.WithCancel(ctx)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return []byte("owned image"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	key := requirementImageByteKey{url: "trusted"}
	a, b := make(chan requirementImageByteAnswer, 1), make(chan requirementImageByteAnswer, 1)
	go func() { data, err := operation.read(first, key, load); a <- requirementImageByteAnswer{data, err} }()
	<-started
	go func() { data, err := operation.read(ctx, key, load); b <- requirementImageByteAnswer{data, err} }()
	cancel()
	if result := <-a; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("subscriber cancel: %+v", result)
	}
	close(release)
	if result := <-b; result.err != nil || string(result.data) != "owned image" {
		t.Fatalf("other subscriber: %+v", result)
	}
	if _, err := operation.read(ctx, key, load); err != nil || calls.Load() != 1 {
		t.Fatalf("warm byte read: %v, downloads=%d", err, calls.Load())
	}
}

func TestRequirementImageOperationSeparatesScopeAndDoesNotCacheFailures(t *testing.T) {
	ctx, operation := imageOperationFixture(t)
	key := requirementImageByteKey{service: &Service{}, home: "home", accountContext: "profile", account: "account", epoch: 1, catalog: "catalog", item: "item", url: "URL"}
	keys := []requirementImageByteKey{key}
	for field := 0; field < 8; field++ {
		other := key
		switch field {
		case 0:
			other.service = &Service{}
		case 1:
			other.home += "2"
		case 2:
			other.accountContext += "2"
		case 3:
			other.account += "2"
		case 4:
			other.epoch++
		case 5:
			other.catalog += "2"
		case 6:
			other.item += "2"
		case 7:
			other.url += "2"
		}
		keys = append(keys, other)
	}
	var calls atomic.Int32
	load := func(context.Context) ([]byte, error) { return []byte(fmt.Sprint(calls.Add(1))), nil }
	for _, current := range keys {
		if _, err := operation.read(ctx, current, load); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != int32(len(keys)) {
		t.Fatalf("cross-scope byte reuse: calls=%d", calls.Load())
	}
	failure := errors.New("download failed")
	failedKey := requirementImageByteKey{url: "failure"}
	if _, err := operation.read(ctx, failedKey, func(context.Context) ([]byte, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := operation.read(ctx, failedKey, load); err != nil {
		t.Fatalf("failed flight prevented retry: %v", err)
	}
}

func TestRequirementImageOperationBoundsAdmissionAndRetainedBytes(t *testing.T) {
	ctx, operation := imageOperationFixture(t)
	started, release, results := make(chan struct{}, 4), make(chan struct{}), make(chan error, 4)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var calls atomic.Int32
	load := func(context.Context) ([]byte, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return []byte("image"), nil
	}
	for index := 0; index < requirementImageOperationFlights; index++ {
		go func(index int) {
			_, err := operation.read(ctx, requirementImageByteKey{url: fmt.Sprint(index)}, load)
			results <- err
		}(index)
	}
	for index := 0; index < 2; index++ {
		<-started
	}
	deadline := time.Now().Add(time.Second)
	for {
		operation.mu.Lock()
		admitted := len(operation.flights)
		operation.mu.Unlock()
		if admitted == requirementImageOperationFlights {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d flights admitted", admitted)
		}
		time.Sleep(time.Millisecond)
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := operation.read(wait, requirementImageByteKey{url: "overflow"}, load); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 2 {
		t.Fatalf("admission overflow: %v, calls=%d", err, calls.Load())
	}
	close(release)
	for index := 0; index < requirementImageOperationFlights; index++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < requirementImageOperationEntries+1; index++ {
		if _, err := operation.read(ctx, requirementImageByteKey{url: fmt.Sprintf("small-%d", index)}, func(context.Context) ([]byte, error) { return []byte{1}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	operation.mu.Lock()
	entries := len(operation.cache)
	operation.mu.Unlock()
	if entries != requirementImageOperationEntries {
		t.Fatalf("retained entries=%d", entries)
	}
	for index := 0; index < 2; index++ {
		if _, err := operation.read(ctx, requirementImageByteKey{url: fmt.Sprintf("large-%d", index)}, func(context.Context) ([]byte, error) { return make([]byte, requirementImageOperationBytes/2+1), nil }); err != nil {
			t.Fatal(err)
		}
	}
	operation.mu.Lock()
	bytes, entries := operation.bytes, len(operation.cache)
	operation.mu.Unlock()
	if bytes > requirementImageOperationBytes || entries != 1 {
		t.Fatalf("byte budget=%d entries=%d", bytes, entries)
	}
}

func TestRequirementImageOperationCloseWaitsForLoaderCleanup(t *testing.T) {
	ctx, operation := imageOperationFixture(t)
	started, cleanup := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-cleanup:
		default:
			close(cleanup)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := operation.read(ctx, requirementImageByteKey{url: "image"}, func(ctx context.Context) ([]byte, error) {
			close(started)
			<-ctx.Done()
			<-cleanup
			return nil, ctx.Err()
		})
		result <- err
	}()
	<-started
	wait, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := CloseRequirementImageReadOperation(wait, ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close did not wait for cleanup: %v", err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(cleanup)
	if err := CloseRequirementImageReadOperation(context.Background(), ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := operation.read(context.Background(), requirementImageByteKey{}, func(context.Context) ([]byte, error) { t.Fatal("closed batch started a download"); return nil, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRequirementImageOperationCleanupFailureStopsBatchAndSurvivesClose(t *testing.T) {
	ctx := WithRequirementImageReadOperation(context.Background())
	operation := requirementImageReadOperation(ctx)
	failure := &process.CleanupError{Phase: "fixture", Cause: errors.New("unconfirmed child")}
	if _, err := operation.read(ctx, requirementImageByteKey{url: "first"}, func(context.Context) ([]byte, error) { return nil, failure }); !process.IsCleanupFailure(err) {
		t.Fatalf("cleanup failure lost at read: %v", err)
	}
	if _, err := operation.read(context.Background(), requirementImageByteKey{url: "next"}, func(context.Context) ([]byte, error) { return nil, errors.New("must not start") }); !process.IsCleanupFailure(err) {
		t.Fatalf("cleanup failure did not stop admission: %v", err)
	}
	if err := CloseRequirementImageReadOperation(context.Background(), ctx); !process.IsCleanupFailure(err) {
		t.Fatalf("cleanup failure lost at drain: %v", err)
	}
}

func TestRequirementCommentImageBatchSharesBytesAndKeepsPerCommentAuthorization(t *testing.T) {
	f := newRequirementReadFixture(t)
	comments := domain.RequirementComments{Comments: []domain.RequirementComment{
		{ID: "first", Content: "First comment ![](" + commentImageFixtureURL + ")"},
		{ID: "second", Content: "Different comment ![](" + commentImageFixtureURL + ")"},
	}}
	key := requirementCacheKey("comments", f.raw, f.item)
	if _, err := f.service.writeRequirementCommentsCache(key, comments, 1); err != nil {
		t.Fatal(err)
	}
	ctx, _ := imageOperationFixture(t)
	read := func(resource RequirementCommentResource) RequirementCommentResource {
		t.Helper()
		payload := f.payload()
		payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"] = "comment", resource.CommentID, resource.Generation, resource.URL
		value, err := f.service.downloadRequirementAttachment(ctx, f.config, payload)
		if err != nil {
			t.Fatal(err)
		}
		result, ok := value.(RequirementCommentResource)
		if !ok || result.Path == "" || result.Error != "" {
			t.Fatalf("comment image result=%#v", value)
		}
		return result
	}
	resources := commentImageResources(comments.Comments)
	first, second := read(resources[0]), read(resources[1])
	if first.CommentID == second.CommentID || first.Path != second.Path {
		t.Fatalf("per-comment ownership or shared storage: %+v %+v", first, second)
	}
	downloads := func() int {
		count := 0
		for _, call := range f.calls(t) {
			if strings.HasPrefix(call, "attachment +download ") {
				count++
			}
		}
		return count
	}
	if downloads() != 1 {
		t.Fatalf("same batch downloaded %d times", downloads())
	}
	entries, err := os.ReadDir(filepath.Join(f.service.requirementCommentImageDirectory(key), "assets"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("duplicate assets=%d err=%v", len(entries), err)
	}
	payload := f.payload()
	payload["scope"], payload["commentID"], payload["commentGeneration"], payload["fileUrl"] = "comment", "not-authorized", resources[0].Generation, resources[0].URL
	if _, err := f.service.downloadRequirementAttachment(ctx, f.config, payload); err == nil || downloads() != 1 {
		t.Fatalf("byte reuse bypassed comment ownership: %v downloads=%d", err, downloads())
	}
	// Same URLs, new authoritative comment contents in the same operation:
	// catalog generation must keep the previous bytes out of this read.
	for index := range comments.Comments {
		comments.Comments[index].Content += " refreshed"
	}
	if _, err := f.service.writeRequirementCommentsCache(key, comments, 2); err != nil {
		t.Fatal(err)
	}
	resources = commentImageResources(comments.Comments)
	read(resources[0])
	read(resources[1])
	if downloads() != 2 {
		t.Fatalf("catalog change did not reload once: downloads=%d", downloads())
	}
}
