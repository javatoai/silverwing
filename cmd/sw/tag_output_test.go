package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/releasenotice"
)

func TestTagTextPreflightShowsDecisionAndBranches(t *testing.T) {
	target, version := "uat", "1.2.3.beta-1"
	item := application.CLITagPreflightItem{
		Target: application.CLITagTarget{Service: "支付", Module: "api", SelectionKey: "payment:api"},
		Preview: delivery.TagPreflight{
			SourceBranch: "feature/x", Remote: "origin", TargetBranch: &target, EstimatedTag: version,
		},
	}
	var out bytes.Buffer
	o := options{}
	o.outputResult(&out, io.Discard, application.CLITagPreflightResult{ID: "preview-1", Items: []application.CLITagPreflightItem{item}}, 0, nil)
	checkTagText(t, out.String(), "预检编号 preview-1", "预检全部通过", "支付 / api", "服务选择值 payment:api", "源分支 feature/x", "目标分支 origin/uat", "预估 Tag "+version)

	out.Reset()
	item.Preview.ConflictFiles = []string{"dir/测试.txt"}
	item.Error, item.Worktree = "需要解决冲突", "D:/temp/冲突 目录"
	o.outputResult(&out, io.Discard, application.CLITagPreflightResult{ID: "preview-1", Items: []application.CLITagPreflightItem{item}}, exitConflict, nil)
	checkTagText(t, out.String(), "预检未全部通过", "预检发现合并冲突", "冲突文件 dir/测试.txt", "冲突工作区 D:/temp/冲突 目录")
	if strings.Contains(out.String(), "预检全部通过") {
		t.Fatal(out.String())
	}
}

func TestTagTextStatusSeparatesBuildAndUAT(t *testing.T) {
	tag := "1.2.3.beta-1"
	row := delivery.TagObservation{
		TagOperationReport: delivery.TagOperationReport{Operation: domain.TagOperation{
			OperationID: "op-a", ServiceName: "支付", Tag: &tag, State: domain.TagOperationSuccess,
			GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess, UAT: domain.GenbuBuilding},
		}},
		Git: delivery.TagPushStatus{State: "PUSHED"}, GenbuService: "payment-api",
	}
	var out bytes.Buffer
	o := options{batch: "batch-a"}
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "批次 ID batch-a", "操作 ID op-a", "Tag "+tag, "Genbu 服务 payment-api",
		"Tag 推送 已推送且提交一致 · Genbu 编译 成功 · UAT 发版 进行中",
		"整批 Tag 已推送且编译成功", "UAT 发版尚未全部确认成功")
	if strings.Contains(out.String(), "整批 UAT 发版成功") {
		t.Fatal(out.String())
	}

	out.Reset()
	row.Operation.GenbuStatus.UAT = domain.GenbuSuccess
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "整批 UAT 发版成功", "本批 UAT 运行版本尚未全部就绪")
	if strings.Contains(out.String(), "可以自测") {
		t.Fatal(out.String())
	}

	out.Reset()
	row.Runtime = &delivery.TagRuntimeStatus{State: delivery.TagRuntimeReady, Namespace: "bp",
		Namespaces: []delivery.TagRuntimeNamespace{{Namespace: "bp", Versions: []string{tag}, PodCount: 1, ReadyPods: 1, MatchingPods: 1}}}
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "整批 UAT 发版成功", "bp · 运行版本 "+tag, "目标版本已就绪，可以自测")

	out.Reset()
	row.Operation.State = domain.TagOperationTagPushed
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "Tag 推送和编译尚未全部确认成功")
	if strings.Contains(out.String(), "可以自测") {
		t.Fatal(out.String())
	}
	row.Operation.State = domain.TagOperationSuccess

	out.Reset()
	other := row
	anotherTag := "1.2.3.beta-2"
	other.Operation.OperationID, other.Operation.Tag = "op-b", &anotherTag
	other.Runtime = &delivery.TagRuntimeStatus{State: delivery.TagRuntimeNotReady, Namespace: "bp"}
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row, other}, 0, nil)
	checkTagText(t, out.String(), "同一 Genbu 服务包含多个 Tag，请按操作 ID 分开核对")
	if strings.Contains(out.String(), "可以自测") {
		t.Fatal(out.String())
	}

	out.Reset()
	reason := "查询超时"
	row.Operation.GenbuStatus.FailureReason = &reason
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, exitError, nil)
	checkTagText(t, out.String(), "Genbu 编译 查询未确认 · UAT 发版 查询未确认", "Genbu 查询异常 查询超时")
	if strings.Contains(out.String(), "整批 Tag 已推送且编译成功") || strings.Contains(out.String(), "整批 UAT 发版成功") {
		t.Fatal(out.String())
	}

	out.Reset()
	row.GenbuService = ""
	row.Operation.GenbuStatus.FailureReason = nil
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, exitError, nil)
	checkTagText(t, out.String(), "Genbu 编译 查询未确认 · UAT 发版 查询未确认")
}

func TestTagTextConflictAndNotificationKeepActionDetails(t *testing.T) {
	var out bytes.Buffer
	o := options{}
	o.outputResult(&out, io.Discard, application.CLITagJobResult{BatchID: "batch-a", Items: []application.CLITagJobItem{
		{OperationID: "op-a", Phase: application.TagJobPhaseConflict, Record: &application.TagRecord{
			State: string(domain.TagOperationConflict), ConflictSummary: "禁止将 B 分支合并到 A 分支",
			Conflict: &domain.TagConflict{Worktree: "D:/temp/冲突 目录"}, ConflictFiles: []string{"README.md"},
		}},
		{OperationID: "op-b", Phase: application.TagJobPhaseBlocked, Record: &application.TagRecord{BlockedBy: "op-a"}},
	}}, exitConflict, nil)
	checkTagText(t, out.String(), "批次 ID batch-a", "操作 ID op-a", "合并冲突", "禁止将 B 分支合并到 A 分支",
		"冲突工作区 D:/temp/冲突 目录", "冲突文件 README.md", "操作 ID op-b", "阻塞操作 ID op-a")

	out.Reset()
	o.outputResult(&out, io.Discard, application.CLITagNoticeResult{
		BatchID: "batch-a",
		Recipients: []releasenotice.RecipientDelivery{
			{Self: true, State: "unknown", Error: "等待飞书确认"},
		},
		Warnings: []string{"未取得需求标题，卡片使用任务名称"},
	}, exitError, nil)
	checkTagText(t, out.String(), "本人 · 结果待核实", "原因 等待飞书确认", "通知提示 未取得需求标题，卡片使用任务名称")
}

func TestTagCLINotifyRejectsTestRecipients(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"tag", "notify", "--batch", "synthetic", "--with-testers"}, &out, &stderr)
	if code != exitUsage || out.Len() != 0 {
		t.Fatal("removed recipient option reached the notification workflow", code, out.String(), stderr.String())
	}
}

func checkTagText(t *testing.T, output string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("missing %q in:\n%s", fragment, output)
		}
	}
}
