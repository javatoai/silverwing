//go:build windows

package process

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var isProcessInJob = kernel32.NewProc("IsProcessInJob")

type jobMemberHandle struct {
	handle        syscall.Handle
	pid           uint32
	creation      syscall.Filetime
	identityKnown bool
	verified      bool
}

func (p *processCleanup) captureJobBaseline() error {
	if p.baselineAttempted {
		return nil
	}
	p.baselineAttempted = true
	var accounting jobBasicAccounting
	ok, _, callErr := queryJob.Call(uintptr(p.handle), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
	if ok == 0 {
		return fmt.Errorf("QueryInformationJobObject 累计进程数：%w", callErr)
	}
	p.jobTotalBaseline = &accounting.TotalProcesses
	return nil
}

// A complete census is a snapshot. A process born after the baseline can have
// left the Job's list before its handle signals, so an empty Job cannot confirm
// that unobserved cohort. This guard fails conservatively; it is not a monitor
// for short-lived processes that exited before the baseline/census.
func confirmJobCohort(baseline *uint32, total uint32) error {
	if baseline == nil {
		return &CleanupError{Phase: "确认进程树退出", Cause: errors.New("缺少终止前 Job 累计进程数，成员退出未确认")}
	}
	if *baseline != total {
		return &CleanupError{Phase: "确认进程树退出", Cause: fmt.Errorf("Job 累计进程数从 %d 变为 %d，新生成员退出未确认", *baseline, total)}
	}
	return nil
}

// jobMemberIDs returns only a complete census of this private Job, including
// nested jobs. A PID is a discovery hint; it does not authorize process actions.
func (p *processCleanup) jobMemberIDs(deadline time.Time) ([]uint32, error) {
	if p.handle == 0 {
		return nil, errors.New("Job 句柄已关闭，无法查询成员")
	}
	const maximumMembers = 65536
	wordSize := unsafe.Sizeof(uintptr(0))
	headerWords := uintptr(8) / wordSize
	capacity := uint32(16)
	for {
		buffer := make([]uintptr, headerWords+uintptr(capacity))
		header := (*struct{ Assigned, Count uint32 })(unsafe.Pointer(&buffer[0]))
		var returnedBytes uint32
		ok, _, callErr := queryJob.Call(uintptr(p.handle), 3, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))*wordSize, uintptr(unsafe.Pointer(&returnedBytes)))
		if ok != 0 && header.Count == header.Assigned && header.Count <= capacity {
			if returnedBytes < uint32(8+uintptr(header.Count)*wordSize) {
				return nil, errors.New("Job 成员查询返回的数据不完整")
			}
			ids := make([]uint32, header.Count)
			for i := range ids {
				pid := buffer[headerWords+uintptr(i)]
				if pid == 0 || uint64(pid) > uint64(^uint32(0)) {
					return nil, errors.New("Job 成员查询返回无效进程标识")
				}
				ids[i] = uint32(pid)
			}
			return ids, nil
		}
		if ok == 0 && !errors.Is(callErr, syscall.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("QueryInformationJobObject 成员：%w", callErr)
		}
		capacity = max(capacity*2, header.Assigned)
		if capacity > maximumMembers || time.Until(deadline) <= 0 {
			return nil, errors.New("未能在清理期限内完整查询 Job 成员")
		}
	}
}

// Keep a handle if CloseHandle fails so the final Close can retry it. An
// unverified handle is never killed or awaited, even if its PID was in a census.
func (p *processCleanup) closeMemberCandidate(member jobMemberHandle) error {
	if err := syscall.CloseHandle(member.handle); err != nil {
		p.members = append(p.members, member)
		return fmt.Errorf("关闭 Job 成员候选 PID %d：%w", member.pid, err)
	}
	return nil
}

