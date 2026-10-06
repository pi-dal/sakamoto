package com.pidal.sakamoto.runtime

import kotlinx.coroutines.Deferred
import kotlinx.coroutines.withTimeoutOrNull

/** Successful completion must be distinct from withTimeoutOrNull's null result. */
data class ProbeResult(val error: String? = null)

object ProbeCompletion {
    suspend fun await(task: Deferred<ProbeResult>, timeoutMillis: Long): ProbeResult =
        withTimeoutOrNull(timeoutMillis) { task.await() } ?: ProbeResult("Network check timed out")
}
