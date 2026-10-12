package com.pidal.sakamoto.runtime

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlin.coroutines.resume

object PeerTCPDiagnostics {
    suspend fun check(context: Context, address: String, port: Int, verifySSH: Boolean = true): TCPConnectivityProbe.Outcome {
        val before = VpnDiagnostics.snapshot(context)
        val network = before.network
        if (network == null || MobilecoreRuntime.state.value.serviceState != "Running") {
            return TCPConnectivityProbe.Outcome(address, port, System.currentTimeMillis(), 0, false, "Connect the VPN before checking")
        }
        val result = withContext(Dispatchers.IO) {
            coroutineScope {
                suspendCancellableCoroutine<TCPConnectivityProbe.Outcome> { continuation ->
                    val socket = try { network.socketFactory.createSocket() } catch (error: Exception) {
                        continuation.resume(TCPConnectivityProbe.Outcome(address, port, System.currentTimeMillis(), 0, false,
                            "Could not open a VPN socket; reconnect and retry", error.javaClass.simpleName))
                        return@suspendCancellableCoroutine
                    }
                    continuation.invokeOnCancellation { runCatching { socket.close() } }
                    launch {
                        val result = try {
                            TCPConnectivityProbe.check(address, port, socket, verifySSH = verifySSH)
                        } catch (error: Exception) {
                            runCatching { socket.close() }
                            TCPConnectivityProbe.Outcome(address, port, System.currentTimeMillis(), 0, false,
                                "TCP check could not complete; retry", error.javaClass.simpleName)
                        }
                        if (continuation.isActive) continuation.resume(result)
                    }
                }
            }
        }
        val after = VpnDiagnostics.snapshot(context)
        return if (after.network != network || MobilecoreRuntime.state.value.serviceState != "Running") {
            result.copy(detail = "VPN changed during the check; retry on the current VPN")
        } else result
    }
}
