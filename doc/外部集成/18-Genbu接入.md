# Genbu 接入

[返回目录](../Index.md)

> 适用产品版本：Silverwing Go 0.0.1　｜　代码核对日期：2026-10-19

Silverwing 在 Tag 历史中通过 Genbu CLI 查询某个服务与 Tag 的构建、UAT 状态。此接入是状态探测，不是部署控制台：创建并推送 Git Tag、Genbu 构建成功、UAT 发布成功是三个不同结果，不能相互替代。

![Tag 与 Genbu 状态流转示意](../assets/delivery-tag-flow.png)

> 示意图按当前实现绘制，不是界面截图。

## 配置 CLI 与服务映射

1. 打开 **设置 → CLI → Genbu CLI**，检查当前路径和版本。显式配置路径时，Silverwing 会运行 `--version` 验证可执行文件。路径留空时，应用先解析随应用提供的资源，再尝试操作系统路径发现；成功识别版本后会保存解析到的路径。
2. 版本校验只证明该文件能启动并返回版本，不会验证 Genbu 账号权限、项目访问权限、服务名或 Tag 是否存在。设置页没有 Genbu 登录流程；安装和授权应按团队提供的发行版/权限说明办理，不要猜测安装或登录命令。
3. 在服务配置中启用 **Genbu 探测**，填写准确的 **Genbu 服务名**。默认值使用 Silverwing 服务显示名，但显示名不一定等于 Genbu 服务标识，应向项目维护人确认。探测按任务所属服务 ID 找到这项配置，再将服务名与本次 Tag 传给 CLI；映射错误会导致查询失败或找不到记录。网络环境需要代理时，再按团队网络配置检查 `GENBU` 对应代理设置。

## 查看状态

选择任务并打开 **Tag** 页。只有 Tag 操作状态为成功或已推送且记录中存在有效 Tag，同时服务启用了 Genbu 探测时，Silverwing 才会查询状态。查询调用形如 `genbu query-tag --json &lt;service&gt; &lt;tag&gt;`；Silverwing 读取返回结果并更新历史记录，不会调用构建或发布命令。

Tag 页打开时会自动轮询：新记录、未知状态或构建进行中通常每 5 秒查询；其他空闲记录通常每 30 秒查询。离开 Tag 页会停止自动轮询。UAT 成功、构建失败或有更新的发布记录取代当前记录后，也会停止相应轮询。页面菜单中的 **刷新** 可手动强制重新读取。

例：虚构服务 `billing-api` 对应 Genbu 名称 `billing-service`，Tag 历史记录显示 `2.4.1`。先确认服务配置已启用探测且名称与 Genbu 项目一致，再打开任务的 Tag 页；当远端状态变化时，检查同一条记录下分开的 **构建** 和 **UAT** 徽标。若显示未找到或查询失败，先核对 CLI 路径/版本、服务映射和该 Tag 的远端记录，再点 **刷新**；不要重复创建 Tag 来“触发构建”。

## 状态含义与边界

- **Tag 操作成功：**Git 写入完成，Tag 已创建并推送。历史列表中本地操作状态的“构建成功”文案容易误读；它并非 Genbu 构建结果。
- **Genbu 构建成功：**Genbu 查询结果中的 Build 状态为成功，只说明构建阶段完成；它不能证明 UAT 已发布。
- **Genbu UAT 成功：**UAT 徽标显示成功，才代表查询到 UAT 发布成功状态。仍应以目标服务/环境和记录时间核验是否对应预期发布。

Genbu 返回的初始、构建中、成功、失败等状态会映射到界面状态。当前界面主要展示 **构建**、**UAT** 两类徽标；虽会解析并保存 Production 状态，但不在该页展示。该页也不提供 Genbu 构建日志详情、启动构建或部署按钮，因此不要把状态查询写成自动部署。

**核验顺序：**先确认 Tag 历史中的仓库、服务、模块和 Tag；再分别读取本地 Git 操作状态、Genbu 构建状态、Genbu UAT 状态。若三者不一致，以各自状态字段及对应系统中的记录为准，不用“Tag 成功”替代构建或 UAT 的证据。

## 源码索引

- 可执行文件解析、版本检查：`internal/executable/resolver.go`、`internal/platform/cli.go`、`internal/platform/cli_resolution.go`
- CLI 设置与服务映射：`internal/desktop/settings_cli_ui.go`、`internal/desktop/settings.go`、`internal/desktop/service_editor.go`
- Tag 记录与 Genbu 查询：`internal/delivery/tag.go`、`internal/delivery/tag_probe.go`、`internal/application/delivery_actions.go`
- 轮询调度和历史状态展示：`internal/desktop/tag_genbu_poll_schedule.go`、`internal/desktop/tag_history_ui.go`、`internal/desktop/tag_status_ui.go`

## 相关文档

[Tag 交付](../日常使用/12-Tag交付.md) · [网络代理与 CLI 环境](../配置与数据/22-网络代理与CLI环境.md) · [外部 CLI 与 Codex 集成](../技术架构/26-外部CLI与Codex集成.md)
