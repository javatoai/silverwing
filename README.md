# silverwing-qa

面向测试同事的原生桌面工作台，使用 Go 与 MyGO 构建。分支：`release/snb-qa`。

## 功能

- 任务创建、搜索、置顶、归档、恢复及删除。
- 任务资料、需求详情与评论、需求说明三个页签，默认打开资料。
- 本地资料：Markdown、PDF、图片、表格与图表阅读；普通文本、JSON、SQL 等源码编辑。
- Meegle 需求查询、参与人、评论、基于需求创建任务及可选的 AI 命名。
- Codex 打开任务、全局与项目组 AGENTS、任务说明模板、Skills、插件和 MCP。
- 全部本地实用工具：JSON、日期时间、文本对比、端口查询与结束进程。
- 可选的任务资料 Git 同步，以及 Git 来源的 Skills 和插件。

已删除服务与代码仓库管理、Worktree、代码 Git 操作、Tag、Genbu、生产发布、IDE 与 Cursor 集成。

## 首次使用

1. 在“设置 → 存储”确认任务、资料目录及资料子目录。QA 默认数据根目录为 `~/silverwing-qa`，任务在其 `tasks` 下，资料在其 `materials` 下。
2. 在“设置 → CLI”配置 Meegle CLI 和项目，再在需求页读取需求并创建任务。Codex 和 Meegle 均需单独安装或配置。
3. 创建任务时填写名称、项目组和说明，可使用模板及选择在 Codex 中打开。无代码仓库也能创建任务。
4. 任务右键菜单可打开资料、置顶、归档、恢复或删除。删除只移除该任务目录，外部关联资料保留。
5. 需要团队同步时，再配置资料 Git 地址、已有远程分支和同步周期。Git 配置只用于资料及扩展来源。

QA 与开发版独立安装、独立存储，不自动迁移开发版配置和任务。QA 配置备份包含 `edition: qa` 标识，只能导入同版数据。

## 构建

```powershell
go test -timeout 10m ./...
pwsh -File scripts/build-windows.ps1 -PortableOnly
```

需要 `go.mod` 中指定的 Go 版本。Windows ZIP 完整解压后运行 `silverwing-qa/silverwing-qa.exe`，保留旁边的 `resources` 目录。

Windows 支持 x64 ZIP、Setup EXE、MSI；macOS 支持 Apple silicon DMG。完整打包说明见 [RELEASE.md](docs/RELEASE.md)。QA 发布标签为 `qa-vX.Y.Z`，资产前缀为 `silverwing-qa-`，更新页只指向 QA 发布渠道。

## 维护

本分支专门维护测试能力；后续从开发分支同步公共功能时需重新核对 [版本边界](docs/QA-EDITION.md)。产品分支 `release/snb-product` 基于 QA 版，功能保持一致，安装与数据独立。

[English](README.en.md) · [架构](docs/ARCHITECTURE.md) · [原生阅读依赖](docs/NATIVE-MEDIA.md)

本项目使用 MIT 协议，第三方依赖按其各自协议分发，打包时生成第三方声明。
