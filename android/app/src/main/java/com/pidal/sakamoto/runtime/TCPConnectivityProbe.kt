package com.pidal.sakamoto.runtime

import java.io.IOException
import java.net.ConnectException
import java.net.InetSocketAddress
import java.net.Socket
import java.net.SocketTimeoutException
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone

/** A single TCP handshake; callers supply an OS/VPN-bound socket, never a core dialer. */
object TCPConnectivityProbe {
    data class Outcome(
        val address: String, val port: Int, val measuredAtMillis: Long,
        val elapsedMs: Long, val connected: Boolean, val detail: String,
        val nativeError: String = "", val checkedSSH: Boolean = false, val sshGreeting: String? = null,
    ) {
        val report: String get() {
            val time = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US).apply { timeZone = TimeZone.getTimeZone("UTC") }.format(Date(measuredAtMillis))
            return "App TCP check\nTime: $time\nTarget: [$address]:$port\nResult: $detail\nTCP handshake: ${if (connected) "completed" else "not completed"}\nService response: ${if (checkedSSH) sshGreeting ?: "SSH greeting not verified" else "not checked"}\nDuration: $elapsedMs ms\nNative error: ${nativeError.ifEmpty { "none" }}\nThis does not verify SSH authentication or another app's routing. A TCP handshake alone may be acknowledged by the VPN's user-space stack."
        }
    }

    fun check(address: String, port: Int, socket: Socket, timeoutMs: Int = 8000, verifySSH: Boolean = false): Outcome {
        val startedAt = System.nanoTime()
        var connected = false
        var detail: String
        var nativeError = ""
        var sshGreeting: String? = null
        socket.use {
            if (port !in 1..65535) {
                detail = "Use a port from 1 to 65535"
            } else try {
                it.connect(InetSocketAddress(address, port), timeoutMs.coerceAtLeast(1))
                connected = true
                detail = "App TCP handshake completed; service response not checked"
                if (verifySSH) {
                    sshGreeting = readGreeting(it, startedAt, timeoutMs)
                    detail = if (sshGreeting != null) "SSH greeting received; service responded"
                        else "App TCP handshake completed; no valid SSH greeting received"
                }
            } catch (error: IOException) {
                detail = when {
                    connected -> "App TCP handshake completed; SSH greeting not verified"
                    error is SocketTimeoutException -> "Check timed out; the route, firewall, tailnet policy or service may be blocking TCP"
                    error is ConnectException && error.message?.contains("refused", ignoreCase = true) == true -> "Connection refused; check the listening service and firewall"
                    else -> "TCP connection failed; check the route, service and tailnet policy"
                }
                nativeError = "${error.javaClass.simpleName}: ${error.message.orEmpty().take(600)}"
            }
        }
        return Outcome(address, port, System.currentTimeMillis(), (System.nanoTime() - startedAt) / 1_000_000, connected, detail, nativeError, verifySSH, sshGreeting)
    }

    private fun readGreeting(socket: Socket, startedAt: Long, timeoutMs: Int): String? {
        val line = StringBuilder()
        val input = socket.getInputStream()
        repeat(1024) {
            val remaining = timeoutMs - (System.nanoTime() - startedAt) / 1_000_000
            if (remaining <= 0) throw SocketTimeoutException("SSH greeting deadline")
            socket.soTimeout = remaining.toInt().coerceAtLeast(1)
            val byte = input.read()
            if (byte < 0) return null
            if (byte == 10) {
                if (line.startsWith("SSH-2.0-") || line.startsWith("SSH-1.99-")) return line.toString().trim().take(255)
                line.clear()
            } else line.append(if (byte in 32..126) byte.toChar() else ' ')
        }
        return null
    }
}
