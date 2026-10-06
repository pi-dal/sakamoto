package com.pidal.sakamoto.runtime

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.util.Log
import com.pidal.sakamoto.command.CommandClientRuntime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import java.net.HttpURLConnection
import java.net.URL

/** Observations of Android's VPN network and a real DNS/TLS/HTTP route probe. */
object VpnDiagnostics {
    @Volatile private var probeGeneration = 0L
    data class Snapshot(val consent: Boolean, val vpn: Boolean, val interfaceName: String, val physical: String, val dns: String, val channel: Boolean, val network: android.net.Network?)
    fun snapshot(context: Context): Snapshot {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        val networks = cm.allNetworks.toList()
        val owned = networks.filter { network -> cm.getNetworkCapabilities(network)?.let {
            it.hasTransport(NetworkCapabilities.TRANSPORT_VPN) &&
                (android.os.Build.VERSION.SDK_INT < 29 || it.ownerUid == android.os.Process.myUid())
        } == true }
        // During reload Android briefly reports both old and new VPN agents.
        // Prefer the active owned network; otherwise the newest owned agent.
        val vpn = cm.activeNetwork?.takeIf { it in owned }
            ?: owned.maxByOrNull { it.networkHandle }
        val properties = vpn?.let { cm.getLinkProperties(it) }
        val physical = networks.filter { network -> cm.getNetworkCapabilities(network)?.let {
            it.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) && it.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
        } == true }.mapNotNull { cm.getLinkProperties(it)?.interfaceName }
        return Snapshot(VpnService.prepare(context) == null, vpn != null, properties?.interfaceName.orEmpty(),
            physical.joinToString(", "), properties?.dnsServers?.joinToString(", ") { it.hostAddress.orEmpty() }.orEmpty(), CommandClientRuntime.channelConnected.value, vpn)
    }

    suspend fun probe(context: Context) {
        Log.d("SakamotoProbe", "probe requested")
        val generation = synchronized(this) { probeGeneration += 1; probeGeneration }
        probeAttempt(context, generation, retryOnNetworkChange = true)
    }

    private suspend fun probeAttempt(context: Context, generation: Long, retryOnNetworkChange: Boolean) {
        val snapshot = snapshot(context)
        if (MobilecoreRuntime.state.value.serviceState != "Running" || !snapshot.vpn) {
            MobilecoreRuntime.recordRouteProbe("VPN is not connected; connect before checking")
            return
        }
        MobilecoreRuntime.beginProbe()
        Log.d("SakamotoProbe", "probe started vpn=${snapshot.interfaceName} physical=${snapshot.physical} dns=${snapshot.dns}")
        val task = kotlinx.coroutines.CoroutineScope(Dispatchers.IO).async {
            try {
                require(snapshot.physical.isNotEmpty()) { "No Wi-Fi or mobile internet network" }
                val vpnNetwork = snapshot.network ?: error("VPN network disappeared")
                Log.d("SakamotoProbe", "dns start network=$vpnNetwork")
                vpnNetwork.getAllByName("www.gstatic.com")
                Log.d("SakamotoProbe", "dns passed")
                val connection = vpnNetwork.openConnection(URL("https://www.gstatic.com/generate_204")) as HttpURLConnection
                Log.d("SakamotoProbe", "https start")
                connection.connectTimeout = 8000
                connection.readTimeout = 8000
                connection.instanceFollowRedirects = false
                try { require(connection.responseCode == 204) { "DNS resolved; HTTPS returned ${connection.responseCode} instead of 204" } }
                finally { connection.disconnect() }
                ProbeResult()
            } catch (error: Exception) {
                Log.e("SakamotoProbe", "probe failed: ${error.javaClass.simpleName}")
                ProbeResult(when (error) {
                    is java.net.UnknownHostException -> "DNS lookup failed"
                    is java.net.SocketTimeoutException -> "Network check timed out"
                    is javax.net.ssl.SSLException -> "TLS verification failed"
                    else -> error.message ?: "Network check failed"
                })
            }
        }
        val result = try { ProbeCompletion.await(task, 12_000) } finally { task.cancel() }
        val error = result.error
        Log.d("SakamotoProbe", "probe finished error=$error")
        if (generation == probeGeneration && MobilecoreRuntime.state.value.serviceState == "Running") {
            val current = snapshot(context)
            if (current.network != snapshot.network) {
                if (retryOnNetworkChange && current.vpn) probeAttempt(context, generation, retryOnNetworkChange = false)
                else MobilecoreRuntime.recordRouteProbe("VPN network changed during check; retry")
            } else MobilecoreRuntime.recordRouteProbe(error.orEmpty())
        }
    }
}
