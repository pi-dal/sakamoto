package com.pidal.sakamoto.bg

import android.content.Intent
import android.net.NetworkCapabilities
import android.net.ProxyInfo
import android.net.VpnService
import android.os.Build
import android.os.IBinder
import android.os.Process
import android.provider.Settings as AndroidSettings
import android.system.OsConstants
import android.util.Log
import com.pidal.sakamoto.SakamotoApplication
import io.nekohasekai.libbox.BridgeOptions
import io.nekohasekai.libbox.BridgeSession
import io.nekohasekai.libbox.ConnectionOwner
import io.nekohasekai.libbox.InterfaceUpdateListener
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.LocalDNSTransport
import io.nekohasekai.libbox.NetworkInterface as LibboxNetworkInterface
import io.nekohasekai.libbox.NetworkInterfaceIterator
import io.nekohasekai.libbox.NeighborUpdateListener
import io.nekohasekai.libbox.Notification
import io.nekohasekai.libbox.PlatformInterface
import io.nekohasekai.libbox.PlatformUser
import io.nekohasekai.libbox.ShellSession
import io.nekohasekai.libbox.StringIterator
import io.nekohasekai.libbox.TunOptions
import io.nekohasekai.libbox.WIFIState
import java.net.Inet6Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface

/**
 * The VPN (TUN) service — a port of upstream SagerNet/sing-box-for-android
 * VPNService.kt + PlatformInterfaceWrapper.kt for the surface this client
 * uses.
 *
 * The two load-bearing overrides are:
 *   * autoDetectInterfaceControl(fd) = protect(fd) — libbox's
 *     auto-detect-interface binds sockets per default interface; binding a
 *     fd outside the VPN requires VpnService.protect.
 *   * openTun(options) — turns libbox's TunOptions into a VpnService.Builder
 *     and hands the established fd back to the core. Route/exclude handling
 *     differs at API 33 (IpPrefix-based addRoute/excludeRoute) exactly like
 *     upstream.
 *
 * Root/USB/shell/bridge platform features of upstream are declared
 * unsupported here (they need root or hardware this client does not target);
 * each returns the same style of loud error upstream uses for its own
 * unsupported legs instead of a silent fake.
 *
 * STATUS PENDING SDK BUILD VERIFICATION: not compiled on this machine (no
 * Android SDK/JDK) — see android/README.md.
 */
class SakamotoVpnService : VpnService(), PlatformInterface {

    private val service = TunnelBoxService(this, this)

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int =
        service.onStartCommand()

    override fun onBind(intent: Intent?): IBinder? = super.onBind(intent)

    override fun onDestroy() {
        service.stopService()
        super.onDestroy()
    }

    override fun onRevoke() {
        // System/another VPN revoked us — stop the box, report through the
        // runtime so the Home phase folds to Disconnected.
        service.stopService()
    }

    // --- PlatformInterface ------------------------------------------------

    override fun usePlatformAutoDetectInterfaceControl(): Boolean = true

    // The app does not provide a platform-local DNS transport. Returning null
    // lets libbox use the configured DNS servers inside the tunnel.
    override fun localDNSTransport(): LocalDNSTransport? = null

    override fun autoDetectInterfaceControl(fd: Int) {
        protect(fd)
    }

    override fun openTun(options: TunOptions): Int {
        if (prepare(this) != null) error("android: missing vpn permission")

        val builder = Builder()
            .setSession("sakamoto")
            .setMtu(options.mtu)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            builder.setMetered(false)
        }

        val inet4Address = options.inet4Address
        while (inet4Address.hasNext()) {
            val address = inet4Address.next()
            builder.addAddress(address.address(), address.prefix())
        }

        val inet6Address = options.inet6Address
        while (inet6Address.hasNext()) {
            val address = inet6Address.next()
            builder.addAddress(address.address(), address.prefix())
        }

