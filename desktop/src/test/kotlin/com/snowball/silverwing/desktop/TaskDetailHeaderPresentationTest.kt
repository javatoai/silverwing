package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.TaskLifecycleStatus
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class TaskDetailHeaderPresentationTest {
    @Test
    fun `requirement copy icons remain configurable and retain the legacy fallback`() {
        val independentlyConfigured = taskAreaCopyIconPresentationFor(
            AppConfig(
                showTaskAreaBranchCopyIcons = false,
                showTaskAreaRequirementCopyIcons = true,
                showTaskAreaProjectNameCopyIcons = false,
            ),
        )

        assertTrue(independentlyConfigured.showRequirementCopyIcons)

        val legacyDisabled = taskAreaCopyIconPresentationFor(AppConfig(showTaskAreaCopyIcons = false))
        assertFalse(legacyDisabled.showRequirementCopyIcons)
        assertFalse(taskAreaCopyIconPresentationFor(AppConfig(showTaskAreaRequirementCopyIcons = false)).showRequirementCopyIcons)
    }

    @Test
    fun `lifecycle primary action switches between archive and restore`() {
        assertEquals(TaskLifecyclePrimaryAction.ARCHIVE, taskLifecyclePrimaryAction(TaskLifecycleStatus.ACTIVE))
        assertEquals(TaskLifecyclePrimaryAction.RESTORE, taskLifecyclePrimaryAction(TaskLifecycleStatus.ARCHIVED))
    }

    @Test
    fun `short participant list stays inline and long list collapses to count`() {
        assertEquals(
            ParticipantSummary("测试：张三、李四", "测试：张三、李四"),
            participantSummary("测试", listOf("张三", "李四")),
        )
        assertEquals(
            ParticipantSummary("产品 3 人", "产品：张三、李四、王五"),
            participantSummary("产品", listOf("张三", "李四", "王五")),
        )
    }

    @Test
    fun `participant summary removes blank and duplicate names`() {
        assertEquals(
            ParticipantSummary("测试：张三", "测试：张三"),
            participantSummary("测试", listOf("", " 张三 ", "张三")),
        )
        assertNull(participantSummary("测试", listOf("", "  ")))
    }
}
