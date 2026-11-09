package main

import (
	"fmt"
	"io"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
)

func (o options) printResult(out io.Writer, data any, code int) {
	switch result := data.(type) {
	case []application.CLITagTarget:
		for _, target := range result {
			state := "可选"
			remote := target.Remote
			if !target.Selectable {
				state = "不可选"
				remote = "" // An unresolved target still contains its configured ref.
			}
			fmt.Fprintln(out, tagTargetText(target), "·", state)
			printTagBranches(out, target.SourceBranch, remote, target.TargetBranch)
			printTagDetail(out, "原因", target.Error)
		}
	case application.CLITagPreflightResult:
		printTagPreflight(out, result, code == 0)
	case application.CLITagJobResult:
		printTagJob(out, result)
	case []delivery.TagObservation:
		printTagStatus(out, result, o.batch, code == 0)
	case application.CLITagNoticeResult:
		printTagNotice(out, result)
	}
}

func tagTargetText(target application.CLITagTarget) string {
	name := target.Service
	if target.Module != "" {
		name += " / " + target.Module
	}
	return fmt.Sprintf("%s · 服务选择值 %s", name, target.SelectionKey)
}

func printTagBranches(out io.Writer, source, remote, target string) {
	printTagDetail(out, "源分支", source)
	if target != "" {
		if remote != "" {
			target = remote + "/" + target
		}
		printTagDetail(out, "目标分支", target)
	}
}

func printTagPreflight(out io.Writer, result application.CLITagPreflightResult, complete bool) {
	items := result.Items
	if result.ID != "" {
		fmt.Fprintln(out, "预检编号", result.ID)
	}
	if complete && len(items) > 0 {
		fmt.Fprintf(out, "预检全部通过（%d 项）\n", len(items))
	} else {
		fmt.Fprintln(out, "预检未全部通过")
	}
	for _, item := range items {
		preview := item.Preview
		state := "预检通过"
		if item.BlockedBy != "" {
			state = "等待前序预检"
		} else if len(preview.ConflictFiles) > 0 {
			state = "预检发现合并冲突"
		} else if item.Error != "" {
			state = "预检失败"
		}
		fmt.Fprintln(out, tagTargetText(item.Target), "·", state)
		printTagBranches(out, preview.SourceBranch, preview.Remote, tagTextValue(preview.TargetBranch))
		printTagDetail(out, "预估 Tag", preview.EstimatedTag)
		printTagDetail(out, "原因", item.Error)
		if item.Error != "" {
			printTagDetail(out, "操作 ID", item.OperationID)
			printTagDetail(out, "冲突工作区", item.Worktree)
		}
		printTagConflict(out, nil, preview.ConflictFiles, item.BlockedBy)
	}
	for _, warning := range result.Warnings {
		printTagDetail(out, "部署范围", warning)
	}
}

func printTagJob(out io.Writer, job application.CLITagJobResult) {
	if job.BatchID != "" {
		fmt.Fprintln(out, "批次 ID", job.BatchID)
	}
	for _, item := range job.Items {
		record := item.Record
		name := item.SelectionKey
		if record != nil && record.ServiceName != "" {
			name = record.ServiceName + " · " + name
		}
		fmt.Fprintln(out, name, "· 操作 ID", item.OperationID, "·", tagJobText(item))
		printTagDetail(out, "原因", item.Error)
		if record == nil {
			continue
		}
		printTagDetail(out, "Tag", record.Tag)
		printTagDetail(out, "保留工作区", record.PreflightWorktree)
		if unresolvedConflict(record.State, record.Conflict) {
			printTagDetail(out, "合并提示", record.ConflictSummary)
		} else if item.Error == "" && record.State != string(domain.TagOperationSuccess) {
			printTagDetail(out, "说明", record.Detail)
		}
		printTagConflict(out, record.Conflict, record.ConflictFiles, record.BlockedBy)
	}
}

func tagJobText(item application.CLITagJobItem) string {
	if item.Record != nil && unresolvedConflict(item.Record.State, item.Record.Conflict) {
		return "合并冲突"
	}
	switch item.Phase {
	case application.TagJobPhaseBlocked:
		return "等待关联操作"
	case application.TagJobPhaseCanceled:
		return "已取消"
	case application.TagJobPhaseFailed:
		return "构建失败"
	case application.TagJobPhaseCompleted:
		if item.Record != nil && item.Record.State == string(domain.TagOperationSuccess) && item.Error == "" {
			return "Tag 已推送"
		}
	}
	return "构建未完成"
}