        if (options.autoRoute) {
            if (options.dnsMode.value != Libbox.DNSModeDisabled) {
                val dnsServerAddress = options.dnsServerAddress
                while (dnsServerAddress.hasNext()) {
                    builder.addDnsServer(dnsServerAddress.next())
                }
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                val inet4RouteAddress = options.inet4RouteAddress
                if (inet4RouteAddress.hasNext()) {
                    while (inet4RouteAddress.hasNext()) {
                        builder.addRoute(inet4RouteAddress.next().toIpPrefix())
                    }
                } else if (options.inet4Address.hasNext()) {
                    builder.addRoute("0.0.0.0", 0)
                }

                val inet6RouteAddress = options.inet6RouteAddress
                if (inet6RouteAddress.hasNext()) {
                    while (inet6RouteAddress.hasNext()) {
                        builder.addRoute(inet6RouteAddress.next().toIpPrefix())
                    }
                } else if (options.inet6Address.hasNext()) {
                    builder.addRoute("::", 0)
                }

                val inet4RouteExcludeAddress = options.inet4RouteExcludeAddress
                while (inet4RouteExcludeAddress.hasNext()) {
                    builder.excludeRoute(inet4RouteExcludeAddress.next().toIpPrefix())
                }

                val inet6RouteExcludeAddress = options.inet6RouteExcludeAddress
                while (inet6RouteExcludeAddress.hasNext()) {
                    builder.excludeRoute(inet6RouteExcludeAddress.next().toIpPrefix())
                }
            } else {
                val inet4RouteRange = options.inet4RouteRange
                while (inet4RouteRange.hasNext()) {
                    val address = inet4RouteRange.next()
                    builder.addRoute(address.address(), address.prefix())
                }
                val inet6RouteRange = options.inet6RouteRange
                while (inet6RouteRange.hasNext()) {
                    val address = inet6RouteRange.next()
                    builder.addRoute(address.address(), address.prefix())
                }
            }

            val includePackage = options.includePackage
            while (includePackage.hasNext()) {
                try {
                    builder.addAllowedApplication(includePackage.next())
                } catch (e: Exception) {
                    Log.w("SakamotoVpnService", "addAllowedApplication failed", e)
                }
            }

            val excludePackage = options.excludePackage
            while (excludePackage.hasNext()) {
                try {
                    builder.addDisallowedApplication(excludePackage.next())
                } catch (e: Exception) {
                    Log.w("SakamotoVpnService", "addDisallowedApplication failed", e)
                }
            }
        }

