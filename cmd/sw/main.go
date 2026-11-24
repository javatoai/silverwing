package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"silverwing-go/internal/application"
	"silverwing-go/internal/buildinfo"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/profilelaunch"
)

const (
	exitError    = 1
	exitUsage    = 2
	exitConflict = 3
)

const usage = `用法:
  sw [--conf-dir <目录>] [--json] tag <命令> [选项]
  sw [--conf-dir <目录>] [--json] uat <命令> [选项]
  sw [--conf-dir <目录>] [--json] service <命令> [选项]
  sw [--conf-dir <目录>] [--json] dragon <命令> [选项]
  sw --version

Tag 命令:
  list: 列出任务的服务和分支
    sw tag list --task <任务目录名>

  build: 预检或构建 UAT Tag
    sw tag build --task <任务目录名> (--service <服务[:模块]> ... | --all-services)
                 --dry-run [--preflight <预检编号>]
    sw tag build --task <任务目录名> (--service <服务[:模块]> ... | --all-services)
                 --preflight <预检编号>

  status: 查询 Tag 推送、编译、UAT 发版和运行状态
    sw tag status (--batch <ID> | --operation <ID>) [--task <任务目录名>]
                  [--namespace <UAT namespace>]

  retry: 续接指定操作
    sw tag retry --operation <ID> [--task <任务目录名>]

  remote-list: 列出服务的远端 Tag
    sw tag remote-list --service <服务> [--keyword <关键词>] [--page <页>]
  query: 查询指定 Tag 的构建和发布状态
    sw tag query --service <服务> --tag <Tag>
  logs: 查看 Tag 构建日志
    sw tag logs --service <服务> --tag <Tag> [--tail-lines <1-1000>]

UAT 命令（默认预检，确认后传入 --preflight）:
  sw uat release --service <服务> --tag <Tag> [--preflight <预检编号>]
  sw uat restart --service <服务> [--preflight <预检编号>]
  sw uat release-batch --batch <批次ID> [--task <任务目录名>] [--preflight <预检编号>]

` + serviceUsage + `
青龙提测:
  sw dragon submit --task <任务目录名> [--remark <备注>] [--sync-comment] [--preflight <预检编号>]
  sw dragon status --task <任务目录名>
  sw dragon recheck --task <任务目录名> [--approval-id <审批ID>]
  sw dragon retry-comment --task <任务目录名>
  sw dragon approvals [--no <审批编号>] [--keyword <关键词>] [--page <页>] [--size <每页条数>]
  sw dragon detail --approval-id <审批ID>

选项:
  --conf-dir <目录>: 指定 Silverwing 数据目录，默认使用本机桌面数据目录。
  --home <目录>: --conf-dir 的兼容别名。
  --json: 输出结构化 JSON，默认输出中文结果。
  --version: 显示版本。
  --help, -h: 显示帮助。
  --conf-dir、--home 和 --json 也可放在子命令之后。

退出码:
  0: 成功
  1: 失败或尚未完成
  2: 参数错误
  3: 合并冲突
`

type serviceFlags []string

func (s *serviceFlags) String() string { return strings.Join(*s, ", ") }
func (s *serviceFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type options struct {
	command, home, task, batch, operation string
	preflight, namespace                  string
	asJSON, all, dryRun                   bool
	services                              serviceFlags
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	var o options
	var directories profilelaunch.DirectoryFlags
	global := newCLIFlagSet("sw")
	directories.Register(global)
	global.BoolVar(&o.asJSON, "json", false, "JSON 输出")
	version := global.Bool("version", false, "显示版本")
	if err := global.Parse(args); err != nil {
		return flagExit(stderr, err)
	}
	if *version && global.NArg() == 0 {
		fmt.Fprintln(out, "sw "+buildinfo.FullVersion())
		return 0
	}
	args = global.Args()
	if len(args) == 0 || len(args) == 1 && (args[0] == "tag" || args[0] == "uat") {
		fmt.Fprint(out, usage)
		return 0
	}
	switch args[0] {
	case "uat":
		return runUAT(ctx, args[1:], directories, o.asJSON, out, stderr)
	case "service":
		return runService(ctx, args[1:], directories, o.asJSON, out, stderr)
	case "dragon":
		return runDragon(ctx, args[1:], directories, o.asJSON, out, stderr)
	}
	if len(args) < 2 || args[0] != "tag" {
		return usageError(stderr, fmt.Sprintf("未知命令 %q", args[0]))
	}
	if args[1] == "--help" || args[1] == "-h" || args[1] == "help" {
		fmt.Fprint(out, usage)
		return 0
	}
	switch args[1] {
	case "remote-list", "query", "logs":
		return runRemoteTag(ctx, args[1:], directories, o.asJSON, out, stderr)
	}
	o.command = args[1]
	flags := newCLIFlagSet("sw tag " + o.command)
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", o.asJSON, "JSON 输出")
	flags.StringVar(&o.task, "task", "", "任务目录名")
	switch o.command {
	case "list":
	case "build":
		flags.Var(&o.services, "service", "服务或服务:模块，可重复")
		flags.BoolVar(&o.all, "all-services", false, "选择全部可用服务")
		flags.BoolVar(&o.dryRun, "dry-run", false, "仅预检")
		flags.StringVar(&o.preflight, "preflight", "", "使用原预检编号复检或执行")
	case "status":
		flags.StringVar(&o.batch, "batch", "", "批次 ID")
		flags.StringVar(&o.operation, "operation", "", "操作 ID")
		flags.StringVar(&o.namespace, "namespace", "", "核对指定 UAT namespace 的 Pod")
	case "retry":
		flags.StringVar(&o.operation, "operation", "", "操作 ID")
	default:
		return usageError(stderr, fmt.Sprintf("未知 Tag 命令 %q", o.command))
	}
	if err := flags.Parse(args[2:]); err != nil {
		return flagExit(stderr, err)
	}
	blankNamespace := false
	flags.Visit(func(f *flag.Flag) {
		blankNamespace = blankNamespace || f.Name == "namespace" && strings.TrimSpace(o.namespace) == ""
	})
	if blankNamespace {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("请为 --namespace 指定 UAT namespace"))
	}
	if flags.NArg() != 0 {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("存在多余参数"))
	}
	if err := o.validate(); err != nil {
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
	o.home = home
	app, err := application.NewTagCLI(o.home)
	if err != nil {
		return o.outputResult(out, stderr, nil, exitError, err)
	}
	data, code, err := o.execute(ctx, app)
	return o.outputResult(out, stderr, data, code, err)
}

func newCLIFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	return flags
}

func flagExit(stderr io.Writer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stderr, usage)
		return 0
	}
	return usageError(stderr, err.Error())
}

func usageError(stderr io.Writer, message string) int {
	printCLIError(stderr, message)
	fmt.Fprintln(stderr)
	fmt.Fprint(stderr, usage)
	return exitUsage
}

func printCLIError(stderr io.Writer, message string) {
	fmt.Fprintln(stderr, "错误:", message)
}

func (o options) validate() error {
	switch o.command {
	case "list", "build":
		if strings.TrimSpace(o.task) == "" {
			return errors.New("请指定 --task")
		}
		if o.command == "build" && o.all == (len(o.services) > 0) {
			return errors.New("请指定 --service 或 --all-services，二者只能选一种")
		}
		if o.command == "build" && !o.dryRun && strings.TrimSpace(o.preflight) == "" {
			return errors.New("请先用 --dry-run 预检，确认后用 --preflight 指定预检编号")
		}
	case "status":
		if (o.batch == "") == (o.operation == "") {
			return errors.New("请指定一个 --batch 或 --operation")
		}
	case "retry":
		if o.operation == "" {
			return errors.New("请指定 --operation")
		}
	}
	return nil
}

func (o options) execute(ctx context.Context, app *application.App) (any, int, error) {
	switch o.command {
	case "list":
		items, err := app.CLITagList(ctx, o.task)
		return items, 0, err
	case "build":
		if o.dryRun {
			result, err := app.CLITagPreflight(ctx, o.task, o.preflight, o.services, o.all)
			code := 0
			for _, item := range result.Items {
				if len(item.Preview.ConflictFiles) > 0 {
					code = exitConflict
				} else if item.Error != "" && code == 0 {
					code = exitError
				}
			}
			return result, code, err
		}
		job, err := app.CLITagBuild(ctx, o.task, o.preflight, o.services, o.all)
		return job, jobExit(job), err
	case "retry":
		job, err := app.CLITagRetry(ctx, o.task, o.operation)
		return job, jobExit(job), err
	case "status":
		items, err := app.CLITagStatus(ctx, application.CLITagHistoryQuery{Task: o.task, Batch: o.batch, Operation: o.operation, UATNamespace: o.namespace})
		code := 0
		for _, item := range items {
			if unresolvedConflict(string(item.Operation.State), item.Operation.Conflict) {
				code = exitConflict
			} else if code == 0 && (item.Git.State != "PUSHED" || item.Operation.GenbuStatus.FailureReason != nil) {
				code = exitError
			}
		}
		return items, code, err
	}
	return nil, exitUsage, errors.New("未知 Tag 命令")
}

func jobExit(job application.CLITagJobResult) int {
	code := 0
	for _, item := range job.Items {
		if item.Record != nil && unresolvedConflict(item.Record.State, item.Record.Conflict) {
			return exitConflict
		}
		if item.Record == nil || item.Record.State != string(domain.TagOperationSuccess) || item.Error != "" {
			code = exitError
		}
	}
	return code
}

func unresolvedConflict(state string, conflict *domain.TagConflict) bool {
	return state == string(domain.TagOperationConflict) && (conflict == nil || conflict.MergeSHA == "")
}

func (o options) outputResult(out, stderr io.Writer, data any, code int, err error) int {
	message := ""
	if err != nil {
		message = err.Error()
		if code == 0 {
			code = exitError
		}
	}
	if o.asJSON {
		preflight := ""
		var warnings []string
		switch result := data.(type) {
		case application.CLITagPreflightResult:
			preflight, data, warnings = result.ID, result.Items, result.Warnings
		case application.CLIUATOperationResult:
			preflight = result.Preflight
		case application.CLIUATBatchResult:
			preflight = result.Preflight
		case application.CLIDragonQAResult:
			preflight = result.Preflight
		}
		response := struct {
			OK          bool     `json:"ok"`
			Data        any      `json:"data,omitempty"`
			Error       string   `json:"error,omitempty"`
			PreflightID string   `json:"preflightId,omitempty"`
			Warnings    []string `json:"warnings,omitempty"`
		}{code == 0, data, message, preflight, warnings}
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(response); err != nil {
			printCLIError(stderr, err.Error())
			return exitError
		}
	} else {
		o.printResult(out, data, code)
		if message != "" {
			printCLIError(stderr, message)
		}
	}
	return code
}
