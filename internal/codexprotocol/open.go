// Package codexprotocol activates Codex desktop links and repairs broken user associations.
package codexprotocol

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

var errAssociationMissing = errors.New("Codex 协议缺失或启动路径失效")

type desktopCommand struct {
	Executable string
	Arguments  []string
}

type systemAdapter interface {
	activate(context.Context, string) error
	lock(context.Context) (func(), error)
	discover(context.Context) (desktopCommand, error)
	repair(context.Context, desktopCommand) error
}

func open(ctx context.Context, uri string, system systemAdapter) error {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "codex" || parsed.Host != "threads" {
		return errors.New("Codex 深链无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = system.activate(ctx, uri)
	if !errors.Is(err, errAssociationMissing) {
		return err
	}
	command, err := system.discover(ctx)
	if err != nil {
		return fmt.Errorf("无法恢复 Codex 协议，请检查 Codex 桌面应用安装：%w", err)
	}
	release, err := system.lock(ctx)
	if err != nil {
		return fmt.Errorf("等待 Codex 协议修复：%w", err)
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Another process may have repaired the association while this request waited.
	err = system.activate(ctx, uri)
	if !errors.Is(err, errAssociationMissing) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := system.repair(ctx, command); err != nil {
		return fmt.Errorf("修复 Codex 协议失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := system.activate(ctx, uri); err != nil {
		return fmt.Errorf("Codex 协议已修复，系统仍未接受打开请求：%w", err)
	}
	return nil
}
