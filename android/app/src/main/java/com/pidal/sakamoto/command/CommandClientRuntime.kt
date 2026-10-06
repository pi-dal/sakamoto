package com.pidal.sakamoto.command

import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.TailscaleBinding
import com.pidal.sakamoto.runtime.TailscaleRuntime
import io.nekohasekai.libbox.CommandClient
import io.nekohasekai.libbox.CommandClientHandler
import io.nekohasekai.libbox.CommandClientOptions
import io.nekohasekai.libbox.ConnectionEvents
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.LogIterator
import io.nekohasekai.libbox.OutboundGroupIterator
import io.nekohasekai.libbox.OutboundGroupItemIterator
import io.nekohasekai.libbox.StatusMessage
import io.nekohasekai.libbox.StringIterator
import io.nekohasekai.libbox.TailscalePingHandler
import io.nekohasekai.libbox.TailscalePingResult
import io.nekohasekai.libbox.TailscaleStatusHandler
import io.nekohasekai.libbox.TailscaleStatusSubscription
import io.nekohasekai.libbox.TailscaleStatusUpdate

/**
 * App-process side of the libbox command channel — the Android mirror of the
 * iOS app's LibboxCoreCommanding. libbox's CommandServer runs inside the
 * tunnel service (bg/TunnelBoxService); this CommandClient connects to it
 * over libbox's own local transport (same mechanism upstream SFA uses — the
 * channel is libbox-internal, there is no app-group/socket path to manage).
 *
 * Only the streams the Home/Data pages consume are subscribed:
 *   CommandStatus — traffic/connection counters (Data page).
 *   CommandGroup  — selectable groups; the selected item and its delay feed
 *                   Mobilecore.nodeStatus so the Home page can show
 *                   selected ≠ reachable with the TUI's own vocabulary.
 *
 * Clash-mode words are the routing-mode words ("Rule"/"Global"/"Direct"):
 * cycling computes the NEXT word with Mobilecore.nextRoutingMode (the Go
 * bridge owns the cycle), then applies it with client.setClashMode.
 *
 * STATUS PENDING SDK BUILD VERIFICATION — see android/README.md.
 */
object CommandClientRuntime : CommandClientHandler {

    private var client: CommandClient? = null
    private val _channelConnected = kotlinx.coroutines.flow.MutableStateFlow(false)
    val channelConnected: kotlinx.coroutines.flow.StateFlow<Boolean> = _channelConnected

    /** Latest status message for the Data page (atomic ref, UI reads copy). */
    @Volatile
    var lastStatus: StatusMessage? = null
        private set

    @Volatile
    var lastGroupsSummary: String = ""
        private set

    fun start() {
        if (client != null) return
        val options = CommandClientOptions()
        options.addCommand(Libbox.CommandStatus)
        options.addCommand(Libbox.CommandGroup)
        options.addCommand(Libbox.CommandClashMode)
        options.addCommand(Libbox.CommandConnections)
        options.addCommand(Libbox.CommandLog)
        // The daemon reads this as a time.Duration (nanoseconds): 1s.
        options.statusInterval = 1_000_000_000L
        client = Libbox.newCommandClient(this, options)
        try {
            client?.connect()
        } catch (e: Exception) {
            client = null
            MobilecoreRuntime.setNotice("command client: ${e.message}")
        }
    }

    fun stop() {
        _channelConnected.value = false
        val current = client
        client = null
        try {
            current?.disconnect()
        } catch (e: Exception) {
            // Channel already down — stopping anyway.
        }
    }

    fun setClashMode(mode: String) {
        try {
            (client ?: error("Command channel unavailable")).setClashMode(mode.lowercase())
        } catch (e: Exception) {
            MobilecoreRuntime.setNotice("mode change: ${e.message}")
        }
    }

    fun urlTest(outboundTag: String) {
        if (outboundTag.isBlank()) {
            MobilecoreRuntime.setNotice("no selected node to test yet")
            return
        }
        MobilecoreRuntime.setNodeTesting(true)
        try {
            (client ?: error("Command channel unavailable")).urlTest(outboundTag)
        } catch (e: Exception) {
            MobilecoreRuntime.setNodeTesting(false)
            MobilecoreRuntime.setNotice("url test: ${e.message}")
        }
    }

