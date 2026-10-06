package com.snowball.silverwing.core

import java.nio.file.Path
import java.time.Clock
import java.time.Instant
import java.util.concurrent.CancellationException

/** Persists read-only Genbu status beside every Git Tag operation with an exact Tag. */
class GenbuTagProbeService(
    private val operations: TagOperationStore = TagOperationStore(),
    private val genbu: GenbuTagStatusProvider = ProcessGenbuTagStatusService(),
    private val clock: Clock = Clock.systemUTC(),
) {
    fun probe(config: AppConfig, tasks: List<TaskManifest>, force: Boolean = false): Boolean {
        checkProbeInterrupted()
        val taskRoot = config.taskRoot?.takeIf(String::isNotBlank)?.let(Path::of) ?: return false
        val candidates = tasks.flatMap { task -> candidatesForTask(config, taskRoot, task) }
        var changed = false
        candidates.groupBy { it.genbuServiceName }.values.forEach { serviceCandidates ->
            checkProbeInterrupted()
            changed = probeService(serviceCandidates.sortedByDescending { it.operation.createdAt }, force) || changed
        }
        return changed
    }

    private fun candidatesForTask(config: AppConfig, taskRoot: Path, task: TaskManifest): List<Candidate> {
        val group = config.groups.firstOrNull { it.id == task.groupId } ?: return emptyList()
        val services = group.services.associateBy(GroupServiceConfig::id)
        val directory = taskRoot.resolve(task.taskDirectoryName)
        return runCatching { operations.list(directory) }.getOrDefault(emptyList()).mapNotNull { operation ->
            val service = services[operation.groupServiceId] ?: return@mapNotNull null
            if (!service.genbuProbeEnabled || operation.tag.isNullOrBlank()) return@mapNotNull null
            Candidate(directory, operation, service.genbuServiceName.trim())
        }
    }

    private fun probeService(candidates: List<Candidate>, force: Boolean): Boolean {
        var changed = false
        val latestReleasedIndex = candidates.indexOfFirst { it.operation.genbuStatus.uat == GenbuStageStatus.SUCCESS }
        if (!force && latestReleasedIndex >= 0) {
            changed = stopOlderCandidates(candidates.drop(latestReleasedIndex + 1)) || changed
        }
        val candidatesToProbe = if (force || latestReleasedIndex < 0) candidates else candidates.take(latestReleasedIndex)
        candidatesToProbe.forEachIndexed { index, candidate ->
            checkProbeInterrupted()
            val status = candidate.operation.genbuStatus
            if (!force && status.isTerminal()) return@forEachIndexed
            val queried = query(candidate.genbuServiceName, requireNotNull(candidate.operation.tag))
            checkProbeInterrupted()
            val update = operations.updateIfUnchanged(candidate.taskDirectory, candidate.operation) { current ->
                checkProbeInterrupted()
                current.copy(genbuStatus = refreshed(current.genbuStatus, queried))
            }
            if (update?.changed == true) changed = true
            val updated = update?.operation
            if (!force && update?.changed == true && updated?.genbuStatus?.uat == GenbuStageStatus.SUCCESS) {
                changed = stopOlderCandidates(candidates.drop(index + 1)) || changed
                return changed
            }
        }
        return changed
    }

    /**
     * Probes one persisted operation on demand. Unlike background polling this
     * ignores the terminal-state skip policy: an explicit CLI status query
     * always asks Genbu for a live answer. Returns the (possibly refreshed)
     * operation, or null when no such record exists.
     */
    fun probeOperation(config: AppConfig, task: TaskManifest, operationId: String): TagOperation? {
        checkProbeInterrupted()
        val taskRoot = config.taskRoot?.takeIf(String::isNotBlank)?.let(Path::of) ?: return null
        val taskDirectory = taskRoot.resolve(task.taskDirectoryName)
        val operation = runCatching { operations.load(taskDirectory, operationId) }.getOrNull() ?: return null
        val group = config.groups.firstOrNull { it.id == task.groupId } ?: return operation
        val service = group.services.firstOrNull { it.id == operation.groupServiceId } ?: return operation
        val tag = operation.tag
        if (!service.genbuProbeEnabled || tag.isNullOrBlank()) return operation
        val queried = query(service.genbuServiceName.trim(), tag)
        checkProbeInterrupted()
        val update = operations.updateIfUnchanged(taskDirectory, operation) { current ->
            checkProbeInterrupted()
            current.copy(genbuStatus = refreshed(current.genbuStatus, queried))
        } ?: return null
        return update.operation
    }

    private fun refreshed(status: GenbuTagProbeStatus, queried: Result<GenbuTagQueryResult>): GenbuTagProbeStatus =
        queried.fold(
            onSuccess = { result -> status.copy(
                build = result.build,
                uat = result.uat,
                production = result.production,
                notFound = result.notFound,
                builtCompletedAt = result.builtCompletedAt,
                releasedCompletedAt = result.uatReleasedCompletedAt,
                productionReleasedCompletedAt = result.productionReleasedCompletedAt,
                checkedAt = SilverWingTime.format(Instant.now(clock)),
                failureReason = null,
            ) },
            onFailure = { error -> status.copy(
                notFound = false,
                checkedAt = SilverWingTime.format(Instant.now(clock)),
                failureReason = error.message ?: "Genbu 探测失败",
            ) },
        )

    private fun stopOlderCandidates(candidates: List<Candidate>): Boolean {
        var changed = false
        candidates.forEach { older ->
            checkProbeInterrupted()
            val oldStatus = older.operation.genbuStatus
            if (oldStatus.uat != GenbuStageStatus.SUCCESS && !oldStatus.stoppedByNewerRelease) {
                val update = operations.updateIfUnchanged(older.taskDirectory, older.operation) { current ->
                    checkProbeInterrupted()
                    if (current.genbuStatus.uat == GenbuStageStatus.SUCCESS || current.genbuStatus.stoppedByNewerRelease) {
                        current
                    } else {
                        current.copy(genbuStatus = current.genbuStatus.copy(
                            stoppedByNewerRelease = true,
                            failureReason = null,
                        ))
                    }
                }
                changed = (update?.changed == true) || changed
            }
        }
        return changed
    }

    private data class Candidate(
        val taskDirectory: Path,
        val operation: TagOperation,
        val genbuServiceName: String,
    )

    private fun query(service: String, tag: String): Result<GenbuTagQueryResult> = try {
        Result.success(genbu.query(service, tag))
    } catch (cancelled: CancellationException) {
        throw cancelled
    } catch (interrupted: InterruptedException) {
        throw interrupted
    } catch (error: Throwable) {
        Result.failure(error)
    }

    private fun checkProbeInterrupted() {
        if (Thread.currentThread().isInterrupted) throw InterruptedException("Genbu 探测已取消")
    }
}

/**
 * Terminal states stop automatic polling: a released Tag reached its goal, and a
 * failed build is final for its Tag — the UI offers a re-Tag action instead of
 * polling forever. The manual force refresh still re-queries these records.
 */
internal fun GenbuTagProbeStatus.isTerminal(): Boolean =
    uat == GenbuStageStatus.SUCCESS ||
        build == GenbuStageStatus.FAILED ||
        notFound ||
        stoppedByNewerRelease
