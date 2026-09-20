package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.io.TempDir
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class HandoffDocumentWriterTest {
    @TempDir lateinit var temporary: Path

    @Test
    fun `complete authorization cookie and quoted credential values are removed before writing`() {
        val credentials = listOf(
            "Authorization: Bearer synthetic-bearer-secret",
            "- authorization: \"Bearer synthetic quoted secret\"",
            "Proxy-Authorization: Basic synthetic-basic-secret",
            "Cookie: sid=synthetic-one; session=synthetic-two",
            "Set-Cookie: session=\"synthetic spaced secret\"; Path=/",
            "password = 'synthetic password with spaces'",
            "\"access_token\": \"synthetic json token\",",
        )
        val supplied = "# Verified facts\n\n" + credentials.joinToString("\n") + "\n\nNext step: keep this."
        val sanitized = HandoffDocumentWriter.safeMarkdown(supplied)
        assertFalse(sanitized.contains("synthetic"))
        assertEquals(credentials.size, Regex("\\[REDACTED]").findAll(sanitized).count())
        assertTrue(sanitized.startsWith("# Verified facts"))
        assertTrue(sanitized.endsWith("Next step: keep this."))

        val written = HandoffDocumentWriter.write(temporary, supplied)
        assertEquals(sanitized + "\n", Files.readString(written))
    }

    @Test
    fun `empty credential field does not consume the following line`() {
        val supplied = "Authorization:\nNext step: keep this.\nCookie:\n\n# Heading"
        assertEquals(supplied, HandoffDocumentWriter.safeMarkdown(supplied))
    }

    @Test
    fun `standalone supported credential formats are redacted`() {
        val supplied = "synthetic keys sk-abcdefghijklmnop rk_abcdefghijklmnop ghp_abcdefghijklmnop"
        assertEquals("synthetic keys [REDACTED] [REDACTED] [REDACTED]", HandoffDocumentWriter.safeMarkdown(supplied))
    }
}
