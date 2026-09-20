package com.snowball.silverwing.desktop

internal data class SettingsNavigationSection(
    val key: String,
    val label: String,
    /** 是否为一个新的设置功能组的第一个入口。 */
    val startsGroup: Boolean = false,
)

internal fun settingsNavigationSections(): List<SettingsNavigationSection> = listOf(
    SettingsNavigationSection("basic", "外观", startsGroup = true),
    SettingsNavigationSection("paths", "目录"),
    SettingsNavigationSection("groups", "服务与仓库", startsGroup = true),
    SettingsNavigationSection("tag", "Tag设置"),
    SettingsNavigationSection("tools", "开发工具"),
    SettingsNavigationSection("task-area", "任务区"),
    SettingsNavigationSection("agents", "协作说明"),
    SettingsNavigationSection("feishu", "Meegle CLI", startsGroup = true),
    SettingsNavigationSection("genbu", "Genbu CLI"),
    SettingsNavigationSection("cli", "silverwing CLI"),
    SettingsNavigationSection("codex-plugins", "Codex 插件", startsGroup = true),
    SettingsNavigationSection("skills", "Skills"),
    SettingsNavigationSection("git", "Git", startsGroup = true),
    SettingsNavigationSection("logs", "诊断与日志"),
)
