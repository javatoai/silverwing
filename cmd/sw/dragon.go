package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/profilelaunch"
	"silverwing-go/internal/snowball/dragonapi"
)

func runDragon(ctx context.Context, args []string, directories profilelaunch.DirectoryFlags, asJSON bool, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return 0
	}
	command := args[0]
	o := options{asJSON: asJSON}
	var request application.CLIDragonSubmitRequest
	var query dragonapi.QueryInput
	var approvalID int64
	flags := newCLIFlagSet("sw dragon " + command)
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", o.asJSON, "JSON 输出")
	switch command {
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	case "submit":
		flags.StringVar(&request.Remark, "remark", "", "提测备注")
		flags.BoolVar(&request.SyncComment, "sync-comment", false, "同步审批链接到 Meegle 需求评论")
		flags.Func("preflight", "确认预检编号", func(value string) error {
			request.Preflight = strings.TrimSpace(value)
			if request.Preflight == "" {
				return errors.New("请指定有效的预检编号")
			}
			return nil
		})
	case "status", "retry-comment":
	case "recheck", "detail":
		flags.Func("approval-id", "青龙审批 ID", func(value string) error {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return errors.New("青龙审批 ID 必须是正整数")
			}
			approvalID = id
			return nil
		})
	case "approvals":
		flags.StringVar(&query.No, "no", "", "审批编号")
		flags.StringVar(&query.Keyword, "keyword", "", "搜索关键词")
		flags.IntVar(&query.Page, "page", 1, "页码")
		flags.IntVar(&query.Size, "size", 10, "每页数量")
	default:
		return usageError(stderr, fmt.Sprintf("未知青龙命令 %q", command))
	}
	taskCommand := command != "detail" && command != "approvals"
	if taskCommand {
		flags.StringVar(&request.Task, "task", "", "任务目录名")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return flagExit(stderr, err)
	}
	request.Task = strings.TrimSpace(request.Task)
	var err error
	switch {
	case flags.NArg() != 0:
		err = errors.New("存在多余参数")
	case taskCommand && request.Task == "":
		err = errors.New("请指定 --task")
	case command == "detail" && approvalID == 0:
		err = errors.New("请指定 --approval-id")
	case command == "approvals" && (query.Page < 1 || query.Size < 1 || query.Size > 100):
		err = errors.New("--page 必须大于零，--size 必须在 1–100 之间")
	}
	if err != nil {
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
	var result any
	if command == "approvals" {
		result, err = app.CLIDragonApprovals(ctx, query)
		if err != nil {
			result = nil
		}
	} else if command == "detail" {
		result, err = app.CLIDragonDetail(ctx, approvalID)
		if err != nil {
			result = nil
		}
	} else {
		state, stateErr := dragonTaskCommand(ctx, app, command, request, approvalID)
		err = stateErr
		if state.TaskID != "" {
			result = state
		}
	}
	if !o.asJSON && result != nil {
		printDragonResult(out, result)
	}
	return o.outputResult(out, stderr, result, 0, err)
}

func dragonTaskCommand(ctx context.Context, app *application.App, command string, request application.CLIDragonSubmitRequest, approvalID int64) (application.CLIDragonQAResult, error) {
	var state application.CLIDragonQAResult
	var err error
	switch command {
	case "status":
		return app.CLIDragonStatus(ctx, request.Task)
	case "submit":
		state, err = app.CLIDragonSubmit(ctx, request)
	case "recheck":
		id := ""
		if approvalID != 0 {
			id = strconv.FormatInt(approvalID, 10)
		}
		state, err = app.CLIDragonRecheck(ctx, request.Task, id)
	case "retry-comment":
		state, err = app.CLIDragonRetryComment(ctx, request.Task)
	}
	if err != nil {
		return state, err
	}
	if state.Phase == "preview" {
		return state, nil
	}
	if state.Phase != application.DragonQAApproved {
		if state.Message != "" {
			return state, errors.New(state.Message)
		}
		return state, errors.New("青龙提测尚未提交或结果待核实")
	}
	if command == "retry-comment" && state.CommentStatus == "" {
		return state, errors.New("此审批未选择同步 Meegle 评论")
	}
	switch state.CommentStatus {
	case application.DragonQACommentPending, application.DragonQACommentFailed, application.DragonQACommentUnknown:
		return state, errors.New("青龙审批已提交，可用 sw dragon retry-comment 核对或补发 Meegle 评论")
	}
	return state, nil
}

