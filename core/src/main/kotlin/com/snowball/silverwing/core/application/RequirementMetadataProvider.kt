package com.snowball.silverwing.core

/** Retrieves optional requirement metadata; failures are non-blocking to task creation. */
fun interface RequirementMetadataProvider {
    fun fetch(requirementLink: String): RequirementMetadata?
}

/**
 * Optional capability for sources whose displayed Feishu space name differs from the Meegle
 * project key required by the local CLI. Keeping this separate preserves the simple provider
 * contract for metadata sources that only understand a URL.
 */
interface ProjectScopedRequirementMetadataProvider : RequirementMetadataProvider {
    fun fetch(requirementLink: String, projectKey: String): RequirementMetadata?
}

/** Uses a source-specific project key when the provider supports it, otherwise falls back to URL lookup. */
fun RequirementMetadataProvider.fetch(requirementLink: String, projectKey: String?): RequirementMetadata? =
    if (projectKey != null && this is ProjectScopedRequirementMetadataProvider) {
        fetch(requirementLink, projectKey)
    } else {
        fetch(requirementLink)
    }
