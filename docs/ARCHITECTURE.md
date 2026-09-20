# 架构与测试说明

## 模块和分层

Gradle 模块：

```text
core      领域模型、应用编排、Git/JSON/文件系统基础设施
desktop   Compose Desktop 展示、输入和窗口生命周期
cli       只面向 Agent 的 JSON 命令行入口（silverwing agent / silverwing tag）
```

代码按以下依赖方向组织：

```text
Desktop -> Application -> Domain
CLI     -> Application -> Domain
                ^
                |
         Infrastructure
```

- **Domain**：组、仓库、服务模块、任务、工作区、Tag 策略等稳定模型和规则。
- **Application**：通过用例服务编排配置、任务、刷新、工作区创建和 Agent 文档，不包含 Compose 控件。
- **Infrastructure**：实现 Git、JSON、原子文件写入、WatchService 和外部系统适配器。
- **Desktop**：`Main` 负责窗口、主题、导航和装配；`AppSessionStore`、`OperationCoordinator` 以及任务、设置、Agent、交付控制器维护展示状态和回调，不直接执行 Git 命令、解析 JSON 或拼接 AGENTS.md。
- **CLI**：`silverwing` 只暴露 JSON 协议命令，不实现业务规则。`AgentOperationService` 承载任务创建的两阶段 plan/apply 协议，`TagOperationCliFacade` 承载 Tag 构建闭环；后者与桌面 `DeliveryController` 是同一套应用层 Tag 用例的两个入口，共享预检、Git 写策略和仓库锁。

0.5.0 将任务生命周期与工作区健康拆为两个正交模型。`TaskLifecycleStatus` 只决定活跃/归档导航；`WorkspaceHealth` 只决定创建、重试、Git、IDE 和交付能力。任务健康由工作区动态聚合，不写入 JSON。桌面层的 `RequirementController` 负责 Meegle 请求去重、缓存、并发限制和过期回写保护，`DesktopActions` 是剪贴板与操作系统动作的唯一边界。

配置、任务、Git、工作区创建、Agent 文档和飞书集成都通过小接口及构造函数注入。标准 Worktree 与独立克隆是两个 `WorkspaceProvisioner` 策略，控制器只选择策略并展示结果。

## 启动和刷新边界

首次启动会创建 `~/silverwing`、`~/silverwing/tasks` 和 `~/silverwing/config/` 下的八个当前配置分片。后续启动读取本地配置、处理未完成的任务根目录迁移日志、读取任务清单和 Agent 文件，并在后台静默补齐尚未配置的开发工具路径、解析系统终端和检查最近任务的本地 Git 状态；这些启动探测只访问固定本机候选目录与 PATH，不阻塞 UI、不递归扫描磁盘、不联网。不探测旧产品目录，不 Fetch、不访问飞书。顶部手动刷新会校验已配置仓库、重新读取任务与 Agent 文件、刷新飞书状态和当前任务 Git 状态。

## 持久化

- 配置目录：`~/silverwing/config/`（`layout.json`、`workspace.json`、`services.json`、`tag.json`、`tools.json`、`git.json`、`integrations.json`、`appearance.json`）
- 默认任务根目录：`~/silverwing/tasks`
- 任务说明模板：`~/silverwing/agents/task-templates.json`
- 全局说明：`~/silverwing/agents/global/AGENTS.md`
- 组说明：`~/silverwing/agents/groups/<groupId>/AGENTS.md`
- 任务清单：`<taskDir>/silverwing.json`
- 最终说明：`<taskDir>/AGENTS.md`
- Tag 操作快照：`<taskDir>/tag-operations/*.json`
- 构建历史：`<taskDir>/tag-build-history.jsonl`
- 仓库锁：`~/silverwing/locks/<git-common-dir-hash>.lock`
- 任务根目录迁移日志：`~/silverwing/migrations/task-root.json`

配置和任务使用严格 schema。文件写入采用同目录临时文件加原子替换，避免进程中断留下半个 JSON 或覆盖用户正在维护的 Agent 文档。

任务根目录迁移与任务写操作共享根目录锁。迁移先记录原路径、目标路径、原清单和工作区策略；同文件系统移动、跨文件系统复制后修复标准 Worktree，更新目标清单和 Agent 系统区，并验证 Git 快照。配置是提交点：提交前失败回滚到源路径，提交后启动恢复只继续清理旧目录。

## Git 隔离和安全

- 人工选择的仓库按规范化 `git-common-dir` 标识和去重。
- 一个标准服务中，每个模块都对应一个独立 Worktree；相同基础分支也不会合并物理目录。旧任务清单若记录共享路径，运行时仍按规范化路径去重以保持兼容。
- 独立克隆从 `origin` 完整克隆到任务目录，并直接使用选定分支。
- Tag 构建按仓库公共目录获取 OS 文件锁，临时 Worktree 路径包含仓库 Hash 和 UUID。
- 测试分支和 Tag Push 均非强制；不自动 Pull/Rebase、不 Force Push、不自动解决冲突。

## 验证

核心测试使用真实临时 Bare Remote、Clone 和 Linked Worktree，覆盖：

- 有序组与服务数组、单组界面降级、空组删除约束；
- 人工仓库校验、`git-common-dir` 去重以及 Bare/Linked Worktree 拒绝；
- 启动零扫描与用户触发的手动刷新；
- 标准 Worktree、独立克隆、单模块兼容和多模块分支后缀；
- Tag 组级和子级开关、克隆分支 Tag；
- 三级 Agent 合成、人工区保留、外部同步和冲突处理；
- 归档、删除、合并预检及 Tag 状态机。
- `{num}` 分支占位符、Meegle 标题保护以及本地未提交/未推送状态；
- 可注入交付流水线注册表和 Tag 适配器。

提交前至少运行：

```powershell
.\gradlew.bat test
.\gradlew.bat :desktop:compileKotlin
.\gradlew.bat :desktop:run
.\scripts\build-windows.ps1
```

Windows 脚本会构建绿色目录/Zip、EXE 与 MSI。Release 工作流还会在 macOS 运行同一测试集并构建 DMG。
