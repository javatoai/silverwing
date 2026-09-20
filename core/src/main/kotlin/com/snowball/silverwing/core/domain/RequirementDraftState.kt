package com.snowball.silverwing.core

/** Pure creation-form state that protects explicit user edits from asynchronous metadata results. */
data class RequirementDraftState(
    val requirementLink: String = "",
    val taskName: String = "",
    val requirementTitle: String? = null,
    val branch: String = "",
    val nameEdited: Boolean = false,
    val branchEdited: Boolean = false,
    val metadataLoading: Boolean = false,
    val metadataHint: String? = null,
) {
    fun changeRequirement(value: String, branchPrefix: String, title: String? = null): RequirementDraftState {
        val resolved = BranchPrefixResolver.resolve(branchPrefix, value)
        return copy(
            requirementLink = value,
            requirementTitle = title?.takeIf(String::isNotBlank),
            branch = if (branchEdited) branch else resolved ?: branchPrefix,
            metadataLoading = FeishuWorkItemLink.parse(value) != null,
            metadataHint = if (BranchPrefixResolver.containsUnresolvedPlaceholder(branchPrefix) && resolved == null) {
                "未从需求编号或链接中解析到编号"
            } else null,
        )
    }

    fun changeGroup(branchPrefix: String): RequirementDraftState = copy(
        branch = if (branchEdited) branch else BranchPrefixResolver.resolve(branchPrefix, requirementLink) ?: branchPrefix,
        metadataHint = if (!branchEdited && BranchPrefixResolver.containsUnresolvedPlaceholder(branchPrefix) &&
            BranchPrefixResolver.resolve(branchPrefix, requirementLink) == null
        ) "未从需求编号或链接中解析到编号" else null,
    )

    fun editName(value: String): RequirementDraftState = copy(taskName = value, nameEdited = true)

    fun editBranch(value: String): RequirementDraftState = copy(branch = value, branchEdited = true)

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
