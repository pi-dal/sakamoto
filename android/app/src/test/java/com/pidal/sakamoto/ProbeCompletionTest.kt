package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.ProbeCompletion
import com.pidal.sakamoto.runtime.ProbeResult
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

class ProbeCompletionTest {
    @Test fun successfulCompletionIsNotReportedAsTimeout() = runBlocking {
        val result = ProbeCompletion.await(CompletableDeferred(ProbeResult()), 1000)
        assertNull(result.error)
    }

    @Test fun completedFailureKeepsItsReason() = runBlocking {
        val result = ProbeCompletion.await(CompletableDeferred(ProbeResult("DNS lookup failed")), 1000)
        assertEquals("DNS lookup failed", result.error)
    }

    @Test fun unfinishedProbeReturnsTimeoutWithoutWaitingForWorker() = runBlocking {
        val worker = CompletableDeferred<ProbeResult>()
        val result = ProbeCompletion.await(worker, 10)
        assertEquals("Network check timed out", result.error)
        assertFalse(worker.isCompleted)
        worker.complete(ProbeResult())
        assertEquals("Network check timed out", result.error)
    }
}
