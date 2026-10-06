package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.ExternalSkillSourceSnapshot
import com.snowball.silverwing.core.CodexPluginMarketplaceSnapshot
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.SkillSource
import com.snowball.silverwing.core.CommandProxyTarget
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SettingsExperienceTest {
    @Test
    fun `settings navigation groups pages and only shows tabs for multi page categories`() {
        val categories = settingsNavigationCategories()
        val byKey = categories.associateBy(SettingsNavigationCategory::key)

        assertEquals(
            listOf("基础", "存储", "研发配置", "研发任务", "Codex", "CLI", "Skill 安装", "代理", "Git", "系统与诊断"),
            categories.map(SettingsNavigationCategory::label),
        )
        assertEquals(
            listOf("项目组", "开发工具"),
            byKey.getValue("development").pages.map(SettingsNavigationPage::label),
        )
        assertEquals(
            listOf("Git 配置", "分支名设置", "Tag 设置"),
            byKey.getValue("git").pages.map(SettingsNavigationPage::label),
        )
        assertEquals(listOf("git", "branch-naming", "tag"), byKey.getValue("git").pages.map(SettingsNavigationPage::key))
        assertEquals(
            listOf("Meegle CLI", "Lark CLI", "Genbu CLI", "silverwing CLI"),
            byKey.getValue("commands").pages.map(SettingsNavigationPage::label),
        )
        assertEquals(
            listOf("任务创建区", "任务详情区", "任务协作说明"),
            byKey.getValue("tasks").pages.map(SettingsNavigationPage::label),
        )
        assertEquals(listOf("Codex 插件", "MCP 管理"), byKey.getValue("codex").pages.map(SettingsNavigationPage::label))
        assertEquals("Skill 安装", byKey.getValue("skill-sources").pages.single().label)
        assertEquals("task-creation", byKey.getValue("tasks").pages.first().key)
        assertFalse(categories.flatMap { it.pages }.any { it.key == "codex-task-creation" })
        assertFalse(byKey.getValue("basic").showsTabs())
        assertTrue(byKey.getValue("codex").showsTabs())
        assertFalse(byKey.getValue("skill-sources").showsTabs())
        assertFalse(byKey.getValue("proxy").showsTabs())
        assertTrue(byKey.getValue("git").showsTabs())
        assertTrue(byKey.getValue("storage").showsTabs())
        assertTrue(byKey.getValue("development").showsTabs())
        assertTrue(byKey.getValue("tasks").showsTabs())
        assertTrue(byKey.getValue("commands").showsTabs())
        assertTrue(byKey.getValue("system").showsTabs())
        assertEquals("tasks", normalizeSettingsCategory("task-creation", byKey.keys))
        assertEquals("tasks", normalizeSettingsCategory("codex-task-creation", byKey.keys))
        assertEquals("codex", normalizeSettingsCategory("codex-plugins", byKey.keys))
        assertEquals("commands", normalizeSettingsCategory("lark", byKey.keys))
        assertEquals("git", normalizeSettingsCategory("branch-naming", byKey.keys))
        assertEquals("git", normalizeSettingsCategory("tag", byKey.keys))
        assertEquals("git", normalizeSettingsCategory("git", byKey.keys))
        assertEquals("development", normalizeSettingsCategory("development", byKey.keys))
        assertEquals("system", normalizeSettingsCategory("logs", byKey.keys))
        assertEquals("https://gitlab.snowballtech.com/common/silverwing", SILVERWING_OPEN_SOURCE_REPOSITORY_URL)
    }

    @Test
    fun `task creation default tools allow Codex and Cursor together without discarding other tools`() {
        val withCodex = toggledDefaultWorkspaceToolIds(listOf("legacy-tool"), "codex", true)
        val withBoth = toggledDefaultWorkspaceToolIds(withCodex, "cursor", true)

        assertEquals(listOf("legacy-tool", "codex", "cursor"), withBoth)
        assertEquals(withBoth, toggledDefaultWorkspaceToolIds(withBoth, "cursor", true))
        assertEquals(listOf("legacy-tool", "cursor"), toggledDefaultWorkspaceToolIds(withBoth, "codex", false))
    }

    @Test
    fun `network proxy presents every independently selectable command category`() {
        val options = commandProxyTargetPresentations()

        assertEquals(CommandProxyTarget.entries.toList(), options.map(CommandProxyTargetPresentation::target))
        assertEquals("Codex CLI", options.first().title)
        assertEquals("Genbu CLI", options.last().title)
        assertTrue(options.all { it.description.isNotBlank() })
    }

    @Test
    fun `Skill source presentation uses title count and actual discovered roots`() {
        val source = SkillSource("team", "团队 Skills", "https://example.test/team/skills.git")
        val legacySnapshot = ExternalSkillSourceSnapshot(
            sourceId = source.id,
            skills = listOf(ExternalSkillCatalogItem("deploy", "Deploy", "skills/deploy")),
            updatedAt = "2026-09-21 14:13:27",
        )

        assertEquals("26 个 Skill", extensionSourceCountLabel(26, "Skill"))
        assertEquals("1 个插件", extensionSourceCountLabel(1, "插件"))
        assertEquals(listOf("skills"), effectiveSkillSourceRoots(source, legacySnapshot))
        assertEquals("发现目录：skills", skillSourceDirectoryStatus(source, legacySnapshot))

        val multiRoot = legacySnapshot.copy(discoveredRoots = listOf(".agents/skills", "skills"))
        assertEquals("发现目录：.agents/skills、skills", skillSourceDirectoryStatus(source, multiRoot))
        assertEquals("发现目录：skills", skillSourceDirectoryStatus(source, legacySnapshot.copy(error = "refresh failed")))
    }

    @Test
    fun `Skill source presentation distinguishes unloaded and empty discovery`() {
        val source = SkillSource("team", "团队 Skills", "https://example.test/team/skills.git")

        assertEquals("尚未发现目录", skillSourceDirectoryStatus(source, null))
        assertEquals(
            "未发现有效 Skill 目录",
            skillSourceDirectoryStatus(source, ExternalSkillSourceSnapshot(source.id, updatedAt = "2026-09-21 14:13:27")),
        )
    }

    @Test
    fun `extension source metadata keeps configuration and runtime details on separate lines`() {
        val pluginSource = CodexPluginMarketplaceSource(
            id = "shared-toolkit",
            name = "ai-shared-toolkit",
            repositoryUrl = "https://gitlab.example.test/fp/ai-shared-toolkit.git",
            ref = "master",
            marketplaceDirectory = ".",
        )
        val pluginStatus = CodexPluginMarketplaceSnapshot(
            sourceId = pluginSource.id,
            marketplaceName = "ai-shared-toolkit",
            updatedAt = "2026-09-21 14:18:41",
        )
        val skillSource = SkillSource(
            id = "team-skills",
            name = "Team Skills",
            repositoryUrl = "https://gitlab.example.test/fp/skills.git",
            ref = "main",
        )
        val skillStatus = ExternalSkillSourceSnapshot(
            sourceId = skillSource.id,
            discoveredRoots = listOf("skills"),
            updatedAt = "2026-09-21 14:18:41",
        )

        assertEquals(
            listOf("分支：master · Marketplace：仓库根目录", "最近成功：2026-09-21 14:18:41"),
            pluginSourceMetadataLines(pluginSource, pluginStatus),
        )
        assertEquals(
            listOf("分支：main · 发现目录：skills", "最近成功：2026-09-21 14:18:41"),
            skillSourceMetadataLines(skillSource, skillStatus),
        )
    }
}
