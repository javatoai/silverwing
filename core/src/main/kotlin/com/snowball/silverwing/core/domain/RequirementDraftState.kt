package com.snowball.silverwing.core

/** 纯创建表单状态：异步结果不得覆盖用户已经明确修改过的字段。 */
data class RequirementDraftState(
    val requirementLink: String = "",
    val taskName: String = "",
    val requirementTitle: String? = null,
    val branch: String = "",
    val nameEdited: Boolean = false,
    val branchEdited: Boolean = false,
    /** 分支仍采用 AI 后缀时保留它，以便切换项目组后重新拼接本机前缀。 */
    val aiBranchSuffix: String? = null,
    val metadataLoading: Boolean = false,
    val metadataHint: String? = null,
) {
    fun changeRequirement(value: String, branchPrefix: String, title: String? = null): RequirementDraftState {
        val resolved = BranchPrefixResolver.resolve(branchPrefix, value)
        return copy(
            requirementLink = value,
            requirementTitle = title?.takeIf(String::isNotBlank),
            branch = if (branchEdited) branch else resolved ?: branchPrefix,
            aiBranchSuffix = null,
            metadataLoading = FeishuWorkItemLink.parse(value) != null,
            metadataHint = if (BranchPrefixResolver.containsUnresolvedPlaceholder(branchPrefix) && resolved == null) {
                "未从需求编号或链接中解析到编号"
            } else null,
        )
    }

    fun changeGroup(branchPrefix: String): RequirementDraftState = copy(
        branch = if (branchEdited) {
            branch
        } else {
            val resolved = BranchPrefixResolver.resolve(branchPrefix, requirementLink) ?: branchPrefix
            aiBranchSuffix?.let { suffix -> RequirementAiNamingRules.composeBranch(resolved, suffix) } ?: resolved
        },
        metadataHint = if (!branchEdited && BranchPrefixResolver.containsUnresolvedPlaceholder(branchPrefix) &&
            BranchPrefixResolver.resolve(branchPrefix, requirementLink) == null
        ) "未从需求编号或链接中解析到编号" else null,
    )

    fun editName(value: String): RequirementDraftState = copy(taskName = value, nameEdited = true)

    fun editBranch(value: String): RequirementDraftState = copy(branch = value, branchEdited = true, aiBranchSuffix = null)

    /** 只把最新模型建议写入尚未被用户手工编辑的字段。 */
    fun applyAiNaming(
        requestedLink: String,
        suggestion: RequirementAiNamingSuggestion,
        branchPrefix: String,
    ): RequirementDraftState {
        if (requestedLink != requirementLink) return this
        val validated = RequirementAiNamingRules.requireValid(suggestion)
        val resolvedPrefix = BranchPrefixResolver.resolve(branchPrefix, requirementLink) ?: branchPrefix
        return copy(
            taskName = if (nameEdited) taskName else validated.folderName,
            branch = if (branchEdited) branch else RequirementAiNamingRules.composeBranch(resolvedPrefix, validated.branchSuffix),
            aiBranchSuffix = if (branchEdited) null else validated.branchSuffix,
        )
    }

    fun applyMetadata(requestedLink: String, metadata: RequirementMetadata?): RequirementDraftState {
        if (requestedLink != requirementLink) return this
        val title = metadata?.title?.takeIf(String::isNotBlank)
        return copy(
            requirementTitle = title,
            metadataLoading = false,
            metadataHint = if (metadata == null) "未获取到需求标题，可手工填写" else metadataHint,
        )
    }
}
