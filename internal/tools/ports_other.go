//go:build !windows && !darwin

package tools

import (
	"context"
	"fmt"
	"runtime"
)

func queryUnix(ctx context.Context, port int) ([]PortProcess, error) {
	return nil, fmt.Errorf("当前系统暂不支持端口查询：%s", runtime.GOOS)
}

func (s *PortService) queryPlatform(ctx context.Context, port int) ([]PortProcess, error) {
	return queryUnix(ctx, port)
}

func (s *PortService) inspectPortProcess(ctx context.Context, pid int) (PortProcess, error) {
	return inspectPortProcess(ctx, pid)
}

func (s *PortService) forceTerminatePortProcess(ctx context.Context, item PortProcess) error {
	return forceTerminatePortProcess(ctx, item)
}

func inspectPortProcess(ctx context.Context, pid int) (PortProcess, error) {
	return PortProcess{}, fmt.Errorf("当前系统暂不支持进程详情查询：%s", runtime.GOOS)
}

func forceTerminatePortProcess(ctx context.Context, item PortProcess) error {
	return fmt.Errorf("当前系统暂不支持强制结束进程：%s", runtime.GOOS)
}
