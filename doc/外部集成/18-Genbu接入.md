# Genbu 与 Dragon 平台连接

[返回目录](../Index.md)

Silverwing 内置 Genbu 与 Dragon 连接。Genbu 用于查询 Tag 构建、发布及服务状态。Dragon 用于提交青龙提测审批。两项连接共用账号，各自管理启用状态和登录。

![Tag 与 Genbu 状态流转示意](../assets/delivery-tag-flow.png)

> 示意图按当前实现绘制，不是界面截图。

## 保存账号并测试连接

1. 打开 **设置 → 工具与连接 → 平台连接**，填写共用账号和密码，点 **保存账号**。编辑同一个账号时，密码留空可保留已保存的密码。
2. 按需启用 Genbu 或 Dragon，分别点击对应的 **测试登录**。测试成功表示账号能登录该平台。服务查询和审批提交仍需该平台授予对应权限。
3. 使用 Tag 状态查询时，在服务配置中启用 **Genbu 探测**，填写平台上的准确服务名。使用青龙提测时，在任务的「任务分支」页打开 **青龙提测**，核对需求和服务后提交。

## 查看状态

选择任务并打开 **Tag** 页。只有 Tag 操作状态为成功或已推送且记录中存在有效 Tag，同时服务启用了 Genbu 探测时，Silverwing 才会查询状态。Silverwing 通过 Genbu 接口读取状态并更新历史记录。

Tag 页打开时会自动轮询：新记录、未知状态或构建进行中通常每 5 秒查询；其他空闲记录通常每 30 秒查询。离开 Tag 页会停止自动轮询。UAT 成功、构建失败或有更新的发布记录取代当前记录后，也会停止相应轮询。记录下方的 **刷新** 可手动强制重新读取。

例：虚构服务 `billing-api` 对应 Genbu 名称 `billing-service`，Tag 历史记录显示 `2.4.1`。先确认服务配置已启用探测且名称与 Genbu 项目一致，再打开任务的 Tag 页；当远端状态变化时，检查同一条记录下分开的 **构建** 和 **UAT** 徽标。若显示未找到或查询失败，先核对 Genbu 登录结果、服务映射和该 Tag 的远端记录，再点 **刷新**；不要重复创建 Tag 来“触发构建”。

## 状态含义与边界

- **Tag 操作成功：**Git 写入完成，Tag 已创建并推送。历史列表中本地操作状态的“构建成功”文案容易误读；它并非 Genbu 构建结果。
- **Genbu 构建成功：**Genbu 查询结果中的 Build 状态为成功，只说明构建阶段完成；它不能证明 UAT 已发布。
- **Genbu UAT 成功：**UAT 徽标显示成功，才代表查询到 UAT 发布成功状态。仍应以目标服务/环境和记录时间核验是否对应预期发布。

Tag 历史会显示 **构建** 和 **UAT** 两类状态。出现构建失败时，可在 Genbu 查看对应 Tag 的构建日志。

**核验顺序：**先确认 Tag 历史中的仓库、服务、模块和 Tag；再分别读取本地 Git 操作状态、Genbu 构建状态、Genbu UAT 状态。若三者不一致，以各自状态字段及对应系统中的记录为准，不用“Tag 成功”替代构建或 UAT 的证据。

## 发布到 UAT 和重启 Pod

服务列表和任务的「服务信息」在 PROD 仅提供 Pod 明细和脱敏配置查询。修改配置和查看配置审批在 UAT 提供。

在 UAT 的「查看配置审批」中打开详情，左侧显示当前配置，右侧显示该审批拟议的修改。待审批且当前账号有权限时，可核对差异后点击「同意审批」。提交前会重新核对审批和当前配置。结果待核实时，可刷新详情查看平台状态。

- 切到 UAT，在服务列表点「发布到 UAT」，或在任务的「服务信息」点「发布」。选择或输入完整 Tag，再点「发布到 UAT」。应用会核对构建和发布目标，然后提交。在任务的「Tag」页，点构建成功记录上的「发布」会直接带入该 Tag。
- 在任务的「Tag」页，点批次卡片上的「批量发布」，核对服务和 Tag 后提交。批次只包含开启 Genbu 探测的服务，这些服务都通过检查后开始发布，结果按服务显示。
- 在任务的「服务信息」点「重启」，或在服务列表的更多菜单中选择「重启 UAT Pod」。核对命名空间和受影响的 Pod 数，再确认重启整个 Group。
- 提交后可查看 Pod 明细，核对运行版本和就绪状态。

```text
sw uat release --service <服务> --tag <Tag>
sw uat restart --service <服务>
sw uat release-batch --batch <批次ID>
```

以上命令默认预检。确认目标后，在同一条命令中加上 `--preflight <预检编号>` 提交。

桌面和 `sw` 提交发布时会检查该 Tag 的 UAT 开关。开关已开启时直接发布。开关关闭时自动开启，确认生效后提交发布。

## 命令行查询与提测

在桌面「平台连接」保存共用账号后，`sw` 按数据目录中的平台开关和服务映射访问平台。自定义数据目录使用 `--conf-dir`，需要结构化输出时增加 `--json`。

Windows 用户可在「设置 → 工具与连接 → Silverwing CLI」选择目录并安装。安装完成后，软件会将目录加入当前用户的 PATH。重新打开终端后，运行 `sw --version` 核对安装。

- `sw tag remote-list --service <服务>` 分页查远端 Tag。`sw tag query --service <服务> --tag <Tag>` 查看精确 Tag 的构建和发布状态。`sw tag logs` 使用相同参数读取脱敏日志，`--tail-lines` 默认 100。
- `sw service pods`、`sw service config`、`sw service approvals` 接收 `--env uat|prod --service <服务>`。`sw service approval` 和 `sw service approval-diff` 另需 `--id <审批编号>`。差异以读取时的当前配置为基线，文本和 JSON 均脱敏。生产业务和配置审批在 CLI 中均只读。
- `sw dragon submit --task <任务目录名>` 从任务清单取服务，跳过 `feign-client`，完成后展示预检编号。需要备注或同步需求评论时增加 `--remark <备注>` 或 `--sync-comment`。确认后在原命令加 `--preflight <预检编号>`。使用 `sw dragon status --task <任务目录名>` 核对回执。
- 青龙审批可通过 `sw dragon approvals` 和 `sw dragon detail --approval-id <审批ID>` 查询。提交结果待核实时用 `sw dragon recheck --task <任务目录名>`。审批成功而评论失败时用 `sw dragon retry-comment --task <任务目录名>` 补发。

## 源码索引

- 平台连接和账号设置见 `internal/application/platform_connections.go`、`internal/desktop/settings_platform_connections.go`。
- Tag 记录与 Genbu 查询：`internal/delivery/tag.go`、`internal/delivery/tag_probe.go`、`internal/application/delivery_actions.go`
- 轮询调度和历史状态展示：`internal/desktop/tag_genbu_poll_schedule.go`、`internal/desktop/tag_history_ui.go`、`internal/desktop/tag_status_ui.go`

## 相关文档

[Tag 交付](../日常使用/12-Tag交付.md) · [网络代理与 CLI 环境](../配置与数据/22-网络代理与CLI环境.md) · [外部 CLI 与 Codex 集成](../技术架构/26-外部CLI与Codex集成.md)
