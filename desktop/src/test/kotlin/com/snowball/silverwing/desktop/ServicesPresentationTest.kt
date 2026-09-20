package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.GroupServiceConfig
import com.snowball.silverwing.core.RepositoryConfig
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class ServicesPresentationTest {
    @Test
    fun `single group does not render group navigation`() {
        assertFalse(serviceGroupNavigationVisible(1))
        assertTrue(serviceGroupNavigationVisible(2))
    }

    @Test
    fun `invalid selected service group falls back to first available group`() {
        assertEquals("first", resolveServiceGroupSelection("removed", listOf("first", "second")))
        assertEquals("second", resolveServiceGroupSelection("second", listOf("first", "second")))
        assertEquals(null, resolveServiceGroupSelection("removed", emptyList()))
    }

    @Test
    fun `service filter matches name id repository name and local path`() {
        val service = GroupServiceConfig.standard("data-center", "repo-data", "数据中心")
        val repository = RepositoryConfig(
            id = "repo-data",
            name = "data-center-repository",
            rootPath = "D:/workspace/data-center",
            gitCommonDirectory = "D:/workspace/data-center/.git",
        )

        assertTrue(serviceMatchesQuery(service, repository, "数据"))
        assertTrue(serviceMatchesQuery(service, repository, "repository"))
        assertTrue(serviceMatchesQuery(service, repository, "workspace/data"))
        assertFalse(serviceMatchesQuery(service, repository, "payment"))
    }
}
