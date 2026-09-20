package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Test

class TagOutputFormatterTest {
    @Test
    fun `formats successful tags with the publish hint right after the link`() {
        val first = operation("android-transit-service", "3.11.69.beta-1")
        val second = operation("api-service", "2.4.34.beta-6")

        assertEquals(
            """需求链接：https://project.feishu.cn/obt/userstory/detail/7035269559
测试Tag已构建完毕，辛苦发版：

android-transit-service · 3.11.69.beta-1
api-service · 2.4.34.beta-6""",
            TagOutputFormatter.format(
                "https://project.feishu.cn/obt/userstory/detail/7035269559",
                listOf(first, second),
                includeFailures = false,
            ),
        )
    }

    @Test
    fun `successful output without a link starts with the publish hint`() {
        assertEquals(
            """测试Tag已构建完毕，辛苦发版：

api-service · 2.4.34.beta-6""",
            TagOutputFormatter.format("", listOf(operation("api-service", "2.4.34.beta-6")), includeFailures = false),
        )
    }

    @Test
    fun `omits blank requirement link and includes failures in detailed output`() {
        val success = operation("api-service", "2.4.34.beta-6")
        val failure = operation("job-manager", null).copy(
            state = TagOperationState.FAILED,
            message = "远端推送失败",
        )

        assertEquals(
            """api-service · 2.4.34.beta-6

构建失败：
job-manager · 构建失败 · 远端推送失败

测试Tag未全部构建成功，请处理失败项后重试""",
            TagOutputFormatter.format("", listOf(success, failure), includeFailures = true),
        )
    }

    @Test
    fun `mixed batch output keeps the requirement link and never claims all tags are built`() {
        val success = operation("api-service", "2.4.34.beta-6")
        val failure = operation("job-manager", null).copy(
            state = TagOperationState.CONFLICT,
            message = "存在未解决冲突",
        )

        assertEquals(
            """需求链接：https://project.feishu.cn/obt/userstory/detail/7060612727

api-service · 2.4.34.beta-6

构建失败：
job-manager · 存在冲突 · 存在未解决冲突

测试Tag未全部构建成功，请处理失败项后重试""",
            TagOutputFormatter.format(
                "https://project.feishu.cn/obt/userstory/detail/7060612727",
                listOf(success, failure),
                includeFailures = true,
            ),
        )
    }

    private fun operation(serviceName: String, tag: String?): TagOperation = TagOperation(
        operationId = serviceName,
        folderName = "TASK-1",
        serviceName = serviceName,
        repositoryId = serviceName,
        sourceBranch = "feature/TASK-1",
        targetBranch = "release/test",
        remote = "origin",
        state = TagOperationState.SUCCESS,
        createdAt = "2026-01-01T00:00:00Z",
        updatedAt = "2026-01-01T00:00:00Z",
        tag = tag,
    )
}
