package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import com.snowball.silverwing.core.TaskLifecycleStatus
import com.snowball.silverwing.core.TaskManifest
import org.junit.jupiter.api.Test
import kotlin.test.assertEquals

class AppSessionStoreTest {
    @Test
    fun `navigation selects the newest task visible in the destination category`() {
        val active = task("active", TaskLifecycleStatus.ACTIVE)
        val archived = task("archived", TaskLifecycleStatus.ARCHIVED)
        val session = AppSessionStore(AppConfig(), listOf(active, archived))

        session.navigation = NavigationItem.ARCHIVED
        assertEquals(archived, session.selectedTask)

        session.navigation = NavigationItem.TASKS
        assertEquals(active, session.selectedTask)
    }

    @Test
    fun `archived route remains reachable when there are no active tasks`() {
        val archived = task("archived", TaskLifecycleStatus.ARCHIVED)
        val session = AppSessionStore(AppConfig(), listOf(archived))

        session.navigation = NavigationItem.ARCHIVED

        assertEquals(archived, session.selectedTask)
        assertEquals(NavigationItem.TASKS, sidebarNavigationSelection(session.navigation))

        session.navigation = NavigationItem.TASKS
        assertEquals(null, session.selectedTask)
    }

    @Test
    fun `navigation preserves a selection already visible in destination category`() {
        val newest = task("newest", TaskLifecycleStatus.ARCHIVED)
        val selected = task("selected", TaskLifecycleStatus.ARCHIVED)
        val session = AppSessionStore(AppConfig(), listOf(newest, selected)).apply {
            this.selectedTask = selected
        }

        session.navigation = NavigationItem.ARCHIVED

        assertEquals(selected, session.selectedTask)
    }

    @Test
    fun `task reload keeps selection inside the current destination category`() {
        val archived = task("archived", TaskLifecycleStatus.ARCHIVED)
        val oldActive = task("old-active", TaskLifecycleStatus.ACTIVE)
        val newActive = task("new-active", TaskLifecycleStatus.ACTIVE)
        val session = AppSessionStore(AppConfig(), listOf(oldActive, archived))

        session.navigation = NavigationItem.TASKS
        session.replaceTasks(listOf(archived, newActive), preferredFolder = oldActive.folderName)

        assertEquals(newActive, session.selectedTask)
    }

    private fun task(name: String, status: TaskLifecycleStatus) = TaskManifest(
        folderName = name,
        taskDirectoryName = name,
        featureBranch = "feature/$name",
        createdAt = "2026-08-09 00:00:00",
        updatedAt = "2026-08-09 00:00:00",
        lifecycleStatus = status,
        services = emptyList(),
    )
}
