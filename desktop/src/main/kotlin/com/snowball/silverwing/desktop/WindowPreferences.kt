package com.snowball.silverwing.desktop

import java.util.prefs.Preferences
import kotlin.math.roundToInt

/**
 * Stores presentation-only desktop preferences outside configuration shards so changing
 * window geometry never changes the product configuration schema.
 */
internal object WindowPreferences {
    internal const val PREFERENCES_NODE = "com/snowball/silverwing/window"
    private val preferences = Preferences.userRoot().node(PREFERENCES_NODE)
    internal const val CURRENT_LAYOUT_VERSION = 2
    private const val LAYOUT_VERSION_KEY = "layoutVersion"
    private const val TASK_LIST_PANE_WIDTH_KEY = "taskListPaneWidth"
    private const val TASK_LIST_PANE_COLLAPSED_KEY = "taskListPaneCollapsed"
    private const val WORKSPACE_NAVIGATION_COLLAPSED_KEY = "workspaceNavigationCollapsed"
    private const val MATERIALS_DIRECTORY_PANE_WIDTH_KEY = "materialsDirectoryPaneWidth"
    private const val EXPAND_REQUIREMENT_MATERIALS_SECOND_LEVEL_FOLDERS_KEY =
        "expandRequirementMaterialsSecondLevelFolders"

    private const val DEFAULT_WIDTH = 1600
    private const val DEFAULT_HEIGHT = 980

    data class Snapshot(
        val width: Int = DEFAULT_WIDTH,
        val height: Int = DEFAULT_HEIGHT,
        val maximized: Boolean = true,
        val settingsSection: String = "basic",
        val taskListPaneWidth: Int? = null,
        val taskListPaneCollapsed: Boolean = false,
        val materialsDirectoryPaneWidth: Int? = null,
        val expandRequirementMaterialsSecondLevelFolders: Boolean = false,
        val workspaceNavigationCollapsed: Boolean = false,
    )

    fun load(preferences: Preferences = this.preferences): Snapshot = snapshotFor(
        width = preferences.getInt("width", DEFAULT_WIDTH),
        height = preferences.getInt("height", DEFAULT_HEIGHT),
        maximized = preferences.getBoolean("maximized", false),
        layoutVersion = preferences.getInt(LAYOUT_VERSION_KEY, 0),
        settingsSection = preferences.get("settingsSection", "basic"),
        taskListPaneWidth = preferences.getInt(TASK_LIST_PANE_WIDTH_KEY, 0).takeIf { it > 0 },
        taskListPaneCollapsed = preferences.getBoolean(TASK_LIST_PANE_COLLAPSED_KEY, false),
        materialsDirectoryPaneWidth = preferences.getInt(MATERIALS_DIRECTORY_PANE_WIDTH_KEY, 0).takeIf { it > 0 },
        expandRequirementMaterialsSecondLevelFolders = preferences.getBoolean(
            EXPAND_REQUIREMENT_MATERIALS_SECOND_LEVEL_FOLDERS_KEY,
            false,
        ),
        workspaceNavigationCollapsed = preferences.getBoolean(WORKSPACE_NAVIGATION_COLLAPSED_KEY, false),
    )

    fun saveWindow(width: Int, height: Int, maximized: Boolean) {
        if (!maximized) {
            preferences.putInt("width", width)
            preferences.putInt("height", height)
        }
        preferences.putBoolean("maximized", maximized)
        preferences.putInt(LAYOUT_VERSION_KEY, CURRENT_LAYOUT_VERSION)
    }

    fun savePhysicalWindow(
        width: Int,
        height: Int,
        maximized: Boolean,
        scaleX: Double,
        scaleY: Double,
    ) {
        val logical = physicalToLogical(width, height, scaleX, scaleY)
        saveWindow(logical.widthDp.roundToInt(), logical.heightDp.roundToInt(), maximized)
    }

    fun saveSettingsSection(section: String) {
        preferences.put("settingsSection", section)
    }

    fun saveWorkspaceNavigationCollapsed(collapsed: Boolean, preferences: Preferences = this.preferences) {
        preferences.putBoolean(WORKSPACE_NAVIGATION_COLLAPSED_KEY, collapsed)
    }

    fun saveTaskListPaneWidth(width: Int) {
        preferences.putInt(TASK_LIST_PANE_WIDTH_KEY, width)
    }

    fun saveTaskListPaneLayout(width: Int, collapsed: Boolean) {
        saveTaskListPaneWidth(width)
        preferences.putBoolean(TASK_LIST_PANE_COLLAPSED_KEY, collapsed)
    }

    fun saveMaterialsDirectoryPaneWidth(width: Int) {
        preferences.putInt(MATERIALS_DIRECTORY_PANE_WIDTH_KEY, width)
    }

    fun saveExpandRequirementMaterialsSecondLevelFolders(enabled: Boolean) {
        preferences.putBoolean(EXPAND_REQUIREMENT_MATERIALS_SECOND_LEVEL_FOLDERS_KEY, enabled)
    }

    internal fun physicalToLogical(
        width: Int,
        height: Int,
        scaleX: Double,
        scaleY: Double,
    ): LogicalWindowSize = LogicalWindowSize(
        (width / scaleX.coerceAtLeast(1.0)).roundToInt().toFloat(),
        (height / scaleY.coerceAtLeast(1.0)).roundToInt().toFloat(),
    )

    /**
     * Earlier releases stored an OS-clamped initial window size. On high-density macOS
     * displays that could become a very small floating window on every launch.
     * Treat those older layouts as a one-time migration to the normal maximized
     * desktop window. The first subsequent close records the user's actual choice.
     */
    internal fun snapshotFor(
        width: Int = DEFAULT_WIDTH,
        height: Int = DEFAULT_HEIGHT,
        maximized: Boolean = false,
        layoutVersion: Int = 0,
        settingsSection: String = "basic",
        taskListPaneWidth: Int? = null,
        taskListPaneCollapsed: Boolean = false,
        materialsDirectoryPaneWidth: Int? = null,
        expandRequirementMaterialsSecondLevelFolders: Boolean = false,
        workspaceNavigationCollapsed: Boolean = false,
    ): Snapshot = Snapshot(
        width = width,
        height = height,
        maximized = layoutVersion < CURRENT_LAYOUT_VERSION || maximized,
        settingsSection = settingsSection,
        taskListPaneWidth = taskListPaneWidth,
        taskListPaneCollapsed = taskListPaneCollapsed,
        materialsDirectoryPaneWidth = materialsDirectoryPaneWidth,
        expandRequirementMaterialsSecondLevelFolders = expandRequirementMaterialsSecondLevelFolders,
        workspaceNavigationCollapsed = workspaceNavigationCollapsed,
    )
}

internal data class LogicalWindowSize(val widthDp: Float, val heightDp: Float)
