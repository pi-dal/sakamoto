package com.pidal.sakamoto.bg

import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.SakamotoApplication
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.security.TailscaleAuthKeyStore
import com.pidal.sakamoto.security.TailscaleConfigInjection
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.CommandServerHandler
import io.nekohasekai.libbox.Libbox
import io.nekohasekai.libbox.OverrideOptions
import io.nekohasekai.libbox.PlatformInterface
import io.nekohasekai.libbox.SystemProxyStatus

/**
 * The tunnel-side service wrapper — a direct port of upstream
 * SagerNet/sing-box-for-android BoxService.kt for the parts this client
 * uses: CommandServerHandler + CommandServer(handler, platformInterface),
 * startOrReloadService(configContent, OverrideOptions) with the staged
 * config, and the foreground-service lifecycle around it.
 *
 * Upstream reads the profile from its Room database; we read the staged
 * generated config from ConfigRepository (filesDir JSON). Everything else —
 * the notification plumbing, the SERVICE_CLOSE broadcast, the error/alert
 * path that maps to the core's "Unavailable" service state — follows the
 * upstream shape.
 *
 * STATUS PENDING SDK BUILD VERIFICATION: this file compiles against the
 * gomobile-generated libbox API by construction (signatures mirrored from
 * upstream + experimental/libbox v1.14.2 sources), but no Android SDK/JDK
 * exists on the current machine, so it has NOT been compiled. See
 * android/README.md for the exact toolchain gap.
 */
