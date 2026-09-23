package com.snowball.silverwing.core

import kotlinx.serialization.encodeToString
import kotlinx.serialization.builtins.SetSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.Locale
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.createDirectories
import kotlin.io.path.exists
import kotlin.io.path.name

class DiagnosticsExporter(
    private val paths: ApplicationPaths = ApplicationPaths.systemDefault(),
    private val runner: CommandRunner = ProcessCommandRunner(),
    private val git: GitClient = GitClient(runner),
    private val json: Json = Json { prettyPrint = true; encodeDefaults = true },
    private val meegleExecutable: MeegleExecutable = MeegleExecutable.pathFallback(),
) {
    fun export(config: AppConfig, manifestFailures: String? = null): Path {
        paths.diagnostics.createDirectories()
        val target = paths.diagnostics.resolve("silverwing-diagnostics-${System.currentTimeMillis()}.zip")
        ZipOutputStream(Files.newOutputStream(target)).use { zip ->
            zip.text("system.txt", systemSummary())
            zip.text("config-summary.json", sanitizedConfigJson(config))
            zip.text("repositories.txt", repositorySummary(config))
            manifestFailures?.takeIf(String::isNotBlank)?.let { zip.text("task-scan-failures.txt", it) }
            if (paths.logs.exists()) {
                Files.list(paths.logs).use { files ->
                    files.filter { it.name.startsWith("application-") && it.name.endsWith(".jsonl") }
                        .sorted(Comparator.reverseOrder())
                        .limit(3)
                        .forEach { file -> zip.file("logs/${file.name}", file) }
                }
            }
        }
        return target
    }

    private fun systemSummary(): String = buildString {
        appendLine("silverwing config schema: $CURRENT_APP_CONFIG_SCHEMA_VERSION")
        appendLine("OS: ${System.getProperty("os.name")} ${System.getProperty("os.version")} ${System.getProperty("os.arch")}")
        appendLine("Java: ${System.getProperty("java.version")} (${System.getProperty("java.vendor")})")
        appendLine("Git: ${gitVersion()}")
        val meegle = meegleExecutable.resolve()
        val environment = meegleExecutable.environment()
        appendLine("Meegle: ${command(listOf(meegle, "--version"), environment)}")
        appendLine("Meegle auth: ${command(listOf(meegle, "auth", "status", "--format", "json"), environment)}")
    }

    private fun repositorySummary(config: AppConfig): String = buildString {
        config.repositories.forEach { repository ->
            appendLine("[${repository.id}] ${repository.name}")
            appendLine("root=${redactUserHome(repository.rootPath)}")
            val root = Path.of(repository.rootPath)
            runCatching {
                git.worktrees(root).forEach { record ->
                    appendLine("worktree=${redactUserHome(record.path.toString())} branch=${record.branch} locked=${record.locked} detached=${record.detached}")
                }
            }.onFailure { appendLine("worktree-error=${it.message}") }
            appendLine()
        }
    }

    private fun command(command: List<String>, environment: Map<String, String> = emptyMap()): String = runCatching {
        val result = runner.run(command, timeout = Duration.ofSeconds(10), environment = environment)
        result.stdout.ifBlank { result.stderr }.trim().ifBlank { "exit=${result.exitCode}" }
    }.getOrElse { "unavailable: ${it.message}" }

    private fun AppConfig.sanitizedForDiagnostics(): AppConfig = copy(
        taskRoot = taskRoot?.let(::redactUserHome),
        repositories = repositories.map { repository ->
            repository.copy(
                rootPath = redactUserHome(repository.rootPath),
                gitCommonDirectory = redactUserHome(repository.gitCommonDirectory),
                originUrl = repository.originUrl?.replace(Regex("(?i)(https?://)[^/@]+@"), "$1***@"),
            )
        },
        developmentTools = developmentTools.map { it.copy(path = "<configured:${it.type.name}>") },
        terminalExecutable = terminalExecutable?.let { "<configured>" },
        meegleExecutablePath = meegleExecutablePath?.let { "<configured>" },
        codexExecutablePath = codexExecutablePath?.let { "<configured>" },
        // AppConfig validates proxy endpoints. Leave the field null while copying and
        // replace it in the serialized JSON below with a non-sensitive status marker.
        commandProxyUrl = null,
        commandProxyNoProxy = null,
        commandProxyUsername = null,
        commandProxyPassword = null,
        commandProxyTargets = emptySet(),
        larkExecutablePath = larkExecutablePath?.let { "<configured>" },
        gitExecutablePath = gitExecutablePath?.let { "<configured>" },
        genbuExecutablePath = genbuExecutablePath?.let { "<configured>" },
    )

    private fun sanitizedConfigJson(config: AppConfig): String {
        val safeConfig = config.sanitizedForDiagnostics()
        val configJson = json.encodeToJsonElement(AppConfig.serializer(), safeConfig).jsonObject
        val redacted = if (config.commandProxyUrl == null) {
            configJson
        } else {
            JsonObject(
                configJson + mapOf(
                    "commandProxyUrl" to JsonPrimitive("<configured>"),
                    "commandProxyTargets" to json.encodeToJsonElement(
                        SetSerializer(CommandProxyTarget.serializer()),
                        config.commandProxyTargets,
                    ),
                ),
            )
        }
        return json.encodeToString(redacted)
    }

    private fun gitVersion(): String = runCatching {
        val result = git.version()
        result.stdout.ifBlank { result.stderr }.trim().ifBlank { "exit=${result.exitCode}" }
    }.getOrElse { "unavailable: ${it.message}" }

    private fun redactUserHome(value: String): String {
        val home = System.getProperty("user.home").orEmpty()
        return if (home.isNotBlank()) value.replace(home, "<USER_HOME>", ignoreCase = true) else value
    }

    private fun ZipOutputStream.text(name: String, content: String) {
        putNextEntry(ZipEntry(name))
        write(content.toByteArray(StandardCharsets.UTF_8))
        closeEntry()
    }

    private fun ZipOutputStream.file(name: String, file: Path) {
        putNextEntry(ZipEntry(name))
        Files.copy(file, this)
        closeEntry()
    }
}
