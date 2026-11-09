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
	"silverwing-go/internal/productprofile"
)

const (
	exitError    = 1
	exitUsage    = 2
	exitConflict = 3
)

const usage = `用法
  sw tag list   --task <任务目录名> [--json]
  sw tag build  --task <任务目录名> (--service <服务[:模块]> ... | --all-services) --dry-run [--preflight <预检编号>] [--json]
  sw tag build  --task <任务目录名> (--service <服务[:模块]> ... | --all-services) --preflight <预检编号> [--json]
  sw tag status (--batch <ID> | --operation <ID>) [--task <任务目录名>] [--namespace <UAT namespace>] [--json]
  sw tag retry  --operation <ID> [--task <任务目录名>] [--json]
  sw tag notify --batch <ID> [--task <任务目录名>] [--json]

--home <目录> 指定 Silverwing 数据目录。默认使用本机桌面数据目录。
默认输出中文结果。--json 输出结构化结果。
退出码 1 表示失败或尚未完成，2 表示参数错误，3 表示合并冲突。
--version 显示版本。
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
	global := flag.NewFlagSet("sw", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { fmt.Fprint(global.Output(), usage) }
	global.StringVar(&o.home, "home", "", "Silverwing 数据目录")
	global.BoolVar(&o.asJSON, "json", false, "JSON 输出")
	version := global.Bool("version", false, "显示版本")
	if err := global.Parse(args); err != nil {
		return flagExit(err)
	}
	if *version && global.NArg() == 0 {
		fmt.Fprintln(out, "sw "+buildinfo.FullVersion())
		return 0
	}
	args = global.Args()
	if len(args) == 0 || len(args) == 1 && args[0] == "tag" {
		fmt.Fprint(out, usage)
		return 0
	}
	if len(args) < 2 || args[0] != "tag" {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	if args[1] == "--help" || args[1] == "-h" || args[1] == "help" {
		fmt.Fprint(out, usage)
		return 0
	}
	o.command = args[1]
	flags := flag.NewFlagSet("sw tag "+o.command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = global.Usage
	flags.StringVar(&o.home, "home", o.home, "Silverwing 数据目录")
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
	case "notify":
		flags.StringVar(&o.batch, "batch", "", "批次 ID")
	default:
		fmt.Fprintln(stderr, "未知 Tag 命令", o.command)
		return exitUsage
	}
	if err := flags.Parse(args[2:]); err != nil {
		return flagExit(err)
	}
	if flags.NArg() != 0 {
		return o.outputResult(out, stderr, nil, exitUsage, errors.New("存在多余参数"))
	}
	if err := o.validate(); err != nil {
		return o.outputResult(out, stderr, nil, exitUsage, err)
	}
	if o.home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return o.outputResult(out, stderr, nil, exitError, err)
		}
		o.home = productprofile.DefaultHome(home)
	}
	app, err := application.NewTagCLI(o.home)
	if err != nil {
		return o.outputResult(out, stderr, nil, exitError, err)
	}
	data, code, err := o.execute(ctx, app)
	return o.outputResult(out, stderr, data, code, err)
}

func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return exitUsage
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
	case "notify":
		if o.batch == "" {
			return errors.New("请指定 --batch")
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
	case "notify":
		result, err := app.CLITagNotify(ctx, o.task, o.batch)
		code := 0
		for _, recipient := range result.Recipients {
			if recipient.State != "sent" || recipient.Error != "" {
				code = exitError
			}
		}
		return result, code, err
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
		if result, ok := data.(application.CLITagPreflightResult); ok {
			preflight, data, warnings = result.ID, result.Items, result.Warnings
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
			fmt.Fprintln(stderr, err)
			return exitError
		}
	} else {
		o.printResult(out, data, code)
		if message != "" {
			fmt.Fprintln(stderr, message)
		}
	}
	return code
}
