# Skills 管理

[返回知识库目录](../Index.md)

> 适用：Silverwing Go / MyGO，源码版本 0.0.1；按 2026-10-19 的代码核对。

Silverwing 提供两件独立的事：浏览本机已安装的 Skill 文件，以及从 Git 仓库发现、安装和更新 Skill。它展示和管理文件，不会在应用里执行 Skill；外部 Agent 是否发现新安装的 Skill，还取决于该工具自己的读取方式。

![扩展来源、预览、安装与配置边界的通用示意图](../assets/extensions-lifecycle.png)

## 浏览本机 Skills

打开 **设置 → skill → 本机 Skill**。页面读取当前操作系统用户主目录下 `~/.agents/skills` 中带有 `SKILL.md` 的普通文件夹，显示已安装列表、文件树和预览。选择 Skill 后可点文件查看内容；支持复制文件内容、复制路径、刷新列表，以及对当前 Skill 执行 **备份并卸载**。

本机浏览器用于检查内容，不提供编辑或保存按钮，也不会运行文件中的命令。卸载前会创建 Silverwing 备份，再从本机 Skill 目录移除该 Skill。目录里没有可识别的 `SKILL.md` 时，列表会为空。

## 添加 Git 来源并安装

前置条件是本机 Git 可用，且能访问目标仓库。进入 **设置 → skill → 安装 Skill → 添加来源**，填写显示名称、Git 仓库地址、可选分支或标签，以及 Skills 相对目录。界面默认填入 `skills`；如果仓库实际放在 `.agents/skills` 等位置，要把路径改成仓库内正确的相对目录。

添加来源会立即保存 Silverwing 来源设置，并通过 Git 克隆来源、读取 `SKILL.md` 清单。成功后来源卡片列出发现目录和 Skill 名称；点 **刷新目录** 可重新获取仓库并发现版本变化。打开某个 Skill 的 **预览** 后，先检查说明和步骤，再点 **安装**。Silverwing 会把文件复制到 `~/.agents/skills/<Skill 名称>`，来源本身缓存在 `<Silverwing Home>/codex/skill-sources/<来源 ID>`。

仓库应为每个 Skill 提供独立目录和 `SKILL.md`，其中需要 `name` 与 `description` 元数据。例如：

```text
team-skills/
└─ skills/
   └─ api-check/
      ├─ SKILL.md
      └─ references/
         └─ checklist.md
```

如果目标目录中已经存在同名 Skill，Silverwing 会将它标为外部安装，要求先确认 **接管**。接管会先备份并覆盖原目录，之后该 Skill 才能由 Silverwing 更新和卸载。已由 Silverwing 管理的 Skill 可从来源卡片选择 **更新** 或 **卸载**；更新和卸载前都会备份现有内容，备份存于 `<Silverwing Home>/codex/skill-backups`。

## 移除来源与卸载的区别

来源卡片里的 **移除来源** 会移除 Silverwing 的来源配置、Git 缓存和发现记录，但保留已安装 Skill。要删除某个 Skill，需在来源卡片上对 Silverwing 管理的项目执行卸载，或在 **本机 Skill** 页对当前项目执行备份并卸载。来源移除后，本机 Skill 仍可能被外部 Agent 读取；若需要彻底清理，先在安装来源卡片完成卸载，再移除来源。

来源状态会标识 **未安装**、**外部安装** 或 **由 Silverwing 管理**。若刷新目录失败，先确认 Git 路径、网络访问、分支或标签名称、Skills 相对目录和仓库的 `SKILL.md` 结构；已有发现快照可能仍显示为旧状态，要以最近一次成功刷新时间为准。

## 源码索引

- 设置入口与来源操作：`internal/desktop/settings.go`、`internal/desktop/settings_skills_ui.go`
- 本机 Skills 浏览：`internal/desktop/skills_browser_ui.go`、`internal/platform/extensions.go`
- Git 缓存、元数据发现、安装、备份与卸载：`internal/platform/extensions.go`、`internal/platform/extensions_context.go`
- Silverwing home 下的缓存路径：`internal/storage/paths.go`

相关阅读：[首次配置](../入门与基础/03-首次配置.md)、[IDE 与 Agent 配置](13-IDE与Agent配置.md)、[Codex 插件与 MCP](16-Codex插件与MCP.md)、[目录与数据存储](../配置与数据/20-目录与数据存储.md)。
