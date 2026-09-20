package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.params.ParameterizedTest
import org.junit.jupiter.params.provider.CsvSource
import org.junit.jupiter.api.Test

class TagVersioningTest {
    @ParameterizedTest
    @CsvSource(
        "1.6.89.beta-9, 1.6.89.beta-10",
        "1.6.89.7, 1.6.90.beta-1",
        "1.6.89, 1.6.90.beta-1",
    )
    fun `computes compatible next tag`(current: String, expected: String) {
        assertEquals(expected, TagVersioning.next(current))
    }

    @Test
    fun `rejects unsupported tag`() {
        assertThrows(IllegalArgumentException::class.java) {
            TagVersioning.next("v1.2.3")
        }
    }

    @Test
    fun `selects latest tag from active version line like reference script`() {
        val tags = listOf(
            VersionTag("2.0.0", createdAtEpochSeconds = 100),
            VersionTag("1.6.88", createdAtEpochSeconds = 200),
            VersionTag("1.6.89.beta-2", createdAtEpochSeconds = 300),
            VersionTag("1.6.89.beta-10", createdAtEpochSeconds = 250),
            VersionTag("not-a-version", createdAtEpochSeconds = 400),
        )

        assertEquals("1.6.89.beta-10", TagVersioning.latest(tags))
    }

    @Test
    fun `prefers release and numeric build over beta in the active line`() {
        val tags = listOf(
            VersionTag("1.6.89.beta-99", createdAtEpochSeconds = 300),
            VersionTag("1.6.89", createdAtEpochSeconds = 200),
            VersionTag("1.6.89.7", createdAtEpochSeconds = 100),
        )

        assertEquals("1.6.89.7", TagVersioning.latest(tags))
    }
}
