package com.pidal.sakamoto.runtime

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.SakamotoApplication
import com.pidal.sakamoto.bg.SakamotoVpnService
import kotlinx.coroutines.*

/** One sanitized status projection shared by the widget and service notification. */
object SystemStatusSurface {
    data class Display(val title: String, val detail: String, val experiment: String, val running: Boolean, val latency: WidgetPresentation.Model)
    private var job: Job? = null
    fun display(context: Context): Display {
        val state = MobilecoreRuntime.state.value
        val physical = VpnDiagnostics.snapshot(context)
        val running = state.serviceState == "Running" && physical.vpn
        val title = when {
            state.serviceState == "Starting" -> context.getString(R.string.phase_starting)
            state.serviceState == "Stopping" -> context.getString(R.string.phase_stopping)
            running && physical.physical.isEmpty() -> context.getString(R.string.vpn_no_network)
            running && state.probeState == "Reachable" -> context.getString(R.string.phase_reachable)
            running -> context.getString(R.string.phase_tun_running)
            physical.vpn -> context.getString(R.string.widget_unconfirmed)
            else -> context.getString(R.string.phase_disconnected)
        }
        val experiment = ExperimentRuntime.settings(context)
        // A widget may outlive the process. Cross-check system VPN ownership
        // before reusing any in-memory successful probe label.
        val observedProbe = if (running) state.probeDetail else context.getString(R.string.vpn_not_checked)
        val probe = observedProbe + if (running && state.probeAt > 0) " · " + java.text.DateFormat.getTimeInstance(java.text.DateFormat.SHORT).format(java.util.Date(state.probeAt)) else ""
        val latency = WidgetPresentation.resolve(state, physical.vpn, physical.physical.isNotEmpty(), com.pidal.sakamoto.command.CommandClientRuntime.groups.value, System.currentTimeMillis() / 1000)
        val measured = if (latency.latency == WidgetPresentation.Latency.FRESH) "${latency.delay} ms" else ""
        return Display(title, listOf(state.routingMode.ifEmpty { "—" }, latency.node.ifEmpty { "—" }, measured, probe).filter { it.isNotEmpty() }.joinToString(" · "),
            "${context.getString(R.string.experiments_title)}: ${experiment.mode} · ${ExperimentRuntime.learned(context).size} ${context.getString(R.string.experiment_learned_short)}", running, latency)
    }
    fun pending(context: Context, action: String): PendingIntent = PendingIntent.getService(context, action.hashCode(),
        Intent(context, SakamotoVpnService::class.java).setAction(action), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    fun open(context: Context, action: String = ""): PendingIntent = PendingIntent.getActivity(context, action.hashCode(),
        Intent(context, MainActivity::class.java).setAction(action).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    fun notification(context: Context): android.app.Notification {
        val display = display(context)
        return NotificationCompat.Builder(context, SakamotoApplication.CHANNEL_SERVICE)
            .setSmallIcon(R.drawable.ic_service)
            .setContentTitle(display.title).setContentText(display.detail)
            .setStyle(NotificationCompat.BigTextStyle().bigText("${display.detail}\n${display.experiment}\n${ExperimentRuntime.status.value.message}"))
            .setContentIntent(open(context, ACTION_STATUS))
            .setCategory(NotificationCompat.CATEGORY_SERVICE).setOnlyAlertOnce(true).setOngoing(true)
            .addAction(R.drawable.ic_service, context.getString(R.string.disconnect), pending(context, ACTION_STOP))
            .addAction(R.drawable.ic_info, context.getString(R.string.vpn_check_now), pending(context, ACTION_CHECK))
            .addAction(R.drawable.ic_route, context.getString(R.string.experiment_recover), pending(context, ACTION_RECOVER))
            .build()
    }
    fun toggle(context: Context): PendingIntent = PendingIntent.getBroadcast(context, ACTION_TOGGLE.hashCode(),
        Intent(context, com.pidal.sakamoto.widget.WidgetToggleReceiver::class.java).setAction(ACTION_TOGGLE),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    const val ACTION_TOGGLE = "com.pidal.sakamoto.TOGGLE"
    const val ACTION_STOP = "com.pidal.sakamoto.STOP"
    const val ACTION_CHECK = "com.pidal.sakamoto.CHECK"
    const val ACTION_RECOVER = "com.pidal.sakamoto.RECOVER"
    const val ACTION_CONNECT = "com.pidal.sakamoto.CONNECT"
    const val ACTION_STATUS = "com.pidal.sakamoto.STATUS"
    fun start(context: Context) {
        if (job != null) return
        job = CoroutineScope(SupervisorJob() + Dispatchers.Main).launch {
            var previous: Display? = null
            var previousExperiment = ""
            while (isActive) {
                val display = display(context)
                val experiment = ExperimentRuntime.status.value.message
                if (display != previous || experiment != previousExperiment) {
                    context.getSystemService(android.app.NotificationManager::class.java).notify(1, notification(context))
                    com.pidal.sakamoto.widget.VpnWidgetProvider.updateAll(context)
                    previous = display; previousExperiment = experiment
                }
                delay(2000)
            }
        }
    }
    fun stop(context: Context) {
        job?.cancel(); job = null
        context.getSystemService(android.app.NotificationManager::class.java).cancel(1)
        com.pidal.sakamoto.widget.VpnWidgetProvider.updateAll(context)
    }
}
