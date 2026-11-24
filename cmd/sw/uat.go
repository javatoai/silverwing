package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/profilelaunch"
)

func runUAT(ctx context.Context, args []string, directories profilelaunch.DirectoryFlags, asJSON bool, out, stderr io.Writer) int {
	request := application.CLIUATOperationRequest{Action: args[0]}
	switch request.Action {
	case "--help", "-h", "help":
		fmt.Fprint(out, usage)
		return 0
	case "release-batch":
		return runUATBatch(ctx, args[1:], directories, asJSON, out, stderr)
	case "release", "restart":
	default:
		return usageError(stderr, fmt.Sprintf("未知 UAT 命令 %q", request.Action))
	}
	o := options{asJSON: asJSON}
	flags := newCLIFlagSet("sw uat " + request.Action)
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", o.asJSON, "JSON 输出")
	flags.StringVar(&request.Service, "service", "", "服务 ID 或唯一服务名称")
	if request.Action == "release" {
		flags.StringVar(&request.Tag, "tag", "", "已构建的完整 Tag")
	}
	registerUATPreflightFlag(flags, &request.Preflight)
	if err := flags.Parse(args[1:]); err != nil {
		return flagExit(stderr, err)
	}
	request.Service = strings.TrimSpace(request.Service)
	request.Tag = strings.TrimSpace(request.Tag)
	var err error
	switch {
	case flags.NArg() != 0:
		err = errors.New("存在多余参数")
	case request.Service == "":
		err = errors.New("请指定 --service")
	case request.Action == "release" && request.Tag == "":
		err = errors.New("请指定 --tag")
	}
	if err != nil {
		return o.outputResult(out, stderr, nil, exitUsage, err)
	}
	app, code, err := openUATCLI(directories)
	if err != nil {
		return o.outputResult(out, stderr, nil, code, err)
	}
	result, err := app.CLIUATOperation(ctx, request)
	if result.Status == "" && err != nil {
		return o.outputResult(out, stderr, nil, exitError, err)
	}
	return o.outputResult(out, stderr, result, 0, err)
}

func runUATBatch(ctx context.Context, args []string, directories profilelaunch.DirectoryFlags, asJSON bool, out, stderr io.Writer) int {
	o := options{asJSON: asJSON}
	request := application.CLIUATBatchRequest{}
	flags := newCLIFlagSet("sw uat release-batch")
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", o.asJSON, "JSON 输出")
	flags.StringVar(&request.Batch, "batch", "", "本地 Tag 批次 ID")
	flags.StringVar(&request.Task, "task", "", "任务目录名")
	registerUATPreflightFlag(flags, &request.Preflight)
	if err := flags.Parse(args); err != nil {
		return flagExit(stderr, err)
	}
	request.Batch = strings.TrimSpace(request.Batch)
	if flags.NArg() != 0 {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("存在多余参数"))
	}
	if request.Batch == "" {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("请指定 --batch"))
	}
	app, code, err := openUATCLI(directories)
	if err != nil {
		return o.outputResult(out, stderr, nil, code, err)
	}
	result, err := app.CLIUATBatchRelease(ctx, request)
	return o.outputResult(out, stderr, result, 0, err)
}

func registerUATPreflightFlag(flags *flag.FlagSet, preflight *string) {
	flags.Func("preflight", "使用预检编号提交", func(value string) error {
		*preflight = strings.TrimSpace(value)
		if *preflight == "" {
			return errors.New("请指定有效的预检编号")
		}
		return nil
	})
}

func openUATCLI(directories profilelaunch.DirectoryFlags) (*application.App, int, error) {
	home, err := directories.Resolve()
	if err != nil {
		code := exitError
		if directories.Explicit() {
			code = exitUsage
		}
		return nil, code, err
	}
	app, err := application.NewUATCLI(home)
	if err != nil {
		return nil, exitError, err
	}
	return app, 0, nil
}

func printUATBatch(out io.Writer, result application.CLIUATBatchResult) {
	if result.BatchID == "" {
		return
	}
	fmt.Fprintln(out, "UAT 批量发布")
	printTagField(out, "任务", result.Task)
	printTagField(out, "批次 ID", result.BatchID)
	for _, skipped := range result.Skipped {
		fmt.Fprintf(out, "跳过 %s/%s（%s），Genbu 探测已关闭\n", skipped.ServiceID, skipped.ModuleID, skipped.Tag)
	}
	for _, row := range result.Items {
		fmt.Fprintln(out)
		service := row.Result.Service
		if service == "" {
			service = row.Item.ServiceID
		}
		printTagField(out, "服务", service)
		printTagDetail(out, "发布 Tag", row.Item.Tag)
		printTagDetail(out, "Group", row.Result.Group)
		printTagDetail(out, "Namespace", row.Result.Namespace)
		printTagDetail(out, "操作 ID", row.Item.OperationID)
		printTagDetail(out, "共用发布项", row.SharedWith)
		status := "预检未通过"
		switch row.Result.Status {
		case "preview":
			if row.Error == "" {
				status = "预检通过"
			}
		case "submitted":
			status = "已提交"
		case "unknown":
			status = "待核实"
		case "rejected":
			status = "拒绝"
		case "not-sent":
			status = "未发出"
		}
		printTagDetail(out, "状态", status)
		printTagDetail(out, "原因", row.Error)
		if row.Result.EnableStatus == "unknown" {
			printTagDetail(out, "UAT 开关", "开启结果待核实")
		}
	}
	if result.Preflight != "" {
		fmt.Fprintln(out)
		printTagField(out, "预检编号", result.Preflight)
		fmt.Fprintln(out, "确认后在原命令中加入 --preflight <预检编号>")
	}
}

func printUATOperation(out io.Writer, result application.CLIUATOperationResult) {
	switch result.Status {
	case "preview":
		fmt.Fprintln(out, "UAT 预检通过")
	case "submitted":
		fmt.Fprintln(out, "UAT 操作已提交，生效情况可在 Pod 明细中核对")
	case "unknown":
		fmt.Fprintln(out, "UAT 操作结果待核实，请查看 Pod 明细")
	case "rejected":
		fmt.Fprintln(out, "Genbu 拒绝了 UAT 操作")
	case "not-sent":
		fmt.Fprintln(out, "UAT 操作未发出")
	}
	printTagField(out, "服务", result.Service)
	if result.GenbuService != result.Service {
		printTagField(out, "Genbu 服务", result.GenbuService)
	}
	printTagField(out, "环境", result.Environment)
	printTagField(out, "Group", result.Group)
	if result.GroupID != 0 {
		printTagField(out, "Group ID", fmt.Sprint(result.GroupID))
	}
	printTagField(out, "Namespace", result.Namespace)
	if result.Action == "restart" {
		printTagField(out, "重启范围", fmt.Sprintf("整个 Group（预检时 %d 个 Pod）", result.PodCount))
	} else {
		printTagField(out, "发布 Tag", result.Tag)
		switch result.EnableStatus {
		case "required":
			printTagField(out, "UAT 开关", "提交发布时自动开启")
		case "enabled":
			printTagField(out, "UAT 开关", "已自动开启")
		case "rejected":
			printTagField(out, "UAT 开关", "开启失败")
		case "unknown":
			printTagField(out, "UAT 开关", "开启结果待核实")
		}
	}
	if result.Status == "preview" {
		printTagField(out, "预检编号", result.Preflight)
		fmt.Fprintln(out, "确认后在原命令中加入 --preflight <预检编号>")
	}
}
