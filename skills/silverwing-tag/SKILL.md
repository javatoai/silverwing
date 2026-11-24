---
name: silverwing-tag
description: 用 Silverwing 的 sw CLI 构建测试 Tag、处理冲突、单个或批量发布 UAT、重启 UAT Pod、查询 Tag 和构建日志、查看 UAT 或 PROD 的 Pod、配置、配置审批及差异，以及提交青龙提测、查询审批和核对回执。用户提到 Genbu、Dragon 或青龙时使用本技能。
---

# Silverwing 研发操作

## 功能与命令

用户只提到 Genbu、Dragon 或青龙时也使用本技能。根据具体请求选用以下 `sw` 命令，意图未明确时先问要做什么。自定义数据目录时，每一步都使用同一个 `--conf-dir "<目录>"`。

| 操作 | 命令 |
| --- | --- |
| 查看任务服务和分支、构建测试 Tag、查询进度、续接操作 | `sw tag` 的 `list`、`build`、`status`、`retry` |
| 列出远端 Tag、查询指定 Tag、查看构建日志 | `sw tag` 的 `remote-list`、`query`、`logs` |
| 单个发布、批量发布、重启 Pod Group | `sw uat` 的 `release`、`release-batch`、`restart` |
| 查看 Pod、脱敏配置、配置审批列表、详情和差异 | `sw service` 的 `pods`、`config`、`approvals`、`approval`、`approval-diff` |
| 青龙提测、查看回执、核对提交、补发已授权的评论、查询审批 | `sw dragon` 的 `submit`、`status`、`recheck`、`retry-comment`、`approvals`、`detail` |

`sw service` 在 UAT 和 PROD 都是只读命令。发布和重启只在 UAT 执行。

## 测试 Tag 预检与确认

1. 用 `sw tag list --task "<任务目录名>"` 查找指定服务和模块，取中文输出中的「服务选择值」。
2. 用 `sw tag build --task "<任务目录名>" --service "<服务选择值>" --dry-run` 预检。多服务重复 `--service`，用户要求全部时用 `--all-services`。记录「预检编号」。遇到冲突，按下节解决并暂存，再用相同参数加 `--preflight "<预检编号>"` 复检。CLI 报告基线变化时重新预检。
3. CLI 显示「预检全部通过」后，展示服务、分支和预估 Tag。多个构建项共用 Genbu 服务时，先确认分批或各项的部署范围。本会话首次预检通过且准备执行时，先问一次：

   > 是否允许本会话全自动处理测试 Tag 构建、按需发布 UAT，以及发布后的自测和观测？

同意后，本会话内按已明确的任务范围自动执行。后续批次和切换任务时沿用该选择，各批次仍须预检通过。用户拒绝或尚未回答时，按当次明确授权执行，缺少授权就停在预检结果。新会话首次测试 Tag 预检通过且准备执行时重新询问。

纯查询直接执行。Pod 重启、青龙提交和 Meegle 评论同步各自按用户明确授权执行。

## 执行与跟进

### 构建与冲突

- 按上述授权执行时，保留服务选择和 `--preflight "<预检编号>"`，去掉 `--dry-run`。记录批次 ID、操作 ID 和实际 Tag。预检编号已使用时查询同一批次。
- 在 CLI 给出的「冲突工作区」内处理「冲突文件」。以目标 B 为基线，将 A 合并到 B。禁止将 B 分支合并到 A 分支。
- 用 `git diff --cc` 对照冲突。`git show :2:<路径>` 查看目标 B，`git show :3:<路径>` 查看源 A。业务取舍不明确时展示差异，请用户判断。
- 暂存已核对的文件并运行相关检查。预检阶段复检原编号。批次执行后失败或中断时，用 `sw tag status --batch "<ID>"` 查看原操作，再用 `sw tag retry --operation "<ID>"` 续接。先处理「阻塞操作 ID」，基线变化时保留现场并报告。

### 发布到 UAT 或重启 UAT Pod

