package tools

import (
	"context"
	"errors"
	"os"
	"sort"
	"time"

	"silverwing-go/internal/process"
)

type PortEndpoint struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	State    string `json:"state"`
}
type PortProcess struct {
	Protocol             string         `json:"protocol"`
	Address              string         `json:"address"`
	Port                 int            `json:"port"`
	PID                  int            `json:"pid"`
	Name                 string         `json:"name"`
	Path                 string         `json:"path"`
	CommandLine          string         `json:"commandLine"`
	User                 string         `json:"user"`
	State                string         `json:"state"`
	StartedAt            string         `json:"startedAt"`
	StartedAtEpochMillis int64          `json:"startedAtEpochMillis"`
	StartTimeDisplay     string         `json:"startTimeDisplay"`
	CanTerminate         bool           `json:"canTerminate"`
	Endpoints            []PortEndpoint `json:"endpoints"`
}
type PortTerminationResult struct {
	Status    string        `json:"status"`
	Message   string        `json:"message"`
	Processes []PortProcess `json:"processes"`
}
type PortService struct{ Runner *process.Runner }

func NewPortService(runner *process.Runner) *PortService {
	if runner == nil {
		runner = process.NewRunner()
	}
	return &PortService{runner}
}
func (s *PortService) Query(ctx context.Context, port int) ([]PortProcess, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("端口号必须是 1–65535 的整数")
	}
	items, err := s.queryPlatform(ctx, port)
	if err != nil {
		return nil, err
	}
	return groupPortProcesses(items), nil
}
func groupPortProcesses(items []PortProcess) []PortProcess {
	grouped := map[int]*PortProcess{}
	for _, item := range items {
		p := grouped[item.PID]
		if p == nil {
			copy := item
			copy.Endpoints = []PortEndpoint{}
			p = &copy
			grouped[item.PID] = p
		}
		endpoints := item.Endpoints
		if len(endpoints) == 0 {
			endpoints = []PortEndpoint{{item.Protocol, item.Address, item.Port, item.State}}
		}
		for _, endpoint := range endpoints {
			duplicate := false
			for _, existing := range p.Endpoints {
				if existing == endpoint {
					duplicate = true
					break
				}
			}
			if !duplicate {
				p.Endpoints = append(p.Endpoints, endpoint)
			}
		}
	}
	result := make([]PortProcess, 0, len(grouped))
	for _, p := range grouped {
		sort.Slice(p.Endpoints, func(i, j int) bool {
			a, b := p.Endpoints[i], p.Endpoints[j]
			if a.Protocol != b.Protocol {
				return a.Protocol < b.Protocol
			}
			if a.Address != b.Address {
				return a.Address < b.Address
			}
			return a.State < b.State
		})
		if len(p.Endpoints) > 0 {
			e := p.Endpoints[0]
			p.Protocol, p.Address, p.Port, p.State = e.Protocol, e.Address, e.Port, e.State
		}
		start, err := time.Parse(time.RFC3339Nano, p.StartedAt)
		p.CanTerminate = p.PID > 0 && p.PID != os.Getpid() && err == nil
		if err == nil {
			p.StartedAtEpochMillis = start.UnixMilli()
			p.StartTimeDisplay = start.In(time.Local).Format("2006年01月02日 15:04:05")
		}
		result = append(result, *p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result
}
func (s *PortService) Terminate(ctx context.Context, item PortProcess, confirmed bool) error {
	result, err := s.TerminateDetailed(ctx, item, confirmed)
	if err != nil {
		return err
	}
	if result.Status != "EXITED" && result.Status != "PORT_REOCCUPIED" {
		return errors.New(result.Message)
	}
	return nil
}
func (s *PortService) TerminateDetailed(ctx context.Context, item PortProcess, confirmed bool) (PortTerminationResult, error) {
	result := PortTerminationResult{Processes: []PortProcess{}}
	finish := func(status, message string) (PortTerminationResult, error) {
		result.Status, result.Message = status, message
		return result, nil
	}
	if !confirmed {
		return finish("CONFIRMATION_REQUIRED", "结束进程需要确认")
	}
	if item.Port < 1 || item.Port > 65535 || item.PID < 0 {
		return finish("FAILED", "端口号或进程 ID 无效")
	}
	if item.PID == 0 {
		return finish("SYSTEM_PROCESS", "不能结束系统进程")
	}
	if item.PID == os.Getpid() {
		return finish("CURRENT_APPLICATION", "不能结束当前应用自身")
	}
	if _, err := time.Parse(time.RFC3339Nano, item.StartedAt); err != nil {
		return finish("MISSING_IDENTITY", "无法确认进程启动时间，拒绝结束进程")
	}
	before, err := s.Query(ctx, item.Port)
	if err != nil {
		return result, err
	}
	result.Processes = before
	var owner *PortProcess
	for i := range before {
		if before[i].PID == item.PID {
			owner = &before[i]
			break
		}
	}
	if owner == nil {
		return finish("NOT_OWNED", "所选进程已不再占用端口，请刷新")
	}
	if owner.StartedAt == "" {
		return finish("MISSING_IDENTITY", "无法确认进程启动时间，拒绝结束进程")
	}
	if !samePortProcessIdentity(*owner, item) {
		return finish("IDENTITY_CHANGED", "进程身份已变化，请刷新后重试")
	}
	if err = s.forceTerminatePortProcess(ctx, item); err != nil {
		return finish("FAILED", "无法提交强制结束请求："+err.Error())
	}
	// Once a kill is submitted, closing the view must not discard its outcome.
	observeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	exited, waitErr := s.awaitPortProcessExit(observeCtx, item)
	cancel()
	refreshCtx, refreshCancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	result.Processes, err = s.Query(refreshCtx, item.Port)
	refreshCancel()
	if err != nil {
		return finish("FAILED", "强制结束请求已提交，但刷新端口占用失败："+err.Error())
	}
	if exited {
		if len(result.Processes) > 0 {
			return finish("PORT_REOCCUPIED", "所选进程已结束，但端口被其他进程占用")
		}
		return finish("EXITED", "所选进程已结束，端口已释放")
	}
	if errors.Is(waitErr, context.DeadlineExceeded) {
		return finish("TIMED_OUT", "等待进程结束超时，请刷新查看")
	}
	if waitErr != nil {
		return finish("FAILED", "等待进程结束时发生错误："+waitErr.Error())
	}
	return finish("STILL_RUNNING", "强制结束请求已提交，进程仍在运行")
}
func samePortProcessIdentity(a, b PortProcess) bool {
	left, le := time.Parse(time.RFC3339Nano, a.StartedAt)
	right, re := time.Parse(time.RFC3339Nano, b.StartedAt)
	return a.PID == b.PID && le == nil && re == nil && left.Equal(right)
}
func (s *PortService) awaitPortProcessExit(ctx context.Context, item PortProcess) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		current, err := s.inspectPortProcess(ctx, item.PID)
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if current.StartedAt == "" {
			return false, errors.New("无法读取进程身份")
		}
		if !samePortProcessIdentity(current, item) {
			return true, nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}
