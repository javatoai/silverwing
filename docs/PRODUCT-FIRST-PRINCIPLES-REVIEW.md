# silverwing：第一性原理产品审查

> 调研日期：2026-08-09。本文把“是否有人会使用”与“是否值得做成独立产品”分开判断；外部事实优先引用产品或项目的一手资料。

## 结论先行

**这不是虚假需求，但“再做一个 Git Worktree 管理器”会是虚假定位。**

Git 已原生支持多个同时存在的工作目录；VS Code、Codex、Cursor 以及 Worktrunk 分别覆盖了其中的大部分操作。silverwing 的可成立价值不在创建目录或启动 IDE，而在把公司内部研发任务的**任务来源、跨服务拓扑、可审计的 Agent 指令、Git 风险状态与唯一现有交付动作（Tag）**收束为一次可复现、可恢复、可培训的任务生命周期。

因此，产品应定位为：**面向多仓库/多服务内部研发团队的任务级 Agent 工作区控制平面**，而不是“AI IDE 启动器”或“Worktree GUI”。

这个结论成立的前提是：目标团队确实反复遇到“一个需求要跨多个服务、每次都要搭建相同环境/说明、且需要保留 Tag 交付痕迹”的问题。若多数任务只改一个仓库、一次只开一个任务、没有统一任务来源或交付动作，则 silverwing 的额外配置与 UI 成本高于收益，应停止扩展。

## 从第一性原理拆解

研发人员完成一次 Agent 辅助任务，最低需要：

1. **隔离的可写代码副本**：不能让并行任务或 Agent 互相污染。
2. **正确上下文**：任务目标、可改范围、跨服务关系和本地约束要进入 Agent。
3. **可执行的开发入口**：能把正确目录交给 IDE/Agent。
4. **安全的交付判定**：知道未提交、未推送、是否能安全归档，以及如何触发既有交付动作。
5. **低认知成本**：以上步骤必须比手工命令、复制说明和记忆规则更省时，且出错时可恢复。

Git 解决第 1 项的底层机制；IDE/Agent 解决第 3 项和部分第 2 项。silverwing 只有在系统性解决第 2、4、5 项，并把第 1 项安全地编排起来时才有独立价值。

## 已有替代方案与其边界