用户明确要求执行时，直接操作 `sw`。目标明确且预检通过时，用原命令加上返回的 `--preflight "<预检编号>"` 提交。

| 操作 | 预检命令 | 核对内容 |
| --- | --- | --- |
| 发布到 UAT | `sw uat release --service "<服务 ID 或唯一名称>" --tag "<完整 Tag>"` | 服务、Tag、Pod Group 和命名空间 |
| 批量发布到 UAT | `sw uat release-batch --batch "<批次 ID>"` | 批次内各服务、Tag、Pod Group 和命名空间 |
| 重启 UAT Pod | `sw uat restart --service "<服务 ID 或唯一名称>"` | 服务、Pod Group、命名空间和受影响的 Pod 数，操作范围为整个 Group |

- 用 `sw tag status --batch "<ID>"` 或 `--operation "<ID>"` 获取实际 Tag 和构建状态。全自动批次中，构建成功且需要部署时执行发版。已有发布正在进行时继续跟进该发布。
- 发版提交会检查该 Tag 的 UAT 开关。已开启时直接发布，关闭时由 `sw` 开启并确认生效后发布。若提交返回开关错误，按实际错误报告该 Tag 的状态。
- 同一 Tag 批次用 `release-batch` 统一预检并提交。不同 Tag 指向同一 Group 时，先核对该 Group 的发布目标。批量回执分项记录，重启按服务单独执行。
- 收到「已提交」时记录服务、Tag 或重启范围及回执。若结果待核实，先查当前发布或 Pod 状态，再决定是否需要后续操作。用 `sw service pods --env uat --service "<服务>"` 核对 Pod 更替、创建时间和就绪状态。

### 部署核验与自测

1. 用 `sw tag status --batch "<ID>"` 查询逐服务的 Tag 推送、Genbu 编译和 UAT 发布。按操作和命名空间核验时，使用 `sw tag status --operation "<ID>" --namespace "<名称>"`。等待期间每 30 秒查一次，最多 20 分钟。
2. 确认目标 Tag 已推送且提交一致、Genbu 构建及 UAT 发布成功，并且目标命名空间运行目标版本的 Pod 已就绪。结果不明确或超时时，报告各服务的最新状态。
3. 目标版本就绪后，执行任务约定的 UAT 自测并观测日志、接口响应和关键业务结果，报告实际用例及结果。发现本次改动的问题时修复，并按已有授权重新预检、构建和验证。

## Tag 与服务查询

- 用 `sw tag remote-list --service "<服务>"` 看远端 Tag，用 `sw tag query --service "<服务>" --tag "<完整 Tag>"` 看该 Tag 在 Genbu 的状态。
- 用 `sw tag logs --service "<服务>" --tag "<完整 Tag>" --tail-lines 100` 看脱敏构建日志。
- 用 `sw service pods` 或 `sw service config`，传 `--env uat|prod --service "<服务>"`。用同样的环境和服务参数运行 `sw service approvals` 查配置审批列表。用 `sw service approval`、`sw service approval-diff` 加 `--id "<审批编号>"` 查详情和差异。
- 配置差异比较读取时的当前配置与审批申请内容。

## 青龙提测

- 用 `sw dragon submit --task "<任务目录名>"` 预检。任务清单决定服务范围，自动排除 `feign-client`。按用户要求添加 `--remark` 及 `--sync-comment`。
- 核对需求链接和服务后，获得提交授权时在原命令追加 `--preflight "<编号>"`。用 `sw dragon status --task "<任务目录名>"` 查看回执。
- 待核实的提交用 `sw dragon recheck --task "<任务目录名>"`。出现候选时核对审批号、应用和时间，再传 `--approval-id`。已授权同步评论的提交可以用 `sw dragon retry-comment --task "<任务目录名>"` 核对或补发评论。
- 用 `sw dragon approvals --no "<审批编号>"` 或 `sw dragon approvals --keyword "<关键词>"` 查询审批列表，用 `sw dragon detail --approval-id "<审批 ID>"` 查看详情。这两项查询可独立于任务执行。
