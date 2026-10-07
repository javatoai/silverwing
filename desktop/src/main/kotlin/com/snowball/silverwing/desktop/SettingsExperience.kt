package com.snowball.silverwing.desktop

internal data class SettingsNavigationPage(
    val key: String,
    val label: String,
)

/** A compact Settings category with one or more pages shown as contextual tabs. */
internal data class SettingsNavigationCategory(
    val key: String,
    val label: String,
    val pages: List<SettingsNavigationPage>,
)

internal fun SettingsNavigationCategory.showsTabs(): Boolean = pages.size > 1

internal fun toggledDefaultWorkspaceToolIds(current: List<String>, toolId: String, checked: Boolean): List<String> =
    if (checked) (current + toolId).distinct() else current.filterNot { it == toolId }

internal const val SILVERWING_OPEN_SOURCE_REPOSITORY_URL =
    "https://gitlab.snowballtech.com/common/silverwing"

internal fun settingsNavigationCategories(): List<SettingsNavigationCategory> = listOf(
    SettingsNavigationCategory("basic", "基础", listOf(SettingsNavigationPage("basic", "外观"))),
    SettingsNavigationCategory(
        "storage",
        "存储",
        listOf(
            SettingsNavigationPage("paths", "目录"),
            SettingsNavigationPage("config-backup", "配置与备份"),
        ),
    ),
    SettingsNavigationCategory(
        "development",
        "研发配置",
        listOf(
            SettingsNavigationPage("groups", "项目组"),
            SettingsNavigationPage("tools", "开发工具"),
        ),
    ),
    SettingsNavigationCategory(
        "tasks",
        "任务",
        listOf(
            SettingsNavigationPage("task-creation", "任务创建区"),
            SettingsNavigationPage("task-area", "任务详情区"),
        ),
    ),
    SettingsNavigationCategory("agents", "AGENTS.md", listOf(SettingsNavigationPage("agents", "AGENTS.md"))),
    SettingsNavigationCategory(
        "codex",
        "Codex",
        listOf(SettingsNavigationPage("codex-plugins", "Codex 插件"), SettingsNavigationPage("codex-mcp", "MCP 管理")),
    ),
    SettingsNavigationCategory(
        "commands",
        "CLI",
        listOf(
            SettingsNavigationPage("feishu", "Meegle CLI"),
            SettingsNavigationPage("lark", "Lark CLI"),
            SettingsNavigationPage("genbu", "Genbu CLI"),
            SettingsNavigationPage("cli", "silverwing CLI"),
        ),
    ),
    SettingsNavigationCategory("skill-sources", "Skill 安装", listOf(SettingsNavigationPage("skill-sources", "Skill 安装"))),
    SettingsNavigationCategory("proxy", "代理", listOf(SettingsNavigationPage("network-proxy", "网络代理"))),
    SettingsNavigationCategory(
        "git",
        "Git",
        listOf(
            SettingsNavigationPage("git", "Git 配置"),
            SettingsNavigationPage("branch-naming", "分支名设置"),
            SettingsNavigationPage("tag", "Tag 设置"),
        ),
    ),
    SettingsNavigationCategory(
        "system",
        "系统与诊断",
        listOf(
            SettingsNavigationPage("logs", "诊断与日志"),
            SettingsNavigationPage("about", "关于"),
        ),
    ),
)

/**
 * Settings are remembered by category only. Earlier versions persisted a leaf page key;
 * map it to its new owner so the first visit after the navigation redesign still feels natural.
 */
internal fun normalizeSettingsCategory(savedValue: String, supportedKeys: Set<String>): String {
    val category = when (savedValue) {
        "basic", "overview", "branches", "skills" -> "basic"
        "paths", "config-backup", "storage" -> "storage"
        "groups", "tools", "development" -> "development"
        "task-creation", "codex-task-creation", "task-area", "tasks" -> "tasks"
        "agents" -> "agents"
        "codex-plugins", "codex-mcp", "codex" -> "codex"
        "feishu", "lark", "genbu", "cli", "advanced", "commands" -> "commands"
        "skill-sources" -> "skill-sources"
        "network-proxy", "proxy" -> "proxy"
        "git", "branch-naming", "tag" -> "git"
        "logs", "about", "system" -> "system"
        else -> "basic"
    }
    return category.takeIf(supportedKeys::contains) ?: "basic"
}
