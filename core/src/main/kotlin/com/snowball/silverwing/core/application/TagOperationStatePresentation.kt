package com.snowball.silverwing.core

/** User-facing text for persisted Tag operation states. */
fun TagOperationState.userFacingLabel(): String = when (this) {
    TagOperationState.CREATED -> "已创建"
    TagOperationState.PREFLIGHT_PASSED -> "预检通过"
    TagOperationState.SOURCE_BRANCH_PUSHED,
    TagOperationState.FEATURE_PUSHED,
    -> "源分支已推送"
    TagOperationState.TARGET_BRANCH_PUSHED,
    TagOperationState.TEST_BRANCH_PUSHED,
    -> "目标分支已推送"
    TagOperationState.LOCAL_TAG_CREATED -> "本地Tag已创建"
    TagOperationState.TAG_PUSHED -> "Tag已推送"
    TagOperationState.SUCCESS -> "构建成功"
    TagOperationState.CONFLICT -> "存在冲突"
    TagOperationState.FAILED -> "构建失败"
    TagOperationState.PARTIAL -> "部分完成"
}
