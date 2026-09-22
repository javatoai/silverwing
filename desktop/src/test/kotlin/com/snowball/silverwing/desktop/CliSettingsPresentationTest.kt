package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertFalse
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
}