class TunnelBoxService(
    private val service: Service,
    private val platformInterface: PlatformInterface,
) : CommandServerHandler {

    companion object {
        private const val TAG = "TunnelBoxService"
        private const val NOTIFICATION_ID = 1
        const val ACTION_CLOSE = "com.pidal.sakamoto.action.SERVICE_CLOSE"

        fun start(context: Context) {
            val intent = Intent(context, SakamotoVpnService::class.java)
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            context.sendBroadcast(
                Intent(ACTION_CLOSE).setPackage(context.packageName),
            )
        }
    }

    var fileDescriptor: ParcelFileDescriptor? = null

    private var commandServer: CommandServer? = null
    @Volatile private var reloading = false
    @Volatile private var starting = false

    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            when (intent.action) {
                ACTION_CLOSE -> stopService()
            }
        }
    }
    private var receiverRegistered = false

    /** Called from SakamotoVpnService.onStartCommand. */
    @Synchronized fun onStartCommand(): Int {
        if (commandServer != null || starting) return Service.START_NOT_STICKY
        starting = true
        MobilecoreRuntime.setServiceState("Starting")
        showForegroundNotification(starting = true)
        if (!receiverRegistered) {
            ContextCompat.registerReceiver(
                service,
                receiver,
                IntentFilter(ACTION_CLOSE),
                ContextCompat.RECEIVER_NOT_EXPORTED,
            )
            receiverRegistered = true
        }
        // Upstream: GlobalScope + Dispatchers.IO. A plain thread keeps the
        // dependency set minimal without changing the semantics.
        Thread {
            try {
                val server = CommandServer(this, platformInterface)
                server.start()
                commandServer = server
                startServer(server)
            } catch (e: Exception) {
                stopAndAlert("start command server: ${e.message}")
            } finally { starting = false }
        }.start()
        return Service.START_NOT_STICKY
    }

    private fun startServer(server: CommandServer) {
        try {
            com.pidal.sakamoto.runtime.ExperimentRuntime.restorePending(service)
            val base = ConfigRepository.readGeneratedContent(service)
            if (base == null) {
                stopAndAlert("empty configuration — import a config in the Config tab first")
                return
            }
            val staged = com.pidal.sakamoto.runtime.AutomaticConnectionSettings.load(service).applying(base)
            // Tailscale auth key injection at start time (mirrors the iOS
            // TailscaleConfigInjection): the stored key merges into the
            // tailscale endpoint config here, in the tunnel process's start
            // path. It is never logged, and the notice below never carries it.
            val content = try {
                val storedKey = TailscaleAuthKeyStore(service).readAuthKey()
                if (storedKey.isNullOrEmpty()) staged
                else TailscaleConfigInjection.inject(storedKey, staged)
            } catch (e: TailscaleConfigInjection.ConfigInjectionError.NoTailscaleEndpoint) {
                stopAndAlert("a Tailscale auth key is stored but the config has no tailscale endpoint")
                return
            } catch (e: Exception) {
                stopAndAlert("config injection failed: ${e.message}")
                return
            }
            DefaultNetworkMonitor.start()
            // The one libbox entry point that runs the box: the same call
            // upstream makes with its profile content.
            server.startOrReloadService(content, OverrideOptions())
            if (server.needWIFIState()) {
                // Wi-Fi rule matching would need location permissions this
                // client does not request yet — refuse instead of misbehaving.
                stopAndAlert("this config needs Wi-Fi state; location permissions are not granted")
                return
            }
            MobilecoreRuntime.setServiceState("Running")
            if (!ConfigRepository.load(service).sourceNeedsGenerate) MobilecoreRuntime.completeConfigApply()
            com.pidal.sakamoto.runtime.ExperimentRuntime.start(service)
            CommandClientRuntime.start()
            showForegroundNotification(starting = false)
            com.pidal.sakamoto.runtime.SystemStatusSurface.start(service)
        } catch (e: Exception) {
            stopAndAlert("create service: ${e.message}")
        }
    }

    /**
     * CommandServerHandler: invoked by the Go core when the box stops
     * (config error, fatal I/O, ...). Upstream closes the tun fd here.
     */
    override fun serviceStop() {
        MobilecoreRuntime.setServiceState("Stopping")
        fileDescriptor?.close()
        fileDescriptor = null
        if (!reloading) {
            cleanup()
            MobilecoreRuntime.setServiceState("Stopped")
            com.pidal.sakamoto.runtime.SystemStatusSurface.stop(service)
        }
    }

    /** CommandServerHandler: the core asked for a reload with current staged config. */
    override fun serviceReload() {
        val server = commandServer ?: return
        try {
            val base = ConfigRepository.readGeneratedContent(service)
            if (base == null) {
                stopAndAlert("empty configuration on reload")
                return
            }
            val staged = com.pidal.sakamoto.runtime.AutomaticConnectionSettings.load(service).applying(base)
            val content = try {
                val storedKey = TailscaleAuthKeyStore(service).readAuthKey()
                if (storedKey.isNullOrEmpty()) staged
                else TailscaleConfigInjection.inject(storedKey, staged)
            } catch (e: Exception) {
                stopAndAlert("reload: auth key injection failed: ${e.message}")
                return
            }
            MobilecoreRuntime.beginConfigApply()
            reloading = true
            server.startOrReloadService(content, OverrideOptions())
            MobilecoreRuntime.setServiceState("Running")
            MobilecoreRuntime.completeConfigApply()
            showForegroundNotification(starting = false)
        } catch (e: Exception) {
            MobilecoreRuntime.configEvent("regenerate_failed")
            stopAndAlert("reload service: ${e.message}")
        } finally { reloading = false }
    }

    override fun getSystemProxyStatus(): SystemProxyStatus? {
        // This client implements the VPN (TUN) service only; the system
        // HTTP-proxy surface of upstream ProxyService is out of scope here.
        return null
    }

    override fun setSystemProxyEnabled(enabled: Boolean) {
        serviceReload()
    }

    override fun triggerNativeCrash() {
        // Debug-only upstream; not wired on this client.
    }

    override fun writeDebugMessage(message: String?) {
        Log.d(TAG, message ?: "")
    }

    override fun connectSSHAgent(): Int = -1

    /** Internal stop: close the box, the channel, and the service itself. */
    fun stopService() {
        val server = commandServer
        if (server != null) {
            try {
                server.closeService()
            } catch (e: Exception) {
                server.setError("android: close service: ${e.message}")
            }
            server.close()
            commandServer = null
        }
        MobilecoreRuntime.setServiceState("Stopping")
        fileDescriptor?.close()
        fileDescriptor = null
        cleanup()
        MobilecoreRuntime.setServiceState("Stopped")
        com.pidal.sakamoto.runtime.SystemStatusSurface.stop(service)
    }

    /** Map a startup/runtime failure onto the core's Unavailable state. */
    private fun stopAndAlert(message: String) {
        com.pidal.sakamoto.runtime.ExperimentRuntime.stop()
        CommandClientRuntime.stop()
        fileDescriptor?.close()
        fileDescriptor = null
        DefaultNetworkMonitor.stop()
        val server = commandServer
        if (server != null) {
            runCatching { server.closeService() }
            runCatching { server.close() }
            commandServer = null
        }
        MobilecoreRuntime.setServiceState("Unavailable")
        MobilecoreRuntime.setNotice(message)
        if (receiverRegistered) {
            runCatching { service.unregisterReceiver(receiver) }
            receiverRegistered = false
        }
        com.pidal.sakamoto.runtime.SystemStatusSurface.stop(service)
        service.stopSelf()
    }

    private fun cleanup() {
        com.pidal.sakamoto.runtime.ExperimentRuntime.stop()
        CommandClientRuntime.stop()
        DefaultNetworkMonitor.stop()
        if (receiverRegistered) {
            runCatching { service.unregisterReceiver(receiver) }
            receiverRegistered = false
        }
        service.stopSelf()
    }

    private fun showForegroundNotification(starting: Boolean) {
        val notification = if (starting) buildNotification(
            title = service.getString(R.string.app_name), body = service.getString(R.string.status_starting),
        ) else com.pidal.sakamoto.runtime.SystemStatusSurface.notification(service)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            ServiceCompat.startForeground(
                service,
                NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED,
            )
        } else {
            ServiceCompat.startForeground(service, NOTIFICATION_ID, notification, 0)
        }
    }

    /** libbox SendNotification: status/log alerts from the running core. */
    fun showLibboxNotification(identifier: String, typeID: Int, title: String, body: String, openURL: String) {
        val manager = SakamotoApplication.appContext.getSystemService(android.app.NotificationManager::class.java)
        val builder = NotificationCompat.Builder(service, SakamotoApplication.CHANNEL_EVENTS)
            .setSmallIcon(R.drawable.ic_service)
            .setContentTitle(title)
            .setContentText(body)
            .setOnlyAlertOnce(true)
            .setAutoCancel(true)
        if (openURL.isNotBlank()) {
            builder.setContentIntent(
                PendingIntent.getActivity(
                    service,
                    0,
                    Intent(service, MainActivity::class.java).apply {
                        this.action = Intent.ACTION_VIEW
                        this.data = android.net.Uri.parse(openURL)
                        addFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
                    },
                    PendingIntent.FLAG_IMMUTABLE,
                ),
            )
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            manager.createNotificationChannel(
                android.app.NotificationChannel(
                    SakamotoApplication.CHANNEL_EVENTS,
                    service.getString(R.string.channel_events),
                    android.app.NotificationManager.IMPORTANCE_HIGH,
                ),
            )
        }
        manager.notify(identifier, typeID, builder.build())
    }

    private fun buildNotification(title: String, body: String): android.app.Notification =
        NotificationCompat.Builder(service, SakamotoApplication.CHANNEL_SERVICE)
            .setSmallIcon(R.drawable.ic_service)
            .setContentTitle(title)
            .setContentText(body)
            .setContentIntent(PendingIntent.getActivity(
                service, 0, Intent(service, MainActivity::class.java),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            ))
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setOnlyAlertOnce(true)
            .setOngoing(true)
            .build()
}
