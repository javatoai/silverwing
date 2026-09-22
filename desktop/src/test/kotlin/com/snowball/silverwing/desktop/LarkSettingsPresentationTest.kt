package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.LarkCliStatus
import com.snowball.silverwing.core.LarkAuthenticationState
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class LarkSettingsPresentationTest {
    @Test
    fun `installed unauthenticated status exposes login and domain selection keeps the page idle`() {
        val status = LarkCliStatus(installed = true, brand = "feishu")
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

    @Test
    fun `status check failures are distinct from a user that needs to log in`() {
        val failed = LarkCliStatus(
            installed = true,
            authenticationState = LarkAuthenticationState.CHECK_FAILED,
            authenticationError = "返回格式无法识别",
        )

        assertEquals(CliDetectionPhase.FAILED, larkCliDetectionPhase(LarkCliState.Ready(failed)))
        assertFalse(larkLoginActionVisible(failed))
        assertFalse(larkLogoutActionVisible(failed))
    }

    @Test
    fun `credential states use user facing labels`() {
        assertEquals("有效", larkTokenStatusLabel("valid"))
        assertEquals("等待自动刷新", larkTokenStatusLabel("needs_refresh"))
        assertEquals("状态未知", larkTokenStatusLabel("future-state"))
    }
}
