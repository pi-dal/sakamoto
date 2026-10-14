package com.pidal.sakamoto.command

import kotlinx.coroutines.launch
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
 * The action channel carries Groups and ClashMode while the VPN runs.
 * An independent observer carries Status/Connections only for visible Data,
 * plus Connections/Log when background learning needs flow evidence.
 * Data uses one-second native updates; learning alone uses five seconds.
 * Observation changes preserve the action client and the running core.
 *
 * Clash-mode words are the routing-mode words ("Rule"/"Global"/"Direct"):
 * cycling computes the NEXT word with Mobilecore.nextRoutingMode (the Go
 * bridge owns the cycle), then applies it with client.setClashMode.
 *
 * Verified through the native power observation instrumentation lane.
 */
object CommandClientRuntime : CommandClientHandler {

    @Volatile private var client: CommandClient? = null
    private val actionGeneration = java.util.concurrent.atomic.AtomicLong()

    private class ActionHandler(private val generation: Long) : CommandClientHandler by CommandClientRuntime {
        private fun lifecycle(action: () -> Unit) = synchronized(CommandClientRuntime) {
            if (actionGeneration.get() == generation && client != null) action()
        }
        override fun connected() = lifecycle { CommandClientRuntime.connected() }
        override fun disconnected(message: String?) = lifecycle { CommandClientRuntime.disconnected(message) }
        override fun writeGroups(message: OutboundGroupIterator?) { writeGroupSnapshot(message, generation) }
        override fun initializeClashMode(modeList: StringIterator?, currentMode: String?) = lifecycle { CommandClientRuntime.initializeClashMode(modeList, currentMode) }
        override fun updateClashMode(newMode: String?) = lifecycle { CommandClientRuntime.updateClashMode(newMode) }
    }
    @Volatile private var dataVisible = false
    @Volatile private var learningEnabled = false
    private var observer: CommandClient? = null
    private val observerGeneration = java.util.concurrent.atomic.AtomicLong()
    private val observerExecutor = java.util.concurrent.Executors.newSingleThreadExecutor()
    private val observerScope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.IO)

    @Synchronized fun setDataVisible(value: Boolean) {
        if (dataVisible == value) return
        dataVisible = value
        lastStatus = null
        connectionList = null; connections = emptyList()
        reconfigureObserver()
    }

    @Synchronized fun setLearningEnabled(value: Boolean) {
        if (learningEnabled == value) return
        learningEnabled = value
        reconfigureObserver()
    }

    /** Serial attachment and generation checks make rapid hide/stop safe.
     * The action client is never replaced when observation demand changes. */
    private fun reconfigureObserver() {
        val generation = observerGeneration.incrementAndGet()
        val data = dataVisible
        val learning = learningEnabled
        observerExecutor.execute {
            if (observerGeneration.get() != generation) return@execute
            runCatching { observer?.disconnect() }
            observer = null
            if (!channelConnected.value || (!data && !learning)) return@execute
            val options = CommandClientOptions()
            if (data) options.addCommand(Libbox.CommandStatus)
            options.addCommand(Libbox.CommandConnections)
            if (learning) options.addCommand(Libbox.CommandLog)
            // Native time.Duration is nanoseconds. Learning ignores byte
            // updates; new/closed evidence is still delivered immediately.
            options.statusInterval = if (data) 1_000_000_000L else 5_000_000_000L
            val candidate = Libbox.newCommandClient(ObservationHandler(generation, data, learning), options)
            observer = candidate
            try { candidate.connect() }
            catch (_: Exception) { retryObserver(generation) }
            if (observerGeneration.get() != generation || !channelConnected.value) {
                runCatching { candidate.disconnect() }
                if (observer === candidate) observer = null
            }
        }
    }

    private fun retryObserver(generation: Long) {
        synchronized(this) {
            if (observerGeneration.get() != generation) return
            lastStatus = null
            connectionList = null; connections = emptyList()
        }
        observerScope.launch {
            kotlinx.coroutines.delay(2000)
            if (observerGeneration.get() == generation && channelConnected.value) reconfigureObserver()
        }
    }

    private class ObservationHandler(private val generation: Long, private val data: Boolean, private val learning: Boolean) : CommandClientHandler by CommandClientRuntime {
        private var firstLogs = true
        private fun intake(action: () -> Unit) = synchronized(CommandClientRuntime) {
            if (observerGeneration.get() == generation && channelConnected.value) action()
        }
        override fun connected() {}
        override fun disconnected(message: String?) { retryObserver(generation) }
        override fun writeStatus(message: StatusMessage?) = intake { if (data) CommandClientRuntime.writeStatus(message) }
        override fun writeConnectionEvents(events: ConnectionEvents?) = intake {
            if (events != null) {
                if (learning) com.pidal.sakamoto.runtime.ExperimentRuntime.connection(events)
                if (data) CommandClientRuntime.writeConnectionEvents(events)
            }
        }
        override fun writeLogs(messageList: LogIterator?) = intake {
            if (firstLogs) { firstLogs = false }
            else if (learning) CommandClientRuntime.writeLogs(messageList)
        }
    }

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
        val candidate: CommandClient
        val generation: Long
        synchronized(this) {
            if (client != null) return
            generation = actionGeneration.incrementAndGet()
            val options = CommandClientOptions()
            options.addCommand(Libbox.CommandGroup)
            options.addCommand(Libbox.CommandClashMode)
            options.statusInterval = 1_000_000_000L
            candidate = Libbox.newCommandClient(ActionHandler(generation), options)
            client = candidate
        }
        try { candidate.connect() }
        catch (error: Exception) {
            synchronized(this) {
                if (actionGeneration.get() == generation) {
                    client = null
                    MobilecoreRuntime.setNotice("command client: ${error.message}")
                }
            }
        }
        if (actionGeneration.get() != generation || client !== candidate) runCatching { candidate.disconnect() }
    }

    fun stop() {
        val current = synchronized(this) {
            actionGeneration.incrementAndGet()
            _channelConnected.value = false
            lastStatus = null; connectionList = null; connections = emptyList()
            _groups.value = emptyList(); lastGroupsSummary = ""
            val old = client
            client = null
            stopTailscaleSubscription()
            old
        }
        reconfigureObserver()
        runCatching { current?.disconnect() }
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
    private val pingGeneration = java.util.concurrent.atomic.AtomicLong()
    private var pingJob: kotlinx.coroutines.Job? = null
    private val pingScope = kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.SupervisorJob() + kotlinx.coroutines.Dispatchers.IO)

    fun cancelTailscalePing() {
        pingGeneration.incrementAndGet()
        pingJob?.cancel()
        pingJob = null
    }

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
        cancelTailscalePing()
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
        cancelTailscalePing()
        val generation = pingGeneration.get()
        pingJob = pingScope.launch {
            val handler = object : TailscalePingHandler {
                private fun record(outcome: com.pidal.sakamoto.runtime.TailscalePingOutcome) {
                    if (pingGeneration.get() == generation && channelConnected.value) {
                        TailscaleRuntime.appendPing(outcome, "$endpointTag / $peerIP")
                    }
                }
                override fun onPingResult(result: TailscalePingResult?) {
                    if (result != null) record(TailscaleBinding.mapPing(result))
                }
                override fun onError(message: String?) {
                    record(com.pidal.sakamoto.runtime.TailscalePingOutcome(0.0, false, "", "", "", message ?: "ping error"))
                }
            }
            try {
                val session = current.startTailscalePing(endpointTag, peerIP, handler)
                try { kotlinx.coroutines.delay(5000) } finally { runCatching { session.close() } }
            } catch (error: kotlinx.coroutines.CancellationException) {
                throw error
            } catch (error: Exception) {
                handler.onError(error.message ?: "start failed")
            }
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

    // --- CommandClientHandler (libbox v1.14.2 surface) ----------------------

    override fun connected() {
        _channelConnected.value = true
        reconfigureObserver()
    }

    override fun disconnected(message: String?) {
        _channelConnected.value = false
        lastStatus = null
        reconfigureObserver()
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

    override fun writeGroups(message: OutboundGroupIterator?) = writeGroupSnapshot(message, actionGeneration.get())

    private fun writeGroupSnapshot(message: OutboundGroupIterator?, generation: Long) {
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
        synchronized(this) {
            if (actionGeneration.get() != generation || client == null) return
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
        if (actionGeneration.get() == generation) com.pidal.sakamoto.runtime.ExperimentRuntime.groupsChanged(snapshots)
    }

    override fun writeOutbounds(message: OutboundGroupItemIterator?) {
        // Standalone outbound listing is a Data-page follow-up.
    }

    override fun initializeClashMode(modeList: StringIterator?, currentMode: String?) {
        // Adopt the running core's mode verbatim; the cycle still goes
        // through Mobilecore.nextRoutingMode.
        if (currentMode != null) {
            MobilecoreRuntime.setRoutingMode(currentMode)
            com.pidal.sakamoto.runtime.ExperimentRuntime.modeChanged()
        }
    }

    override fun updateClashMode(newMode: String?) {
        if (newMode != null) {
            MobilecoreRuntime.setRoutingMode(newMode)
            com.pidal.sakamoto.runtime.ExperimentRuntime.modeChanged()
        }
    }

    data class ConnectionRow(val id: String, val name: String, val source: String, val destination: String, val outbound: String, val rule: String, val closed: Boolean)

    @Volatile var connections: List<ConnectionRow> = emptyList()
        private set
    private var connectionList: io.nekohasekai.libbox.Connections? = null

    @Synchronized override fun writeConnectionEvents(events: ConnectionEvents?) {
        if (events == null) return
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
