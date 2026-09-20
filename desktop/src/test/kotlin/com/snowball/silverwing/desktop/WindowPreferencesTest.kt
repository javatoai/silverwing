package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class WindowPreferencesTest {
    @Test
    fun `window preferences use the silverwing node`() {
        assertEquals("com/snowball/silverwing/window", WindowPreferences.PREFERENCES_NODE)
    }

    @Test
    fun `small floating window sizes are preserved`() {
        assertEquals(
            LogicalWindowSize(1200f, 720f),
            WindowPreferences.physicalToLogical(1200, 720, 1.0, 1.0),
        )
    }

    @Test
    fun `new window starts maximized at the desktop size`() {
        assertEquals(1600, WindowPreferences.Snapshot().width)
        assertEquals(980, WindowPreferences.Snapshot().height)
        assertTrue(WindowPreferences.Snapshot().maximized)
        assertEquals("basic", WindowPreferences.Snapshot().settingsSection)
        assertEquals(null, WindowPreferences.Snapshot().taskListPaneWidth)
    }

    @Test
    fun `legacy window layouts migrate to maximized`() {
        val restored = WindowPreferences.snapshotFor(
            width = 800,
            height = 391,
            maximized = false,
            layoutVersion = WindowPreferences.CURRENT_LAYOUT_VERSION - 1,
        )

        assertTrue(restored.maximized)
        assertEquals(800, restored.width)
        assertEquals(391, restored.height)
    }

    @Test
    fun `current window layouts preserve a user restored floating window`() {
        val restored = WindowPreferences.snapshotFor(
            width = 1200,
            height = 720,
            maximized = false,
            layoutVersion = WindowPreferences.CURRENT_LAYOUT_VERSION,
        )

        assertFalse(restored.maximized)
        assertEquals(1200, restored.width)
        assertEquals(720, restored.height)
    }

    @Test
    fun `current maximized window layouts remain maximized`() {
        assertTrue(
            WindowPreferences.snapshotFor(
                maximized = true,
                layoutVersion = WindowPreferences.CURRENT_LAYOUT_VERSION,
            ).maximized,
        )
    }

    @Test
    fun `task list pane width is retained in the presentation snapshot`() {
        assertEquals(
            316,
            WindowPreferences.snapshotFor(taskListPaneWidth = 316).taskListPaneWidth,
        )
    }

    @Test
    fun `physical window size is persisted as logical size without enforcing a minimum`() {
        assertEquals(
            LogicalWindowSize(1280f, 720f),
            WindowPreferences.physicalToLogical(1920, 1080, 1.5, 1.5),
        )
    }

    @Test
    fun `settings selection restores supported keys and maps only active legacy keys`() {
        val supported = settingsNavigationSections().map { it.key }.toSet()

        assertEquals("logs", normalizeSettingsSection("logs", supported))
        assertEquals("paths", normalizeSettingsSection("paths", supported))
        assertEquals("feishu", normalizeSettingsSection("advanced", supported))
        assertEquals("basic", normalizeSettingsSection("unknown", supported))
        assertEquals("basic", normalizeSettingsSection("overview", supported))
        assertEquals("basic", normalizeSettingsSection("branches", supported))
    }
}
