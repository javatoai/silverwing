package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class TaskCreationDefaultsServiceTest {
    @Test
    fun `global task creation defaults are normalized independently of project groups`() {
        val repository = InMemoryTaskCreationDefaultsRepository(
            AppConfig(
                groups = listOf(
                    GroupConfig("one", "One"),
                    GroupConfig("two", "Two"),
                ),
            ),
        )
        val service = TaskCreationDefaultsService(repository)

        service.updateBranchPrefix(" feature/task-")
        service.updateWorkspaceToolIds(listOf("codex", " cursor ", "codex", ""))

        val saved = repository.load()
        assertEquals("feature/task-", saved.defaultBranchPrefix)
        assertEquals(listOf("codex", "cursor"), saved.defaultWorkspaceToolIds)
        assertEquals(listOf("one", "two"), saved.groups.map(GroupConfig::id))
    }

    @Test
    fun `global branch prefix rejects whitespace after normalization`() {
        val service = TaskCreationDefaultsService(InMemoryTaskCreationDefaultsRepository(AppConfig()))

        assertFailsWith<IllegalArgumentException> {
            service.updateBranchPrefix("feature /task")
        }
    }
}

private class InMemoryTaskCreationDefaultsRepository(
    private var config: AppConfig,
) : ConfigurationRepository {
    override fun load(): AppConfig = config

    override fun save(config: AppConfig) {
        this.config = config
    }
}
