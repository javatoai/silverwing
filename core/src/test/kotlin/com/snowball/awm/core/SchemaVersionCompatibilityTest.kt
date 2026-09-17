package com.snowball.awm.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SchemaVersionCompatibilityTest {
    @Test
    fun `current product and persisted schemas use their current versions`() {
        assertEquals("1.0.10", CURRENT_PRODUCT_VERSION)
        assertEquals("1.0.9", CURRENT_APP_CONFIG_SCHEMA_VERSION)
        assertEquals("1.0.9", CURRENT_TASK_MANIFEST_SCHEMA_VERSION)
    }

    @Test
    fun `same major and minor accept any patch`() {
        assertTrue(SchemaVersionCompatibility.isCompatible("0.5.0", "0.5.0"))
        assertTrue(SchemaVersionCompatibility.isCompatible("0.5.99", "0.5.0"))
    }

    @Test
    fun `other release lines and malformed versions are rejected`() {
        listOf(
            "0.4.9",
            "1.5.0",
            "0.5.-1",
            "0.5.0.1",
            "+0.5.0",
            "00.5.0",
            "0.05.0",
            "v0.5.0",
            " 0.5.0",
            null,
        ).forEach { version ->
            assertFalse(SchemaVersionCompatibility.isCompatible(version, "0.5.0"), "accepted $version")
        }
    }
}