| 能力层 | 已有方案 | 已覆盖的事实 | silverwing 不应重复造的部分 | 仍可能的空白 |
|---|---|---|---|---|
| 多工作目录 | [Git `worktree`](https://git-scm.com/docs/git-worktree.html) | 一个仓库可同时检出多个分支；Git 管理 linked worktree 的元数据、增加、删除、锁定与修复。 | 不能把“创建 worktree”本身当卖点。 | Git 不知道需求、服务拓扑、Agent 说明、组织交付约束。其官方文档还指出子模块支持不完整，不能承诺所有仓库结构都无风险。 |
| IDE 多仓库 | [VS Code 多根工作区](https://code.visualstudio.com/docs/editing/workspaces/multi-root-workspaces) / [VS Code Worktrees](https://code.visualstudio.com/docs/sourcecontrol/branches-worktrees) | 可把多目录放进一个工作区，并已内建 Git Worktree 支持、分支发布与合并。 | 不要做通用 IDE 资源管理器或多根工作区编辑器。 | IDE 工作区不维护业务组、任务来源、跨服务选择策略、组织级 Agent 指令或 Tag 历史。 |
| Agent 本地项目 | [Codex 桌面能力](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan%28.pdf) / [Cursor CLI](https://docs.cursor.com/en/cli/using) | Codex 已有本地目录、worktree、skills、automation 和 Git 能力；Cursor CLI 会读取项目根目录的 `AGENTS.md`/`CLAUDE.md` 并可在脚本中使用。 | 不要托管、伪造或同步第三方 Agent 会话；工具启动器应始终是适配器。 | 各 Agent 不会替公司统一把同一任务的多服务、组说明和交付状态建模；其私有会话/Workspace 数据也不应由 silverwing 写入。 |
| Worktree 工作流自动化 | [Worktrunk 官方项目](https://github.com/max-sixty/worktrunk) | 已提供创建/切换/移除、hooks、交互选择、PR 流程、工作区状态、缓存复制和每 Worktree 端口模板；定位明确为并行 AI agent 的 CLI。 | 不要以“更方便创建/列出 worktree”同质竞争。 | 它是通用 CLI，不连接本公司的飞书需求、组服务配置、任务级 AGENTS 合成与现有 Tag 审计。 |

## silverwing 现有方向中真正有价值的组合

silverwing 当前的核心不是单项功能，而是把以下对象放在同一个任务边界：

- 业务组下的已批准服务清单，支持一个需求选择多个服务；
- 标准 Worktree 与独立克隆两种有意区分的创建策略；
- 全局、组、任务三级 `AGENTS.md` 合成，并保留任务人工区；
- 从需求链接得到任务名称、分支编号和上下文；
- 本地未提交/未推送状态、归档/删除保护；
- 已被团队使用的 Tag 作为首个交付适配器；
- 以适配器启动 Codex、Cursor，而不绑定某一种 Agent。

这套组合可减少两类真实且昂贵的错误：**错误目录/错误服务被 Agent 修改**，以及**任务完成后代码或 Tag 状态不透明而被误归档或漏交付**。它的价值主张应围绕这两个可验证结果，而不是“用了 AI”或“支持更多工具”。

## 虚假需求风险

| 风险 | 为什么会失败 | 可证伪信号 | 应对 |
|---|---|---|---|
| 把 Git 操作包装成 GUI | 熟练开发者可用 Git、IDE 或 Worktrunk 直接完成，额外配置反而更慢。 | 创建任务的中位耗时没有低于手工流程；用户经常绕过 silverwing。 | 只保留公司特有的任务编排与安全检查；通用 Git 能力让给 Git/IDE。 |
| “Agent 工作区”名称大于实际能力 | 若工具只是打开 Codex/Cursor，用户会认为它是脆弱的快捷方式。 | 用户只使用打开 IDE，忽略任务、说明和状态。 | 明确边界：silverwing 管理工作区与约束，不管理 Agent 对话或模型结果。 |
| 过度配置 | 组、模块、Bootstrap、Tag、工具和需求来源都可能让低频用户无法开始。 | 新用户完成首个任务需要培训介入，或默认组也需要大量必填项。 | 默认组的最短路径必须是：选仓库 → 填任务 → 创建；高级能力按需显露。 |
| 用“隔离”承诺超过 Git 能力 | Worktree 隔离文件树和部分 Git 状态，不天然隔离端口、共享数据库、Docker、凭据或外部环境。 | 并行任务仍因端口/同一账号/同一数据库互相干扰。 | 只承诺“代码工作区隔离”；将端口/环境探测做成明确、可选的后续适配器，而非假装已隔离运行时。 |
| 锁死内部流程 | Tag 是当前真实动作，但未来交付方式可能变化。 | 每增加一种交付动作都要修改任务创建主流程或 JSON。 | 保持 Delivery Pipeline Adapter；先为 Tag 建立稳定接口和审计，再按真实需求接 CI/环境。 |
| 需求链接自动化不可靠 | Meegle CLI、登录、空间配置或标题读取失败会让创建流程不可用。 | 用户因自动拉取失败无法手工创建，或频繁关闭该功能。 | 自动拉取必须可选、非阻断、可回退手工输入；记录脱敏失败日志供管理员修复。 |

## 最合适的 ICP（理想客户画像）

第一优先级不是“所有使用 AI 编程的人”，而是具备以下特征的内部团队：

- 一项需求经常同时修改 **2–8 个 Git 服务**；
- 同一开发者每周至少并行处理 2 个任务，或让多个 Agent 并行处理；
- 有统一的需求链接来源和约定分支命名；
- 交付前至少有一个强制、可追踪的动作（当前是 Tag）；
- 团队愿意维护一次性的组/服务配置，且会接受培训；
- 数据和代码必须留在本地，不适合把仓库拓扑交给外部 SaaS。

不适合的场景：单体仓库、低并发、个人开发、没有统一需求/交付流程，或团队已把 Worktrunk + IDE hooks 标准化且没有额外的跨服务治理痛点。

## 建议的验证实验（先验证，后扩张）

### 1. 问题存在性：影子记录两周

招募 5–8 名符合 ICP 的开发者，不要求改变交付方式。记录每个真实任务：涉及服务数、创建/切换环境次数、复制 Agent 说明次数、误操作/遗漏的 Git 或 Tag 事件、从“收到需求”到“可开始编码”的分钟数。若中位任务只有一个服务且无重复搭建，停止投入多服务编排。

### 2. 价值验证：与当前手工流程对照

同一类任务前后对比以下指标：

- 从需求链接到所有目标目录可打开的时间；
- 创建时遗漏服务/选错分支的次数；
- 归档前发现未提交或未推送状态的次数；
- Tag 漏建、错分支或无法追溯的次数；
- 一周后仍由 silverwing 发起的任务比例。

建议阈值：首个任务不比原流程慢；第 3 个任务起中位准备时间降低 30% 以上；至少 60% 试点任务主动通过 silverwing 创建。否则先删功能、减配置，而不是继续增加 Agent/IDE 适配器。

### 3. 可信边界验证：故障演练

用测试仓库演练：未提交文件、未推送提交、远端分支更新、错误的需求链接、Meegle 不可用、Worktree 路径消失、Tag 冲突。验收标准不是“全部自动修复”，而是 silverwing 不丢数据、不误删、不改主工作区、不阻断手工路径，并能清晰给出下一步。

## 产品决策建议

1. **保留**：组→服务→任务→工作区→交付的显式模型、Agent 指令合成、Git 安全状态和 Tag 适配器。
2. **收缩**：把 IDE/Codex/Cursor 维持为启动适配器，不做聊天、项目列表或 IDE Workspace 数据管理。
3. **暂缓**：没有观测到重复人工痛点前，不做 PR/MR、CI、部署、预览环境或运行时全面隔离。
4. **下一阶段的北极星指标**：`任务从需求链接到所有正确工作区可被 Agent 安全打开的中位时间`，并同时观察 `归档/交付前发现的风险数`。效率和风险两个维度必须同时改善。

## 一手来源

- Git 官方：[`git worktree`](https://git-scm.com/docs/git-worktree.html)
- Visual Studio Code 官方：[`Multi-root Workspaces`](https://code.visualstudio.com/docs/editing/workspaces/multi-root-workspaces)、[`Git Branches and Worktrees`](https://code.visualstudio.com/docs/sourcecontrol/branches-worktrees)
- OpenAI 官方：[`Using Codex with your ChatGPT plan`](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan%28.pdf)、[`Codex CLI Getting Started`](https://help.openai.com/en/articles/11096431)
- Cursor 官方：[`Using CLI`](https://docs.cursor.com/en/cli/using)、[`@Files & Folders`](https://docs.cursor.com/context/%40-symbols/%40-files-and-folders)
- Worktrunk 官方项目：[`max-sixty/worktrunk`](https://github.com/max-sixty/worktrunk)