func printDragonResult(out io.Writer, result any) {
	switch value := result.(type) {
	case application.CLIDragonQAResult:
		printDragonTaskResult(out, value)
	case dragonapi.QueryResult:
		fmt.Fprintf(out, "青龙提测审批: %d 条，第 %d 页\n", value.Total, value.Page)
		for _, row := range value.Items {
			fmt.Fprintln(out)
			printDragonApproval(out, row)
		}
	case dragonapi.DetailResult:
		printDragonApproval(out, value.Approval)
		printTagField(out, "审批链接", value.URL)
		printTagField(out, "需求", value.Meego)
		printTagField(out, "服务", strings.Join(value.Apps, "、"))
		printTagField(out, "备注", value.Remark)
	}
}

func printDragonApproval(out io.Writer, row dragonapi.Approval) {
	printTagField(out, "审批 ID", strconv.FormatInt(row.ApprovalID, 10))
	printTagField(out, "审批编号", row.No)
	printTagField(out, "标题", row.Title)
	printTagField(out, "状态", row.Status)
	printTagField(out, "提测人", row.Creator)
	printTagField(out, "创建时间", row.CreatedAt)
}

func printDragonTaskResult(out io.Writer, result application.CLIDragonQAResult) {
	state := "尚未提测"
	switch result.Phase {
	case "preview":
		state = "预检通过"
	case application.DragonQASubmitting:
		state = "正在提交"
	case application.DragonQAApproved:
		state = "已提交"
	case application.DragonQAUnknown:
		state = "待核实"
	case application.DragonQAFailed:
		state = "提交失败"
	}
	printTagField(out, "青龙提测", state)
	printTagField(out, "任务", result.TaskID)
	printTagField(out, "需求链接", result.RequirementURL)
	if len(result.Services) != 0 {
		names := make([]string, 0, len(result.Services))
		for _, service := range result.Services {
			names = append(names, service.Name)
		}
		printTagField(out, "服务", strings.Join(names, "、"))
	}
	printTagField(out, "审批 ID", result.ApprovalID)
	printTagField(out, "审批链接", result.ApprovalURL)
	printTagField(out, "提示", result.Message)
	if result.Preflight != "" {
		printTagField(out, "备注", result.Remark)
		printTagField(out, "同步 Meegle 评论", map[bool]string{true: "是", false: "否"}[result.SyncComment])
		printTagField(out, "预检编号", result.Preflight)
		fmt.Fprintln(out, "确认后在原命令中加入 --preflight <预检编号>")
	}
	switch result.CommentStatus {
	case application.DragonQACommentSynced:
		printTagField(out, "Meegle 评论", "已同步")
	case application.DragonQACommentPending:
		printTagField(out, "Meegle 评论", "待同步")
	case application.DragonQACommentFailed:
		printTagField(out, "Meegle 评论", "同步失败")
	case application.DragonQACommentUnknown:
		printTagField(out, "Meegle 评论", "待核实")
	}
	printTagField(out, "评论提示", result.CommentError)
	for _, candidate := range result.Candidates {
		fmt.Fprintln(out)
		printTagField(out, "候选审批 ID", candidate.ID)
		printTagField(out, "审批链接", candidate.URL)
		printTagField(out, "创建时间", candidate.CreatedAt)
		printTagField(out, "服务", strings.Join(candidate.Apps, "、"))
	}
}
