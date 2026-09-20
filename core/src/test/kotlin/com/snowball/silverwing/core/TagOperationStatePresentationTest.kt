package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals

class TagOperationStatePresentationTest {
    @Test
    fun `every persisted Tag operation state has a Chinese user facing label`() {
        assertEquals(
            mapOf(
                TagOperationState.CREATED to "已创建",
                TagOperationState.PREFLIGHT_PASSED to "预检通过",
                TagOperationState.SOURCE_BRANCH_PUSHED to "源分支已推送",
                TagOperationState.TARGET_BRANCH_PUSHED to "目标分支已推送",
                TagOperationState.LOCAL_TAG_CREATED to "本地Tag已创建",
                TagOperationState.TAG_PUSHED to "Tag已推送",
                TagOperationState.SUCCESS to "构建成功",
                TagOperationState.CONFLICT to "存在冲突",
                TagOperationState.FAILED to "构建失败",
                TagOperationState.PARTIAL to "部分完成",
                TagOperationState.FEATURE_PUSHED to "源分支已推送",
                TagOperationState.TEST_BRANCH_PUSHED to "目标分支已推送",
            ),
            TagOperationState.entries.associateWith(TagOperationState::userFacingLabel),
        )
    }
}
