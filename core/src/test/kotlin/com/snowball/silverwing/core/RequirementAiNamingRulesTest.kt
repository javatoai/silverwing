package com.snowball.silverwing.core

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class RequirementAiNamingRulesTest {
    @Test
    fun `accepts a Chinese-majority folder name and concise English suffix`() {
        val suggestion = RequirementAiNamingSuggestion(
            folderName = "支付超时优化",
            branchSuffix = "payment_timeout",
        )

        RequirementAiNamingRules.requireValid(suggestion)

        assertEquals("feature/123_payment_timeout", RequirementAiNamingRules.composeBranch("feature/123_{ai}", suggestion.branchSuffix))
    }

    @Test
    fun `rejects folder names outside the AI naming contract`() {
        val tooManyHan = RequirementAiNamingSuggestion("一二三四五六七", "valid_name")
        val EnglishMajority = RequirementAiNamingSuggestion("需求abc", "valid_name")

        assertFailsWith<IllegalArgumentException> { RequirementAiNamingRules.requireValid(tooManyHan) }
        assertFailsWith<IllegalArgumentException> { RequirementAiNamingRules.requireValid(EnglishMajority) }
    }

    @Test
    fun `rejects an invalid branch suffix before it reaches Git`() {
        val invalid = RequirementAiNamingSuggestion("修复支付", "Payment-Fix")

        assertFailsWith<IllegalArgumentException> { RequirementAiNamingRules.requireValid(invalid) }
    }
}
