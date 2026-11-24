package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
	"silverwing-go/internal/profilelaunch"
)

const serviceUsage = `服务查询命令（UAT、PROD 均为只读）:
  sw service pods --env <uat|prod> --service <服务>
  sw service config --env <uat|prod> --service <服务> [--namespace <命名空间>] [--file <文件>]
  sw service approvals --env <uat|prod> --service <服务> [--page <页码>] [--page-size <每页条数>]
                       [--status <all|pending|approved|rejected|revoked>]
                       [--start-date <YYYY-MM-DD> --end-date <YYYY-MM-DD>]
  sw service approval --env <uat|prod> --service <服务> --id <审批编号>
  sw service approval-diff --env <uat|prod> --service <服务> --id <审批编号>
`

func runService(ctx context.Context, args []string, directories profilelaunch.DirectoryFlags, asJSON bool, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(out, serviceUsage)
		return 0
	}
	req := application.CLIServiceReadRequest{Command: args[0]}
	o := options{asJSON: asJSON}
	flags := newCLIFlagSet("sw service " + req.Command)
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", o.asJSON, "JSON 输出")
	flags.StringVar(&req.Service, "service", "", "服务 ID、唯一显示名或配置的 Genbu 映射")
	flags.StringVar(&req.Environment, "env", "", "uat 或 prod")
	switch req.Command {
	case "pods":
	case "config":
		flags.StringVar(&req.Namespace, "namespace", "", "配置所在的命名空间")
		flags.StringVar(&req.File, "file", "", "完整的配置文件名")
	case "approvals":
		flags.IntVar(&req.Page, "page", 1, "页码")
		flags.IntVar(&req.PageSize, "page-size", 20, "每页条数")
		flags.StringVar(&req.Status, "status", "all", "审批状态")
		flags.StringVar(&req.StartDate, "start-date", "", "开始日期")
		flags.StringVar(&req.EndDate, "end-date", "", "结束日期")
	case "approval", "approval-diff":
		flags.Int64Var(&req.ApprovalID, "id", 0, "审批编号")
	default:
		return o.outputResult(out, stderr, nil, exitUsage, fmt.Errorf("未知服务命令 %q", req.Command))
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(out, serviceUsage)
		return 0
	} else if err != nil {
		return o.outputResult(out, stderr, nil, exitUsage, err)
	}
	if flags.NArg() != 0 {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("存在多余参数"))
	}
	req.Service = strings.TrimSpace(req.Service)
	req.Environment = strings.ToLower(strings.TrimSpace(req.Environment))
	req.Namespace = strings.TrimSpace(req.Namespace)
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	var validationErr error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "file", "namespace", "status", "start-date", "end-date":
			if strings.TrimSpace(f.Value.String()) == "" {
				validationErr = fmt.Errorf("请为 --%s 指定值", f.Name)
			}
		case "page", "page-size":
			if f.Value.String() == "0" {
				validationErr = fmt.Errorf("--%s 必须大于 0", f.Name)
			}
		}
	})
	if validationErr != nil {
		return o.outputResult(out, stderr, nil, exitUsage, validationErr)
	}
	if err := req.Validate(); err != nil {
		return o.outputResult(out, stderr, nil, exitUsage, err)
	}
	home, err := directories.Resolve()
	if err != nil {
		code := exitError
		if directories.Explicit() {
			code = exitUsage
		}
		return o.outputResult(out, stderr, nil, code, err)
	}
	app, err := application.NewUATCLI(home)
	if err != nil {
		return o.outputResult(out, stderr, nil, exitError, err)
	}
	data, err := app.CLIServiceRead(ctx, req)
	return outputServiceResult(o, out, stderr, data, err)
}

func outputServiceResult(o options, out, stderr io.Writer, data any, err error) int {
	if o.asJSON || data == nil {
		return o.outputResult(out, stderr, data, 0, err)
	}
	printServiceResult(out, data)
	if err != nil {
		printCLIError(stderr, err.Error())
		return exitError
	}
	return 0
}

func printServiceResult(out io.Writer, data any) {
	switch result := data.(type) {
	case production.ServicePods:
		printTagField(out, "服务", result.Service)
		printTagField(out, "环境", strings.ToUpper(result.Environment))
		if len(result.Pods) == 0 {
			fmt.Fprintln(out, "没有查询到 Pod")
		}
		for _, pod := range result.Pods {
			fmt.Fprintf(out, "%s/%s  Group=%s  版本=%s  状态=%s  Ready=%t  重启=%d  创建=%s\n",
				pod.Namespace, pod.Name, pod.GroupName, pod.Version, pod.Phase, pod.Ready, pod.Restarts, pod.CreatedTime)
		}
	case production.GenbuServiceConfig:
		printTagField(out, "服务", result.Service)
		printTagField(out, "环境", strings.ToUpper(result.Environment))
		printTagField(out, "命名空间", result.Namespace)
		printTagField(out, "文件", result.File)
		printTagField(out, "更新时间", result.UpdateTime)
		fmt.Fprint(out, result.Text)
	case production.GenbuApprovalPage:
		fmt.Fprintf(out, "第 %d 页，每页 %d 条，Genbu 返回总数 %d\n", result.Page, result.PageSize, result.Total)
		if len(result.Items) == 0 {
			fmt.Fprintln(out, "本页没有匹配服务的配置审批")
		}
		for _, item := range result.Items {
			fmt.Fprintf(out, "%d  %s  %s  %s  %s  %s  %s\n", item.ID, item.Service, strings.ToUpper(item.Environment),
				item.Status, item.Result, item.Creator, item.CreatedAt)
		}
	case application.CLIConfigApprovalResult:
		printConfigApprovalResult(out, result)
	}
}

func printConfigApprovalResult(out io.Writer, result application.CLIConfigApprovalResult) {
	printTagField(out, "审批编号", fmt.Sprint(result.ID))
	printTagField(out, "服务", result.Service)
	printTagField(out, "环境", strings.ToUpper(result.Environment))
	printTagField(out, "审批状态", result.Status)
	printTagField(out, "执行结果", result.Result)
	printTagField(out, "申请人", result.Creator)
	printTagField(out, "申请时间", result.CreatedAt)
	printTagField(out, "审批人", result.Approver)
	printTagField(out, "审批时间", result.ApprovedAt)
	keys := make([]string, 0, len(result.Details))
	for key := range result.Details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		printTagField(out, key, result.Details[key])
	}
	if result.Comparison == nil {
		printTagField(out, "配置文件", strings.Join(result.FileNames, "、"))
		return
	}
	fmt.Fprintln(out, "对比读取时的当前配置与审批申请内容（已脱敏）")
	if result.Comparison.Compared && len(result.Comparison.Changes) == 0 {
		fmt.Fprintln(out, "当前配置与审批申请内容相同")
	}
	for _, change := range result.Comparison.Changes {
		switch {
		case !change.BeforeExists:
			fmt.Fprintf(out, "新增 %s\n", change.File)
		case !change.AfterExists:
			fmt.Fprintf(out, "删除 %s\n", change.File)
		default:
			fmt.Fprintf(out, "修改 %s\n", change.File)
		}
		if change.Unified != "" {
			fmt.Fprintln(out, strings.TrimRight(change.Unified, "\n"))
		}
		if change.Note != "" {
			fmt.Fprintln(out, change.Note)
		}
	}
}
