package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/buildinfo"
)

func TestSWHelpText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		toStderr bool
	}{
		{name: "no command"},
		{name: "tag group", args: []string{"tag"}},
		{name: "tag help", args: []string{"tag", "help"}},
		{name: "global help flag", args: []string{"--help"}, toStderr: true},
		{name: "command help flag", args: []string{"tag", "build", "-h"}, toStderr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := run(context.Background(), tc.args, &out, &stderr)
			help, other := &out, &stderr
			if tc.toStderr {
				help, other = &stderr, &out
			}
			if code != 0 || other.Len() != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), stderr.String())
			}
			for _, line := range []string{"用法:", "命令:", "选项:", "退出码:", "  list:", "  build:", "  status:", "  retry:", "  notify:"} {
				if !strings.Contains(help.String(), line) {
					t.Fatalf("help missing %q in %q", line, help.String())
				}
			}
		})
	}
}

func TestSWVersionText(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"--version"}, &out, &stderr)
	if want := "sw " + buildinfo.FullVersion() + "\n"; code != 0 || out.String() != want || stderr.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q, want stdout %q", code, out.String(), stderr.String(), want)
	}
}

func TestSWUsageErrorText(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"unknown"}, want: `未知命令 "unknown"`},
		{name: "unknown tag command", args: []string{"tag", "unknown"}, want: `未知 Tag 命令 "unknown"`},
		{name: "unknown global option", args: []string{"--json", "--unknown"}, want: "-unknown"},
		{name: "missing command option value", args: []string{"tag", "status", "--batch"}, want: "-batch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := run(context.Background(), tc.args, &out, &stderr)
			message, help, _ := strings.Cut(stderr.String(), "\n\n")
			if code != exitUsage || out.Len() != 0 || !strings.HasPrefix(message, "错误: ") ||
				!strings.Contains(message, tc.want) || !strings.HasPrefix(help, "用法:\n") {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), stderr.String())
			}
		})
	}
}

func TestSWValidationErrorTextAndJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "list", args: []string{"tag", "list"}, want: "请指定 --task"},
		{name: "dry run", args: []string{"tag", "build", "--task", "synthetic", "--dry-run"}, want: "请指定 --service 或 --all-services，二者只能选一种"},
		{name: "build", args: []string{"tag", "build", "--task", "synthetic", "--all-services"}, want: "请先用 --dry-run 预检，确认后用 --preflight 指定预检编号"},
		{name: "status", args: []string{"tag", "status"}, want: "请指定一个 --batch 或 --operation"},
		{name: "retry", args: []string{"tag", "retry"}, want: "请指定 --operation"},
		{name: "extra argument", args: []string{"tag", "list", "--task", "synthetic", "extra"}, want: "存在多余参数"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := run(context.Background(), tc.args, &out, &stderr)
			if code != exitUsage || out.Len() != 0 || stderr.String() != "错误: "+tc.want+"\n" {
				t.Fatalf("text exit %d, stdout %q, stderr %q", code, out.String(), stderr.String())
			}

			stderr.Reset()
			code = run(context.Background(), append([]string{"--json"}, tc.args...), &out, &stderr)
			if want := fmt.Sprintf("{\"ok\":false,\"error\":%q}\n", tc.want); code != exitUsage || out.String() != want || stderr.Len() != 0 {
				t.Fatalf("JSON exit %d, stdout %q, stderr %q, want stdout %q", code, out.String(), stderr.String(), want)
			}
		})
	}
}

func TestSWBackendErrorTextAndJSON(t *testing.T) {
	var out, stderr bytes.Buffer
	data := application.CLITagPreflightResult{ID: "preview-1", Warnings: []string{"核对部署范围"}}
	err := errors.New("读取预检记录失败")
	o := options{command: "build"}
	code := o.outputResult(&out, &stderr, data, 0, err)
	if code != exitError || stderr.String() != "错误: 读取预检记录失败\n" {
		t.Fatalf("text exit %d, stderr %q", code, stderr.String())
	}

	out.Reset()
	stderr.Reset()
	o.asJSON = true
	code = o.outputResult(&out, &stderr, data, 0, err)
	want := `{"ok":false,"data":null,"error":"读取预检记录失败","preflightId":"preview-1","warnings":["核对部署范围"]}` + "\n"
	if code != exitError || out.String() != want || stderr.Len() != 0 {
		t.Fatalf("JSON exit %d, stdout %q, stderr %q, want stdout %q", code, out.String(), stderr.String(), want)
	}
}
