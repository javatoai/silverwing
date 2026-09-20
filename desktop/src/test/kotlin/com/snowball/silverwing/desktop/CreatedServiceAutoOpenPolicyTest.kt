package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.AppConfig
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class TemporaryDevelopmentToolSelectionPolicyTest {
    @Test
    fun `temporary tool selection is disabled by default`() {
        assertFalse(temporaryDevelopmentToolSelectionEnabled(AppConfig()))
    }

    @Test
    fun `temporary tool selection can be enabled without enabling auto open`() {
        assertTrue(temporaryDevelopmentToolSelectionEnabled(AppConfig(allowTemporaryDevelopmentToolSelection = true)))
    }
}
