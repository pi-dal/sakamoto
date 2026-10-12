package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.TCPConnectivityProbe
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketAddress
import java.net.SocketTimeoutException
import org.junit.Assert.*
import org.junit.Test

class TCPConnectivityProbeTest {
    @Test fun realHandshakeClosesTheSuppliedSocketAndReportsTheTarget() {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { listener ->
            val socket = Socket()
            val outcome = TCPConnectivityProbe.check("127.0.0.1", listener.localPort, socket, 1000)
            assertTrue(outcome.report, outcome.connected)
            assertTrue(socket.isClosed)
            assertTrue(outcome.report.contains("[127.0.0.1]:${listener.localPort}"))
            assertTrue(outcome.report.contains("handshake alone"))
            listener.accept().use { accepted ->
                accepted.soTimeout = 1000
                assertEquals(-1, accepted.getInputStream().read())
            }
        }
    }

    @Test fun sshGreetingConfirmsAServiceResponseWithoutSendingAuthentication() {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { listener ->
            val executor = java.util.concurrent.Executors.newSingleThreadExecutor()
            try {
                val server = executor.submit<Int> {
                    listener.accept().use { accepted ->
                        accepted.soTimeout = 1000
                        accepted.getOutputStream().write("Server notice\r\nSSH-2.0-local_test\r\n".toByteArray())
                        accepted.getOutputStream().flush()
                        accepted.getInputStream().read()
                    }
                }
                val outcome = TCPConnectivityProbe.check("127.0.0.1", listener.localPort, Socket(), 1000, verifySSH = true)
                assertTrue(outcome.report, outcome.connected)
                assertEquals("SSH-2.0-local_test", outcome.sshGreeting)
                assertEquals(-1, server.get(2, java.util.concurrent.TimeUnit.SECONDS).toInt())
            } finally { executor.shutdownNow() }
        }
    }

    @Test fun aSilentServiceIsNotReportedAsVerifiedSSH() {
        ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { listener ->
            val outcome = TCPConnectivityProbe.check("127.0.0.1", listener.localPort, Socket(), 100, verifySSH = true)
            assertTrue(outcome.connected)
            assertTrue(outcome.checkedSSH)
            assertNull(outcome.sshGreeting)
            assertTrue(outcome.detail.contains("not verified"))
            assertTrue(outcome.elapsedMs < 2000)
        }
    }

    @Test fun refusedConnectionRetainsNativeCauseAndClosesTheSocket() {
        val listener = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
        val port = listener.localPort
        listener.close()
        val socket = Socket()
        val outcome = TCPConnectivityProbe.check("127.0.0.1", port, socket, 1000)
        assertFalse(outcome.connected)
        assertTrue(outcome.detail.contains("refused"))
        assertTrue(outcome.nativeError.contains("ConnectException"))
        assertTrue(socket.isClosed)
    }

    @Test fun timeoutDoesNotInventASpecificRootCause() {
        val socket = object : Socket() {
            override fun connect(endpoint: SocketAddress, timeout: Int) { throw SocketTimeoutException("test deadline") }
        }
        val outcome = TCPConnectivityProbe.check("100.64.0.1", 22, socket, 1)
        assertFalse(outcome.connected)
        assertTrue(outcome.detail.contains("may be blocking"))
        assertTrue(socket.isClosed)
    }

    @Test fun invalidPortIsRejectedAndTheSocketIsReleased() {
        val socket = Socket()
        val outcome = TCPConnectivityProbe.check("127.0.0.1", 0, socket)
        assertFalse(outcome.connected)
        assertTrue(outcome.detail.contains("1 to 65535"))
        assertTrue(socket.isClosed)
    }
}
