package com.snowball.silverwing.core

import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * Single application timestamp policy. JSON intentionally omits an offset for
 * readability, so every producer and consumer must interpret it in Shanghai.
 */
object SilverWingTime {
    val zone: ZoneId = ZoneId.of("Asia/Shanghai")
    private val formatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss").withZone(zone)

    fun format(instant: Instant): String = formatter.format(instant)

    fun localDate(instant: Instant): LocalDate = LocalDate.ofInstant(instant, zone)
}
