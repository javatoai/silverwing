package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertNotNull

class TaskNamingTest {
    @Test
    fun `keeps readable unicode and replaces unsafe characters for configuration identifiers`() {
        assertEquals("需求-123-支付改造", TaskNaming.directoryName("需求/123 支付改造"))
    }

    @Test
    fun `protects windows reserved file names for configuration identifiers`() {
        assertEquals("_CON", TaskNaming.directoryName("CON"))
    }

    @Test
    fun `valid task directory name retains Chinese and internal spaces`() {
        val name = "支付 订单优化"

        assertNull(TaskNaming.directoryNameValidationError(name))
        assertEquals(name, TaskNaming.requireValidDirectoryName(name))
    }

    @Test
    fun `unsafe task directory names are rejected without sanitising`() {
        listOf(
            " ",
            " leading",
            "trailing ",
            "trailing.",
            "bad:name",
            "bad\u0000name",
            "CON",
            "x".repeat(TaskNaming.MAX_DIRECTORY_NAME_LENGTH + 1),
        ).forEach { name ->
            assertNotNull(TaskNaming.directoryNameValidationError(name), name)
            assertFailsWith<IllegalArgumentException> { TaskNaming.requireValidDirectoryName(name) }
        }
    }
}
