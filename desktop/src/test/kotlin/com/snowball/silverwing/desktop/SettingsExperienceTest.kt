package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.ExternalSkillCatalogItem
import com.snowball.silverwing.core.ExternalSkillSourceSnapshot
import com.snowball.silverwing.core.CodexPluginMarketplaceSnapshot
import com.snowball.silverwing.core.CodexPluginMarketplaceSource
import com.snowball.silverwing.core.SkillSource
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class SettingsExperienceTest {
    @Test
    fun `settings navigation is flat and omits the environment overview`() {
        val sections = settingsNavigationSections()

        assertEquals(
            listOf("外观", "目录", "项目组", "Tag设置", "开发工具", "任务创建区", "任务详情区", "协作说明", "Meegle CLI", "Lark CLI", "Genbu CLI", "silverwing CLI", "Codex 插件", "Skills", "Git", "诊断与日志"),
            sections.map { it.label },
        )
        assertFalse(sections.any { it.key == "overview" })
        assertFalse(sections.any { it.key == "branches" })
        assertEquals(
            listOf("basic", "groups", "task-creation", "feishu", "codex-plugins", "git"),
            sections.filter { it.startsGroup }.map { it.key },
        )
        assertEquals("basic", normalizeSettingsSection("branches", sections.map { it.key }.toSet()))
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
