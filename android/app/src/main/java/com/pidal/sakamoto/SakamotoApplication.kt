package com.pidal.sakamoto

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.net.ConnectivityManager
import android.os.Build

/**
 * Application entry point.
 *
 * Mirrors upstream SagerNet/sing-box-for-android Application.kt in the parts
 * this client uses: process-wide context accessors for the service/fragment
 * layers, and the notification channel the tunnel foreground service posts
 * into (libbox SendNotification flows through the same manager).
 */
class SakamotoApplication : Application() {

    override fun onCreate() {
        super.onCreate()
        instance = this
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(
                NotificationChannel(
                    CHANNEL_SERVICE,
                    getString(R.string.channel_service),
                    NotificationManager.IMPORTANCE_LOW,
                ),
            )
        }
    }

    companion object {
        const val CHANNEL_SERVICE = "service"
        const val CHANNEL_EVENTS = "events"

        lateinit var instance: SakamotoApplication
            private set

        val appContext: Context get() = instance

        val connectivity: ConnectivityManager?
            get() = appContext.getSystemService(ConnectivityManager::class.java)
    }
}
