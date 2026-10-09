package com.pidal.sakamoto.bg

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import com.pidal.sakamoto.runtime.AutomaticConnectionSettings
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.MobilecoreRuntime

/** Credential-encrypted configuration is available after the first unlock. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action !in setOf(Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED)) return
        if (!AutomaticConnectionSettings.load(context).startOnBoot) return
        if (VpnService.prepare(context) != null) return
        val config = ConfigRepository.load(context)
        if (config.generatedContent.isBlank() || config.sourceNeedsGenerate) return
        try { TunnelBoxService.start(context) }
        catch (_: Exception) { MobilecoreRuntime.setNotice("Automatic VPN startup failed. Open Home to reconnect.") }
    }
}
