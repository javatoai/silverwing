package com.snowball.silverwing.core

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import java.time.Duration
import java.util.Locale

class MeegleRequirementMetadataProvider(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val isWindows: Boolean = System.getProperty("os.name")
        .lowercase(Locale.ROOT)
        .contains("win"),
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(isWindows),
) : ProjectScopedRequirementMetadataProvider {
    override fun fetch(requirementLink: String): RequirementMetadata? =
        FeishuWorkItemLink.parse(requirementLink)?.projectKey?.let { projectKey ->
            fetch(requirementLink, projectKey)
        }

    override fun fetch(requirementLink: String, projectKey: String): RequirementMetadata? {
        val workItem = FeishuWorkItemLink.parse(requirementLink) ?: return null
        val result = runCatching {
            val command = meegleExecutable.resolve()
            runner.run(
                command = listOf(
                    command,
                    "workitem",
                    "get",
                    "--project-key",
                    projectKey,
                    "--work-item-id",
                    workItem.workItemId,
                    "--format",
                    "json",
                ),
                // Metadata is optional while creating a task. A short bound
                // prevents an unavailable local CLI from blocking the form.
                timeout = Duration.ofSeconds(8),
                environment = meegleExecutable.environment(),
            )
        }.getOrNull() ?: return null
        if (!result.succeeded) return null
        return runCatching {
            val root = json.parseToJsonElement(result.stdout).jsonObject
            val attributes = root["work_item_attribute"] as? JsonObject
            RequirementMetadata(
                title = sequenceOf(
                    root["name"],
                    root["work_item_name"],
                    attributes?.get("name"),
                    attributes?.get("work_item_name"),
                ).mapNotNull { it.textOrNull() }.firstOrNull(),
                status = (attributes?.get("work_item_status") as? JsonObject)
                    ?.get("name")
                    .textOrNull(),
                participants = participantsFor(workItem.kind, attributes?.get("role_members") as? JsonArray),
            ).takeIf { attributes != null || it.title != null || it.status != null || !it.participants.isEmpty }
        }.getOrNull()
    }

    private fun participantsFor(kind: String, roles: JsonArray?): RequirementParticipants {
        val qcOwners = membersFor(roles, "QC Owner")
        return when (kind) {
            "userstory" -> RequirementParticipants(
                qcOwners = qcOwners,
                productManagers = membersFor(roles, "产品经理"),
            )
            "technical", "bug" -> RequirementParticipants(qcOwners = qcOwners)
            else -> RequirementParticipants()
        }
    }

    private fun membersFor(roles: JsonArray?, roleName: String): List<RequirementPerson> = roles
        .orEmpty()
        .mapNotNull { it as? JsonObject }
        .filter { role -> (role["name"] as? JsonPrimitive)?.contentOrNull == roleName }
        .flatMap { role -> (role["members"] as? JsonArray).orEmpty() }
        .mapNotNull { member ->
            val value = member as? JsonObject ?: return@mapNotNull null
            value["name"].textOrNull()?.let { name ->
                RequirementPerson(name, value["email"].textOrNull())
            }
        }

    private fun JsonElement?.textOrNull(): String? =
        (this as? JsonPrimitive)?.contentOrNull?.trim()?.ifBlank { null }

    private companion object {
        val json = Json { ignoreUnknownKeys = true }
    }
}