    fun selectOutbound(groupTag: String, outboundTag: String): Boolean {
        return try {
            (client ?: error("Connect the tunnel before selecting a node")).selectOutbound(groupTag, outboundTag)
            true
        } catch (e: Exception) {
            MobilecoreRuntime.setNotice("select: ${e.message}")
            false
        }
    }

    /** Ask the running core to reload with the current staged config. */
    fun reloadService(): Boolean {
        return try {
            (client ?: error("Connect the tunnel before applying configuration")).serviceReload()
            true
        } catch (e: Exception) {
            MobilecoreRuntime.setNotice("reload: ${e.message}")
            false
        }
    }

    // --- Built-in Tailscale endpoint (libbox v1.14.2 command RPCs) ---------
    // Same surface as the iOS app's LibboxCoreCommanding + TailscaleController:
    // SubscribeTailscaleStatus / SetTailscaleExitNode / TailscaleLogout /
    // StartTailscalePing. NOT wired on purpose: Taildrop / Tailscale SSH /
    // serve — see TailscaleCapabilities.unsupported.

    private var tailscaleSubscription: TailscaleStatusSubscription? = null

    /** Subscribe to the tailnet status stream (one live subscription). */
    fun subscribeTailscaleStatus() {
        val current = client ?: run {
            TailscaleRuntime.setNotice("command channel not connected")
            return
        }
        if (tailscaleSubscription != null) return
        try {
            tailscaleSubscription = current.subscribeTailscaleStatus(tailscaleStatusHandler)
            TailscaleRuntime.setSubscribed(true)
        } catch (e: Exception) {
            tailscaleSubscription = null
            TailscaleRuntime.setSubscribed(false)
            TailscaleRuntime.setNotice("tailscale subscribe: ${e.message}")
        }
    }

    fun stopTailscaleSubscription() {
        tailscaleSubscription?.let { runCatching { it.close() } }
        tailscaleSubscription = null
        TailscaleRuntime.setSubscribed(false)
    }

    /** Pick a peer as exit node. Empty stableID clears the selection. */
    fun setTailscaleExitNode(endpointTag: String, stableID: String) {
        try {
            (client ?: error("Command channel unavailable")).setTailscaleExitNode(endpointTag, stableID)
        } catch (e: Exception) {
            TailscaleRuntime.setNotice("exit node: ${e.message}")
        }
    }

    fun tailscaleLogout(endpointTag: String) {
        try {
            (client ?: error("Command channel unavailable")).tailscaleLogout(endpointTag)
        } catch (e: Exception) {
            TailscaleRuntime.setNotice("logout: ${e.message}")
        }
    }

    /** Latency probe of one peer (direct vs DERP relay). */
    fun startTailscalePing(endpointTag: String, peerIP: String) {
        val current = client ?: run {
            TailscaleRuntime.setNotice("command channel not connected")
            return
        }
        try {
            current.startTailscalePing(endpointTag, peerIP, tailscalePingHandler)
        } catch (e: Exception) {
            TailscaleRuntime.appendPing(
                com.pidal.sakamoto.runtime.TailscalePingOutcome(
                    latencyMs = 0.0, direct = false, endpoint = "", peerRelay = "",
                    derpRegionCode = "", error = e.message ?: "start failed",
                ),
            )
        }
    }

    private val tailscaleStatusHandler = object : TailscaleStatusHandler {
        override fun onStatusUpdate(status: TailscaleStatusUpdate?) {
            if (status == null) return
            TailscaleRuntime.applyEndpoints(TailscaleBinding.mapUpdate(status.endpoints()))
        }

        override fun onError(message: String?) {
            TailscaleRuntime.setNotice(message ?: "tailscale status error")
        }
    }

    private val tailscalePingHandler = object : TailscalePingHandler {
        override fun onPingResult(result: TailscalePingResult?) {
            if (result == null) return
            TailscaleRuntime.appendPing(TailscaleBinding.mapPing(result))
        }

        override fun onError(message: String?) {
            TailscaleRuntime.appendPing(
                com.pidal.sakamoto.runtime.TailscalePingOutcome(
                    latencyMs = 0.0, direct = false, endpoint = "", peerRelay = "",
                    derpRegionCode = "", error = message ?: "ping error",
                ),
            )
        }
    }

    // --- CommandClientHandler (libbox v1.14.2 surface) ----------------------

