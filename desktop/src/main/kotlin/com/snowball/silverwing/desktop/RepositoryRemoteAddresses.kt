package com.snowball.silverwing.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.snowball.silverwing.core.RepositoryRemoteAddress
import com.snowball.silverwing.core.RepositoryRemoteAddressCatalog
import com.snowball.silverwing.core.RepositoryRemoteAddressFailure
import com.snowball.silverwing.core.RepositoryRemoteAddressFailureKind
import com.snowball.silverwing.core.RepositoryRemoteAddressReadException
import com.snowball.silverwing.core.RepositoryRemoteAddressReadStage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import java.nio.file.Path

internal data class RepositoryAddressKey(val id: String, val path: String)
internal sealed interface RepositoryAddressesState {
    data object Loading : RepositoryAddressesState
    data class Loaded(val addresses: List<RepositoryRemoteAddress>) : RepositoryAddressesState
    data object Failed : RepositoryAddressesState
}

/** 多组共享仓库缓存；路径变化和强制刷新使旧请求失效，不轮询或访问远程网络。 */
internal class RepositoryRemoteAddresses(
    private val catalog: RepositoryRemoteAddressCatalog,
    private val scope: CoroutineScope,
    private val dispatcher: CoroutineDispatcher,
) {
    private var states by mutableStateOf<Map<RepositoryAddressKey, RepositoryAddressesState>>(emptyMap())
    private var failures by mutableStateOf<Map<RepositoryAddressKey, RepositoryRemoteAddressFailure>>(emptyMap())
    private val jobs = mutableMapOf<RepositoryAddressKey, Job>()
    private val versions = mutableMapOf<RepositoryAddressKey, Long>()
    private val permits = Semaphore(4)

    private fun key(id: String, path: String) = RepositoryAddressKey(id,
        runCatching { Path.of(path).toAbsolutePath().normalize().toString() }.getOrDefault(path))

    fun state(id: String, path: String): RepositoryAddressesState = states[key(id, path)] ?: RepositoryAddressesState.Loading
    fun failure(id: String, path: String): RepositoryRemoteAddressFailure? = failures[key(id, path)]

    fun load(id: String, path: String, force: Boolean = false) {
        val key = key(id, path)
        if (!force && key in states) return
        // 同一仓库换路径后，不允许旧路径结果覆盖新路径。
        jobs.keys.filter { it.id == id }.toList().forEach { jobs.remove(it)?.cancel() }
        versions.keys.filter { it.id == id && it != key }.toList().forEach(versions::remove)
        val version = (versions[key] ?: 0) + 1
        versions[key] = version
        failures = failures.filterKeys { it.id != id }
        states = states.filterKeys { it.id != id || it == key } + (key to RepositoryAddressesState.Loading)
        val job = scope.launch(start = CoroutineStart.LAZY) {
            val runningJob = coroutineContext[Job]
            fun current() = versions[key] == version && key in states && jobs[key] === runningJob
            try {
                val result = RepositoryAddressesState.Loaded(permits.withPermit {
                    runInterruptible(dispatcher) { catalog.list(Path.of(path)) }
                })
                if (current()) {
                    failures = failures - key
                    states = states + (key to result)
                }
            } catch (cancelled: CancellationException) {
                if (current()) {
                    failures = failures - key
                    states = states + (key to RepositoryAddressesState.Failed)
                }
                throw cancelled
            } catch (error: Exception) {
                if (current()) {
                    val failure = (error as? RepositoryRemoteAddressReadException)?.failure
                        ?: RepositoryRemoteAddressFailure(RepositoryRemoteAddressReadStage.REMOTE_LIST, RepositoryRemoteAddressFailureKind.OTHER)
                    failures = failures + (key to failure)
                    states = states + (key to RepositoryAddressesState.Failed)
                }
            } finally {
                if (jobs[key] === runningJob) jobs.remove(key)
            }
        }
        jobs[key] = job
        job.invokeOnCompletion {
            if (jobs[key] === job) {
                jobs.remove(key)
                if (versions[key] == version && states[key] is RepositoryAddressesState.Loading) {
                    failures = failures - key
                    states = states + (key to RepositoryAddressesState.Failed)
                }
            }
        }
        job.start()
    }

    fun refresh() = states.keys.toList().forEach { load(it.id, it.path, force = true) }
}
