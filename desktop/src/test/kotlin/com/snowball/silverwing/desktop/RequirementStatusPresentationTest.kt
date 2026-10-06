@file:OptIn(androidx.compose.ui.ExperimentalComposeUiApi::class, androidx.compose.ui.InternalComposeUiApi::class)

package com.snowball.silverwing.desktop

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.ui.ImageComposeScene
import androidx.compose.ui.use
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.graphics.luminance
import com.snowball.silverwing.core.ThemePreference
import kotlin.test.*

class RequirementStatusPresentationTest {
    @Test fun `review planning development testing completion and paused states use their categories`() {
        val cases = mapOf(
            "待评审" to RequirementStatusCategory.REVIEW, "评审中" to RequirementStatusCategory.REVIEW, "评审" to RequirementStatusCategory.REVIEW,
            "待排期" to RequirementStatusCategory.PLANNING, "开发中" to RequirementStatusCategory.DEVELOPMENT, "研发" to RequirementStatusCategory.DEVELOPMENT,
            "待测试" to RequirementStatusCategory.TESTING, "验收中" to RequirementStatusCategory.TESTING,
            "已完成" to RequirementStatusCategory.DONE, "已发布" to RequirementStatusCategory.DONE,
            "DONE" to RequirementStatusCategory.DONE, "resolved" to RequirementStatusCategory.DONE,
            "已取消" to RequirementStatusCategory.PAUSED, "暂停" to RequirementStatusCategory.PAUSED,
            "自定义状态" to RequirementStatusCategory.UNKNOWN,
        )
        cases.forEach { (status, expected) -> assertEquals(expected, requirementStatusCategory(status), status) }
    }

    @Test fun `negative and pending stages cannot be mistaken for complete`() {
        listOf("未完成", "尚未完成", "未验收", "未发布", "未关闭", "not done", "not completed", "unresolved", "incomplete", "undone")
            .forEach { assertEquals(RequirementStatusCategory.UNKNOWN, requirementStatusCategory(it), it) }
        assertEquals(RequirementStatusCategory.TESTING, requirementStatusCategory("开发完成，待测试"))
        assertEquals(RequirementStatusCategory.DEVELOPMENT, requirementStatusCategory("开发中，尚未完成"))
        assertEquals(RequirementStatusCategory.PAUSED, requirementStatusCategory("已取消，开发完成"))
    }

    @Test fun `status text has readable contrast on both themes and selected task backgrounds`() {
        val statuses = listOf("待评审", "待排期", "开发中", "待测试", "已完成", "已取消", "未知状态", "读取失败")
        for (theme in listOf(ThemePreference.LIGHT, ThemePreference.DARK)) {
            val ratios = linkedMapOf<String, Float>()
            ImageComposeScene(300, 150) {
                SilverWingTheme(theme) {
                    val scheme = MaterialTheme.colorScheme
                    val backgrounds = listOf(scheme.surface, scheme.surfaceVariant,
                        scheme.primary.copy(alpha = 0.10f).compositeOver(scheme.surface))
                    statuses.forEach { status ->
                        val foreground = if (status == "读取失败") scheme.requirementFailureColor() else scheme.requirementStatusColor(status)
                        backgrounds.forEachIndexed { index, surface ->
                            val background = foreground.copy(alpha = 0.12f).compositeOver(surface)
                            val a = foreground.luminance(); val b = background.luminance()
                            ratios["$status/$index"] = (maxOf(a, b) + 0.05f) / (minOf(a, b) + 0.05f)
                        }
                    }
                    Text("需求状态")
                }
            }.use { it.render(System.nanoTime()).close() }
            assertEquals(statuses.size * 3, ratios.size)
            ratios.forEach { (status, ratio) -> assertTrue(ratio >= 4.5f, "$theme $status contrast=$ratio") }
        }
    }
}
