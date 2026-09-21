package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SchemaVersionCompatibilityTest {
    @Test
    fun `product version is 2_0_2 while persisted aggregate schemas remain 2_0_0`() {
        assertEquals("2.0.2", CURRENT_PRODUCT_VERSION)
        assertEquals("2.0.0", CURRENT_APP_CONFIG_SCHEMA_VERSION)
        assertEquals("2.0.0", CURRENT_TASK_MANIFEST_SCHEMA_VERSION)
    }

    @Test
    fun `manifest compatibility is scoped to the silverwing release line`() {
        assertTrue(SchemaVersionCompatibility.isCompatible("2.0.7", CURRENT_TASK_MANIFEST_SCHEMA_VERSION))
        assertFalse(SchemaVersionCompatibility.isCompatible("1.0.9", CURRENT_TASK_MANIFEST_SCHEMA_VERSION))
    }
}
