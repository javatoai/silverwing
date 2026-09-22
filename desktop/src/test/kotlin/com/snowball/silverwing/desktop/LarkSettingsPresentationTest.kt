package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LarkCliStatus
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LarkSettingsPresentationTest {
    @Test
    fun `installed unauthenticated status exposes login and domain selection keeps the page idle`() {
        val status = LarkCliStatus(installed = true, brand = "feishu", authenticationError = "not logged in")
        val state = LarkCliState.Ready(status)

        assertEquals(CliDetectionPhase.AUTH_REQUIRED, larkCliDetectionPhase(state))
        assertTrue(larkLoginActionVisible(status))
        assertFalse(larkLogoutActionVisible(status))
        assertTrue(larkLoginActionEnabled(status, state, saving = false, busy = false, deviceLoginActive = false))
        assertFalse(larkLoginActionEnabled(status, state, saving = false, busy = false, deviceLoginActive = true))
    }

    @Test
    fun `authenticated status exposes only logout and busy states disable it`() {
        val status = LarkCliStatus(installed = true, authenticated = true, tokenStatus = "valid")
        val ready = LarkCliState.Ready(status)

        assertEquals(CliDetectionPhase.READY, larkCliDetectionPhase(ready))
        assertTrue(larkLogoutActionVisible(status))
        assertFalse(larkLoginActionVisible(status))
        assertTrue(larkLogoutActionEnabled(status, ready, saving = false, busy = false))
        assertFalse(larkLogoutActionEnabled(status, LarkCliState.Loading(status), saving = false, busy = false))
        assertFalse(larkLogoutActionEnabled(status, ready, saving = true, busy = false))
        assertFalse(larkLogoutActionEnabled(status, ready, saving = false, busy = true))
    }

    @Test
    fun `missing cli and protocol failure hide authentication actions`() {
        val missing = LarkCliStatus(installed = false)
        assertEquals(CliDetectionPhase.FAILED, larkCliDetectionPhase(LarkCliState.Ready(missing)))
        assertFalse(larkLoginActionVisible(missing))
        assertEquals(CliDetectionPhase.FAILED, larkCliDetectionPhase(LarkCliState.Failed("parse failed")))
    }
}
