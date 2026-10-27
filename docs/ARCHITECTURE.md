# QA 架构

`cmd/silverwing` 启动 MyGO 原生界面，`internal/desktop` 管理任务、资料、需求、设置与工具界面。界面通过 `internal/application` 的进程内 Action/Snapshot 调用业务，无本地 HTTP 服务。

- `domain`：QA 配置、任务、需求、资料同步与扩展来源模型。
- `storage`：配置分片、备份、任务清单、锁和持久化；配置与任务检查 QA 版标识。
- `workspace`：普通目录任务生命周期、资料 Git 同步、任务根目录迁移、AGENTS 生成和 Codex 打开。
- `documents` / `editor` / `media`：资料目录边界、读写、草稿、原生阅读和编辑。
- `integrations`：Meegle 需求、评论、身份与 AI 命名。
- `platform`：Codex、Skills、插件、MCP、本机环境与诊断。
- `tools`：JSON、时间、文本对比和端口操作。

任务不创建 Git 工作区。资料与扩展来源仍使用 Git，其范围校验、锁、暂存区检查、身份检查与失败提示保留。任务菜单操作绑定任务路径、创建时间、配置及资料身份，后台执行前重新核对，避免作用到已切换或被替换的任务。

任务数据、阅读状态、缓存、日志和配置以传入的应用 home 为根。默认 home 是 `~/silverwing-qa`，显式 `--home` 也会隔离默认任务和资料目录。没有开发版自动迁移路径。
