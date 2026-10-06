package com.pidal.sakamoto.widget

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import com.pidal.sakamoto.bg.TunnelBoxService
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.SystemStatusSurface
import com.pidal.sakamoto.runtime.VpnDiagnostics

/** Widget interaction grants foreground-service launch eligibility. Resolve the
 * direction at click time so a cached RemoteViews cannot send the wrong action. */
class WidgetToggleReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != SystemStatusSurface.ACTION_TOGGLE) return
        val state = MobilecoreRuntime.state.value.serviceState
        if (state == "Starting" || state == "Stopping") return
        try {
            val system = VpnDiagnostics.snapshot(context)
            // A just-closed agent can linger in ConnectivityManager. The
            // same-process Stopped state is authoritative for our service.
            if (state == "Running" || (state == "Unavailable" && system.vpn)) {
                MobilecoreRuntime.setServiceState("Stopping")
                context.startService(Intent(context, com.pidal.sakamoto.bg.SakamotoVpnService::class.java)
                    .setAction(SystemStatusSurface.ACTION_STOP))
            } else if (VpnService.prepare(context) == null) {
                MobilecoreRuntime.clearNotice()
                MobilecoreRuntime.setServiceState("Starting")
                TunnelBoxService.start(context)
            } else {
                context.startActivity(Intent(context, com.pidal.sakamoto.MainActivity::class.java)
                    .setAction(SystemStatusSurface.ACTION_CONNECT).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
        } catch (_: Exception) {
            MobilecoreRuntime.setServiceState("Unavailable")
            MobilecoreRuntime.setNotice(context.getString(com.pidal.sakamoto.R.string.widget_toggle_failed))
            context.startActivity(Intent(context, com.pidal.sakamoto.MainActivity::class.java)
                .setAction(SystemStatusSurface.ACTION_STATUS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } finally { VpnWidgetProvider.updateAll(context) }
    }
}
