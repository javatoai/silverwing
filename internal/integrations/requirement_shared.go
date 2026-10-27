package integrations

import (
	"crypto/sha256"
	"encoding/hex"
	"silverwing-go/internal/process"
	"sync"
)

func materialsHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

var requirementRetainedLocks = struct {
	sync.Mutex
	owners map[string][]func()
}{owners: map[string][]func(){}}

// Retain a comment lock when a child process has not finished cleanup.
func finishRequirementCommentLock(key string, release func(), err error) {
	if !process.IsCleanupFailure(err) {
		release()
		return
	}
	requirementRetainedLocks.Lock()
	requirementRetainedLocks.owners[key] = append(requirementRetainedLocks.owners[key], release)
	requirementRetainedLocks.Unlock()
}
