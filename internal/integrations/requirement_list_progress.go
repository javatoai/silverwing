package integrations

import (
	"context"
	"errors"
	"sync/atomic"
)

// RequirementListProgress describes completed work in one list read. It is
// transient and never becomes part of the list result or persistent cache.
type RequirementListProgress struct {
	Sequence         uint64
	Phase, Status    string
	WorkItemType     string
	Label, Message   string
	Completed, Total int
}

type requirementListReporter struct {
	ctx      context.Context
	callback func(RequirementListProgress)
	sequence atomic.Uint64
	phase    string
}

func (p *requirementListReporter) emit(event RequirementListProgress) {
	if p == nil || p.callback == nil || p.ctx.Err() != nil {
		return
	}
	p.phase = event.Phase
	event.Sequence = p.sequence.Add(1)
	p.callback(event)
}

func (p *requirementListReporter) update(phase, status, message string, completed, total int) {
	p.emit(RequirementListProgress{Phase: phase, Status: status, Message: message, Completed: completed, Total: total})
}

// ReadRequirementListWithProgress uses the same identity, cache and query path
// as Execute; the callback cannot initiate or authorize another operation.
func (s *Service) ReadRequirementListWithProgress(ctx context.Context, kind string, payload map[string]any, callback func(RequirementListProgress)) (any, error) {
	if kind != "requirements.refresh" && kind != "requirements.list" {
		return nil, errors.New("需求列表进度入口仅支持读取或刷新需求列表")
	}
	if ctx == nil {
		return nil, errors.New("需求列表读取上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := s.config.Load()
	if err != nil {
		return nil, err
	}
	p := &requirementListReporter{ctx: ctx, phase: "account"}
	if callback != nil {
		p.callback = func(event RequirementListProgress) {
			event.Message = s.safeDiagnostic(config, event.Message)
			callback(event)
		}
	}
	p.update("account", "running", "正在核对 Meegle 账号…", 0, 0)
	result, err := s.listRequirementsWithProgress(ctx, config, payload, kind == "requirements.refresh", p)
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	if err != nil {
		p.update(p.phase, "failed", s.safeDiagnostic(config, err.Error()), 0, 0)
	}
	return result, err
}
