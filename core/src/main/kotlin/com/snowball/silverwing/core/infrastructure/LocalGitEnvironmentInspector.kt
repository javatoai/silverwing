package com.snowball.silverwing.core

import java.time.Duration

data class GitConfigValue(
    val key: String,
    val value: String,
    val origin: String? = null,
)

data class LocalGitEnvironmentSnapshot(
    val gitExecutable: String?,
    val gitVersion: String?,
    val systemUser: String,
    val globalUserName: GitConfigValue?,
    val globalUserEmail: GitConfigValue?,
    val globalCredentialHelpers: List<GitConfigValue>,
    val globalKeyConfig: List<GitConfigValue>,
    val errors: List<String>,
)

/**
 * Reads the local Git executable, version and global config only. It never
 * opens a configured repository or runs a command that can contact a remote.
 */
class LocalGitEnvironmentInspector(
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val gitExecutable: GitExecutable = ConfiguredGitExecutable({ null }, runner),
) {
    fun inspect(): LocalGitEnvironmentSnapshot {
        val errors = mutableListOf<String>()
        val executable = gitExecutable.resolve()
        val version = runLocal(listOf(executable, "--version"), errors, "读取 Git 版本")?.trim()
        val globalConfig = runLocal(
            listOf(executable, "config", "--global", "--show-origin", "--list"),
            errors,
            "读取全局 Git 配置",
        )?.let(::parseConfigValues).orEmpty()
        return LocalGitEnvironmentSnapshot(
            gitExecutable = executable.takeIf { gitExecutable.source() != GitCommandSource.PATH_FALLBACK },
            gitVersion = version,
            systemUser = System.getProperty("user.name").orEmpty(),
            globalUserName = globalConfig.lastOrNull { it.key == "user.name" },
            globalUserEmail = globalConfig.lastOrNull { it.key == "user.email" },
            globalCredentialHelpers = globalConfig.filter { it.key == "credential.helper" },
            globalKeyConfig = globalConfig.filter(::isKeyConfig),
            errors = errors,
        )
    }

    private fun runLocal(command: List<String>, errors: MutableList<String>, label: String): String? {
        val result = runCatching { runner.run(command, timeout = Duration.ofSeconds(10)) }
            .getOrElse { error ->
                errors += "$label 失败：${error.message ?: error::class.simpleName}"
                return null
            }
        if (!result.succeeded) {
            errors += "$label 失败：${result.stderr.ifBlank { result.stdout }.trim().ifBlank { "退出码 ${result.exitCode}" }}"
            return null
        }
        return result.stdout
    }

    companion object {
        internal fun parseConfigValues(output: String): List<GitConfigValue> = output.lineSequence().mapNotNull { raw ->
            val line = raw.trim()
            if (line.isEmpty()) return@mapNotNull null
            val tab = line.indexOf('\t')
            val parts = if (tab >= 0) listOf(line.substring(0, tab), line.substring(tab + 1))
            else line.split(Regex("\\s+"), limit = 2)
            val origin = parts.takeIf { it.size == 2 }?.first()
            val keyValue = if (parts.size == 2) parts[1] else parts[0]
            val separator = keyValue.indexOf('=')
            if (separator < 0) GitConfigValue(keyValue, "", origin)
            else GitConfigValue(keyValue.substring(0, separator), keyValue.substring(separator + 1), origin)
        }.toList()

        private fun isKeyConfig(value: GitConfigValue): Boolean = value.key in setOf(
            "user.name",
            "user.email",
            "credential.helper",
            "core.autocrlf",
            "core.ignorecase",
            "core.longpaths",
            "pull.rebase",
            "push.default",
            "init.defaultbranch",
            "commit.gpgsign",
            "user.signingkey",
        )
    }
}
