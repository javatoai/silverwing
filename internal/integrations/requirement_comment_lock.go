package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// Match the source gateway's process-wide stripes. File locks additionally
// coordinate same-user Go instances across separate application homes.
var requirementCommentProcessLocks = func() [64]chan struct{} {
	var locks [64]chan struct{}
	for i := range locks {
		locks[i] = make(chan struct{}, 1)
	}
	return locks
}()

func requirementCommentLockIdentity(item domain.ParticipatedWorkItem) string {
	return item.ProjectKey + "\n" + item.ID
}

func requirementCommentLockStripe(identity string) int {
	// Valid project keys and numeric IDs are ASCII, so this is Java String's
	// hashCode followed by and(Int.MAX_VALUE), as used by the source gateway.
	var hash uint32
	for i := 0; i < len(identity); i++ {
		hash = 31*hash + uint32(identity[i])
	}
	return int(hash&0x7fffffff) % len(requirementCommentProcessLocks)
}

func (s *Service) requirementCommentLockPath(item domain.ParticipatedWorkItem) (string, error) {
	temporary, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", fmt.Errorf("解析当前用户临时目录：%w", err)
	}
	// Match Source's user-temp scope: AppHome must not split the lock domain.
	// Requirement identity only chooses a fixed hash filename below this root.
	digest := sha256.Sum256([]byte(requirementCommentLockIdentity(item)))
	name := hex.EncodeToString(digest[:16]) // Source stableTextHash(identity, 32).
	return filepath.Join(temporary, "silverwing-meegle-comment-locks", name+".lock"), nil
}

func (s *Service) acquireRequirementCommentLock(ctx context.Context, item domain.ParticipatedWorkItem) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validMeegleProjectKey.MatchString(item.ProjectKey) || !numericIDPattern.MatchString(item.ID) {
		return nil, errors.New("需求评论锁的项目 Key 或需求编号不合法")
	}
	path, err := s.requirementCommentLockPath(item)
	if err != nil {
		return nil, err
	}
	processLock := requirementCommentProcessLocks[requirementCommentLockStripe(requirementCommentLockIdentity(item))]
	select {
	case processLock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseProcess := func() { <-processLock }
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			releaseProcess()
			return nil, err
		}
		releaseFile, err := storage.AcquireLock(path)
		if err == nil {
			if err := ctx.Err(); err != nil {
				releaseFile()
				releaseProcess()
				return nil, err
			}
			var releaseOnce sync.Once
			return func() {
				releaseOnce.Do(func() {
					releaseFile()
					releaseProcess()
				})
			}, nil
		}
		if !errors.Is(err, storage.ErrLockBusy) {
			releaseProcess()
			return nil, fmt.Errorf("获取需求评论文件锁：%w", err)
		}
		select {
		case <-ctx.Done():
			releaseProcess()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
