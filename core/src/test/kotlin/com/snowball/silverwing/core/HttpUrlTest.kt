package com.snowball.silverwing.core

import org.junit.jupiter.api.Assertions.assertFalse
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test

class HttpUrlTest {
    @Test
    fun `accepts http and https urls with host`() {
        assertTrue(isHttpUrl("https://project.feishu.cn/obt/userstory/detail/1"))
        assertTrue(isHttpUrl("http://example.com/path"))
        assertTrue(isHttpUrl("  https://example.com  "))
    }

    @Test
    fun `rejects blank plain text and invalid urls`() {
        assertFalse(isHttpUrl(""))
        assertFalse(isHttpUrl("   "))
        assertFalse(isHttpUrl("飞书需求：支付改造"))
        assertFalse(isHttpUrl("ftp://example.com"))
        assertFalse(isHttpUrl("https://"))
        assertFalse(isHttpUrl("not a url"))
    }
}
