package com.snowball.silverwing.core

import kotlinx.serialization.Serializable

@Serializable
data class RequirementComment(
    val id: String,
    val content: String,
    val authorKey: String,
    val authorName: String,
    /** Keep the time returned by Meegle, including its original timezone/format. */
    val createdAt: String,
    val updatedAt: String? = null,
    val parentId: String? = null,
    val attachmentUrl: String? = null,
)

@Serializable
data class RequirementComments(val comments: List<RequirementComment>, val warnings: List<String> = emptyList())
