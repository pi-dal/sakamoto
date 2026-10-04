package com.pidal.sakamoto.bg

import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import com.pidal.sakamoto.SakamotoApplication
import io.nekohasekai.libbox.InterfaceUpdateListener
import java.net.NetworkInterface

/**
 * Port of upstream SagerNet/sing-box-for-android DefaultNetworkMonitor.kt,
 * trimmed to what this client needs: libbox's default-interface monitor is
 * fed the active network's name/index (upstream's full version adds a
 * suspend DefaultNetworkListener + connectivity callbacks per feature; the
 * listener contract — InterfaceUpdateListener.updateDefaultInterface — is
 * identical).
 */
object DefaultNetworkMonitor {

    private var listener: InterfaceUpdateListener? = null
    private var callback: ConnectivityManager.NetworkCallback? = null

    fun setListener(value: InterfaceUpdateListener?) {
        listener = value
        notifyCurrent()
    }

    fun start() {
        if (callback != null) return
        val cm = SakamotoApplication.connectivity ?: return
        callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = notifyCurrent()

            override fun onLost(network: Network) = notifyCurrent()

            override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) =
                notifyCurrent()
        }.also {
            cm.registerNetworkCallback(
                NetworkRequest.Builder()
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    .build(),
                it,
            )
            // API 24+ (this project's minSdk, matching the main libbox AAR).
            cm.registerDefaultNetworkCallback(it)
        }
    }

    fun stop() {
        val cm = SakamotoApplication.connectivity ?: return
        callback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        callback = null
    }

    private fun notifyCurrent() {
        val listener = listener ?: return
        val cm = SakamotoApplication.connectivity ?: return
        val network = cm.activeNetwork
        val linkProperties = network?.let { cm.getLinkProperties(it) }
        val name = linkProperties?.interfaceName
        if (network == null || name == null) {
            listener.updateDefaultInterface("", -1, false, false)
            return
        }
        // Upstream retries briefly while LinkProperties settle; one pass plus
        // the callback retries above is enough for a skeleton wiring.
        val index = try {
            NetworkInterface.getByName(name)?.index ?: -1
        } catch (e: Exception) {
            -1
        }
        val capabilities = cm.getNetworkCapabilities(network)
        val expensive =
            capabilities == null || !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
        listener.updateDefaultInterface(name, index, expensive, false)
    }
}
