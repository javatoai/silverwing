package com.snowball.silverwing.core

import java.net.URI

/** 编号在本机解析；AI 占位符留给经过校验的命名结果替换。 */
object BranchPrefixResolver {
    private const val NUM_PLACEHOLDER = "{num}"
    private val digits = Regex("\\d+")

    fun resolve(prefix: String, requirement: String): String? {
        if (NUM_PLACEHOLDER !in prefix) return prefix
        val number = extractNumber(requirement) ?: return null
        return prefix.replace(NUM_PLACEHOLDER, number)
    }

    fun containsUnresolvedPlaceholder(branch: String): Boolean = NUM_PLACEHOLDER in branch || "{ai}" in branch

    fun extractNumber(requirement: String): String? {
        FeishuWorkItemLink.parse(requirement)?.let { return it.workItemId }
        val trimmed = requirement.trim().trimEnd(',', '，', '。', ';', '；')
        val path = runCatching {
            URI(trimmed).takeIf { it.scheme.equals("http", true) || it.scheme.equals("https", true) }?.path
        }.getOrNull()
        return digits.findAll(path ?: trimmed).lastOrNull()?.value
    }
}
