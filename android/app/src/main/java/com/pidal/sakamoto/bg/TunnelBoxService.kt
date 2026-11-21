package com.pidal.sakamoto.bg

import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.SakamotoApplication
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ExperimentRuntime
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.SystemStatusSurface
import com.pidal.sakamoto.security.TailscaleAuthKeyStore
import com.pidal.sakamoto.security.TailscaleConfigInjection
import io.nekohasekai.libbox.*
import io.nekohasekai.mobilecore.Mobilecore
import io.nekohasekai.mobilecore.RecoverySession
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import org.json.JSONObject

/** One native lifecycle worker; Go owns admission, generations and repair policy. */
class TunnelBoxService(private val service: Service, private val platformInterface: PlatformInterface) : CommandServerHandler {
    companion object {
        private const val TAG = "TunnelBoxService"
        private const val NOTIFICATION_ID = 1
        const val ACTION_CLOSE = "com.pidal.sakamoto.action.SERVICE_CLOSE"
        const val EXTRA_MANUAL = "manual"
        const val EXTRA_REPAIR = "repair"
        const val EXTRA_REQUEST = "request"
        private val requests = AtomicLong()
        private val ownership = Any()
        @Volatile private var active: TunnelBoxService? = null
        fun start(context: Context, manual: Boolean = true, repair: Boolean = false) {
            val intent = Intent(context, SakamotoVpnService::class.java).putExtra(EXTRA_MANUAL, manual)
                .putExtra(EXTRA_REPAIR, repair).putExtra(EXTRA_REQUEST, requests.incrementAndGet())
            ContextCompat.startForegroundService(context, intent)
        }
        fun stop(context: Context) {
            com.pidal.sakamoto.runtime.TunnelRepair.cancel()
            requests.incrementAndGet()
            val owner = active
            if (owner != null) owner.stopService(cancelRepair = false)
            else context.sendBroadcast(Intent(ACTION_CLOSE).setPackage(context.packageName))
        }
        fun stopForRepair() { requests.incrementAndGet(); active?.stopService(cancelRepair = false) }
        /** Fresh native query with a bounded caller wait, never on Android's UI thread. */
        fun verifiedInstance(): Long = active?.verifyInstance() ?: 0L
        fun applySaved(expected: ConfigRepository.StagedConfig) {
            checkNotNull(active) { "Connect the tunnel before applying configuration" }.reload(expected)
        }
        fun hasOwner(): Boolean = active != null
    }
    private val gate = Mobilecore.newRuntimeGate()
    private val stopping = AtomicBoolean()
    private var elapsedMillis: () -> Long = android.os.SystemClock::elapsedRealtime
    private val applying = AtomicBoolean()
    private val worker = Executors.newSingleThreadScheduledExecutor { runnable -> Thread(runnable, "sakamoto-vpn-lifecycle").apply { isDaemon = true } }
    private val probeWorker = Executors.newSingleThreadExecutor { runnable -> Thread(runnable, "sakamoto-vpn-check").apply { isDaemon = true } }
    private var probeFuture: java.util.concurrent.Future<Long>? = null
    @Volatile private var commandServer: CommandServer? = null
    @Volatile private var starting = false
    @Volatile private var reloading = false
    @Volatile private var closingNative = false
    private val publicationLock = Any()
    @Volatile private var ticket = 0L
    private var session: RecoverySession? = null
    private var journalTicket: String? = null
    private var stableTask: java.util.concurrent.ScheduledFuture<*>? = null
    private val tunLock = Any()
    private val admissionLock = Any()
    private var tun: ParcelFileDescriptor? = null
    private var receiverRegistered = false
    private data class Profile(val content: String, val metadata: String, val retained: Boolean = false)
    private var applied: Profile? = null
    // The lifecycle worker owns this permission. A failed Apply keeps the confirmed selection pinned.
    private var cleanDisconnect: ConfirmedProfile? = null
    @Volatile private var destroyed = false
    @Volatile private var completedState: String? = null
    fun onDestroyed() {
        destroyed = true
        if (completedState != null) releaseOwnership() else stopService()
    }
    private fun releaseOwnership() {
        synchronized(ownership) {
            if (active === this) { active = null; completedState?.let { MobilecoreRuntime.setServiceState(it) } }
        }
    }
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) { if (intent.action == ACTION_CLOSE) stopService() }
    }

    /** Establishment and stop share a short fd publication lock. A late fd is closed. */
    fun acceptTun(fd: ParcelFileDescriptor): Int = synchronized(tunLock) {
        if (!gate.current(ticket) || stopping.get()) { fd.close(); error("VPN startup was canceled") }
        tun?.close(); tun = fd; fd.fd
    }
    private fun closeTun() = synchronized(tunLock) { runCatching { tun?.close() }; tun = null }
    private fun current(value: Long) { check(!stopping.get() && gate.current(value)) { "VPN operation was canceled" } }

    @Synchronized fun onStartCommand(manual: Boolean, repair: Boolean, request: Long): Int {
        showForegroundNotification(true)
        if (request != 0L && requests.get() != 0L && request != requests.get()) {
            synchronized(ownership) { if (active == null) active = this }
            stopService(cancelRepair = false); return Service.START_NOT_STICKY
        }
        if (stopping.get() || commandServer != null || starting) return Service.START_NOT_STICKY
        synchronized(ownership) {
            if (active != null && active !== this) {
                MobilecoreRuntime.setNotice("The previous core has not finished stopping. Use Android VPN settings if it remains unresponsive.")
                service.stopSelf(); return Service.START_NOT_STICKY
            }
            active = this
        }
        ticket = gate.begin()
        if (ticket == 0L) return Service.START_NOT_STICKY
        starting = true
        MobilecoreRuntime.setServiceState("Starting")
        if (!receiverRegistered) {
            ContextCompat.registerReceiver(service, receiver, IntentFilter(ACTION_CLOSE), ContextCompat.RECEIVER_NOT_EXPORTED)
            receiverRegistered = true
        }
        val operation = ticket
        worker.execute {
            try {
                current(operation)
                val journal = synchronized(admissionLock) {
                    current(operation)
                    val created = Mobilecore.newRecoverySession(service.filesDir.absolutePath)
                    session = created
                    journalTicket = created.begin(manual)
                    created
                }
                current(operation)
                ExperimentRuntime.restorePending(service)
                val server = CommandServer(this, platformInterface)
                commandServer = server
                server.start()
                current(operation)
                val staged = ConfigRepository.load(service)
                com.pidal.sakamoto.runtime.TunnelPackage.validateRouteSource(staged.generatedContent, staged.routeSourceVersion)
                val receipt = ConfigRepository.manifestReceipt(service)
                check(ConfigRepository.loadAt(service, receipt) == staged)
                val confirmed = confirmedProfile(staged)
                val entry = confirmed.selected()
                if (entry != null && (staged.sourceNeedsGenerate || ((repair || !entry.connectStaged) && entry.receipt != receipt))) {
                    val saved = retainedProfile(Mobilecore.newRecoverySession(confirmed.directory(entry).absolutePath))
                    startProfile(server, journal, saved, operation, initial = true, rollbackOf = staged)
                    return@execute
                }
                val rollback = rollbackDirectory(staged)
                if (entry == null && rollback.exists()) {
                    val saved = retainedProfile(Mobilecore.newRecoverySession(rollback.absolutePath))
                    val origin = JSONObject(saved.metadata).getString("applyBaseReceipt")
                    startProfile(server, journal, saved, operation, initial = true,
                        rollbackOf = if (staged.sourceNeedsGenerate) staged else ConfigRepository.loadAt(service, origin))
                    return@execute
                }
                try {
                    if (entry?.connectStaged == true) confirmed.commit(entry.copy(connectStaged = false))
                    val profile = currentProfile(staged)
                    startProfile(server, journal, profile, operation, initial = true, checkpoint = if (entry?.receipt != receipt) {
                        { captured ->
                            val next = confirmed.prepare(captured, profile.metadata, receipt, service.filesDir)
                            val publish: () -> Unit = { check(ConfigRepository.loadAt(service, receipt) == staged); confirmed.commit(next) }
                            publish
                        }
                    } else null)
                    cleanDisconnect = confirmed
                }
                catch (error: Exception) {
                    current(operation)
                    journal.failed(checkNotNull(journalTicket))
                    val retained = if (entry != null) {
                        retainedProfile(Mobilecore.newRecoverySession(confirmed.directory(entry).absolutePath))
                    } else {
                        val stable = retainedProfile(journal)
                        val owner = staged.modeCacheID.ifEmpty { staged.legacyExperimentID }
                        check(owner.isNotEmpty() && JSONObject(stable.metadata).optString("profileID") == owner) {
                            "No verified recovery profile for the selected configuration"
                        }
                        stable
                    }
                    journalTicket = journal.beginFallback()
                    startProfile(server, journal, retained, operation, initial = true, rollbackOf = if (entry != null) staged else null)
                }
            } catch (error: Exception) {
                if (!stopping.get()) failAndClose(error as? com.pidal.sakamoto.runtime.TunnelPackage.LegacyRouteSource)
            } finally { starting = false }
        }
        return Service.START_NOT_STICKY
    }
    private fun currentProfile(config: ConfigRepository.StagedConfig = ConfigRepository.load(service)): Profile {
        check(!config.sourceNeedsGenerate) { "Generate the current sources before connecting" }
        val base = config.runtimeContent.takeIf { it.isNotBlank() } ?: error("No complete generated configuration")
        val staged = com.pidal.sakamoto.runtime.AutomaticConnectionSettings.load(service).applying(base)
        val clean = TailscaleConfigInjection.withoutAuthKey(staged)
        // runtimeContent checked the imported rules before applying device-owned DNS rules.
        return Profile(ExperimentRuntime.prepareContent(service, clean), JSONObject().put("hostMetadata", config.hostMetadata)
            .put("profileID", config.modeCacheID.ifEmpty { config.legacyExperimentID }).put("routeSourceVersion", 1).toString())
    }
    private fun confirmedProfile(config: ConfigRepository.StagedConfig) =
        ConfirmedProfile(ConfigRepository.packageDirectory(service, config.confirmedRevision)) { ConfigRepository.load(service).learningCommit }

    private fun rollbackDirectory(config: ConfigRepository.StagedConfig) =
        java.io.File(ConfigRepository.packageDirectory(service, config.confirmedRevision), ".apply-rollback")

    private fun retainedProfile(journal: RecoverySession): Profile = Profile(journal.retainedConfig(), journal.retainedMetadata(), retained = true)
    private fun startProfile(server: CommandServer, journal: RecoverySession, profile: Profile, operation: Long, initial: Boolean, beforeNative: (() -> Unit)? = null, confirmNative: (() -> Unit)? = null, rollbackOf: ConfigRepository.StagedConfig? = null, checkpoint: ((String) -> (() -> Unit))? = null) {
        current(operation)
        // The active snapshot is the rollback target of this uncommitted transaction.
        // For cold recovery or a concurrent edit, the current privacy policy must match.
        if (profile.retained && (rollbackOf == null || ConfigRepository.load(service) != rollbackOf)) {
            Mobilecore.checkRegionalDNSRecovery(currentProfile().content, profile.content)
        }
        com.pidal.sakamoto.runtime.TunnelPackage.validateRouteSource(profile.content, JSONObject(profile.metadata).optInt("routeSourceVersion"))
        Libbox.checkConfig(profile.content)
        val receipt = checkNotNull(journalTicket)
        journal.prepare(receipt, profile.content, profile.metadata, service.filesDir.absolutePath)
        val captured = journal.preparedConfig()
        val runtime = TailscaleConfigInjection.forTunnel(captured) { TailscaleAuthKeyStore(service).readAuthKey() }
        val publishSnapshot = checkpoint?.invoke(captured)
        current(operation)
        DefaultNetworkMonitor.start()
        beforeNative?.invoke()
        stableTask?.cancel(false)
        server.startOrReloadService(runtime, OverrideOptions())
        current(operation)
        check(server.isServiceRunning() && server.runningInstanceID() > 0) { "Core startup was not confirmed" }
        check(!server.needWIFIState()) { "Configuration requires unavailable Wi-Fi permissions" }
        check(synchronized(tunLock) { tun != null }) { "VPN interface was not established" }
        val instance = server.runningInstanceID()
        confirmNative?.invoke()
        if (initial) journal.ready(receipt, instance.toString(), Libbox.version()) else journal.replaced(receipt, instance.toString(), Libbox.version())
        current(operation)
        publishSnapshot?.invoke()
        applied = profile.copy(content = captured)
        if (!profile.retained) {
            ExperimentRuntime.start(service, com.pidal.sakamoto.runtime.NativeLearningProber(server, instance), persist = { before, next ->
                commitLearning(before, next, server, instance, operation)
            }) { domains ->
                current(operation); server.updateAutomaticDomains(domains, instance)
            }
            if (!ConfigRepository.load(service).sourceNeedsGenerate) MobilecoreRuntime.completeConfigApply()
        } else {
            MobilecoreRuntime.configEvent("modified"); MobilecoreRuntime.configEvent("regenerate_succeeded")
            MobilecoreRuntime.setNotice("Using the verified saved configuration; learning paused. Review Config before applying changes.")
        }
        synchronized(publicationLock) {
            current(operation)
            if (initial) check(gate.ready(operation)) { "VPN startup was canceled" }
            MobilecoreRuntime.recordCoreIdentity(instance)
            MobilecoreRuntime.setServiceState("Running")
        }
        CommandClientRuntime.start()
        showForegroundNotification(false); SystemStatusSurface.start(service)
        stableTask?.cancel(false)
        stableTask = worker.schedule({
            if (!stopping.get() && gate.current(operation) && commandServer === server && server.isServiceRunning() && server.runningInstanceID() == instance) {
                runCatching { journal.stable(receipt, instance.toString()) }.onFailure { Log.w(TAG, "Stable recovery receipt was not promoted") }
            }
        }, Mobilecore.recoveryStableSeconds(), TimeUnit.SECONDS)
    }
    private fun commitLearning(before: ConfigRepository.StagedConfig, next: ConfigRepository.StagedConfig,
                               server: CommandServer, instance: Long, operation: Long) = synchronized(publicationLock) {
        current(operation)
        check(!applying.get() && server.runningInstanceID() == instance)
        val activeProfile = checkNotNull(applied)
        val confirmed = confirmedProfile(before)
        val old = ExperimentRuntime.learned(service, before)
        val domains = org.json.JSONArray(checkNotNull(next.learnedDomains)).toString()
        val content = if (old.isEmpty()) io.nekohasekai.mobileexperiment.Mobileexperiment.resumePausedRulesJSON(activeProfile.content, domains)
            else io.nekohasekai.mobileexperiment.Mobileexperiment.prepareLiveRulesJSON(activeProfile.content, org.json.JSONArray(old).toString(), domains)
        val commit = java.util.UUID.randomUUID().toString()
        val learnedProfile = activeProfile.copy(content = content)
        ConfigRepository.saveLearned(service, next.copy(learningCommit = commit), before) { receipt ->
            check(confirmed.selected()?.receipt == ConfigRepository.manifestReceipt(service)) { "The installed configuration changed" }
            val entry = confirmed.prepare(content, activeProfile.metadata, receipt, service.filesDir)
            current(operation)
            confirmed.commit(entry, whenLearned = commit)
        }
        applied = learnedProfile
    }

    /** Core-requested stop is unexpected unless a stop/reload is already in flight. */
    override fun serviceStop() {
        closeTun()
        if (stopping.get() || reloading || starting || closingNative) return
        synchronized(publicationLock) { gate.stop(); MobilecoreRuntime.setServiceState("Unavailable") }
        worker.execute { if (!stopping.get()) failAndClose() }
    }
    override fun serviceReload() = reload(ConfigRepository.load(service))

    private fun reload(expected: ConfigRepository.StagedConfig) {
        val deadline = elapsedMillis() + 30_000
        fun confirm() {
            check(elapsedMillis() < deadline) { "Apply deadline expired" }
            check(ConfigRepository.load(service) == expected) { "Configuration changed during apply" }
        }
        synchronized(publicationLock) {
            check(applying.compareAndSet(false, true)) { "Configuration is already being applied" }
        }
        val operation = ticket
        reloading = true
        val future = try { worker.submit {
            var nativeApplyStarted = false
            val server = commandServer
            val journal = session
            val previous = applied
            cleanDisconnect = null
            try {
                current(operation)
                checkNotNull(server); checkNotNull(journal)
                val baseReceipt = ConfigRepository.manifestReceipt(service)
                check(ConfigRepository.loadAt(service, baseReceipt) == expected) { "Configuration changed before apply" }
                val confirmed = confirmedProfile(expected)
                confirm()
                val next = currentProfile(expected); Libbox.checkConfig(next.content)
                current(operation)
                MobilecoreRuntime.beginConfigApply()
                startProfile(server, journal, next, operation, initial = false, beforeNative = { confirm(); ExperimentRuntime.quiesce(); nativeApplyStarted = true }, confirmNative = ::confirm, checkpoint = { captured ->
                    val entry = confirmed.prepare(captured, next.metadata, baseReceipt, service.filesDir)
                    val publish: () -> Unit = { confirmed.commit(entry) }
                    publish
                })
                cleanDisconnect = confirmed
            } catch (error: Exception) {
                MobilecoreRuntime.configEvent("regenerate_failed")
                if (!stopping.get() && gate.current(operation) && server != null && journal != null) {
                    if (!nativeApplyStarted && server.isServiceRunning()) {
                        MobilecoreRuntime.setNotice("Configuration rejected; the running core was retained. Review Config before applying again.")
                    } else if (previous != null) {
                        try {
                            startProfile(server, journal, previous.copy(retained = true), operation, initial = false, rollbackOf = expected)
                            MobilecoreRuntime.setNotice("Configuration update failed; the previous running configuration was restored. Review Config before applying again.")
                        } catch (rollback: Exception) { if (!stopping.get()) failAndClose() }
                    } else failAndClose()
                }
                throw IllegalStateException("Configuration was not applied; inspect Home before trying again")
            } finally { reloading = false; applying.set(false) }
        } } catch (error: Exception) { reloading = false; applying.set(false); throw error }
        // This is the Go command-server callback thread, not Android's main
        // thread. A successful native ACK must mean the replacement completed.
        // A native call cannot be canceled safely. The worker owns its deadline and rollback.
        var interrupted = false
        try {
            while (true) {
                try { future.get(); return }
                catch (_: InterruptedException) { interrupted = true }
                catch (error: java.util.concurrent.ExecutionException) { throw IllegalStateException("Configuration was not applied; inspect Home", error.cause) }
            }
        } finally { if (interrupted) Thread.currentThread().interrupt() }
    }
    /** Cancellation/fd removal happen before waiting for any blocked native call. */
    fun stopService(cancelRepair: Boolean = true) {
        if (!stopping.compareAndSet(false, true)) return
        synchronized(publicationLock) { gate.stop(); MobilecoreRuntime.setServiceState("Stopping") }
        stableTask?.cancel(false)
        if (cancelRepair) {
            com.pidal.sakamoto.runtime.TunnelRepair.cancel()
            requests.incrementAndGet()
        }
        closeTun()
        synchronized(admissionLock) {
            runCatching { (session ?: Mobilecore.newRecoverySession(service.filesDir.absolutePath)).stop() }
        }
        worker.execute {
            runCatching { closeNative() }.onSuccess {
                cleanDisconnect?.let { confirmed -> runCatching {
                    confirmed.selected()?.let { confirmed.commit(it.copy(connectStaged = true)) }
                } }
                finishService("Stopped")
            }.onFailure {
                MobilecoreRuntime.setNotice("Core shutdown is unconfirmed. Disconnect in Android VPN settings before trying again.")
            }
        }
    }
    private fun failAndClose(routeError: com.pidal.sakamoto.runtime.TunnelPackage.LegacyRouteSource? = null) {
        stableTask?.cancel(false)
        journalTicket?.let { runCatching { session?.failed(it) } }
        gate.stop(); closeTun()
        if (runCatching { closeNative() }.isFailure) {
            MobilecoreRuntime.setServiceState("Unavailable")
            MobilecoreRuntime.setNotice("Core shutdown is unconfirmed. Disconnect in Android VPN settings before trying again.")
            return
        }
        if (!stopping.get()) {
            MobilecoreRuntime.configEvent("regenerate_failed")
            MobilecoreRuntime.setNotice(routeError?.message ?: "Core startup or recovery failed. Check and repair VPN, or disconnect in Android VPN settings. Private configuration details remain on this device.")
            finishService("Unavailable")
        }
    }
    private fun closeNative() {
        ExperimentRuntime.stop(); CommandClientRuntime.stop(); DefaultNetworkMonitor.stop()
        closingNative = true
        try {
            val server = commandServer
            runCatching { server?.closeService() }
            check(server?.isServiceRunning() != true) { "Core shutdown is unconfirmed" }
            server?.close(); commandServer = null
            session?.releaseSnapshots()
            closeTun(); gate.finishStop()
        } finally { closingNative = false }
    }
    private fun finishService(state: String) {
        stopping.set(true)
        if (receiverRegistered) { runCatching { service.unregisterReceiver(receiver) }; receiverRegistered = false }
        SystemStatusSurface.stop(service)
        completedState = state
        if (destroyed) releaseOwnership()
        ServiceCompat.stopForeground(service, ServiceCompat.STOP_FOREGROUND_REMOVE)
        service.stopSelf()
        probeWorker.shutdown(); worker.shutdown()
    }
    @Synchronized private fun verifyInstance(): Long {
        if (stopping.get() || gate.state() != "Running") return 0L
        if (probeFuture?.isDone == false) return 0L
        val server = commandServer ?: return 0L
        val future = probeWorker.submit<Long> { if (server.isServiceRunning()) server.runningInstanceID() else 0L }
        probeFuture = future
        return runCatching { future.get(1500, TimeUnit.MILLISECONDS) }.getOrDefault(0L)
    }
    override fun getSystemProxyStatus(): SystemProxyStatus? = null
    override fun setSystemProxyEnabled(enabled: Boolean) { serviceReload() }
    override fun triggerNativeCrash() {}
    override fun writeDebugMessage(message: String?) { Log.d(TAG, message ?: "") }
    override fun connectSSHAgent(): Int = -1

    private fun showForegroundNotification(starting: Boolean) {
        val notification = if (starting) buildNotification(service.getString(R.string.app_name), service.getString(R.string.status_starting)) else SystemStatusSurface.notification(service)
        ServiceCompat.startForeground(service, NOTIFICATION_ID, notification, if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED else 0)
    }
    fun showLibboxNotification(identifier: String, typeID: Int, title: String, body: String, openURL: String) {
        val manager = SakamotoApplication.appContext.getSystemService(android.app.NotificationManager::class.java)
        val builder = NotificationCompat.Builder(service, SakamotoApplication.CHANNEL_EVENTS).setSmallIcon(R.drawable.ic_service)
            .setContentTitle(title).setContentText(body).setOnlyAlertOnce(true).setAutoCancel(true)
        if (openURL.isNotBlank()) builder.setContentIntent(PendingIntent.getActivity(service, 0, Intent(service, MainActivity::class.java).apply {
            action = Intent.ACTION_VIEW; data = android.net.Uri.parse(openURL); addFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
        }, PendingIntent.FLAG_IMMUTABLE))
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) manager.createNotificationChannel(android.app.NotificationChannel(
            SakamotoApplication.CHANNEL_EVENTS, service.getString(R.string.channel_events), android.app.NotificationManager.IMPORTANCE_HIGH))
        manager.notify(identifier, typeID, builder.build())
    }
    private fun buildNotification(title: String, body: String): android.app.Notification = NotificationCompat.Builder(service, SakamotoApplication.CHANNEL_SERVICE)
        .setSmallIcon(R.drawable.ic_service).setContentTitle(title).setContentText(body)
        .setContentIntent(PendingIntent.getActivity(service, 0, Intent(service, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        .setCategory(NotificationCompat.CATEGORY_SERVICE).setOnlyAlertOnce(true).setOngoing(true).build()
}
