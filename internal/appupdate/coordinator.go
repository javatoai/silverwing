package appupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"silverwing-go/internal/domain"
)

type Coordinator struct {
	mu       sync.Mutex
	source   Source
	cache    string
	snapshot Snapshot
	plan     *Plan
	cancel   context.CancelFunc
}

func New(source Source, cache string) *Coordinator { return &Coordinator{source: source, cache: cache} }

func (c *Coordinator) Status() Snapshot { c.mu.Lock(); defer c.mu.Unlock(); return c.snapshot }

func (c *Coordinator) Prepare(ctx context.Context, installation Installation, version string) (snapshot Snapshot, err error) {
	compare, valid := domain.CompareReleaseVersions(version, installation.Identity.Version)
	if !valid || compare <= 0 {
		return snapshot, errors.New("目标不是较新的稳定版本")
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return snapshot, errors.New("已有更新正在准备")
	}
	if err := os.MkdirAll(c.cache, 0o700); err != nil {
		c.mu.Unlock()
		return snapshot, err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		c.mu.Unlock()
		return snapshot, err
	}
	id := hex.EncodeToString(idBytes)
	jobDir, err := os.MkdirTemp(c.cache, "job-")
	if err != nil {
		c.mu.Unlock()
		return snapshot, err
	}
	ctx, cancel := context.WithCancel(ctx)
	c.cancel, c.plan, c.snapshot = cancel, nil, Snapshot{ID: id, Version: version, Phase: "preparing"}
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.cancel = nil
		if err != nil {
			c.snapshot.Phase, c.snapshot.Error = "failed", err.Error()
			if errors.Is(err, context.Canceled) {
				c.snapshot.Phase = "cancelled"
			}
			c.plan = nil
		}
		snapshot = c.snapshot
	}()
	if installation.Kind != MacApp {
		if err = VerifyLayout(installation.Root, installation.Layout); err != nil {
			return snapshot, err
		}
	}
	layoutHash, err := Digest(installation.LayoutPath)
	if err != nil {
		return snapshot, err
	}
	exeHash, err := Digest(installation.Executable)
	if err != nil {
		return snapshot, err
	}
	bundle, err := c.source.Prepare(ctx, installation, version, filepath.Join(jobDir, "new"), func(downloaded, total int64) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.snapshot.Downloaded, c.snapshot.Total = downloaded, total
	})
	if err != nil {
		return snapshot, err
	}
	plan := Plan{Schema: 1, ID: id, Installation: installation, New: bundle, Backup: filepath.Join(jobDir, "backup"), ParentPID: os.Getpid(), LayoutDigest: layoutHash, ExecutableDigest: exeHash, HealthToken: id}
	if installation.Kind == MSI || installation.Kind == Setup {
		c.mu.Lock()
		c.snapshot.Phase = "recovery"
		c.snapshot.Downloaded, c.snapshot.Total = 0, 0
		c.mu.Unlock()
		recovery, e := c.source.Prepare(ctx, installation, installation.Identity.Version, filepath.Join(jobDir, "recovery"), nil)
		if e != nil {
			return snapshot, fmt.Errorf("无法准备当前版本的恢复安装包：%w", e)
		}
		plan.Recovery = &recovery
	}
	if err = ctx.Err(); err != nil {
		return snapshot, err
	}
	if err = WriteJSON(filepath.Join(jobDir, "plan.json"), plan); err != nil {
		return snapshot, err
	}
	c.mu.Lock()
	if err = ctx.Err(); err != nil {
		c.mu.Unlock()
		return snapshot, err
	}
	c.plan = &plan
	c.snapshot.Phase = "ready"
	c.mu.Unlock()
	return snapshot, nil
}

func (c *Coordinator) Cancel() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelLocked()
}

func (c *Coordinator) CancelJob(id string) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" || c.snapshot.ID != id {
		return c.snapshot, errors.New("取消请求不属于当前更新")
	}
	return c.cancelLocked(), nil
}

func (c *Coordinator) cancelLocked() Snapshot {
	if c.cancel != nil {
		c.cancel()
	}
	c.plan = nil
	if c.snapshot.ID != "" {
		c.snapshot.Phase = "cancelled"
	}
	return c.snapshot
}

// Plan is available only after the explicit prepare has completed successfully.
func (c *Coordinator) Plan(id string) (Plan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil || c.plan == nil || c.snapshot.Phase != "ready" || c.snapshot.ID != id {
		return Plan{}, errors.New("更新准备已失效，请重新准备")
	}
	return *c.plan, nil
}
