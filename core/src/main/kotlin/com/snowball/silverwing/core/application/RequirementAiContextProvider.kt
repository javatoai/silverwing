package com.snowball.silverwing.core

/** 这是唯一允许跨越到本机 Codex CLI 边界的需求数据。 */
data class RequirementAiContext(
    val title: String,
    val body: String,
) {
    init {
        require(title.isNotBlank()) { "需求标题不能为空" }
        require(body.isNotBlank()) { "需求正文不能为空" }
        require(body.codePointCount(0, body.length) <= MAX_BODY_CODE_POINTS) {
            "需求正文不能超过 $MAX_BODY_CODE_POINTS 个字符"
        }
    }

    companion object {
        const val MAX_BODY_CODE_POINTS = 4_000

        fun truncateBody(value: String): String {
            if (value.codePointCount(0, value.length) <= MAX_BODY_CODE_POINTS) return value
            return value.substring(0, value.offsetByCodePoints(0, MAX_BODY_CODE_POINTS))
        }
    }
}

/** 先在本机读取需求标题和正文，再把二者交给 Codex 生成命名。 */
fun interface RequirementAiContextProvider {
    fun fetch(requirementLink: String, projectKey: String?): RequirementAiContext
}

/** 在不接触仓库和任务配置的前提下，生成受约束的命名建议。 */
interface RequirementAiNamingService {
    fun suggest(
        context: RequirementAiContext,
        forbiddenFolderNames: Set<String> = emptySet(),
    ): RequirementAiNamingSuggestion
}
