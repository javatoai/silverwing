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

func TestTagTextListGroupsSelectableAndUnavailableServices(t *testing.T) {
	var out bytes.Buffer
	o := options{command: "list"}
	o.outputResult(&out, io.Discard, []application.CLITagTarget{
		{Service: "支付", Module: "api", SelectionKey: "payment:api", Selectable: true, SourceBranch: "feature/x", Remote: "origin", TargetBranch: "uat"},
		{Service: "支付", Module: "worker", SelectionKey: "payment:worker", SourceBranch: "feature/x", Remote: "origin", TargetBranch: "origin/uat", Error: "模块未启用 Tag"},
	}, 0, nil)
	want := `服务列表: 2 项（1 项可选）

支付 / api: 可选
  服务选择值: payment:api
  源分支: feature/x
  目标分支: origin/uat

支付 / worker: 不可选
  服务选择值: payment:worker
  源分支: feature/x
  目标分支: origin/uat
  原因: 模块未启用 Tag
`
	if got := out.String(); got != want {
		t.Fatalf("unexpected service list:\n%s", got)
	}
}

func TestTagTextPreflightShowsDecisionAndBranches(t *testing.T) {
	target, version := "uat", "1.2.3.beta-1"
	item := application.CLITagPreflightItem{
		Target: application.CLITagTarget{Service: "支付", Module: "api", SelectionKey: "payment:api"},
		Preview: delivery.TagPreflight{
			SourceBranch: "feature/x", Remote: "origin", TargetBranch: &target, EstimatedTag: version,
		},
	}
	var out bytes.Buffer
	o := options{command: "build", dryRun: true}
	o.outputResult(&out, io.Discard, application.CLITagPreflightResult{ID: "preview-1", Items: []application.CLITagPreflightItem{item}, Warnings: []string{"多个构建项共用 Genbu 服务"}}, 0, nil)
	checkTagText(t, out.String(), "预检编号: preview-1\n预检结果: 预检全部通过（1 项）\n\n支付 / api: 预检通过",
		"  服务选择值: payment:api", "  源分支: feature/x", "  目标分支: origin/uat", "  预估 Tag: "+version,
		"\n\n部署范围: 多个构建项共用 Genbu 服务")

	out.Reset()
	item.Preview.ConflictFiles = []string{"dir/测试.txt"}
	item.OperationID = "op-preview"
	item.Error, item.Worktree = "需要解决冲突", "D:/temp/冲突 目录"
	o.outputResult(&out, io.Discard, application.CLITagPreflightResult{ID: "preview-1", Items: []application.CLITagPreflightItem{item}}, exitConflict, nil)
	checkTagText(t, out.String(), "预检结果: 预检未全部通过", "支付 / api: 预检发现合并冲突", "  操作 ID: op-preview",
		"  原因: 需要解决冲突", "  冲突文件: dir/测试.txt", "  冲突工作区: D:/temp/冲突 目录")
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
	o := options{command: "status", batch: "batch-a"}
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "批次 ID: batch-a\n\n服务: 支付\n  操作 ID: op-a", "  Tag: "+tag, "  Genbu 服务: payment-api",
		"  Tag 推送: 已推送且提交一致\n  Genbu 编译: 成功\n  UAT 发版: 进行中",
		"\n\n批次汇总:\n  Tag 推送与编译: 整批 Tag 已推送且编译成功\n  UAT 发版: UAT 发版尚未全部确认成功")
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
		Namespaces: []delivery.TagRuntimeNamespace{
			{Namespace: "bp", Versions: []string{tag}, PodCount: 1, ReadyPods: 1, MatchingPods: 1},
			{Namespace: "other", Versions: []string{"other-version"}, PodCount: 2},
		}}
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "整批 UAT 发版成功", "  UAT 运行状态: 目标版本已就绪",
		"  namespace: bp\n    运行版本: "+tag+"\n    Pod: 版本匹配 1/1，就绪 1/1", "目标版本已就绪，可以自测")
	if strings.Contains(out.String(), "namespace: other") {
		t.Fatal(out.String())
	}

	out.Reset()
	operation := options{command: "status", operation: "op-a", namespace: "bp"}
	operation.outputResult(&out, io.Discard, []delivery.TagObservation{row}, 0, nil)
	checkTagText(t, out.String(), "服务: 支付\n  操作 ID: op-a", "  namespace: bp")
	if strings.Contains(out.String(), "批次汇总:") {
		t.Fatal(out.String())
	}

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
	checkTagText(t, out.String(), "  Genbu 编译: 查询未确认\n  UAT 发版: 查询未确认", "  Genbu 查询异常: 查询超时")
	if strings.Contains(out.String(), "整批 Tag 已推送且编译成功") || strings.Contains(out.String(), "整批 UAT 发版成功") {
		t.Fatal(out.String())
	}

	out.Reset()
	row.GenbuService = ""
	row.Operation.GenbuStatus.FailureReason = nil
	o.outputResult(&out, io.Discard, []delivery.TagObservation{row}, exitError, nil)
	checkTagText(t, out.String(), "  Genbu 编译: 查询未确认\n  UAT 发版: 查询未确认")
}