    override fun connected() {
        _channelConnected.value = true
    }

    override fun disconnected(message: String?) {
        _channelConnected.value = false
        // Service gone: the tunnel service owns the service-state vocabulary;
        // nothing to fold here beyond clearing group-derived UI state.
        lastGroupsSummary = ""
        _groups.value = emptyList()
        synchronized(this) { connectionList = null; connections = emptyList() }
        stopTailscaleSubscription()
    }

    override fun setDefaultLogLevel(level: Int) {
    }

    override fun clearLogs() {
    }

    override fun writeLogs(messageList: LogIterator?) {
        if (messageList == null) return
        while (messageList.hasNext()) com.pidal.sakamoto.runtime.ExperimentRuntime.log(messageList.next().message)
    }

    override fun writeStatus(message: StatusMessage?) {
        lastStatus = message
    }

    private val _groups = kotlinx.coroutines.flow.MutableStateFlow<List<com.pidal.sakamoto.runtime.ConfigRepository.Group>>(emptyList())
    val groups: kotlinx.coroutines.flow.StateFlow<List<com.pidal.sakamoto.runtime.ConfigRepository.Group>> = _groups

    override fun writeGroups(message: OutboundGroupIterator?) {
        if (message == null) return
        val snapshots = mutableListOf<com.pidal.sakamoto.runtime.ConfigRepository.Group>()
        while (message.hasNext()) {
            val group = message.next()
            val items = mutableListOf<com.pidal.sakamoto.runtime.ConfigRepository.GroupItem>()
            val iterator = group.items
            while (iterator.hasNext()) {
                val item = iterator.next()
                items.add(com.pidal.sakamoto.runtime.ConfigRepository.GroupItem(item.tag, item.type, item.urlTestDelay, item.urlTestTime))
            }
            snapshots.add(com.pidal.sakamoto.runtime.ConfigRepository.Group(group.tag, group.type, group.selectable, group.selected, items))
        }
        _groups.value = snapshots
        lastGroupsSummary = snapshots.joinToString(" · ") { it.tag }
        // The primary group is stable; later selectors must not overwrite
        // the Home selected value merely because they arrived last.
        val primary = snapshots.firstOrNull { it.selectable }
        val selected = primary?.items?.firstOrNull { it.tag == primary.selected }
        if (selected != null) {
            MobilecoreRuntime.setSelectedNode(selected.tag, selected.delay)
            MobilecoreRuntime.setNodeTesting(false)
        }
    }

    override fun writeOutbounds(message: OutboundGroupItemIterator?) {
        // Standalone outbound listing is a Data-page follow-up.
    }

    override fun initializeClashMode(modeList: StringIterator?, currentMode: String?) {
        // Adopt the running core's mode verbatim; the cycle still goes
        // through Mobilecore.nextRoutingMode.
        if (currentMode != null) MobilecoreRuntime.setRoutingMode(currentMode)
    }

    override fun updateClashMode(newMode: String?) {
        if (newMode != null) MobilecoreRuntime.setRoutingMode(newMode)
    }

    data class ConnectionRow(val id: String, val name: String, val source: String, val destination: String, val outbound: String, val rule: String, val closed: Boolean)

    @Volatile var connections: List<ConnectionRow> = emptyList()
        private set
    private var connectionList: io.nekohasekai.libbox.Connections? = null

    @Synchronized override fun writeConnectionEvents(events: ConnectionEvents?) {
        if (events == null) return
        com.pidal.sakamoto.runtime.ExperimentRuntime.connection(events)
        val list = connectionList ?: Libbox.newConnections().also { connectionList = it }
        list.applyEvents(events)
        list.sortByDate()
        val iterator = list.iterator()
        val rows = mutableListOf<ConnectionRow>()
        while (iterator.hasNext() && rows.size < 100) {
            val connection = iterator.next()
            rows.add(ConnectionRow(connection.id, connection.domain.ifEmpty { connection.destination }, connection.source, connection.destination, connection.outbound, connection.rule, connection.closedAt != 0L))
        }
        connections = rows
    }

    fun closeConnection(id: String): Boolean {
        return try {
            val current = client ?: error("Connect the tunnel first")
            current.closeConnection(id)
            true
        } catch (error: Exception) {
            MobilecoreRuntime.setNotice("close connection: ${error.message}")
            false
        }
    }
}