func printTagStatus(out io.Writer, rows []delivery.TagObservation, batchID string, complete bool) {
	if batchID != "" {
		fmt.Fprintln(out, "批次 ID", batchID)
	}
	buildsReady := complete && len(rows) > 0
	uatReady := buildsReady
	runtimeReady := buildsReady
	serviceTags := map[string]string{}
	sharedVersions := false
	for _, row := range rows {
		op, status := row.Operation, row.Operation.GenbuStatus
		build, uat := genbuStageText(status.Build), genbuStageText(status.UAT)
		switch {
		case status.FailureReason != nil || row.GenbuService == "":
			build, uat = "查询未确认", "查询未确认"
		case status.NotFound:
			build, uat = "暂无此 Tag 记录", "暂无此 Tag 记录"
		}
		probeOK := row.GenbuService != "" && status.FailureReason == nil && !status.NotFound
		buildsReady = buildsReady && row.Git.State == "PUSHED" && op.State == domain.TagOperationSuccess &&
			probeOK && status.Build == domain.GenbuSuccess
		uatReady = uatReady && probeOK && status.UAT == domain.GenbuSuccess
		runtimeReady = runtimeReady && row.Runtime != nil && row.Runtime.State == delivery.TagRuntimeReady
		if tag := tagTextValue(op.Tag); row.GenbuService != "" && tag != "" {
			if previous := serviceTags[row.GenbuService]; previous != "" && previous != tag {
				sharedVersions = true
			}
			serviceTags[row.GenbuService] = tag
		}

		fmt.Fprintln(out, op.ServiceName, "· 操作 ID", op.OperationID)
		printTagDetail(out, "Tag", tagTextValue(op.Tag))
		fmt.Fprintf(out, "  Tag 推送 %s · Genbu 编译 %s · UAT 发版 %s\n", gitPushText(row.Git.State), build, uat)
		printTagDetail(out, "Genbu 服务", row.GenbuService)
		printTagDetail(out, "远端校验", row.Git.Error)
		printTagDetail(out, "Genbu 查询异常", tagTextValue(status.FailureReason))
		printTagDetail(out, "保留工作区", op.PreflightWorktree)
		if op.PreflightID != "" && op.State != domain.TagOperationSuccess {
			printTagDetail(out, "上次执行结果", tagTextValue(op.Message))
		}
		printTagRuntime(out, row.Runtime)
		if unresolvedConflict(string(op.State), op.Conflict) {
			printTagDetail(out, "合并提示", row.ConflictSummary())
		} else if op.BlockedBy == "" {
			printTagDetail(out, "说明", tagTextValue(row.Guidance))
		}
		printTagConflict(out, op.Conflict, op.ConflictFiles, op.BlockedBy)
	}
	if batchID == "" {
		return
	}
	if buildsReady {
		fmt.Fprintln(out, "整批 Tag 已推送且编译成功")
	} else {
		fmt.Fprintln(out, "Tag 推送和编译尚未全部确认成功")
	}
	if uatReady {
		fmt.Fprintln(out, "整批 UAT 发版成功")
	} else {
		fmt.Fprintln(out, "UAT 发版尚未全部确认成功")
	}
	if buildsReady && uatReady && sharedVersions {
		fmt.Fprintln(out, "同一 Genbu 服务包含多个 Tag，请按操作 ID 分开核对 UAT 运行版本")
	} else if buildsReady && uatReady && runtimeReady {
		fmt.Fprintln(out, "目标版本已就绪，可以自测")
	} else if uatReady {
		fmt.Fprintln(out, "本批 UAT 运行版本尚未全部就绪")
	}
}

func printTagRuntime(out io.Writer, runtime *delivery.TagRuntimeStatus) {
	if runtime == nil {
		return
	}
	state := "尚未就绪"
	switch runtime.State {
	case delivery.TagRuntimeReady:
		state = "目标版本已就绪"
	case delivery.TagRuntimeUnknown:
		state = "查询未确认"
	case delivery.TagRuntimeAmbiguous:
		state = "需确认 namespace"
	}
	printTagDetail(out, "UAT 运行状态", state)
	printTagDetail(out, "运行核对提示", runtime.Error)
	for _, ns := range runtime.Namespaces {
		if runtime.Namespace != "" && ns.Namespace != runtime.Namespace {
			continue
		}
		versions := strings.Join(ns.Versions, "、")
		if versions == "" {
			versions = "未知"
		}
		fmt.Fprintf(out, "  %s · 运行版本 %s · 版本匹配 %d/%d · 就绪 %d/%d\n",
			ns.Namespace, versions, ns.MatchingPods, ns.PodCount, ns.ReadyPods, ns.PodCount)
	}
}

func gitPushText(state string) string {
	switch state {
	case "PUSHED":
		return "已推送且提交一致"
	case "NOT_CREATED":
		return "无 Tag 记录"
	case "NOT_PUSHED":
		return "远端暂无 Tag"
	case "MISMATCH":
		return "远端 Tag 指向不同提交"
	default:
		return "查询未确认"
	}
}

func genbuStageText(state domain.GenbuStageStatus) string {
	switch state {
	case domain.GenbuInitial:
		return "未开始"
	case domain.GenbuBuilding:
		return "进行中"
	case domain.GenbuSuccess:
		return "成功"
	case domain.GenbuFailed:
		return "失败"
	default:
		return "未知"
	}
}

func printTagNotice(out io.Writer, result application.CLITagNoticeResult) {
	fmt.Fprintln(out, "批次 ID", result.BatchID)
	for _, receipt := range result.Recipients {
		name := receipt.Name
		if receipt.Self {
			name = "本人"
		}
		state := "结果待核实"
		switch {
		case receipt.State == "sent" && receipt.MessageID != "":
			state = "已发送"
			if receipt.Skipped {
				state = "已发送（沿用回执）"
			}
		case receipt.State == "failed":
			state = "发送失败"
		}
		fmt.Fprintln(out, name, "·", state)
		printTagDetail(out, "飞书消息 ID", receipt.MessageID)
		printTagDetail(out, "原因", receipt.Error)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(out, "通知提示", warning)
	}
}

func printTagConflict(out io.Writer, conflict *domain.TagConflict, files []string, blocker string) {
	if conflict != nil {
		printTagDetail(out, "冲突工作区", conflict.Worktree)
	}
	for _, file := range files {
		printTagDetail(out, "冲突文件", file)
	}
	printTagDetail(out, "阻塞操作 ID", blocker)
}

func printTagDetail(out io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(out, "  %s %s\n", label, value)
	}
}

func tagTextValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
