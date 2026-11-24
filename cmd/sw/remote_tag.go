package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/profilelaunch"
)

func runRemoteTag(ctx context.Context, args []string, directories profilelaunch.DirectoryFlags, asJSON bool, out, stderr io.Writer) int {
	o := options{asJSON: asJSON}
	request := application.CLIRemoteTagRequest{Command: args[0]}
	flags := newCLIFlagSet("sw tag " + request.Command)
	directories.Register(flags)
	flags.BoolVar(&o.asJSON, "json", asJSON, "JSON 输出")
	flags.StringVar(&request.Service, "service", "", "服务 ID、唯一名称或 Genbu 映射")
	if request.Command == "remote-list" {
		flags.StringVar(&request.Keyword, "keyword", "", "Tag 关键词")
		flags.IntVar(&request.Page, "page", 1, "远端 Tag 页码")
	} else {
		flags.StringVar(&request.Tag, "tag", "", "完整 Tag")
	}
	if request.Command == "logs" {
		flags.IntVar(&request.TailLines, "tail-lines", 100, "日志尾部行数")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return flagExit(stderr, err)
	}
	var err error
	switch {
	case flags.NArg() != 0:
		err = errors.New("存在多余参数")
	case strings.TrimSpace(request.Service) == "":
		err = errors.New("请指定 --service")
	case request.Command != "remote-list" && strings.TrimSpace(request.Tag) == "":
		err = errors.New("请指定 --tag")
	case request.Command == "remote-list" && request.Page < 1:
		err = errors.New("--page 必须从 1 开始")
	case request.Command == "logs" && (request.TailLines < 1 || request.TailLines > 1000):
		err = errors.New("--tail-lines 必须在 1 到 1000 之间")
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
	result, err := app.CLIRemoteTag(ctx, request)
	if err != nil && result.Service == "" {
		return o.outputResult(out, stderr, nil, exitError, err)
	}
	return o.outputResult(out, stderr, result, 0, err)
}

func printRemoteTag(out io.Writer, result application.CLIRemoteTagResult) {
	printTagField(out, "服务", result.Service)
	if result.GenbuService != result.Service {
		printTagDetail(out, "Genbu 服务", result.GenbuService)
	}
	if result.Page != nil {
		fmt.Fprintf(out, "第 %d 页 · 共 %d 个 Tag\n", result.Page.Page, result.Page.Total)
		for _, item := range result.Page.Items {
			printTagField(out, item.Tag, item.CreatedAt)
		}
		return
	}
	printTagDetail(out, "Tag", result.Tag)
	if result.Status != nil && !result.Status.NotFound {
		printTagDetail(out, "构建", genbuStageText(result.Status.Build))
		printTagDetail(out, "UAT 发布", genbuStageText(result.Status.UAT))
		printTagDetail(out, "生产发布", genbuStageText(result.Status.Production))
		printTagDetail(out, "构建完成时间", tagTextValue(result.Status.BuiltCompletedAt))
		printTagDetail(out, "UAT 发布完成时间", tagTextValue(result.Status.ReleasedCompletedAt))
		printTagDetail(out, "生产发布完成时间", tagTextValue(result.Status.ProductionReleasedCompletedAt))
	}
	if result.Log != "" {
		fmt.Fprint(out, result.Log)
	}
}
