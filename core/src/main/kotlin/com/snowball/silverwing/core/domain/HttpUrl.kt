package com.snowball.silverwing.core

import java.net.URI
import java.util.Locale

fun isHttpUrl(value: String): Boolean {
    val trimmed = value.trim()
    if (trimmed.isEmpty()) return false
    val lower = trimmed.lowercase(Locale.ROOT)
    if (!lower.startsWith("http://") && !lower.startsWith("https://")) return false
    return runCatching {
        val uri = URI(trimmed)
        !uri.scheme.isNullOrBlank() && !uri.host.isNullOrBlank()
    }.getOrDefault(false)
}
