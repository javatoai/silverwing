package workspace

import (
	"context"
	"errors"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

const comparePrefetchBatchFiles = 4

func compareChangeKey(change CompareChange) string {
	return change.Path + "\x00" + change.OldPath + "\x00" + change.Kind
}

// beginForeground cancels low-priority work before waiting for the current
// batch to release its gate. Concurrent clicks then share the ordinary cache.
func (session *compareSession) beginForeground(ctx context.Context, change CompareChange) (func(), error) {
	session.mu.Lock()
	if session.readGate == nil {
		session.readGate = make(chan struct{}, 1)
	}
	session.foreground++
	session.pinned = compareChangeKey(change)
	if session.prefetchCancel != nil {
		session.prefetchCancel()
	}
	gate := session.readGate
	session.mu.Unlock()
	finish := func() {
		session.mu.Lock()
		session.foreground--
		session.mu.Unlock()
	}
	select {
	case gate <- struct{}{}:
		return func() { <-gate; finish() }, nil
	case <-ctx.Done():
		finish()
		return nil, ctx.Err()
	}
}

// PrefetchSessionContext warms only a retainable window. The caller owns its
// cancellation and can replace candidates as the visible/current file changes.
// Every batch validates the original task/config/scope and repository identity.
func (s *WorkspaceGitDiffService) PrefetchSessionContext(ctx context.Context, manager *WorkspaceGitDiffSessionManager, sessionID, owner, configIdentity string, ws domain.ServiceWorkspace, request CompareRequest, current CompareChange, candidates []CompareChange, mainBranch WorkspaceMainBranch) error {
	if manager == nil {
		return errors.New("Git 差异会话管理器不可用")
	}
	ownerHash, err := gitCompareSessionOwnerHash(owner, configIdentity, ws, request, mainBranch)
	if err != nil {
		return err
	}
	ctx, session, release, err := manager.acquire(ctx, sessionID, ownerHash)
	if err != nil {
		return err
	}
	defer release()
	if request.ExpectedBaseCommit != session.state.baseCommit || request.ExpectedTargetCommit == "" || request.ExpectedTargetCommit != session.state.targetCommit {
		return errors.New("Git 差异快照已变化，请重新读取文件列表后再预览")
	}
	window := make([]CompareChange, 0, comparePreviewCacheFiles)
	seen := make(map[string]bool)
	for _, change := range append([]CompareChange{current}, candidates...) {
		if change.Path == "" {
			continue
		}
		canonical, found := matchCompareChange(session.files, change)
		if !found {
			return errors.New("预读文件已不属于当前差异，请重新读取文件列表")
		}
		key := compareChangeKey(canonical)
		if !seen[key] && len(window) < comparePreviewCacheFiles {
			seen[key] = true
			window = append(window, canonical)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	session.mu.Lock()
	if session.foreground > 0 {
		session.mu.Unlock()
		return context.Canceled
	}
	if session.prefetchCancel != nil {
		session.prefetchCancel()
	}
	if session.readGate == nil {
		session.readGate = make(chan struct{}, 1)
	}
	session.prefetchGeneration++
	generation := session.prefetchGeneration
	session.prefetchCancel = cancel
	if current.Path != "" {
		session.pinned = compareChangeKey(current)
	}
	gate := session.readGate
	session.mu.Unlock()
	defer func() {
		session.mu.Lock()
		if session.prefetchGeneration == generation {
			session.prefetchCancel = nil
		}
		session.mu.Unlock()
	}()
	for len(window) > 0 {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		session.mu.Lock()
		foreground := session.foreground
		session.mu.Unlock()
		if foreground > 0 || ctx.Err() != nil {
			<-gate
			return context.Canceled
		}
		batch := make([]CompareChange, 0, comparePrefetchBatchFiles)
		for len(window) > 0 && len(batch) < comparePrefetchBatchFiles {
			change := window[0]
			window = window[1:]
			if _, hit := session.cached(change); !hit {
				batch = append(batch, change)
			}
		}
		started := time.Now()
		if len(batch) > 0 {
			err = s.withPreparedWorkspace(ctx, ws, session.state, func(string) error {
				return session.prefetchBatch(ctx, batch)
			})
		}
		<-gate
		observeGitComparePhase(ctx, "prefetch_batch", started, map[string]any{"files": len(batch), "failed": err != nil})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func batchCompareChange(change CompareChange) bool {
	if change.OldPath != "" || change.Kind != string(domain.WorkspaceGitModified) && change.Kind != string(domain.WorkspaceGitAdded) && change.Kind != string(domain.WorkspaceGitDeleted) {
		return false
	}
	for _, r := range change.Path {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./", r)) {
			return false
		}
	}
	return change.Path != ""
}

func (session *compareSession) prefetchBatch(ctx context.Context, changes []CompareChange) error {
	ordinary := make([]CompareChange, 0, len(changes))
	for _, change := range changes {
		if batchCompareChange(change) {
			ordinary = append(ordinary, change)
			continue
		}
		// Quoted paths, copies, renames and non-blob entries retain the original
		// single-file validation and output limits.
		if _, _, err := session.preview(ctx, change); err != nil {
			return err
		}
	}
	if len(ordinary) == 0 {
		return ctx.Err()
	}
	requests := make([]compareBlobRequest, 0, len(ordinary)*2)
	offsets := []int{0}
	paths := make([]string, 0, len(ordinary))
	for _, change := range ordinary {
		blobs, _, _ := session.state.previewBlobRequests(change)
		requests = append(requests, blobs...)
		offsets = append(offsets, len(requests))
		paths = append(paths, change.Path)
	}
	// At most 8 distinct blobs of 512 KiB and 4 independently bounded patches
	// can be retained by this batch; oversized objects never enter cat-file.
	blobs, complete, err := session.state.readBlobs(ctx, requests)
	if err != nil {
		return err
	}
	args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=0", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", session.state.baseTree, session.state.targetTree, "--"}
	args = append(args, paths...)
	patch, err := session.state.service.runInState(ctx, session.state, len(ordinary)*3*MaxWorkspaceFilePreviewBytes, false, nil, args...)
	if err != nil {
		return err
	}
	if !complete || patch.ExitCode != 0 || patch.StdoutTruncated || patch.StderrTruncated {
		return session.prefetchSingles(ctx, ordinary)
	}
	patches, valid := splitCompareBatchPatch(patch.Stdout, ordinary)
	if !valid {
		return session.prefetchSingles(ctx, ordinary)
	}
	for i, change := range ordinary {
		text := patches[change.Path]
		if len(text) > 3*MaxWorkspaceFilePreviewBytes {
			continue
		}
		preview, cacheable, err := session.state.previewWithInputs(ctx, change, blobs[offsets[i]:offsets[i+1]], true, &text)
		if err != nil {
			return err
		}
		if cacheable && preview.Comparison != nil && !preview.Comparison.Truncated && ctx.Err() == nil {
			session.cachePreview(change, preview)
		}
	}
	return ctx.Err()
}

func (session *compareSession) prefetchSingles(ctx context.Context, changes []CompareChange) error {
	for _, change := range changes {
		if _, _, err := session.preview(ctx, change); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func splitCompareBatchPatch(patch string, changes []CompareChange) (map[string]string, bool) {
	headers := make(map[string]string, len(changes))
	for _, change := range changes {
		headers["diff --git a/"+change.Path+" b/"+change.Path] = change.Path
	}
	result := make(map[string]string, len(changes))
	path, start, offset := "", 0, 0
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if path != "" {
				result[path] = patch[start:offset]
			}
			var exists bool
			path, exists = headers[strings.TrimSuffix(line, "\n")]
			if !exists || result[path] != "" {
				return nil, false
			}
			start = offset
		}
		offset += len(line)
	}
	if path != "" {
		result[path] = patch[start:]
	}
	return result, len(result) == len(changes)
}
