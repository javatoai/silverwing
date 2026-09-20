package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.BranchReuseConflict
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class CreateTaskBranchReuseInspectionTest {
    @Test
    fun `active preflight advances to creation only after inspection succeeds`() = runTest {
        val response = CompletableDeferred<List<BranchReuseConflict>>()
        val callbacks = mutableListOf<String>()
        val inspection = CreateTaskBranchReuseInspection(this, StandardTestDispatcher(testScheduler)) {
            callbacks += "error"
        }

        inspection.start(
            inspect = { response.await() },
            onResolved = { conflicts -> if (conflicts.isEmpty()) callbacks += "create" },
            onFinished = { callbacks += "finished" },
        )
        testScheduler.runCurrent()
        assertTrue(callbacks.isEmpty())

        response.complete(emptyList())
        testScheduler.advanceUntilIdle()

        assertEquals(listOf("create", "finished"), callbacks)
    }

    @Test
    fun `abandoning create prevents a late successful preflight from creating a task`() = runTest {
        val response = CompletableDeferred<List<BranchReuseConflict>>()
        val callbacks = mutableListOf<String>()
        var inspectionStarted = false
        val inspection = CreateTaskBranchReuseInspection(this, StandardTestDispatcher(testScheduler)) {
            callbacks += "error"
        }

        inspection.start(
            inspect = {
                inspectionStarted = true
                // Model a subprocess/read which finishes even after the dialog closes.
                withContext(NonCancellable) { response.await() }
            },
            onResolved = { callbacks += "create" },
            onFinished = { callbacks += "finished" },
        )
        testScheduler.runCurrent()
        assertTrue(inspectionStarted)

        // Both the cancel button and the dialog's onDispose use this entry point.
        inspection.cancel()
        inspection.cancel()
        response.complete(emptyList())
        testScheduler.advanceUntilIdle()

        assertTrue(callbacks.isEmpty(), "An abandoned preflight must not create, finish, or report an error")
    }

    @Test
    fun `cancel before scheduled inspection starts performs no work`() = runTest {
        var inspected = false
        var created = false
        val inspection = CreateTaskBranchReuseInspection(this, StandardTestDispatcher(testScheduler)) {
            throw it
        }

        inspection.start(
            inspect = { inspected = true; emptyList() },
            onResolved = { created = true },
            onFinished = {},
        )
        inspection.cancel()
        testScheduler.advanceUntilIdle()

        assertFalse(inspected)
        assertFalse(created)
    }

    @Test
    fun `late failure from a replaced request cannot finish or fail its replacement`() = runTest {
        val oldResponse = CompletableDeferred<Unit>()
        val newResponse = CompletableDeferred<List<BranchReuseConflict>>()
        val callbacks = mutableListOf<String>()
        val inspection = CreateTaskBranchReuseInspection(this, StandardTestDispatcher(testScheduler)) {
            callbacks += "error:${it.message}"
        }

        inspection.start(
            inspect = {
                withContext(NonCancellable) {
                    oldResponse.await()
                    error("old request failed")
                }
            },
            onResolved = { callbacks += "old create" },
            onFinished = { callbacks += "old finished" },
        )
        testScheduler.runCurrent()

        inspection.start(
            inspect = { newResponse.await() },
            onResolved = { callbacks += "new create" },
            onFinished = { callbacks += "new finished" },
        )
        testScheduler.runCurrent()
        oldResponse.complete(Unit)
        testScheduler.runCurrent()
        assertTrue(callbacks.isEmpty(), "The replacement remains pending when the older request finishes")

        newResponse.complete(emptyList())
        testScheduler.advanceUntilIdle()

        assertEquals(listOf("new create", "new finished"), callbacks)
    }
}
