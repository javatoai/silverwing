package com.snowball.silverwing.core

import kotlinx.serialization.SerializationException
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.assertFailsWith

class AgentTaskTemplateStoreTest {
    @TempDir
    lateinit var temporary: Path

    private fun store(): AgentTaskTemplateStore =
        AgentTaskTemplateStore(ApplicationPaths(temporary.resolve("home")))

    private fun template(id: String, name: String, content: String = "$name 内容") =
        AgentTaskTemplate(id, name, content, "2026-08-19 16:00:00")

    @Test
    fun `missing template file returns an empty list`() {
        assertEquals(emptyList<AgentTaskTemplate>(), store().list())
    }

    @Test
    fun `saved templates roundtrip sorted by name`() {
        val store = store()
        store.saveAll(listOf(template("b", "beta"), template("a", "alpha")))

        val loaded = store.list()

        assertEquals(listOf("alpha", "beta"), loaded.map { it.name })
        assertEquals("alpha 内容", loaded.first().content)
        assertTrue(Files.exists(ApplicationPaths(temporary.resolve("home")).agentTaskTemplates))
    }

    @Test
    fun `saveAll replaces the whole library atomically`() {
        val store = store()
        store.saveAll(listOf(template("a", "旧模板")))
        store.saveAll(listOf(template("b", "新模板")))

        assertEquals(listOf("新模板"), store().list().map { it.name })
    }

    @Test
    fun `concurrent updates from separate stores preserve both additions`() {
        store().saveAll(listOf(template("a", "alpha")))

        runConcurrentUpdates(
            { current -> current + template("b", "beta") },
            { current -> current + template("c", "gamma") },
        )

        assertEquals(setOf("a", "b", "c"), store().list().map { it.id }.toSet())
    }

    @Test
    fun `concurrent updates from separate stores preserve addition and deletion`() {
        store().saveAll(listOf(template("keep", "保留"), template("remove", "删除")))

        runConcurrentUpdates(
            { current -> current + template("new", "新增") },
            { current -> current.filterNot { it.id == "remove" } },
        )

        assertEquals(setOf("keep", "new"), store().list().map { it.id }.toSet())
    }

    @Test
    fun `failed update validation preserves the existing library`() {
        val store = store()
        val original = listOf(template("a", "重复"))
        store.saveAll(original)

        assertFailsWith<IllegalArgumentException> {
            store.update { current -> current + template("b", "重复") }
        }

        assertEquals(original, store.list())
        assertEquals(listOf("a", "c"), store.update { it + template("c", "新增") }.map { it.id }.sorted())
    }

    @Test
    fun `duplicate template names are rejected`() {
        assertFailsWith<IllegalArgumentException> {
            store().saveAll(listOf(template("a", "重复"), template("b", "重复")))
        }
    }

    @Test
    fun `reserved silverwing markers are rejected`() {
        assertFailsWith<IllegalArgumentException> {
            store().saveAll(listOf(template("a", "恶意", "包含 ${AgentDocumentService.TASK_NOTES_BEGIN} 标记")))
        }
    }

    @Test
    fun `blank template name or content is rejected by the model`() {
        assertFailsWith<IllegalArgumentException> { template("a", " ") }
        assertFailsWith<IllegalArgumentException> { template("a", "名称", " ") }
    }

    @Test
    fun `corrupted template file surfaces a serialization error`() {
        val paths = ApplicationPaths(temporary.resolve("home"))
        Files.createDirectories(paths.agents)
        Files.writeString(paths.agentTaskTemplates, "{ not json")

        assertFailsWith<SerializationException> { AgentTaskTemplateStore(paths).list() }
    }

    private fun runConcurrentUpdates(
        first: (List<AgentTaskTemplate>) -> List<AgentTaskTemplate>,
        second: (List<AgentTaskTemplate>) -> List<AgentTaskTemplate>,
    ) {
        val ready = CountDownLatch(2)
        val start = CountDownLatch(1)
        val activeTransforms = AtomicInteger()
        val maximumConcurrentTransforms = AtomicInteger()
        val executor = Executors.newFixedThreadPool(2)
        try {
            val futures = listOf(first, second).map { transform ->
                val independentStore = store()
                executor.submit<List<AgentTaskTemplate>> {
                    ready.countDown()
                    check(start.await(5, TimeUnit.SECONDS))
                    independentStore.update { current ->
                        val active = activeTransforms.incrementAndGet()
                        maximumConcurrentTransforms.accumulateAndGet(active, ::maxOf)
                        try {
                            Thread.sleep(100)
                            transform(current)
                        } finally {
                            activeTransforms.decrementAndGet()
                        }
                    }
                }
            }
            assertTrue(ready.await(5, TimeUnit.SECONDS))
            start.countDown()
            futures.forEach { it.get(10, TimeUnit.SECONDS) }
            assertEquals(1, maximumConcurrentTransforms.get())
        } finally {
            start.countDown()
            executor.shutdownNow()
        }
    }
}
