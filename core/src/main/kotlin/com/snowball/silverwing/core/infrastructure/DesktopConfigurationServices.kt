package com.snowball.silverwing.core

import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit

/** Read-only branch source used by the service editor; implementations must not fetch or mutate a repository. */
interface RemoteBranchCatalog {
    fun list(repository: Path, remote: String = "origin"): List<String>
}

class GitRemoteBranchCatalog(
    private val git: GitClient = GitClient(),
) : RemoteBranchCatalog {
    override fun list(repository: Path, remote: String): List<String> = git
        .run(repository, "ls-remote", "--heads", remote, timeout = Duration.ofSeconds(20))
        .stdout
        .lineSequence()
        .mapNotNull { line ->
            line.substringAfter("refs/heads/", "").trim().ifBlank { null }?.let { "$remote/$it" }
        }
        .distinct()
        .sorted()
        .toList()
}

/** Stable case-insensitive filtering used by the editable branch dropdown. */
object RemoteBranchSearch {
    fun filter(branches: List<String>, query: String): List<String> = branches
        .filter { query.isBlank() || it.contains(query.trim(), ignoreCase = true) }
}

data class TaskBranchCandidate(
    val branch: String,
    val sources: List<String>,
    val matchedWorkspaceCount: Int,
    val totalWorkspaceCount: Int,
)

data class TaskBranchCatalogResult(
    val candidates: List<TaskBranchCandidate>,
    val failures: List<String> = emptyList(),
)

data class TaskBranchCatalogProgress(
    val completed: Int,
    val total: Int,
)

interface TaskBranchCatalog {
    suspend fun list(
        config: AppConfig,
        groupId: String,
        serviceIds: Set<String>,
        onProgress: (TaskBranchCatalogProgress) -> Unit = {},
    ): TaskBranchCatalogResult
}

/** Lists remote heads only. It never fetches or updates local refs. */
class GitTaskBranchCatalog(private val git: GitClient = GitClient()) : TaskBranchCatalog {
    override suspend fun list(
        config: AppConfig,
        groupId: String,
        serviceIds: Set<String>,
        onProgress: (TaskBranchCatalogProgress) -> Unit,
    ): TaskBranchCatalogResult {
        val group = config.group(groupId)
        val repositories = config.repositories.associateBy(RepositoryConfig::id)
        val matches = linkedMapOf<String, MutableSet<String>>()
        val coverage = linkedMapOf<String, MutableSet<String>>()
        val failures = mutableListOf<String>()
        var totalWorkspaceCount = 0
        val contexts = mutableListOf<TaskBranchQueryContext>()
        group.services.filter { it.id in serviceIds && it.enabled }
            .forEach { service ->
                val repository = repositories[service.repositoryId]
                if (repository == null) {
                    failures += "${service.displayName}：仓库配置不存在"
                    return@forEach
                }
                val root = Path.of(repository.rootPath).toAbsolutePath().normalize()
                val physicalModules = service.modules.filter { it.strategy == WorkspaceStrategy.STANDARD_WORKTREE }
                if (physicalModules.isEmpty()) return@forEach
                totalWorkspaceCount += physicalModules.size
                val derived = TaskBranchNaming.derive("__silverwing_candidate__", service.modules)
                physicalModules.forEach { module ->
                    val workspaceKey = "${service.id}:${module.id}"
                    val suffix = derived.getValue(module.id).removePrefix("__silverwing_candidate__").removePrefix("-")
                    contexts += TaskBranchQueryContext(
                        serviceName = service.displayName,
                        root = root,
                        remote = module.baseRemote,
                        workspaceKey = workspaceKey,
                        suffix = suffix,
                        singlePhysicalModule = service.modules.size == 1,
                    )
                }
            }
        val queryGroups = contexts.groupBy { "${it.root}|${it.remote}" }
        onProgress(TaskBranchCatalogProgress(0, queryGroups.size))
        val completed = AtomicInteger()
        val semaphore = Semaphore(4)
        val results = coroutineScope {
            queryGroups.map { (key, consumers) ->
                async {
                    semaphore.withPermit {
                        val representative = consumers.first()
                        val result = try {
                            Result.success(runInterruptible {
                                git.run(
                                    representative.root,
                                    "ls-remote",
                                    "--heads",
                                    representative.remote,
                                    timeout = Duration.ofSeconds(20),
                                    check = false,
                                )
                            })
                        } catch (cancelled: CancellationException) {
                            throw cancelled
                        } catch (error: Throwable) {
                            Result.failure(error)
                        }
                        onProgress(TaskBranchCatalogProgress(completed.incrementAndGet(), queryGroups.size))
                        key to result
                    }
                }
            }.awaitAll().toMap()
        }
        queryGroups.forEach { (key, consumers) ->
            val queryResult = results.getValue(key)
            val result = queryResult.getOrNull()
            if (result == null || !result.succeeded) {
                val error = queryResult.exceptionOrNull()?.message
                    ?: result?.stderr?.ifBlank { null }
                    ?: "远程分支查询失败"
                consumers.forEach { context ->
                    failures += "${context.serviceName} · ${context.remote}：$error"
                }
            } else {
                val remoteBranches = result.stdout.lineSequence().mapNotNull { line ->
                        line.substringAfter("refs/heads/", "").trim().ifBlank { null }
                    }.toList()
                consumers.forEach { context ->
                    remoteBranches.forEach { remoteBranch ->
                        val candidate = if (context.singlePhysicalModule) {
                            remoteBranch
                        } else {
                            remoteBranch.removeSuffix("-${context.suffix}").takeIf { it != remoteBranch && it.isNotBlank() }
                        } ?: return@forEach
                        matches.getOrPut(candidate) { linkedSetOf() } += "${context.serviceName} · ${context.remote}"
                        coverage.getOrPut(candidate) { linkedSetOf() } += context.workspaceKey
                    }
                }
            }
        }
        return TaskBranchCatalogResult(
            candidates = matches.map { (branch, sources) ->
                TaskBranchCandidate(branch, sources.sorted(), coverage[branch].orEmpty().size, totalWorkspaceCount)
            }.sortedBy(TaskBranchCandidate::branch),
            failures = failures.distinct(),
        )
    }
}

private data class TaskBranchQueryContext(
    val serviceName: String,
    val root: Path,
    val remote: String,
    val workspaceKey: String,
    val suffix: String,
    val singlePhysicalModule: Boolean,
)

fun interface DevelopmentToolRecommendationService {
    fun recommend(repositoryRoot: Path): DevelopmentToolType
}

class RootMarkerDevelopmentToolRecommendationService : DevelopmentToolRecommendationService {
    override fun recommend(repositoryRoot: Path): DevelopmentToolType {
        val jvmMarkers = listOf("settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts", "pom.xml")
        if (jvmMarkers.any { Files.exists(repositoryRoot.resolve(it)) }) return DevelopmentToolType.INTELLIJ_IDEA
        val webMarkers = listOf("package.json", "pnpm-workspace.yaml", "yarn.lock", "package-lock.json", "bun.lock", "bun.lockb")
        return if (webMarkers.any { Files.exists(repositoryRoot.resolve(it)) }) {
            DevelopmentToolType.WEBSTORM
        } else {
            DevelopmentToolType.INTELLIJ_IDEA
        }
    }
}