        if (options.isHTTPProxyEnabled && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            builder.setHttpProxy(
                ProxyInfo.buildDirectProxy(
                    options.httpProxyServer,
                    options.httpProxyServerPort,
                    options.httpProxyBypassDomain.toList(),
                ),
            )
        }
        val pfd = builder.establish()
            ?: error("android: the application is not prepared or is revoked")
        service.fileDescriptor = pfd
        return pfd.fd
    }

    override fun useProcFS(): Boolean = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q

    override fun findConnectionOwner(
        ipProtocol: Int,
        sourceAddress: String,
        sourcePort: Int,
        destinationAddress: String,
        destinationPort: Int,
    ): ConnectionOwner {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            error("android: findConnectionOwner requires API 29")
        }
        val connectivity = SakamotoApplication.connectivity
            ?: error("android: connectivity manager unavailable")
        val uid = connectivity.getConnectionOwnerUid(
            ipProtocol,
            InetSocketAddress(sourceAddress, sourcePort),
            InetSocketAddress(destinationAddress, destinationPort),
        )
        if (uid == Process.INVALID_UID) error("android: connection owner not found")
        val packages = packageManager.getPackagesForUid(uid)
        val owner = ConnectionOwner()
        owner.userId = uid
        owner.userName = packages?.firstOrNull() ?: ""
        owner.setAndroidPackageNames(StringListIterator(packages?.toList() ?: emptyList()))
        return owner
    }

    override fun startDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        DefaultNetworkMonitor.setListener(listener)
    }

    override fun closeDefaultInterfaceMonitor(listener: InterfaceUpdateListener) {
        DefaultNetworkMonitor.setListener(null)
    }

    override fun getInterfaces(): NetworkInterfaceIterator {
        val connectivity = SakamotoApplication.connectivity
        val systemInterfaces = NetworkInterface.getNetworkInterfaces()?.toList() ?: emptyList()
        val interfaces = mutableListOf<LibboxNetworkInterface>()
        if (connectivity != null) {
            for (network in connectivity.allNetworks) {
                val linkProperties = connectivity.getLinkProperties(network) ?: continue
                val capabilities = connectivity.getNetworkCapabilities(network) ?: continue
                val boxInterface = LibboxNetworkInterface()
                boxInterface.name = linkProperties.interfaceName ?: continue
                val systemInterface = systemInterfaces.find { it.name == boxInterface.name } ?: continue
                boxInterface.dnsServer = StringListIterator(
                    linkProperties.dnsServers.mapNotNull { it.hostAddress },
                )
                boxInterface.gateway = StringListIterator(
                    linkProperties.routes
                        .filter { it.destination.prefixLength == 0 }
                        .mapNotNull { it.gateway }
                        .filterNot { it.isAnyLocalAddress }
                        .mapNotNull { it.hostAddress },
                )
                boxInterface.type = when {
                    capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Libbox.InterfaceTypeWIFI
                    capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Libbox.InterfaceTypeCellular
                    capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> Libbox.InterfaceTypeEthernet
                    else -> Libbox.InterfaceTypeOther
                }
                boxInterface.index = systemInterface.index
                boxInterface.mtu = try {
                    systemInterface.mtu
                } catch (e: Exception) {
                    0
                }
                boxInterface.addresses = StringListIterator(
                    systemInterface.interfaceAddresses.mapNotNull { it.toPrefix() },
                )
                var dumpFlags = 0
                if (capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) {
                    dumpFlags = OsConstants.IFF_UP or OsConstants.IFF_RUNNING
                }
                if (systemInterface.isLoopback) {
                    dumpFlags = dumpFlags or OsConstants.IFF_LOOPBACK
                }
                if (systemInterface.supportsMulticast()) {
                    dumpFlags = dumpFlags or OsConstants.IFF_MULTICAST
                }
                boxInterface.flags = dumpFlags
                boxInterface.metered =
                    !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
                interfaces.add(boxInterface)
            }
        }
        return NetworkInterfaceListIterator(interfaces)
    }

    override fun underNetworkExtension(): Boolean = false

    override fun includeAllNetworks(): Boolean = false

    override fun clearDNSCache() {
        // No platform DNS cache to clear without the local resolver wiring.
    }

    override fun readWIFIState(): WIFIState? {
        // Deprecated wifiManager.connectionInfo like upstream; needs location
        // permission this client does not request, so null is the honest
        // answer (the core treats it as "unknown Wi-Fi").
        return try {
            @Suppress("DEPRECATION")
            val wifiInfo = getSystemService(android.net.wifi.WifiManager::class.java)?.connectionInfo
                ?: return null
            var ssid = wifiInfo.ssid
            if (ssid == "<unknown ssid>") {
                return WIFIState("", "")
            }
            if (ssid.startsWith("\"") && ssid.endsWith("\"")) {
                ssid = ssid.substring(1, ssid.length - 1)
            }
            WIFIState(ssid, wifiInfo.bssid)
        } catch (e: Exception) {
            null
        }
    }

    override fun sendNotification(notification: Notification) {
        service.showLibboxNotification(
            identifier = notification.identifier,
            typeID = notification.typeID,
            title = notification.title,
            body = notification.body,
            openURL = notification.openURL,
        )
    }

    override fun cancelNotification(identifier: String, typeID: Int) {
        val manager = getSystemService(android.app.NotificationManager::class.java)
        manager.cancel(identifier, typeID)
    }

    override fun startNeighborMonitor(listener: NeighborUpdateListener) {
        // Upstream feeds this from a rooted neighbor table; without root the
        // monitor is a no-op (neighbor features are not part of this client).
    }

    override fun closeNeighborMonitor(listener: NeighborUpdateListener?) {
    }

    override fun registerMyInterface(name: String?) {
    }

    override fun usePlatformShell(): Boolean = false

    override fun checkPlatformShell() {
        error("missing root permission")
    }

    override fun openShellSession(
        user: PlatformUser?,
        command: String?,
        environ: StringIterator?,
        term: String?,
        rows: Int,
        cols: Int,
    ): ShellSession = error("platform shell is not supported")

    override fun lookupUser(username: String?): PlatformUser {
        val pkg = username ?: error("android: lookupUser requires a package name")
        val info = packageManager.getApplicationInfo(pkg, 0)
        val platformUser = PlatformUser()
        platformUser.username = pkg
        platformUser.uid = info.uid
        platformUser.gid = info.uid
        platformUser.homeDir = info.dataDir
        return platformUser
    }

    override fun lookupSFTPServer(): String = error("not supported")

    override fun readSystemSSHHostKey(): String = error("not supported")

    /** The device name the built-in Tailscale endpoint presents. */
    override fun tailscaleHostname(): String =
        AndroidSettings.Global.getString(
            contentResolver,
            AndroidSettings.Global.DEVICE_NAME,
        )?.takeIf { it.isNotBlank() }
            ?: "${Build.MANUFACTURER} ${Build.MODEL}"

    override fun usePlatformBridge(): Boolean = false

    override fun createBridge(options: BridgeOptions?): BridgeSession =
        error("platform bridge is not supported")

    // --- helpers -----------------------------------------------------------

    private fun StringIterator.toList(): List<String> {
        val result = mutableListOf<String>()
        while (hasNext()) result.add(next())
        return result
    }

    private fun io.nekohasekai.libbox.RoutePrefix.toIpPrefix(): android.net.IpPrefix =
        android.net.IpPrefix(InetAddress.getByName(address()), prefix())

    private fun java.net.InterfaceAddress.toPrefix(): String? {
        val host = address.hostAddress ?: return null
        return if (address is Inet6Address) {
            val scoped = Inet6Address.getByAddress(address.address).hostAddress ?: host
            "$scoped/$networkPrefixLength"
        } else {
            "$host/$networkPrefixLength"
        }
    }

    private class StringListIterator(
        private val values: List<String>,
    ) : StringIterator {
        private val iterator = values.iterator()
        override fun len(): Int = values.size
        override fun hasNext(): Boolean = iterator.hasNext()
        override fun next(): String = iterator.next()
    }

    private class NetworkInterfaceListIterator(
        private val values: List<LibboxNetworkInterface>,
    ) : NetworkInterfaceIterator {
        private val iterator = values.iterator()
        override fun hasNext(): Boolean = iterator.hasNext()
        override fun next(): LibboxNetworkInterface = iterator.next()
    }
}
