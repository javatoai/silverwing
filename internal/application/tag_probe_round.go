package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

type tagProbeRoundKey struct{}

// A holder belongs to one scheduler tick, not to the application. It shares
// only the authoritative local candidate read; repository queries, progress
// and cancellation continue to belong to their individual requests.
type tagProbeRound struct {
	ctx      context.Context
	mu       sync.Mutex
	scope    string
	started  bool
	done     chan struct{}
	snapshot *delivery.TagProbeSnapshot
	err      error
}

func NewTagProbeRoundContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tagProbeRoundKey{}, &tagProbeRound{ctx: ctx, done: make(chan struct{})})
}

func tagProbeRoundScope(home, account string, config domain.AppConfig, tasks []domain.TaskManifest) string {
	encoded, _ := json.Marshal(struct {
		Home, Account string
		Config        domain.AppConfig
		Tasks         []domain.TaskManifest
	}{home, account, config, tasks})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func readTagProbeRound(ctx context.Context, scope string, read func(context.Context) (*delivery.TagProbeSnapshot, error)) (*delivery.TagProbeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	round, ok := ctx.Value(tagProbeRoundKey{}).(*tagProbeRound)
	if !ok {
		return read(ctx)
	}
	round.mu.Lock()
	if round.started && round.scope != scope {
		round.mu.Unlock()
		return nil, errors.New("本轮 Tag 探测的账号、配置或任务身份已变化，请重新读取")
	}
	if !round.started {
		round.started, round.scope = true, scope
		round.mu.Unlock()
		// A cancelled repository must not cancel the shared local read needed by
		// its siblings. The owning session still cancels the entire round.
		snapshot, err := read(round.ctx)
		round.mu.Lock()
		round.snapshot, round.err = snapshot, err
		close(round.done)
		round.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return snapshot, err
	}
	done := round.done
	round.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return round.snapshot, round.err
	}
}
