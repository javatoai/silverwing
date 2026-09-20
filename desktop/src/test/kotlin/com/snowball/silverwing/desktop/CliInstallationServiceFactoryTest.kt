package com.snowball.silverwing.desktop

import kotlin.test.Test
import kotlin.test.assertIs

class CliInstallationServiceFactoryTest {
    @Test
    fun `Windows and macOS both select a supported CLI installer`() {
        assertIs<WindowsCliInstallationService>(platformCliInstallationService("Windows 11"))
        assertIs<MacCliInstallationService>(platformCliInstallationService("Mac OS X"))
    }

    @Test
    fun `other platforms retain the explicit unsupported installer`() {
        kotlin.test.assertFalse(platformCliInstallationService("Linux").inspect().supported)
    }
}
