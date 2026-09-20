package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.MeegleCliStatus
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MeegleSettingsPresentationTest {
    @Test
    fun `installed unauthenticated status exposes the relogin action`() {
        val status = MeegleCliStatus(installed = true, authenticationError = "not logged in")
        val state = MeegleCliState.Ready(status)

        assertEquals(CliDetectionPhase.AUTH_REQUIRED, meegleCliDetectionPhase(state))
        assertTrue(meegleLoginActionVisible(status))
        assertTrue(meegleLoginActionEnabled(status, state, saving = false, busy = false))
    }

    @Test
    fun `missing cli and protocol failure do not expose the relogin action`() {
        val missing = MeegleCliStatus(installed = false)
        val protocolFailure = MeegleCliState.Failed("Meegle 登录状态 JSON 解析失败")

        assertEquals(CliDetectionPhase.FAILED, meegleCliDetectionPhase(MeegleCliState.Ready(missing)))
        assertFalse(meegleLoginActionVisible(missing))
        assertEquals(CliDetectionPhase.FAILED, meegleCliDetectionPhase(protocolFailure))
    }

    @Test
    fun `relogin action is disabled while checking saving or logging in`() {
        val status = MeegleCliStatus(installed = true)
        val ready = MeegleCliState.Ready(status)

        assertFalse(meegleLoginActionEnabled(status, MeegleCliState.Loading(status), saving = false, busy = false))
        assertFalse(meegleLoginActionEnabled(status, ready, saving = true, busy = false))
        assertFalse(meegleLoginActionEnabled(status, ready, saving = false, busy = true))
    }

    @Test
    fun `manual login command uses configured executable and oauth arguments`() {
        assertEquals(
            "& 'F:\\npm pkgs\\global\\meegle.cmd' auth login --host project.feishu.cn --format json",
            buildMeegleLoginCommand("F:\\npm pkgs\\global\\meegle.cmd", "Windows 11"),
        )
        assertEquals(
            "meegle auth login --host project.feishu.cn --format json",
            buildMeegleLoginCommand("meegle", "Linux"),
        )
    }
}
