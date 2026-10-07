package com.snowball.silverwing.core

import kotlinx.serialization.json.*
import java.time.Duration
import java.util.Locale

sealed interface MeegleRequirementIdentityResult {
    data class Authenticated(val identity: String) : MeegleRequirementIdentityResult
    data object LoggedOut : MeegleRequirementIdentityResult
    data object Unavailable : MeegleRequirementIdentityResult
}

/** Only non-secret identity fields are used; unknown or logged-out identities never share a cache. */
class MeegleRequirementCacheIdentity(
    private val runner: CommandRunner,
    private val executable: MeegleExecutable,
) {
    fun read(): String? = (check() as? MeegleRequirementIdentityResult.Authenticated)?.identity

    fun check(): MeegleRequirementIdentityResult {
        val command = executable.resolve()
        val environment = executable.environment()
        // Unlike auth status, this local command identifies the active profile without reading credentials.
        // CLI versions that cannot establish it simply opt out of persistent caching.
        val profileResult = runner.run(listOf(command, "config", "profile", "current", "--format", "json"),
            timeout = Duration.ofSeconds(10), environment = environment)
        if (!profileResult.succeeded) return MeegleRequirementIdentityResult.Unavailable
        val profile = profileResult.stdout.trim().takeIf {
            it.isNotBlank() && it.length <= 256 && it.lineSequence().count() == 1
        } ?: return MeegleRequirementIdentityResult.Unavailable
        fun read(vararg arguments: String): JsonObject? {
            val result = runner.run(listOf(command) + arguments + listOf("--format", "json"),
                timeout = Duration.ofSeconds(15), environment = environment)
            if (!result.succeeded) return null
            return Json.parseToJsonElement(result.stdout) as? JsonObject
        }
        val auth = read("auth", "status") ?: return MeegleRequirementIdentityResult.Unavailable
        when ((auth["authenticated"] as? JsonPrimitive)?.booleanOrNull) {
            false -> return MeegleRequirementIdentityResult.LoggedOut
            null -> return MeegleRequirementIdentityResult.Unavailable
            true -> Unit
        }
        val host = auth.text("host")?.lowercase(Locale.ROOT) ?: return MeegleRequirementIdentityResult.Unavailable
        val user = read("user", "me") ?: return MeegleRequirementIdentityResult.Unavailable
        val account = user.text("user_key") ?: return MeegleRequirementIdentityResult.Unavailable
        return MeegleRequirementIdentityResult.Authenticated(buildJsonArray {
            add("meegle"); add(command); add(profile); add(host); add(account)
            // Project IDs are also part of every cache key. Retain tenant fields when the CLI exposes them.
            for (key in listOf("tenant_key", "tenant_id", "enterprise_id")) {
                add(auth.text(key) ?: user.text(key) ?: "")
            }
        }.toString())
    }

    private fun JsonObject.text(key: String): String? =
        (get(key) as? JsonPrimitive)?.contentOrNull?.trim()?.takeIf(String::isNotEmpty)
}
