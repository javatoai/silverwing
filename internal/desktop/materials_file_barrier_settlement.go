package desktop

import (
	"context"
	"errors"
	"fmt"
)

// A resumed root action releases its launch gate, but a still-running request
// keeps result ownership until its normal UI callback can settle it. Abandon or
// shutdown explicitly ends that ownership and uses the engine-only fallback.
func (request *materialsFileJob) barrierOwnsResult() bool {
	request.leaseMu.Lock()
	barrier := request.barrier
	request.leaseMu.Unlock()
	// UI consumption precedes lease completion; it does not release ownership.
	if barrier == nil || barrier.owner == nil {
		return false
	}
	barrier.owner.mu.Lock()
	defer barrier.owner.mu.Unlock()
	return !barrier.abandoned && !barrier.owner.closed
}

// Called on the window thread after barrier.wait and before capturing a new
// root-action plan. A completed rename remains durable despite the barrier's
// cancellation: settle its original lease and existing UI/reading bridge first.
func (s *app) settleMaterialsFileBarrier(barrier *materialsFileJobBarrier) error {
	if barrier == nil {
		return nil
	}
	if barrier.owner == nil || barrier.owner != s.materialsFileJobs {
		return errors.New("资料文件操作屏障已变化，不能安全切换目录")
	}
	for _, request := range barrier.jobs {
		select {
		case <-request.done:
		default:
			return errors.New("资料文件操作尚未结束，不能安全切换目录")
		}
	}
	var settlementErr error
	for _, request := range barrier.jobs {
		settlementErr = errors.Join(settlementErr, s.settleMaterialsFileBarrierRequest(barrier, request))
	}
	return settlementErr
}

// This same UI-only settlement handles a queued late callback after resume.
// resultReady precedes Window.Update, so it does not wait on the window thread.
func (s *app) settleMaterialsFileBarrierRequest(barrier *materialsFileJobBarrier, request *materialsFileJob) error {
	request.leaseMu.Lock()
	if request.consumed || request.barrier != barrier {
		request.leaseMu.Unlock()
		return nil
	}
	if !request.resultReady {
		request.leaseMu.Unlock()
		return errors.New("资料文件操作没有可验证的后台结果")
	}
	result, resultErr, renamed := request.result, request.resultErr, request.renamed
	request.consumed = true
	request.leaseMu.Unlock()
	defer request.cancel()

	barrier.owner.mu.Lock()
	if barrier.owner.current[request.key] == request {
		delete(barrier.owner.current, request.key)
	}
	closed, abandoned := barrier.owner.closed, barrier.abandoned
	barrier.owner.mu.Unlock()
	if closed || abandoned || request.window.IsDestroyed() || !s.materialsFileScopeMatches(request.scope) {
		request.abandonRenameLease()
		if renamed {
			return errors.New("资料已重命名，但所属任务或目录已变化，请重新打开文件确认内容")
		}
		return nil
	}
	s.retireMaterialTextDeleteResult(request, result)

	if request.action.Kind == "document.rename" && materialsRenameCompleted(result) {
		_, _, _, valid := materialsRenamePaths(request.scope.root, request.action, result)
		leaseErr := request.completeRenameLease(result)
		if valid {
			// Reuse the established source-preserving rename mapping, even when
			// lease completion failed closed after the filesystem already changed.
			s.applyMaterialsRenameResult(request.action, result)
		}
		if !valid && leaseErr == nil {
			leaseErr = errors.New("资料重命名结果与原任务路径不一致")
		}
		if errors.Is(resultErr, context.Canceled) {
			resultErr = nil
		}
		return errors.Join(leaseErr, resultErr)
	}

	request.abandonRenameLease()
	s.restoreMaterialsFileReadRequest(request.action, false)
	if renamed {
		return fmt.Errorf("资料重命名结果无法确认，请重新打开文件确认内容：%w", errors.Join(resultErr, errors.New("缺少已完成的重命名结果")))
	}
	if resultErr != nil && !errors.Is(resultErr, context.Canceled) {
		// A failed read or a known, uncompleted mutation has no rename identity
		// to compose. Preserve its existing feature feedback without blocking a
		// root action that will independently validate the real filesystem.
		s.rejectMaterialsFileJob(request.action, resultErr)
	}
	return nil
}

func (s *app) reportMaterialsFileBarrierError(err error) {
	if err == nil {
		return
	}
	if s.lastError != "" {
		s.lastError += "；"
	}
	s.lastError += "资料文件操作结算失败：" + err.Error()
}
