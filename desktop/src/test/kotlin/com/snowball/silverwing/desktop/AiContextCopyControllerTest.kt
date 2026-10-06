package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.TaskManifest
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertSame

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class AiContextCopyControllerTest {
    @Test fun `changing task root or file selection rejects late results from cancelled loaders`() = runTest {
        val gates = mutableMapOf<AiContextCopyRequest, CompletableDeferred<AiContextCopyDocument>>()
        val reader = AiContextCopyController(this) { request ->
            withContext(NonCancellable) { gates.getOrPut(request) { CompletableDeferred() }.await() }
        }
        val first = request("first")
        val next = request("next")
        reader.load(first); runCurrent()
        reader.load(next); runCurrent()
        gates[first]!!.complete(document("stale")); runCurrent()
        assertEquals(next, assertIs<AiContextCopyLoadState.Loading>(reader.state).request)
        gates[next]!!.complete(document("current")); runCurrent()
        assertEquals("current", assertIs<AiContextCopyLoadState.Ready>(reader.state).document.sections.single().content)
        val otherSelection = next.copy(selectedMaterialPaths = listOf("different.md"))
        reader.load(otherSelection); runCurrent()
        reader.clear()
        gates[otherSelection]!!.complete(document("closed")); runCurrent()
        assertSame(AiContextCopyLoadState.Idle, reader.state)
    }

    @Test fun `failed loader reports readable error and retry can produce a preview`() = runTest {
        var failures = true
        val reader = AiContextCopyController(this) { if (failures) error("文件被占用") else document("retry worked") }
        reader.load(request("same")); runCurrent()
        assertEquals("文件被占用", assertIs<AiContextCopyLoadState.Failed>(reader.state).message)
        failures = false
        reader.load(request("same")); runCurrent()
        assertEquals("retry worked", assertIs<AiContextCopyLoadState.Ready>(reader.state).document.sections.single().content)
        reader.clear()
    }

    private fun request(name: String) = AiContextCopyRequest(TaskManifest(folderName = name, taskDirectoryName = name,
        featureBranch = "feature/$name", createdAt = "now", updatedAt = "now", services = emptyList()))
    private fun document(body: String) = AiContextCopyDocument(listOf(AiContextCopySection("test", "正文", body)))
}
