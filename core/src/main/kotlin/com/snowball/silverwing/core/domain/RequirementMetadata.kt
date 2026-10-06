package com.snowball.silverwing.core

import java.util.Locale
import kotlinx.serialization.Serializable

@Serializable
data class RequirementPerson(val name: String, val email: String? = null)

@Serializable
data class RequirementParticipants(
    val qcOwners: List<RequirementPerson> = emptyList(),
    val productManagers: List<RequirementPerson> = emptyList(),
) {
    val isEmpty: Boolean get() = qcOwners.isEmpty() && productManagers.isEmpty()
}

@Serializable
data class RequirementMetadata(
    val title: String? = null,
    val status: String?,
    val participants: RequirementParticipants = RequirementParticipants(),
)

data class FeishuWorkItemLink(
    val space: String,
    val kind: String,
    val workItemId: String,
    val projectKey: String?,
) {
    companion object {
        private val linkPattern = Regex(
            """^https?://project\.feishu\.cn/([A-Za-z0-9_-]+)/(userstory|othertask|bug|technical)/detail/(\d+)(?:[/?#].*)?$""",
            RegexOption.IGNORE_CASE,
        )
        private val projectKeys = mapOf(
            "obt" to "67c17e40bf0d47db9549cb08",
            "rta" to "680b2de0f1ddf8d50a24dff8",
        )

        fun parse(value: String): FeishuWorkItemLink? {
            val normalized = value.trim().trimEnd(',', '，', '。', ';', '；')
            val match = linkPattern.matchEntire(normalized) ?: return null
            val space = match.groupValues[1].lowercase(Locale.ROOT)
            return FeishuWorkItemLink(
                space = space,
                kind = match.groupValues[2].lowercase(Locale.ROOT),
                workItemId = match.groupValues[3],
                projectKey = projectKeys[space],
            )
        }
    }
}
