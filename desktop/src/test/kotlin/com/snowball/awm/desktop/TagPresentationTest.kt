package com.snowball.awm.desktop

import com.snowball.awm.core.GenbuStageStatus
import com.snowball.awm.core.TagOperation
import com.snowball.awm.core.TagOperationState
import com.snowball.awm.core.GenbuTagProbeStatus
import com.snowball.awm.core.TagHistoryItem
import com.snowball.awm.core.TagWorkspaceCheck
import com.snowball.awm.core.RequirementMetadata
import com.snowball.awm.core.RequirementParticipants
import com.snowball.awm.core.RequirementPerson
import com.snowball.awm.core.userFacingLabel
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TagPresentationTest {
    @Test
    fun `tag history filter matches service task tag branch and message`() {
        val operation = operation(TagOperationState.FAILED)

        assertTrue(tagHistoryMatchesQuery(operation, "operation-center"))
        assertTrue(tagHistoryMatchesQuery(operation, "task-42"))
        assertTrue(tagHistoryMatchesQuery(operation, "v1.2.3"))
        assertTrue(tagHistoryMatchesQuery(operation, "release/test"))
        assertTrue(tagHistoryMatchesQuery(operation, "冲突"))
        assertFalse(tagHistoryMatchesQuery(operation, "unmatched"))
    }

    @Test
    fun `only failed partial and conflict operations are problem records`() {
        assertTrue(tagOperationIsProblem(operation(TagOperationState.FAILED)))
        assertTrue(tagOperationIsProblem(operation(TagOperationState.PARTIAL)))
        assertTrue(tagOperationIsProblem(operation(TagOperationState.CONFLICT)))
        assertTrue(tagOperationIsProblem(operation(TagOperationState.SOURCE_BRANCH_PUSHED)))
        assertFalse(tagOperationIsProblem(operation(TagOperationState.SUCCESS)))
        assertTrue(
            tagOperationIsProblem(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
                ),
            ),
        )
    }

    @Test
    fun `failed partial and Genbu failed builds expose their retry actions`() {
        assertTrue(tagOperationCanRetryFailed(operation(TagOperationState.FAILED)))
        assertFalse(tagOperationCanRetryFailed(operation(TagOperationState.SUCCESS)))
        assertTrue(tagOperationCanResumePartial(operation(TagOperationState.PARTIAL)))
        assertFalse(tagOperationCanResumePartial(operation(TagOperationState.FAILED)))
        val genbuFailed = operation(TagOperationState.SUCCESS).copy(
            genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
        )
        assertTrue(tagOperationCanRetag(genbuFailed))
        assertFalse(tagOperationCanRetag(genbuFailed.copy(
            genbuStatus = genbuFailed.genbuStatus.copy(failureReason = "live query unavailable"),
        )))
        assertFalse(tagOperationCanRetag(operation(TagOperationState.SUCCESS)))
        assertFalse(
            tagOperationCanRetag(
                operation(TagOperationState.FAILED).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED),
                ),
            ),
        )
    }

    @Test
    fun `source branch pushed operation is retryable after interruption`() {
        assertTrue(tagOperationIsRetryableInterrupted(operation(TagOperationState.SOURCE_BRANCH_PUSHED)))
        assertFalse(tagOperationIsRetryableInterrupted(operation(TagOperationState.TARGET_BRANCH_PUSHED)))
    }

    @Test
    fun `active intermediate Tag state is progress rather than an interruption`() {
        val sourcePushed = operation(TagOperationState.SOURCE_BRANCH_PUSHED)

        assertFalse(tagOperationIsProblem(sourcePushed, active = true))
        assertFalse(tagOperationIsRetryableInterrupted(sourcePushed, active = true))
        assertEquals(
            "源分支已推送，正在合并目标分支并创建测试Tag。",
            tagOperationInProgressMessage(sourcePushed),
        )
    }

    @Test
    fun `conflict summary names target and source branches`() {
        val conflict = operation(TagOperationState.CONFLICT).copy(
            sourceBranch = "feature/task-42",
            targetBranch = "uat",
            remote = "upstream",
        )

        assertEquals(
            "我在将服务 operation-center 的 feature/task-42 分支合并到 upstream/uat 时遇到了冲突，请你解决。",
            tagConflictSummary(conflict),
        )
    }

    @Test
    fun `conflict presentation lists files and handles missing file details`() {
        assertEquals(
            "冲突文件：src/A.kt、src/B.kt",
            tagConflictFilesSummary(operation(TagOperationState.CONFLICT).copy(conflictFiles = listOf("src/A.kt", "src/B.kt"))),
        )
        assertEquals(
            "冲突文件：未返回具体文件",
            tagConflictFilesSummary(operation(TagOperationState.CONFLICT)),
        )
    }

    @Test
    fun `conflict copy text contains only the event and files`() {
        val conflict = operation(TagOperationState.CONFLICT).copy(
            sourceBranch = "feature/task-42",
            targetBranch = "uat",
            remote = "upstream",
            conflictFiles = listOf("src/A.kt", "src/B.kt"),
            message = "旧的操作引导不应进入冲突信息",
        )

        assertEquals(
            "我在将服务 operation-center 的 feature/task-42 分支合并到 upstream/uat 时遇到了冲突，请你解决。\n冲突文件：src/A.kt、src/B.kt",
            tagConflictCopyText(conflict),
        )
    }

    @Test
    fun `workspace inspection is available for conflicts and dirty-worktree failures`() {
        assertTrue(tagOperationCanInspectWorkspace(operation(TagOperationState.CONFLICT)))
        assertTrue(
            tagOperationCanInspectWorkspace(
                operation(TagOperationState.FAILED).copy(message = "特性工作区存在未提交改动，请先提交或清理"),
            ),
        )
        assertFalse(tagOperationCanInspectWorkspace(operation(TagOperationState.FAILED).copy(message = "远端分支领先")))
        assertFalse(tagOperationCanInspectWorkspace(operation(TagOperationState.SUCCESS)))
    }

    @Test
    fun `workspace inspection summary exposes concrete changed files`() {
        assertEquals(
            "仍有未提交改动：未暂存：src/A.kt；未跟踪：.idea/workspace.xml",
            tagWorkspaceCheckSummary(TagWorkspaceCheck(listOf("未暂存：src/A.kt", "未跟踪：.idea/workspace.xml"))),
        )
        assertEquals("工作区已干净，可以重新构建测试Tag", tagWorkspaceCheckSummary(TagWorkspaceCheck(emptyList())))
    }

    @Test
    fun `Genbu labels classify build uat and production stages`() {
        assertEquals(
            listOf("构建中", "UAT未发布"),
            genbuTagStatusLabels(operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus()), probeEnabled = true),
        )
        assertEquals(
            emptyList(),
            genbuTagStatusLabels(operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus()), probeEnabled = false),
        )
        assertEquals(
            listOf("构建状态未知", "UAT未发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus(checkedAt = "2026-08-26 11:00:00")),
                probeEnabled = true,
            ),
        )
        assertEquals(
            listOf("构建中", "UAT未发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.BUILDING, checkedAt = "2026-08-26 11:00:00"),
                ),
            ),
        )
        assertEquals(
            listOf("已构建", "UAT未发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.SUCCESS)),
            ),
        )
        assertEquals(
            listOf("构建失败", "UAT未发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.FAILED)),
            ),
        )
        assertEquals(
            listOf("已构建", "UAT已发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.SUCCESS, uat = GenbuStageStatus.SUCCESS),
                ),
            ),
        )
        assertEquals(
            listOf("已构建", "UAT发布失败"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(build = GenbuStageStatus.SUCCESS, uat = GenbuStageStatus.FAILED),
                ),
            ),
        )
        assertEquals(
            listOf("已构建", "UAT已发布", "已生产发布"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(
                        build = GenbuStageStatus.SUCCESS,
                        uat = GenbuStageStatus.SUCCESS,
                        production = GenbuStageStatus.SUCCESS,
                    ),
                ),
            ),
        )
        assertEquals(
            listOf("已构建", "UAT已发布", "生产发布失败"),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(
                    genbuStatus = GenbuTagProbeStatus(
                        build = GenbuStageStatus.SUCCESS,
                        uat = GenbuStageStatus.SUCCESS,
                        production = GenbuStageStatus.FAILED,
                    ),
                ),
            ),
        )
        assertEquals(
            listOf("未在Genbu中找到"),
            genbuTagStatusLabels(operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus(notFound = true))),
        )
        assertEquals(
            emptyList(),
            genbuTagStatusLabels(
                operation(TagOperationState.SUCCESS).copy(genbuStatus = GenbuTagProbeStatus(failureReason = "网络超时")),
                probeEnabled = true,
            ),
        )
    }

    @Test
    fun `group card stays visible when one child matches and only exposes matching child`() {
        val matching = operation(TagOperationState.SUCCESS).copy(
            operationId = "batch-match",
            serviceName = "payment-center",
            batchId = "batch-1",
        )
        val hidden = operation(TagOperationState.FAILED).copy(
            operationId = "batch-hidden",
            serviceName = "gateway",
            batchId = "batch-1",
        )
        val items = listOf(
            TagHistoryItem("batch-1", "batch-1", "task-42", matching.createdAt, hidden.updatedAt, listOf(hidden, matching)),
            TagHistoryItem("single", null, "task-42", matching.createdAt, matching.updatedAt, listOf(operation(TagOperationState.SUCCESS).copy(operationId = "single"))),
        )

        val filtered = filterTagHistoryItems(items, "payment", onlyProblems = false)

        assertEquals(1, filtered.size)
        assertEquals("batch-1", filtered.single().item.batchId)
        assertEquals(listOf("batch-match"), filtered.single().visibleOperations.map(TagOperation::operationId))
    }

    @Test
    fun `batch summary uses the most serious child state`() {
        assertEquals(
            TagOperationState.CONFLICT,
            groupTagState(listOf(operation(TagOperationState.SUCCESS), operation(TagOperationState.FAILED), operation(TagOperationState.CONFLICT))),
        )
        assertEquals(TagOperationState.SUCCESS, groupTagState(listOf(operation(TagOperationState.SUCCESS))))
    }

    @Test
    fun `problem filter retains a batch and only exposes its problem children`() {
        val success = operation(TagOperationState.SUCCESS).copy(operationId = "batch-success", batchId = "batch-problems")
        val failure = operation(TagOperationState.FAILED).copy(operationId = "batch-failure", batchId = "batch-problems")

        val filtered = filterTagHistoryItems(
            listOf(TagHistoryItem("batch-problems", "batch-problems", "task-42", success.createdAt, failure.updatedAt, listOf(success, failure))),
            query = "",
            onlyProblems = true,
        )

        assertEquals(listOf("batch-failure"), filtered.single().visibleOperations.map(TagOperation::operationId))
    }

    @Test
    fun `unfiltered batch exposes successful and failed records for selection`() {
        val success = operation(TagOperationState.SUCCESS).copy(operationId = "batch-success", batchId = "batch-all")
        val failure = operation(TagOperationState.FAILED).copy(operationId = "batch-failure", batchId = "batch-all")

        val filtered = filterTagHistoryItems(
            listOf(TagHistoryItem("batch-all", "batch-all", "task-42", success.createdAt, failure.updatedAt, listOf(success, failure))),
            query = "",
            onlyProblems = false,
        )

        assertEquals(setOf("batch-success", "batch-failure"), visibleTagOperationIds(filtered))
    }

    @Test
    fun `batch announcement follows the original creation order`() {
        val later = operation(TagOperationState.SUCCESS).copy(operationId = "later", createdAt = "2026-08-20 10:02:00")
        val first = operation(TagOperationState.SUCCESS).copy(operationId = "first", createdAt = "2026-08-20 10:00:00")
        val batch = TagHistoryItem("batch-order", "batch-order", "task-42", first.createdAt, later.updatedAt, listOf(later, first))

        assertEquals(listOf("first", "later"), groupAnnouncementOperations(batch).map(TagOperation::operationId))
    }

    @Test
    fun `batch announcement copy message includes distinct QC owners`() {
        val metadata = RequirementMetadata(
            status = "测试中",
            participants = RequirementParticipants(
                qcOwners = listOf(
                    RequirementPerson("张三"),
                    RequirementPerson("李四"),
                    RequirementPerson("张三"),
                    RequirementPerson(" "),
                ),
            ),
        )

        assertEquals("测试Tag发版信息已复制，发给张三、李四", tagAnnouncementCopyMessage(metadata))
        assertEquals("测试Tag发版信息已复制", tagAnnouncementCopyMessage(null))
    }

    @Test
    fun `copying a tag operation record uses its Chinese state label`() {
        val copy = tagOperationRecordCopyText(
            operation(TagOperationState.LOCAL_TAG_CREATED).copy(tag = null),
        )

        assertEquals("服务名：operation-center\n状态：本地Tag已创建\n说明：合并存在冲突", copy)
        assertEquals("本地Tag已创建", TagOperationState.LOCAL_TAG_CREATED.userFacingLabel())
    }

    private fun operation(state: TagOperationState) = TagOperation(
        operationId = "operation-42-$state",
        folderName = "task-42",
        serviceName = "operation-center",
        repositoryId = "operation-center",
        sourceBranch = "feature/task-42",
        targetBranch = "release/test",
        remote = "origin",
        state = state,
        createdAt = "2026-08-20 10:00:00",
        updatedAt = "2026-08-20 10:00:00",
        tag = "v1.2.3",
        message = "合并存在冲突",
    )
}
