package com.snowball.silverwing.core

/** Extensible source of selectable requirement links. New systems subclass this type and are registered at composition. */
abstract class RequirementLinkSource {
    abstract val sourceId: String
    abstract fun isInstalled(): Boolean
    abstract suspend fun load(projects: List<MeegleProjectConfig>): RequirementLinkLoadResult
}

data class RequirementLinkCandidate(val title: String, val url: String, val sourceId: String)
data class RequirementLinkLoadResult(val candidates: List<RequirementLinkCandidate>, val failures: List<RequirementLinkFailure> = emptyList())
data class RequirementLinkFailure(val stage: String, val projectKey: String? = null, val sprintId: String? = null, val workItemType: String? = null, val message: String)

class RequirementLinkSourceRegistry(sources: List<RequirementLinkSource>) {
    private val values = sources.associateBy(RequirementLinkSource::sourceId)
    fun source(id: String): RequirementLinkSource? = values[id]
}
