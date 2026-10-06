package com.pidal.sakamoto.bg

import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import com.pidal.sakamoto.SakamotoApplication
import io.nekohasekai.libbox.InterfaceUpdateListener
import java.net.NetworkInterface

/** Tracks internet-capable physical networks, never the app's own VPN.
 * Each callback is registered once; link/capability changes refresh both
 * libbox's default interface and Android's VPN underlying-network metadata. */
object DefaultNetworkMonitor {
    private val lock = Any()
    private var listener: InterfaceUpdateListener? = null
    private var callback: ConnectivityManager.NetworkCallback? = null
    private var underlyingChanged: ((Network?) -> Unit)? = null
    private val networks = linkedMapOf<Network, NetworkCapabilities>()
    private val links = mutableMapOf<Network, LinkProperties>()

    fun setListener(value: InterfaceUpdateListener?) = synchronized(lock) {
        listener = value
        notifyCurrent()
    }

    fun setUnderlyingListener(value: ((Network?) -> Unit)?) = synchronized(lock) {
        underlyingChanged = value
        notifyCurrent()
    }

    fun currentNetwork(): Network? = synchronized(lock) { chooseNetwork() }

    fun start() = synchronized(lock) {
        if (callback != null) return@synchronized
        val cm = SakamotoApplication.connectivity ?: return@synchronized
        cm.allNetworks.forEach { network ->
            val capabilities = cm.getNetworkCapabilities(network) ?: return@forEach
            if (eligible(capabilities)) {
                networks[network] = capabilities
                cm.getLinkProperties(network)?.let { links[network] = it }
            }
        }
        val next = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                synchronized(lock) {
                    cm.getNetworkCapabilities(network)?.takeIf(::eligible)?.let { networks[network] = it }
                    cm.getLinkProperties(network)?.let { links[network] = it }
                    notifyCurrent()
                }
            }

            override fun onLost(network: Network) = synchronized(lock) {
                networks.remove(network); links.remove(network)
                notifyCurrent()
            }

            override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = synchronized(lock) {
                if (eligible(capabilities)) networks[network] = capabilities else networks.remove(network)
                notifyCurrent()
            }

            override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) = synchronized(lock) {
                links[network] = properties
                notifyCurrent()
            }
        }
        try {
            cm.registerNetworkCallback(NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                .build(), next)
            callback = next
            notifyCurrent()
        } catch (error: Exception) {
            networks.clear(); links.clear()
            throw error
        }
    }

    fun stop() = synchronized(lock) {
        callback?.let { current -> SakamotoApplication.connectivity?.let { runCatching { it.unregisterNetworkCallback(current) } } }
        callback = null
        networks.clear(); links.clear()
        listener = null
        underlyingChanged = null
    }

    private fun eligible(capabilities: NetworkCapabilities): Boolean =
        capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
            capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) &&
            !capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)

    private fun chooseNetwork(): Network? {
        val active = SakamotoApplication.connectivity?.activeNetwork
        if (active != null && networks.containsKey(active) && links[active]?.interfaceName != null) return active
        return networks.keys.filter { links[it]?.interfaceName != null }.maxByOrNull { network ->
            val capabilities = networks.getValue(network)
            (if (capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) 10 else 0) +
                (if (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) 2 else 0)
        }
    }

    private fun notifyCurrent() {
        val network = chooseNetwork()
        underlyingChanged?.invoke(network)
        val name = network?.let { links[it]?.interfaceName }
        if (network == null || name == null) {
            listener?.updateDefaultInterface("", -1, false, false)
            return
        }
        val index = runCatching { NetworkInterface.getByName(name)?.index ?: -1 }.getOrDefault(-1)
        val expensive = !networks.getValue(network).hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
        listener?.updateDefaultInterface(name, index, expensive, false)
    }
}
