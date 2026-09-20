package com.snowball.silverwing.core

import org.junit.jupiter.api.Test
import java.time.Instant
import java.time.LocalDate
import kotlin.test.assertEquals

class SilverWingTimeTest {
    @Test
    fun `formats application timestamps and log dates in Shanghai time`() {
        val instant = Instant.parse("2026-01-01T16:00:00Z")

        assertEquals("2026-01-02 00:00:00", SilverWingTime.format(instant))
        assertEquals(LocalDate.of(2026, 1, 2), SilverWingTime.localDate(instant))
    }
}
