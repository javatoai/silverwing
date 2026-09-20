package com.snowball.silverwing.desktop

internal data class SettingsNavigationSection(
    val key: String,
    val label: String,
)

internal fun settingsNavigationSections(): List<SettingsNavigationSection> = listOf(
    SettingsNavigationSection("basic", "外观"),
    SettingsNavigationSection("paths", "目录"),
    SettingsNavigationSection("groups", "服务与仓库"),
    SettingsNavigationSection("tag", "Tag设置"),
    SettingsNavigationSection("tools", "开发工具"),
    SettingsNavigationSection("task-area", "任务区"),
    SettingsNavigationSection("agents", "协作说明"),
    SettingsNavigationSection("feishu", "Meegle"),
    SettingsNavigationSection("genbu", "Genbu"),
    SettingsNavigationSection("cli", "silverwing CLI"),
    SettingsNavigationSection("codex-plugins", "Codex 插件"),
    SettingsNavigationSection("skills", "Skills"),
    SettingsNavigationSection("git", "Git"),
    SettingsNavigationSection("logs", "诊断与日志"),
)
