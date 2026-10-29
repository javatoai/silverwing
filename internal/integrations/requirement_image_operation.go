package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"

	"silverwing-go/internal/process"
)

const (
	requirementImageOperationBytes   = 32 << 20
	requirementImageOperationEntries = 16
	requirementImageOperationFlights = 4
)

type requirementImageOperationKey struct{}

// The batch owns downloads; callers own authorization and cancellation of
// their subscriptions. No bytes or identity survive the batch's retirement.
type requirementImageOperation struct {
	owner      context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	done       chan struct{}
	changed    chan struct{}
	slots      chan struct{}
	flights    map[requirementImageByteKey]*requirementImageByteFlight
	cache      map[requirementImageByteKey][]byte
	order      []requirementImageByteKey
	bytes      int
	cleanupErr error
}

type requirementImageByteKey struct {
	service                                           *Service
	home, accountContext, account, catalog, item, url string
	epoch                                             uint64
}

type requirementImageByteFlight struct {
	done chan struct{}
	data []byte // private, immutable after done closes
	err  error
}

func WithRequirementImageReadOperation(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	owner, cancel := context.WithCancel(parent)
	operation := &requirementImageOperation{owner: owner, cancel: cancel, done: make(chan struct{}), changed: make(chan struct{}),
		slots: make(chan struct{}, 2), flights: make(map[requirementImageByteKey]*requirementImageByteFlight), cache: make(map[requirementImageByteKey][]byte)}
	context.AfterFunc(owner, operation.stop)
	return context.WithValue(owner, requirementImageOperationKey{}, operation)
}

func requirementImageReadOperation(ctx context.Context) *requirementImageOperation {
	if ctx == nil {
		return nil
	}
	operation, _ := ctx.Value(requirementImageOperationKey{}).(*requirementImageOperation)
	return operation
}

// Admission failures belong to the caller's authorization/snapshot boundary;
// unlike a network error they must not become another comment's saved error.
type requirementImageAdmissionError struct{ cause error }

func (err *requirementImageAdmissionError) Error() string { return err.cause.Error() }
func (err *requirementImageAdmissionError) Unwrap() error { return err.cause }

func CloseRequirementImageReadOperation(wait, operationContext context.Context) error {
	operation := requirementImageReadOperation(operationContext)
	if operation == nil {
		return nil
	}
	operation.stop()
	select {
	case <-operation.done:
		operation.mu.Lock()
		defer operation.mu.Unlock()
		return operation.cleanupErr
	case <-wait.Done():
		return operation.failure(wait.Err())
	}
}

func (operation *requirementImageOperation) stop() {
	operation.mu.Lock()
	operation.stopLocked()
	operation.mu.Unlock()
	operation.cancel()
}

func (operation *requirementImageOperation) stopLocked() {
	if !operation.closed {
		operation.closed = true
		operation.cache, operation.order, operation.bytes = nil, nil, 0
		close(operation.changed)
		if len(operation.flights) == 0 {
			close(operation.done)
		}
	}
}

// read returns borrowed immutable bytes to the private attachment saver. The
// supplied loader must own its output; public resource DTOs never expose it.
func (operation *requirementImageOperation) read(ctx context.Context, key requirementImageByteKey, load func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, operation.failure(err)
		}
		operation.mu.Lock()
		if operation.closed || operation.owner.Err() != nil {
			err := operation.cleanupErr
			operation.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, context.Canceled
		}
		if data, ok := operation.cache[key]; ok {
			operation.touch(key)
			operation.mu.Unlock()
			return data, nil
		}
		flight := operation.flights[key]
		if flight == nil && len(operation.flights) == requirementImageOperationFlights {
			changed := operation.changed
			operation.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, operation.failure(ctx.Err())
			case <-operation.owner.Done():
				return nil, operation.failure(operation.owner.Err())
			case <-changed:
				continue
			}
		}
		if flight == nil {
			flight = &requirementImageByteFlight{done: make(chan struct{})}
			operation.flights[key] = flight
			go operation.run(key, flight, load)
		}
		operation.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, operation.failure(ctx.Err())
		case <-operation.owner.Done():
			return nil, operation.failure(operation.owner.Err())
		case <-flight.done:
			if err := ctx.Err(); err != nil {
				return nil, operation.failure(err)
			}
			return flight.data, flight.err
		}
	}
}

func (operation *requirementImageOperation) failure(fallback error) error {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	if operation.cleanupErr != nil {
		return operation.cleanupErr
	}
	return fallback
}

func (operation *requirementImageOperation) run(key requirementImageByteKey, flight *requirementImageByteFlight, load func(context.Context) ([]byte, error)) {
	var data []byte
	var err error
	select {
	case operation.slots <- struct{}{}:
		defer func() { <-operation.slots }()
		if err = operation.owner.Err(); err == nil {
			data, err = load(operation.owner)
		}
	case <-operation.owner.Done():
		err = operation.owner.Err()
	}
	operation.mu.Lock()
	cleanupFailed := process.IsCleanupFailure(err)
	defer func() {
		operation.mu.Unlock()
		if cleanupFailed {
			operation.cancel()
		}
	}()
	if cleanupFailed {
		operation.cleanupErr = errors.Join(operation.cleanupErr, err)
		operation.stopLocked()
	}
	if operation.closed || operation.owner.Err() != nil {
		data = nil
		if operation.cleanupErr != nil {
			err = operation.cleanupErr
		} else {
			err = context.Canceled
		}
	}
	flight.data, flight.err = data, err
	delete(operation.flights, key)
	if !operation.closed && err == nil && len(data) > 0 && len(data) <= requirementImageOperationBytes {
		for len(operation.order) > 0 && (len(operation.order) >= requirementImageOperationEntries || operation.bytes+len(data) > requirementImageOperationBytes) {
			oldest := operation.order[0]
			operation.order = operation.order[1:]
			operation.bytes -= len(operation.cache[oldest])
			delete(operation.cache, oldest)
		}
		operation.cache[key] = data
		operation.order = append(operation.order, key)
		operation.bytes += len(data)
	}
	close(flight.done)
	if operation.closed {
		if len(operation.flights) == 0 {
			close(operation.done)
		}
	} else {
		close(operation.changed)
		operation.changed = make(chan struct{})
	}
}

func (operation *requirementImageOperation) touch(key requirementImageByteKey) {
	for index, current := range operation.order {
		if current == key {
			copy(operation.order[index:], operation.order[index+1:])
			operation.order[len(operation.order)-1] = key
			return
		}
	}
}

// A catalog version uses the already-authorized resource projection, not a
// second serialization of potentially 16 MiB of comment prose. Length prefixes
// keep arbitrary external identifiers unambiguous.
func commentImageCatalogVersion(fetchedAt int64, resources []RequirementCommentResource) string {
	digest := sha256.New()
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(fetchedAt))
	_, _ = digest.Write(length[:])
	for _, resource := range resources {
		for _, value := range []string{resource.CommentID, resource.Generation, resource.URL} {
			binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = digest.Write(length[:])
			_, _ = digest.Write([]byte(value))
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}