func TestTagTextConflictAndNotificationKeepActionDetails(t *testing.T) {
	var out bytes.Buffer
	o := options{command: "build"}
	o.outputResult(&out, io.Discard, application.CLITagJobResult{BatchID: "batch-a", Items: []application.CLITagJobItem{
		{OperationID: "op-a", SelectionKey: "payment:api", Phase: application.TagJobPhaseConflict, Record: &application.TagRecord{
			ServiceName: "支付", State: string(domain.TagOperationConflict), ConflictSummary: "禁止将 B 分支合并到 A 分支",
			Conflict: &domain.TagConflict{Worktree: "D:/temp/冲突 目录"}, ConflictFiles: []string{"README.md"},
		}},
		{OperationID: "op-b", Phase: application.TagJobPhaseBlocked, Record: &application.TagRecord{ServiceName: "账单", BlockedBy: "op-a"}},
	}}, exitConflict, nil)
	checkTagText(t, out.String(), "批次 ID: batch-a\n\n支付: 合并冲突\n  操作 ID: op-a", "  服务选择值: payment:api",
		"  合并提示: 禁止将 B 分支合并到 A 分支", "  冲突工作区: D:/temp/冲突 目录", "  冲突文件: README.md",
		"\n\n账单: 等待关联操作\n  操作 ID: op-b\n  阻塞操作 ID: op-a")

	out.Reset()
	o.command = "notify"
	o.outputResult(&out, io.Discard, application.CLITagNoticeResult{
		BatchID: "batch-a",
		Recipients: []releasenotice.RecipientDelivery{
			{Self: true, State: "unknown", Error: "等待飞书确认"},
			{Name: "测试甲", State: "sent", MessageID: "om_a"},
		},
		Warnings: []string{"未取得需求标题，卡片使用任务名称"},
	}, exitError, nil)
	checkTagText(t, out.String(), "批次 ID: batch-a\n\n本人: 结果待核实\n  原因: 等待飞书确认",
		"\n\n测试甲: 已发送\n  飞书消息 ID: om_a\n\n通知提示: 未取得需求标题，卡片使用任务名称")
}

func TestTagTextRetryShowsOperationAndPushedTag(t *testing.T) {
	var out bytes.Buffer
	o := options{command: "retry", operation: "op-a"}
	o.outputResult(&out, io.Discard, application.CLITagJobResult{BatchID: "batch-a", Items: []application.CLITagJobItem{
		{OperationID: "op-a", SelectionKey: "payment:api", Phase: application.TagJobPhaseCompleted, Record: &application.TagRecord{
			ServiceName: "支付", State: string(domain.TagOperationSuccess), Tag: "1.2.3.beta-1",
		}},
	}}, 0, nil)
	want := "批次 ID: batch-a\n\n支付: Tag 已推送\n  操作 ID: op-a\n  服务选择值: payment:api\n  Tag: 1.2.3.beta-1\n"
	if got := out.String(); got != want {
		t.Fatalf("unexpected retry output:\n%s", got)
	}
}

func TestTagCLINotifyRejectsTestRecipients(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"tag", "notify", "--batch", "synthetic", "--with-testers"}, &out, &stderr)
	if code != exitUsage || out.Len() != 0 {
		t.Fatal("removed recipient option reached the notification workflow", code, out.String(), stderr.String())
	}
}

func TestTagCLINotifyValidationErrorTextAndJSON(t *testing.T) {
	var out, stderr bytes.Buffer
	code := run(context.Background(), []string{"tag", "notify"}, &out, &stderr)
	if code != exitUsage || out.Len() != 0 || stderr.String() != "错误: 请指定 --batch\n" {
		t.Fatal("unexpected text error", code, out.String(), stderr.String())
	}
	stderr.Reset()
	code = run(context.Background(), []string{"tag", "notify", "--json"}, &out, &stderr)
	if code != exitUsage || out.String() != "{\"ok\":false,\"error\":\"请指定 --batch\"}\n" || stderr.Len() != 0 {
		t.Fatal("unexpected JSON error", code, out.String(), stderr.String())
	}
}

func TestTagTextNoticeDistinguishesUnsentAndUnknown(t *testing.T) {
	for _, tt := range []struct {
		state, messageID, want string
		skipped                bool
	}{
		{state: "pending", want: "待发送"},
		{state: "queued", want: "待发送"},
		{state: "unknown", want: "结果待核实"},
		{state: "sending", want: "结果待核实"},
		{state: "failed", want: "发送失败"},
		{state: "sent", want: "结果待核实"},
		{state: "sent", messageID: "om_sent", want: "已发送"},
		{state: "sent", messageID: "om_sent", skipped: true, want: "已发送（沿用回执）"},
	} {
		t.Run(tt.state+"/"+tt.want, func(t *testing.T) {
			var out bytes.Buffer
			o := options{}
			o.outputResult(&out, io.Discard, application.CLITagNoticeResult{
				BatchID: "batch-a",
				Recipients: []releasenotice.RecipientDelivery{{
					Self: true, State: tt.state, MessageID: tt.messageID, Skipped: tt.skipped,
				}},
			}, 0, nil)
			want := "批次 ID: batch-a\n\n本人: " + tt.want + "\n"
			if tt.messageID != "" {
				want += "  飞书消息 ID: " + tt.messageID + "\n"
			}
			if got := out.String(); got != want {
				t.Fatalf("state %q: got %q, want %q", tt.state, got, want)
			}
		})
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
