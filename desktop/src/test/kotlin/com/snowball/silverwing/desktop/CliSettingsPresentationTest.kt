package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.CodexCommandSource
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class CliSettingsPresentationTest {
    @Test
    fun `more command actions require a resolved command and an idle settings page`() {
        assertTrue(cliMoreActionsEnabled("git", saving = false, busy = false))
        assertFalse(cliMoreActionsEnabled("", saving = false, busy = false))
        assertFalse(cliMoreActionsEnabled("git", saving = true, busy = false))
        assertFalse(cliMoreActionsEnabled("git", saving = false, busy = true))
    }

    @Test
    fun `manual command editor starts closed unless saving that path failed`() {
        assertFalse(cliManualConfigInitiallyExpanded(pathSaveFailed = false))
        assertTrue(cliManualConfigInitiallyExpanded(pathSaveFailed = true))
    }

    @Test
    fun `discovered Codex executable pre-fills the editor without becoming a manual change`() {
        val discovered = "C:\\Users\\dev\\AppData\\Local\\OpenAI\\Codex\\bin\\current\\codex.exe"

        assertEquals(discovered, cliPathEditorInitialValue(configuredPath = "", discoveredPath = discovered))
        assertFalse(cliPathEditorHasChanges(discovered, configuredPath = "", discoveredPath = discovered))
        assertTrue(cliPathEditorHasChanges("C:\\tools\\codex.exe", configuredPath = "", discoveredPath = discovered))
    }

    @Test
    fun `only absolute Codex discovery sources provide a path editor default`() {
        val executable = "C:\\Users\\dev\\codex.exe"

        assertEquals(executable, codexDiscoveredExecutablePath(executable, CodexCommandSource.DESKTOP_APP))
        assertEquals(executable, codexDiscoveredExecutablePath(executable, CodexCommandSource.PROBED))
        assertNull(codexDiscoveredExecutablePath("codex", CodexCommandSource.PATH_FALLBACK))
    }
}
