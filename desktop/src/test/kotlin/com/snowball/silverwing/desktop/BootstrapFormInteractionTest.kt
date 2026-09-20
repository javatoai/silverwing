package com.snowball.silverwing.desktop

import com.snowball.silverwing.core.BootstrapCopyRule
import kotlin.test.Test
import kotlin.test.assertEquals

class BootstrapFormInteractionTest {
    @Test
    fun `selected source fills untouched target and preserves manual target`() {
        assertEquals(
            BootstrapCopyRule("config/app.yml", "config/app.yml"),
            BootstrapCopyRule("", "").withSelectedSource("config/app.yml"),
        )
        assertEquals(
            BootstrapCopyRule("config/new.yml", "config/new.yml"),
            BootstrapCopyRule("config/old.yml", "config/old.yml").withSelectedSource("config/new.yml"),
        )
        assertEquals(
            BootstrapCopyRule("config/new.yml", "runtime/app.yml"),
            BootstrapCopyRule("config/old.yml", "runtime/app.yml").withSelectedSource("config/new.yml"),
        )
    }
}
