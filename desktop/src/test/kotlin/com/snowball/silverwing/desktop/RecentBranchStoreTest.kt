package com.snowball.silverwing.desktop

import java.nio.charset.StandardCharsets
import java.util.Base64
import java.util.prefs.AbstractPreferences
import java.util.prefs.Preferences
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class RecentBranchStoreTest {
    @Test fun `long unicode repository and remote names keep separate bounded records`() {
        val preferences = MemoryPreferences()
        val store = RecentBranchStore(preferences)
        val repository = "仓库-".repeat(50)
        val remote = "团队远程-".repeat(50)
        store.record(repository, remote, "$remote/main")
        store.record(repository, "$remote-other", "$remote-other/release")
        assertEquals(listOf("$remote/main"), store.list(repository, remote))
        assertEquals(listOf("$remote-other/release"), store.list(repository, "$remote-other"))
        assertTrue(preferences.keys().all { it.length <= Preferences.MAX_KEY_LENGTH })
    }

    @Test fun `existing recent records migrate on next successful selection`() {
        val preferences = MemoryPreferences()
        val legacy = Base64.getUrlEncoder().withoutPadding()
            .encodeToString("repo|origin".toByteArray(StandardCharsets.UTF_8))
        preferences.put(legacy, "origin/main\u001Forigin/release")
        val store = RecentBranchStore(preferences)
        assertEquals(listOf("origin/main", "origin/release"), store.list("repo", "origin"))
        store.record("repo", "origin", "origin/release")
        assertEquals(listOf("origin/release", "origin/main"), store.list("repo", "origin"))
        assertTrue(preferences.keys().any { it.startsWith("v2-") })
    }

    @Test fun `large branches are stored whole without exceeding preference capacity`() {
        val preferences = MemoryPreferences()
        val store = RecentBranchStore(preferences)
        val branches = (1..5).map { "origin/" + "$it".repeat(2000) }
        branches.forEach { store.record("repo", "origin", it) }
        assertEquals(branches.takeLast(4).reversed(), store.list("repo", "origin"))
        store.record("repo", "origin", "origin/" + "z".repeat(Preferences.MAX_VALUE_LENGTH + 1))
        assertEquals(branches.takeLast(4).reversed(), store.list("repo", "origin"))
        assertTrue(preferences.keys().all { preferences.get(it, "").length <= Preferences.MAX_VALUE_LENGTH })
    }

    private class MemoryPreferences : AbstractPreferences(null, "") {
        private val values = mutableMapOf<String, String>()
        override fun putSpi(key: String, value: String) { values[key] = value }
        override fun getSpi(key: String): String? = values[key]
        override fun removeSpi(key: String) { values.remove(key) }
        override fun removeNodeSpi() = Unit
        override fun keysSpi(): Array<String> = values.keys.toTypedArray()
        override fun childrenNamesSpi(): Array<String> = emptyArray()
        override fun childSpi(name: String): AbstractPreferences = error("No child needed")
        override fun syncSpi() = Unit
        override fun flushSpi() = Unit
    }
}