// retryErr may be resolved by a fresh census when a process exits naturally or
// its PID is reused outside this Job. Retained-handle errors remain fatal.
func (p *processCleanup) retainJobMember(pid uint32) (retryErr, retainedErr error) {
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE|0x1000, false, pid)
	if err != nil {
		return fmt.Errorf("打开 Job 成员 PID %d：%w", pid, err), nil
	}
	member := jobMemberHandle{handle: handle, pid: pid}
	var inJob uint32
	ok, _, callErr := isProcessInJob.Call(uintptr(handle), uintptr(p.handle), uintptr(unsafe.Pointer(&inJob)))
	if ok == 0 || inJob == 0 {
		if ok == 0 {
			retryErr = fmt.Errorf("确认 Job 成员 PID %d：%w", pid, callErr)
		} else {
			retryErr = fmt.Errorf("PID %d 已不属于本命令 Job", pid)
		}
		return retryErr, p.closeMemberCandidate(member)
	}
	member.verified = true
	var exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &member.creation, &exit, &kernel, &user); err != nil {
		p.members = append(p.members, member)
		return nil, fmt.Errorf("确认 Job 成员 PID %d 创建身份：%w", pid, err)
	}
	member.identityKnown = true
	for _, previous := range p.members {
		if previous.verified && previous.identityKnown && previous.pid == pid && previous.creation == member.creation {
			return nil, p.closeMemberCandidate(member)
		}
	}
	p.members = append(p.members, member)
	return nil, nil
}

func (p *processCleanup) captureJobMembers(deadline time.Time) error {
	var retainedErrors []error
	for {
		ids, err := p.jobMemberIDs(deadline)
		if err != nil {
			return errors.Join(append(retainedErrors, err)...)
		}
		var pendingErrors []error
		for _, pid := range ids {
			if time.Until(deadline) <= 0 {
				return errors.Join(append(retainedErrors, errors.New("捕获 Job 成员身份超过清理期限"))...)
			}
			retryErr, retainedErr := p.retainJobMember(pid)
			if retryErr != nil {
				pendingErrors = append(pendingErrors, retryErr)
			}
			if retainedErr != nil {
				retainedErrors = append(retainedErrors, retainedErr)
			}
		}
		if len(pendingErrors) == 0 {
			return errors.Join(retainedErrors...)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.Join(errors.Join(retainedErrors...), errors.Join(pendingErrors...), errors.New("未能确认 Job 成员身份"))
		}
		// Only unresolved census identities are retried. A vanished PID is
		// accepted only when a subsequent complete census no longer lists it.
		time.Sleep(min(remaining, processCleanupPoll))
	}
}

func waitProcessHandle(handle syscall.Handle, deadline time.Time, description string) error {
	remaining := max(time.Duration(0), time.Until(deadline))
	milliseconds := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
	event, err := syscall.WaitForSingleObject(handle, milliseconds)
	if err != nil {
		return fmt.Errorf("等待%s退出：%w", description, err)
	}
	if event != syscall.WAIT_OBJECT_0 {
		return fmt.Errorf("%s 内未能确认%s退出（等待状态 %d）", processCleanupTimeout, description, event)
	}
	return nil
}

func (p *processCleanup) waitJobMembers(deadline time.Time) error {
	var waitErrors []error
	if p.rootHandle == 0 {
		waitErrors = append(waitErrors, errors.New("缺少主进程同步句柄"))
	} else if err := waitProcessHandle(p.rootHandle, deadline, "主进程"); err != nil {
		waitErrors = append(waitErrors, err)
	}
	for _, member := range p.members {
		if member.verified {
			if err := waitProcessHandle(member.handle, deadline, fmt.Sprintf("Job 成员 PID %d", member.pid)); err != nil {
				waitErrors = append(waitErrors, err)
			}
		}
	}
	return errors.Join(waitErrors...)
}

func (p *processCleanup) closeJobMembers() error {
	var closeErrors []error
	remaining := p.members[:0]
	for _, member := range p.members {
		if err := syscall.CloseHandle(member.handle); err != nil {
			remaining = append(remaining, member)
			closeErrors = append(closeErrors, fmt.Errorf("关闭 Job 成员 PID %d 同步句柄：%w", member.pid, err))
		}
	}
	p.members = remaining
	return errors.Join(closeErrors...)
}
