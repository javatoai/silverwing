package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class SettingsExperienceTest {
    @Test
    fun `settings navigation is flat and omits the environment overview`() {
        val sections = settingsNavigationSections()

        assertEquals(
            listOf("外观", "目录", "服务与仓库", "Tag设置", "开发工具", "任务区", "协作说明", "Meegle CLI", "Genbu CLI", "silverwing CLI", "Codex 插件", "Skills", "Git", "诊断与日志"),
            sections.map { it.label },
        )
        assertFalse(sections.any { it.key == "overview" })
        assertFalse(sections.any { it.key == "branches" })
        assertEquals(
            listOf("basic", "groups", "feishu", "codex-plugins", "git"),
            sections.filter { it.startsGroup }.map { it.key },
        )
        assertEquals("basic", normalizeSettingsSection("branches", sections.map { it.key }.toSet()))
    }
}
