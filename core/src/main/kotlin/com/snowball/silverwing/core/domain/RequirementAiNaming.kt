package com.snowball.silverwing.core

/**
 * The model identifier passed to the local Codex CLI for requirement naming.
 *
 * It intentionally stays a small, user-configurable CLI token instead of a
 * product-specific model enum: Codex can add models without requiring a
 * SilverWing release.
 */
object RequirementAiNamingModel {
    const val DEFAULT = "gpt-5.6-luna"

    fun requireValid(value: String): String {
        require(value.isNotBlank()) { "AI 命名模型不能为空" }
        require(value == value.trim()) { "AI 命名模型不能包含首尾空白" }
        require(value.none(Char::isWhitespace)) { "AI 命名模型不能包含空白字符" }
        return value
    }
}

/**
 * A deliberately small result returned by the local Codex CLI for one requirement.
 *
 * The folder name is shown in the creation form only; it is not persisted as a separate
 * requirement summary. The branch suffix is kept separate so SilverWing can preserve the
 * currently selected project's branch prefix locally instead of exposing that configuration
 * to the model.
 */
data class RequirementAiNamingSuggestion(
    val folderName: String,
    val branchSuffix: String,
)

/** 模型输出不可信，写入创建任务草稿前必须通过这些额外约束。 */
object RequirementAiNamingRules {
    const val MAX_FOLDER_CODE_POINTS = 12
    const val MAX_FOLDER_HAN_CHARACTERS = 6
    const val MAX_BRANCH_SUFFIX_WORDS = 4

    private val branchSuffixPattern = Regex("[a-z0-9]+(?:_[a-z0-9]+){0,3}")

    /**
     * Validates the AI-specific contract in addition to the normal Windows directory contract.
     * Keeping this stricter rule separate avoids changing what users may type manually.
     */
    fun requireValid(suggestion: RequirementAiNamingSuggestion): RequirementAiNamingSuggestion {
        val folderName = suggestion.folderName.trim()
        val branchSuffix = suggestion.branchSuffix.trim()
        require(folderName == suggestion.folderName) { "AI 生成的文件夹名不能包含首尾空白" }
        require(branchSuffix == suggestion.branchSuffix) { "AI 生成的分支后缀不能包含首尾空白" }
        require(TaskNaming.directoryNameValidationError(folderName) == null) {
            TaskNaming.directoryNameValidationError(folderName) ?: "AI 生成的文件夹名不合法"
        }
        val codePointCount = folderName.codePointCount(0, folderName.length)
        require(codePointCount <= MAX_FOLDER_CODE_POINTS) {
            "AI 生成的文件夹名不能超过 $MAX_FOLDER_CODE_POINTS 个字符"
        }
        val hanCount = folderName.codePoints().toArray().count { codePoint ->
            Character.UnicodeScript.of(codePoint) == Character.UnicodeScript.HAN
        }
        require(hanCount in 1..MAX_FOLDER_HAN_CHARACTERS) {
            "AI 生成的文件夹名必须含有 1 到 $MAX_FOLDER_HAN_CHARACTERS 个汉字"
        }
        require(hanCount >= codePointCount - hanCount) { "AI 生成的文件夹名应以中文为主" }
        require(branchSuffixPattern.matches(branchSuffix)) {
            "AI 生成的分支后缀只能包含小写英文、数字和下划线，且最多 $MAX_BRANCH_SUFFIX_WORDS 个单词"
        }
        return RequirementAiNamingSuggestion(folderName, branchSuffix)
    }

    /** 仅把已在本机解析的组前缀与校验后的模型后缀拼接，前缀不会发送给模型。 */
    fun composeBranch(resolvedPrefix: String, branchSuffix: String): String = when {
        resolvedPrefix.isBlank() -> branchSuffix
        resolvedPrefix.endsWith('/') || resolvedPrefix.endsWith('_') || resolvedPrefix.endsWith('-') ->
            resolvedPrefix + branchSuffix
        else -> "${resolvedPrefix}_$branchSuffix"
    }
}
