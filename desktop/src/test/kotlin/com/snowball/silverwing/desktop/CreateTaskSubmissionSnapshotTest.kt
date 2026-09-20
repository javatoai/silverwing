package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.BranchReuseConflict
import com.snowball.silverwing.core.BranchReuseKey
import com.snowball.silverwing.core.CreateGroupedTaskRequest
import com.snowball.silverwing.core.TaskModuleSelection
import com.snowball.silverwing.core.TaskServiceSelection
import com.snowball.silverwing.core.WorkspaceStrategy
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

@OptIn(ExperimentalCoroutinesApi::class)
class CreateTaskSubmissionSnapshotTest {
    @Test
    fun `late successful preflight creates the checked draft after the visible draft changes`() = runTest {
        val original = request("checked task", "group-a")
        var visibleDraft = original
        var visibleTools = listOf("checked-tool")
        val snapshot = CreateTaskSubmissionSnapshot(visibleDraft, visibleTools)
        val response = CompletableDeferred<List<BranchReuseConflict>>()
        var inspected: CreateGroupedTaskRequest? = null
        var created: Pair<CreateGroupedTaskRequest, List<String>>? = null
        val inspection = CreateTaskBranchReuseInspection(this, StandardTestDispatcher(testScheduler)) { throw it }
        inspection.start(
            inspect = { inspected = snapshot.request; response.await() },
            onResolved = { snapshot.submit(emptySet(), captureCreation { request, tools -> created = request to tools }) },
            onFinished = {},
        )
        testScheduler.runCurrent()

        visibleDraft = request("edited task", "group-b")
        visibleTools = listOf("edited-tool")
        response.complete(emptyList())
        testScheduler.advanceUntilIdle()

        assertEquals("edited task", visibleDraft.folderName)
        assertEquals(listOf("edited-tool"), visibleTools)
        assertEquals(inspected, assertNotNull(created).first)
        assertEquals(original, created?.first)
        assertEquals(listOf("checked-tool"), created?.second)
    }

    @Test
    fun `reuse confirmation submits the same copied selections and tools that were inspected`() {
        val modules = request("checked task", "group-a").serviceSelections.single().modules.toMutableList()
        val services = mutableListOf(TaskServiceSelection("service-a", modules))
        val serviceIds = mutableListOf("service-a")
        val tools = mutableListOf("checked-tool")
        val snapshot = CreateTaskSubmissionSnapshot(
            request("checked task", "group-a").copy(serviceIds = serviceIds, serviceSelections = services),
            tools,
        )
        val inspected = snapshot.request
        var created: Pair<CreateGroupedTaskRequest, List<String>>? = null
        // This callback is retained while the branch-reuse confirmation dialog is open.
        val confirm: (Set<BranchReuseKey>) -> Unit = { keys ->
            snapshot.submit(keys, captureCreation { request, selectedTools -> created = request to selectedTools })
        }

        modules[0] = modules[0].copy(targetBranch = "feature/edited")
        services.clear()
        serviceIds[0] = "service-b"
        tools[0] = "edited-tool"
        val keys = setOf(BranchReuseKey("repo-a", "feature/checked", "checked-fingerprint"))
        confirm(keys)

        assertEquals(inspected.copy(confirmedBranchReuseKeys = keys), assertNotNull(created).first)
        assertEquals("feature/checked", created?.first?.serviceSelections?.single()?.modules?.single()?.targetBranch)
        assertEquals(listOf("checked-tool"), created?.second)
    }

    private fun request(name: String, groupId: String) = CreateGroupedTaskRequest(
        folderName = name,
        featureBranch = "feature/checked",
        groupId = groupId,
        serviceIds = listOf("service-a"),
        requirementLink = "https://example.invalid/checked",
        taskNotes = "checked notes",
        serviceSelections = listOf(TaskServiceSelection("service-a", listOf(TaskModuleSelection(
            id = "module-a",
            name = "checked module",
            strategy = WorkspaceStrategy.STANDARD_WORKTREE,
            baseRef = "origin/master",
            targetBranch = "feature/checked",
        )))),
    )

    private fun captureCreation(capture: (CreateGroupedTaskRequest, List<String>) -> Unit): CreateTaskAction =
        { name, branch, group, services, link, notes, tools, keys, selections ->
            capture(CreateGroupedTaskRequest(name, branch, group, services, link, notes, keys, serviceSelections = selections), tools)
        }
}
