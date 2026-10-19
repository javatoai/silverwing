# IDE 与 Agent 配置

[返回知识库目录](../Index.md)

> 适用：Silverwing Go / MyGO，源码版本 0.0.1；按 2026-10-19 的代码核对。

Silverwing 负责准备任务和工作区，并把目录交给你选择的外部工具。IDE、Codex 桌面应用、Codex CLI 和 Cursor 是不同入口；配置其中一项，不代表其他工具也已安装或可用。

![任务说明与外部开发工具的打开路径示意图](../assets/agent-context.png)

## 配置 IDE 路径

1. 打开 **设置 → 开发工具 → 开发工具**。列表包含 IntelliJ IDEA、WebStorm、PyCharm、Visual Studio Code、Android Studio 和 DevEco Studio。
2. 首次进入页面时，Silverwing 会尝试为尚未配置的工具探测路径；也可以从右上角 **更多开发工具操作 → 检测缺失工具** 重试。当前自动探测实现覆盖 Windows 与 macOS，检查常见安装目录、JetBrains Toolbox 和系统命令路径。Linux 等环境可直接手动选路径。
3. 路径框留空表示使用自动探测。需要指定版本时点文件夹按钮 **手动选择**，选中应用程序或启动程序后等待自动保存。状态会显示 **已配置**、**自动检测可用**、**路径不存在** 或 **未检测到**。选择 **恢复自动** 可清空手动路径。
4. 在工具卡片的更多菜单里选 **测试打开**。测试前要先在 **设置 → 存储 → 目录** 配好有效的任务根目录；测试会让所选 IDE 打开这个目录，不会验证某个具体服务仓库或任务代码。

## 选择工作区默认工具

IDE 打开入口按“服务配置优先，应用默认值兜底”的方式工作。添加仓库服务时，**设置 → 开发工具 → 全局默认开发工具** 会作为新服务的初始选择；之后可在该项目组的服务配置中单独修改 **默认开发工具**。任务工作区卡片上的 **使用〈工具名〉打开** 会按服务记录的工具类型和对应程序路径打开该 Worktree。

例如，把项目组里的 `payments-api` 服务默认工具设为 Visual Studio Code。之后从任务卡片打开该服务工作区时，会将 `payments-api` 对应的 Worktree 传给 VS Code；另一个服务可以使用不同 IDE。若启动失败，先核对服务的 **默认开发工具**，再到工具卡检查对应路径和状态。

设置页还显示 **允许临时选择开发工具**。该开关在当前版本只保存设置，不影响任务工作区的打开方式；卡片仍按服务配置的默认工具打开。需要更换时，请编辑该服务的默认工具设置。

## Codex 与 Cursor

**在 Codex 中打开** 是任务详情“更多操作”里的桌面应用入口。它通过系统注册的 `codex://` 链接，把任务目录交给 Codex 桌面应用，并请求创建一个新会话；这条路径不启动 Codex CLI。因此能从 Silverwing 打开任务目录，不代表 CLI 已配置；CLI 状态可用也不单独证明桌面应用的链接处理器正常。

Codex CLI 用于 AI 命名、Marketplace 插件和 MCP 配置等功能。可到 **设置 → 任务 → 任务创建区 → Codex CLI 状态 / Codex CLI 命令路径** 检查状态，路径留空时由 Silverwing 自动探测，也可手动选择。Silverwing 的 Codex 配置管理通过本机 CLI 启动 `app-server --stdio` 通信。

Cursor 不在上面的 IDE 路径列表中，而是独立的工作区工具。创建任务时，在表单的 **创建后打开工作区工具** 中选择本次要打开的工具；当前设置页没有可达的全局默认勾选入口。Codex 和 Cursor 打开的是任务目录；Cursor 会请求新窗口，首次打开后还需由用户在 Cursor 里启动 Agent Chat。未检测到 Cursor 时，创建任务表单会显示不可用并禁用该选项。

## 成功标志与排查

- IDE 路径卡显示 **已配置** 或 **自动检测可用**；测试打开后能看到任务根目录。
- 服务工作区按钮的工具名称与服务配置一致，点击后外部 IDE 打开对应 Worktree。
- Codex 桌面入口能打开任务目录；若系统没有 Codex 链接处理器，检查桌面应用安装与系统关联。
- 创建任务时勾选 Codex 或 Cursor 后，创建结束会记录工具打开结果；失败时查看任务卡的工具状态与错误信息。
- IDE 可以启动但路径状态仍报不存在时，重新选择真正的应用程序路径；不要把启动脚本、快捷方式目录或普通项目目录当作 IDE 程序路径。

## 源码索引

- 工具探测和状态：`internal/platform/development_tools.go`、`internal/desktop/settings_core_ui.go`
- 新服务默认值与工作区 IDE 启动：`internal/application/repository_batch.go`、`internal/application/system_actions.go`、`internal/application/system_tool_launch.go`
- Codex 与 Cursor 工作区入口：`internal/application/workspace_tools.go`、`internal/platform/codex_ai.go`
- Codex CLI 定位与进程通信：`internal/executable/resolver.go`、`internal/platform/codex_rpc.go`

相关阅读：[首次配置](../入门与基础/03-首次配置.md)、[第一个多仓库任务](../入门与基础/05-第一个多仓库任务.md)、[Skills 管理](15-Skills管理.md)、[Codex 插件与 MCP](16-Codex插件与MCP.md)。
