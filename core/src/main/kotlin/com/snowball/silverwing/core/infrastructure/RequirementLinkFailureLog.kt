package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.nio.file.Files
import java.nio.file.StandardOpenOption
import java.time.Clock
import java.time.Instant
import java.time.LocalDate
import kotlin.io.path.createDirectories
import kotlin.io.path.name

/** Keeps query failures out of the UI while retaining a short, local diagnostic trail. */
class RequirementLinkFailureLog(private val paths: ApplicationPaths = ApplicationPaths.systemDefault(), private val clock: Clock = Clock.systemUTC()) {
    private val json = Json { encodeDefaults = true }
    fun record(failure: RequirementLinkFailure) = runCatching {
        paths.logs.createDirectories()
        val values = mapOf("timestamp" to SilverWingTime.format(Instant.now(clock)), "source" to "meegle", "stage" to failure.stage, "projectKey" to failure.projectKey.orEmpty(), "sprintId" to failure.sprintId.orEmpty(), "workItemType" to failure.workItemType.orEmpty(), "message" to failure.message.take(300))
        Files.writeString(paths.logs.resolve("requirement-links-${SilverWingTime.localDate(Instant.now(clock))}.jsonl"), json.encodeToString(values) + System.lineSeparator(), StandardOpenOption.CREATE, StandardOpenOption.APPEND)
    }
    fun cleanup() = runCatching {
        val cutoff = SilverWingTime.localDate(Instant.now(clock)).minusDays(14)
        if (Files.exists(paths.logs)) Files.list(paths.logs).use { stream -> stream.filter { it.name.matches(Regex("requirement-links-\\d{4}-\\d{2}-\\d{2}\\.jsonl")) }.forEach { file ->
            if (LocalDate.parse(file.name.removePrefix("requirement-links-").removeSuffix(".jsonl")).isBefore(cutoff)) Files.deleteIfExists(file)
        } }
    }
}
