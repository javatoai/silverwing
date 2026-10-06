package com.snowball.silverwing.desktop

import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible

internal class OperationBusyException : IllegalStateException("另一个操作正在执行，请稍候")

/** Runs one mutating application operation without coupling feature controllers together. */
class OperationRunner internal constructor(
    private val coordinator: OperationCoordinator,
    private val scope: CoroutineScope,
    private val dispatcher: CoroutineDispatcher,
) {
    private var activeJob: Job? = null

    fun <T> run(
        activeMessage: String,
        successMessage: String,
        cancellable: Boolean = false,
        block: () -> T,
        onFailure: (Throwable) -> Unit = {},
        onCancelled: () -> Unit = {},
        showErrorFeedback: Boolean = true,
        onSuccess: (T) -> Unit = {},
    ): Boolean {
        if (!coordinator.begin(activeMessage, cancellable)) {
            val error = OperationBusyException()
            runCatching { onFailure(error) }
                .onFailure { callbackError -> coordinator.errorMessage = OperationFailureDetails.format(callbackError) }
                .onSuccess { if (showErrorFeedback) coordinator.errorMessage = error.message }
            return false
        }
        var started = false
        val job = scope.launch(start = CoroutineStart.LAZY) {
            started = true
            val runningJob = coroutineContext[Job]
            try {
                val value = runInterruptible(dispatcher, block)
                if (activeJob === runningJob) {
                    runCatching { onSuccess(value) }
                        .onSuccess { coordinator.succeed(successMessage) }
                        .onFailure { error -> fail(error, onFailure, showErrorFeedback) }
                }
            } catch (_: CancellationException) {
                if (activeJob === runningJob) cancelled(onCancelled)
            } catch (error: Throwable) {
                if (activeJob === runningJob) fail(error, onFailure, showErrorFeedback)
            } finally {
                if (activeJob === runningJob) activeJob = null
            }
        }
        activeJob = job
        job.invokeOnCompletion { cause ->
            // A scope can be cancelled before a lazy coroutine enters its body.
            if (!started && cause is CancellationException && activeJob === job) {
                try { cancelled(onCancelled) } finally { if (activeJob === job) activeJob = null }
            }
        }
        job.start()
        return true
    }

    /** Independent read lifecycle. The caller registers the returned lazy job before starting it. */
    internal fun <T> read(
        block: () -> T,
        onSuccess: (T) -> Unit,
        onFailure: (Throwable) -> Unit,
        onCancelled: () -> Unit,
    ): Job = scope.launch(start = CoroutineStart.LAZY) {
        try {
            onSuccess(runInterruptible(dispatcher, block))
        } catch (cancelled: CancellationException) {
            runCatching(onCancelled).onFailure { error -> runCatching { onFailure(error) } }
            throw cancelled
        } catch (error: Throwable) {
            runCatching { onFailure(error) }
        }
    }

    fun cancel(): Boolean {
        if (!coordinator.busy || !coordinator.cancellable) return false
        coordinator.markCancelling()
        activeJob?.cancel()
        return true
    }

    /**
     * A failure callback is UI bookkeeping supplied by a feature controller.
     * It must not be able to prevent the shared operation state from leaving
     * the busy state when that bookkeeping itself fails.
     */
    private fun fail(error: Throwable, onFailure: (Throwable) -> Unit, showErrorFeedback: Boolean) {
        var callbackFailed = false
        runCatching { onFailure(error) }
            .onFailure { callbackError ->
                callbackFailed = true
                if (callbackError !== error) {
                    runCatching { error.addSuppressed(callbackError) }
                }
            }
        coordinator.fail(error, showFeedback = showErrorFeedback || callbackFailed)
    }

    private fun cancelled(onCancelled: () -> Unit) {
        val callbackError = runCatching(onCancelled).exceptionOrNull()
        if (callbackError == null) coordinator.cancelled() else coordinator.fail(callbackError)
    }
}
