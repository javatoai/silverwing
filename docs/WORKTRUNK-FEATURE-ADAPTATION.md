# 从 Worktrunk 借鉴到 silverwing 的能力取舍

> 调研日期：2026-08-09。仅使用 Worktrunk 官方网站与官方 GitHub 仓库。本文是产品建议，不引入 Worktrunk 作为 silverwing 的运行时依赖。

## 结论

Worktrunk 的核心是“按分支管理多个 Worktree 的 CLI”；silverwing 的核心是“按飞书研发任务编排多个服务、Agent 工作区和 Tag 交付”。两者在 Git 生命周期上重叠，但 silverwing 已经承担了任务、组、服务组合、`AGENTS.md`、工作区工具和 Tag 等更上层语义。

因此，silverwing 应吸收 Worktrunk 的**安全可观测性和局部自动化原则**，而不是把 Worktrunk 的分支切换器、PR/MR 和通用 Shell 自动化再做一遍。

## 对照表

| Worktrunk 能力 | 官方证据 | silverwing 当前重叠 | 对内部场景的价值 | 实现风险 | 建议 |
| --- | --- | --- | --- | --- | --- |
| 聚合工作区 Git 状态 | [`wt list`](https://worktrunk.dev/list/) 显示未提交、默认分支差异、远端差异；其状态字段含 staged、modified、untracked、冲突和缺失 Worktree | silverwing 已显示“未提交文件数”和“未推送提交数” | 高：任务详情可直接判断能否安全构建 Tag 或归档 | 低：全部使用本地 Git 已知信息，不 fetch | **P0：补充“相对创建基线落后 N 提交 / 冲突中 / 工作区不存在”**；共享物理 Worktree 只检查一次 |
| 生命周期 Hook 与可审计执行 | [`wt hook`](https://worktrunk.dev/hook/) 提供 create/start/commit/merge/remove 阶段；[`FAQ`](https://worktrunk.dev/faq/#what-commands-does-worktrunk-execute) 说明命令批准与日志边界 | silverwing 有 `BootstrapConfig`、复制规则、命令和失败重试 | 高：把初始化、Tag 前校验、服务启动等动作统一成可理解流程 | 中：任意命令会带来误执行和密钥泄露风险 | **P0：演进为“预览 → 明确批准 → 执行 → 任务日志 → 可重试”的强类型动作框架**。不引入可自由拼接的 Shell DSL，也不默认执行依赖安装 |
| 每 Worktree 的确定性端口与服务 URL | [官方 Tips](https://worktrunk.dev/tips-patterns/#dev-server-per-worktree) 使用 branch hash 生成端口并展示/探测服务 URL | silverwing 仅在 Agent 说明中建议动态找空闲端口 | 高：直接解决同机并行 Agent/多服务开发最常见的运行冲突 | 中高：项目技术栈和端口注入方式不同 | **P0：做任务级“运行配置”**：端口策略（关闭/自动/手工）、启动/停止命令、可选健康 URL。创建后不自动启动；只在用户点击后预览影响并执行。端口先探测，冲突可重新分配；只写本地 `.env.local` 或传启动参数，绝不改写/提交仓库端口配置 |
| 启动的进程在清理前可安全处理 | Worktrunk 的 `remove --reap` 会处理 Worktree 相关进程；其近期 [release notes](https://github.com/max-sixty/worktrunk/releases/tag/v0.67.0) 强调进程树和超时边界 | silverwing 的归档/删除已有 Git 安全检查，没有进程所有权模型 | 中高：减少删除/归档时的文件占用和遗留本地服务 | 高：不能扫描或终止不属于 silverwing 的进程 | **P1：仅跟踪 silverwing 自己启动的进程**（PID、工作目录、启动时间、命令摘要）。归档/删除时列出并让用户确认停止，绝不按目录杀全系统进程 |
| 复制忽略文件与构建缓存 | [官方 Tips](https://worktrunk.dev/tips-patterns/#copy-ignored-files) 提供 `wt step copy-ignored`，用来避免冷启动 | silverwing 已有 `BootstrapConfig.copyRules` | 中：可减少多服务任务的本地初始化等待 | 中：`.env`、令牌、机器文件可能被复制 | **P1：增强现有复制规则的预览和白名单**：展示将复制的文件、大小和覆盖影响；默认拒绝敏感模式，不做“复制全部 ignored 文件” |
| 项目共享 + 用户本地覆盖的动作配置 | [`wt config`](https://worktrunk.dev/config/) 区分用户配置和可提交的项目 hooks；项目命令需首次批准 | silverwing 有组、服务与用户目录配置 | 中：团队可复用服务的启动、校验和 Bootstrap 约定 | 中：配置层级不清会增加培训成本 | **P1：服务运行配置采用“组默认 + 服务覆盖 + 任务快照”**，使用 silverwing JSON 强类型模型；不要让 `.config/wt.toml` 成为第二套配置来源 |
| Agent 会话活动标记 | [Agent Integration](https://worktrunk.dev/claude-code/) 可在列表中显示 Agent 状态，同时说明异常退出会留下 marker | silverwing 能打开 Codex/Cursor，但不托管它们的会话 | 低到中：可帮助发现“已发起但未完成”的工作 | 中：外部工具无法可靠报告真实运行状态 | **P2：最多显示“silverwing 已发起 / 状态未知”**，不要声称 Codex、Cursor 或未来工具正在执行 |
| Worktree 路径模板和按分支切换器 | [`wt switch`](https://worktrunk.dev/switch/) 使用可配置路径模板、交互选择和分支快捷方式 | silverwing 以任务根目录、组和服务组合创建工作区 | 低：会把用户从“任务”拉回“分支”心智 | 高：与任务目录、同任务多服务、多模块共享 Worktree 冲突 | **不纳入**。保持 silverwing 的任务目录模型 |
| 自动 merge / rebase / PR/MR / CI 汇总 | [首页](https://worktrunk.dev/) 将 merge、PR checkout、CI、LLM summary 作为高级可选能力 | silverwing 当前真实交付需求只有 Tag | 低：尚无已确认内部需求 | 高：远端权限、冲突处理、审查规则和责任边界复杂 | **不纳入当前路线**。未来只有明确需求出现时才通过 `DeliveryPipelineAdapter` 接入 |
| LLM 提交消息与分支摘要 | [`wt config`](https://worktrunk.dev/config/#llm-commit-messages) 依赖外部 LLM CLI | silverwing 已以 `AGENTS.md` 和 IDE/Agent 启动提供上下文 | 低：不能替代代码审查或质量门禁 | 中：成本、隐私与输出质量 | **不纳入** |

## 下一期最小闭环

建议把下一期收敛为“可安全运行本地服务”，而不是泛化为 DevOps 平台：

1. 任务详情的 Git 健康摘要：未提交、未推送、落后创建基线、冲突、目录缺失。
2. 服务运行配置：端口策略、启动命令、停止命令、健康 URL；默认关闭。
3. 用户点击“启动服务”后才预览并执行，记录任务级日志与 PID；失败可重试。
4. Tag、归档或删除前，仅提醒并处理 silverwing 托管的相关进程。

这条闭环直接服务于“同一研发同时运行多个 Agent/任务/服务”的真实冲突；不要求 PR、CI、部署、数据库或 Docker 隔离。

## 设计边界

- silverwing 启动时仍不执行 Git fetch、远程 CI 查询或外部网络访问；本地 Git 健康检查异步进行。
- 所有有副作用的服务命令都必须显示完整命令、工作目录和潜在文件影响，并要求用户明确触发。
- 不复制或暴露 `.env`、令牌、Cookie、SSH 私钥等敏感内容；日志只记录脱敏后的命令摘要、退出码和耗时。
- 工作区路径、任务、服务、飞书链接与 Tag 历史继续以 silverwing 自己的 schema 持久化，不依赖 Worktrunk 的 `.git/wt` 缓存或 Git config 私有键。

## 官方资料

- [Worktrunk 首页与核心命令](https://worktrunk.dev/)
- [`wt switch`：创建、启动和切换 Worktree](https://worktrunk.dev/switch/)
- [`wt list`：Git 状态、远端差异与 JSON 输出](https://worktrunk.dev/list/)
- [`wt hook`：生命周期 Hook](https://worktrunk.dev/hook/)
- [配置、项目 Hook 与命令批准](https://worktrunk.dev/config/)
- [端口与忽略文件复制等模式](https://worktrunk.dev/tips-patterns/)
- [Agent 集成的能力与状态局限](https://worktrunk.dev/claude-code/)
- [Worktrunk 官方 GitHub 仓库](https://github.com/max-sixty/worktrunk)
