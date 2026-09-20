package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test

class BootstrapPresetsTest {
    @Test
    fun `empty preset has no steps`() {
        val config = BootstrapPresets.empty()
        assertTrue(config.copyRules.isEmpty())
        assertTrue(config.commands.isEmpty())
    }

    @Test
    fun `codeGraph preset runs codegraph init`() {
        val config = BootstrapPresets.codeGraph()
        assertEquals(1, config.commands.size)
        val command = config.commands.single()
        assertEquals("codegraph", command.executable)
        assertEquals(listOf("init", "-i"), command.arguments)
        assertEquals(600, command.timeoutSeconds)
    }
}
