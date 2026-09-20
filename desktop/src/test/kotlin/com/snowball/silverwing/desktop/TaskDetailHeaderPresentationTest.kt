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
    fun `inline copy icon groups are independently configurable and retain the legacy fallback`() {
        val independentlyConfigured = taskAreaCopyIconPresentationFor(
            AppConfig(
                showTaskAreaBranchCopyIcons = false,
                showTaskAreaRequirementCopyIcons = true,
                showTaskAreaProjectNameCopyIcons = false,
            ),
        )

        assertFalse(independentlyConfigured.showBranchNameCopyIcons)
        assertTrue(independentlyConfigured.showRequirementCopyIcons)
        assertFalse(independentlyConfigured.showProjectNameCopyIcons)

        val legacyDisabled = taskAreaCopyIconPresentationFor(AppConfig(showTaskAreaCopyIcons = false))
        assertFalse(legacyDisabled.showBranchNameCopyIcons)
        assertFalse(legacyDisabled.showRequirementCopyIcons)
        assertFalse(legacyDisabled.showProjectNameCopyIcons)
    }

    @Test
    fun `header keeps metadata and lifecycle actions side by side at 720dp and above`() {
        assertEquals(TaskDetailHeaderLayout.SIDE_BY_SIDE, taskDetailHeaderLayout(720f))
        assertEquals(TaskDetailHeaderLayout.SIDE_BY_SIDE, taskDetailHeaderLayout(1_280f))
    }

    @Test
    fun `header stacks lifecycle actions below metadata below 720dp`() {
        assertEquals(TaskDetailHeaderLayout.STACKED, taskDetailHeaderLayout(719.9f))
        assertEquals(TaskDetailHeaderLayout.STACKED, taskDetailHeaderLayout(0f))
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
