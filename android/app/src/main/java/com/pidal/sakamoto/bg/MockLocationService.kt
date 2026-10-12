package com.pidal.sakamoto.bg

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.location.provider.ProviderProperties
import android.location.Location
import android.location.LocationManager
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import com.google.android.gms.common.ConnectionResult
import com.google.android.gms.common.GoogleApiAvailability
import com.google.android.gms.location.FusedLocationProviderClient
import com.google.android.gms.location.LocationServices
import com.pidal.sakamoto.MainActivity
import com.pidal.sakamoto.R
import com.pidal.sakamoto.runtime.MockCoordinates
import com.pidal.sakamoto.runtime.MockLocationRuntime
import com.pidal.sakamoto.runtime.MockLocationSnapshot
import com.pidal.sakamoto.runtime.MockLocationStore

/** Explicit user-started simulation, independent of the VPN. Never boot-started
 * or sticky. Every shutdown removes test providers and exits Google mock mode. */
class MockLocationService : Service() {
    private lateinit var locations: LocationManager
    private val handler = Handler(Looper.getMainLooper())
    private val providers = mutableSetOf<String>()
    private var fused: FusedLocationProviderClient? = null
    private var fusedEnabled = false
    private var fusedAttempted = false
    private var googleAvailability = ConnectionResult.SERVICE_MISSING
    private var closing = false
    private var running = false
    private var starting = false
    private var completedStop = false
    private var recoveryRequired = false
    private var injecting = false
    private var point = MockCoordinates()
    private var fixes = 0L
    private var lastFix = 0L
    private var stopError = ""
    private val setupTimeout = Runnable { finish("Google location setup timed out. Check Play services and retry.") }
    private val stopTimeout = Runnable {
        recoveryRequired = true; stopError = "Google mock-mode exit did not confirm within 8 seconds. Retry Stop simulation before starting again."; completeStop()
    }
    private fun persistProviders() { getSharedPreferences("virtual-location", MODE_PRIVATE).edit().putStringSet("ownedProviders", providers.toSet()).apply() }
    private val tick = object : Runnable {
        override fun run() {
            if (closing || !running || injecting) return
            try {
                if (!MockLocationRuntime.authorized(this@MockLocationService)) error("Select Sakamoto as the mock location app in Developer options")
                providers.forEach { locations.setTestProviderLocation(it, fix(it)) }
                if (fusedEnabled) {
                    injecting = true
                    fused!!.setMockLocation(fix("fused"))
                        .addOnSuccessListener { injecting = false; if (!closing) publishedFix() }
                        .addOnFailureListener { injecting = false; if (!closing) finish("Google mock injection failed. Check mock-location authorization and retry.") }
                } else publishedFix()
            } catch (_: SecurityException) { finish("Mock authorization was withdrawn. Select Sakamoto in Developer options and retry.") }
            catch (error: Exception) { finish(error.message ?: "Mock injection failed") }
        }
    }
    override fun onCreate() {
        super.onCreate()
        locations = getSystemService(LocationManager::class.java)
        fusedAttempted = MockLocationStore(this).needsRecovery()
        providers.addAll(getSharedPreferences("virtual-location", MODE_PRIVATE).getStringSet("ownedProviders", emptySet()) ?: emptySet())
        if (Build.VERSION.SDK_INT >= 26) getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, getString(R.string.virtual_location), NotificationManager.IMPORTANCE_LOW)
        )
        googleAvailability = GoogleApiAvailability.getInstance().isGooglePlayServicesAvailable(this)
        if (googleAvailability == ConnectionResult.SUCCESS) fused = LocationServices.getFusedLocationProviderClient(this)
    }
    override fun onBind(intent: Intent?): IBinder? = null
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == STOP || intent == null) { finish(); return START_NOT_STICKY }
        if (closing) return START_NOT_STICKY
        try {
            point = MockLocationStore(this).load().validate()
            if (!MockLocationRuntime.authorized(this)) error("Select Sakamoto as the mock location app in Developer options")
            if (!locations.isProviderEnabled(LocationManager.GPS_PROVIDER) && !locations.isProviderEnabled(LocationManager.NETWORK_PROVIDER)) error("Turn on device Location Services first")
            val notification = notification()
            if (Build.VERSION.SDK_INT >= 29) startForeground(NOTIFICATION, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION) else startForeground(NOTIFICATION, notification)
            if (running) {
                handler.removeCallbacks(tick); if (!injecting) tick.run()
                getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification)
                return START_NOT_STICKY
            }
            if (starting) return START_NOT_STICKY
            if (providers.isNotEmpty()) { removeProviders(); check(providers.isEmpty()) { "Restore providers using Stop simulation before starting again" } }
            starting = true
            MockLocationRuntime.update(MockLocationSnapshot(phase = "Starting"))
            for (name in listOf(LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER)) {
                @Suppress("DEPRECATION")
                locations.addTestProvider(name, false, false, false, false, true, true, true, ProviderProperties.POWER_USAGE_LOW, ProviderProperties.ACCURACY_FINE)
                providers.add(name); persistProviders()
                locations.setTestProviderEnabled(name, true)
            }
            if (fused != null) {
                fusedAttempted = true; MockLocationStore(this).markRecovery(true)
                handler.postDelayed(setupTimeout, 8000)
                fused!!.setMockMode(true).addOnSuccessListener {
                    handler.removeCallbacks(setupTimeout)
                    if (closing) return@addOnSuccessListener
                    fusedEnabled = true
                    starting = false; running = true; tick.run()
                }.addOnFailureListener { if (!closing) finish("Google mock setup failed. Select Sakamoto as the mock location app and retry.") }
            } else { starting = false; running = true; tick.run() }
        } catch (_: SecurityException) { finish("Location permission or mock authorization is missing. Check permissions and Developer options, then retry.") }
        catch (error: Exception) { finish(error.message ?: "Could not start location simulation") }
        return START_NOT_STICKY
    }
    private fun fix(provider: String) = Location(provider).apply {
        latitude = point.latitude; longitude = point.longitude; accuracy = point.accuracy.toFloat()
        altitude = 0.0; speed = 0f; bearing = 0f
        time = System.currentTimeMillis(); elapsedRealtimeNanos = SystemClock.elapsedRealtimeNanos()
    }
    private fun publishedFix() {
        if (closing) return
        fixes++; lastFix = System.currentTimeMillis()
        MockLocationRuntime.update(MockLocationSnapshot("Active", fixes, lastFix, fusedEnabled))
        handler.postDelayed(tick, 1000)
    }
    private fun notification(): Notification {
        val stop = PendingIntent.getService(this, 801, Intent(this, MockLocationService::class.java).setAction(STOP), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val open = PendingIntent.getActivity(this, 802, Intent(this, MainActivity::class.java), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        return NotificationCompat.Builder(this, CHANNEL).setSmallIcon(R.drawable.ic_node)
            .setContentTitle(getString(R.string.location_notification_title))
            .setContentText(getString(R.string.location_notification_detail))
            .setContentIntent(open).setOngoing(true).setCategory(NotificationCompat.CATEGORY_SERVICE)
            .addAction(0, getString(R.string.location_stop), stop).build()
    }
    private fun removeProviders() {
        for (name in providers.toList()) {
            try { locations.removeTestProvider(name); providers.remove(name) }
            catch (_: IllegalArgumentException) { providers.remove(name) }
            catch (_: Exception) { recoveryRequired = true; stopError = "Android rejected provider cleanup. Select Sakamoto as the mock location app and retry Stop simulation." }
        }
        persistProviders()
    }
    private fun finish(reason: String = "") {
        if (closing) return
        closing = true; running = false; stopError = reason
        MockLocationStore(this).markRecovery(true)
        handler.removeCallbacks(tick); handler.removeCallbacks(setupTimeout)
        MockLocationRuntime.update(MockLocationSnapshot("Stopping", fixes, lastFix, fusedEnabled, reason))
        removeProviders()
        val client = if (fusedAttempted) fused else null
        if (client != null) {
            handler.postDelayed(stopTimeout, 8000)
            try { client.setMockMode(false).addOnCompleteListener { result ->
                if (completedStop) return@addOnCompleteListener
                if (!result.isSuccessful) { recoveryRequired = true; stopError = "Could not exit Google mock mode. Select Sakamoto in Developer options and retry Stop simulation." }
                else fusedEnabled = false
                completeStop()
            } } catch (_: SecurityException) { recoveryRequired = true; stopError = "Google mock cleanup was denied. Reselect Sakamoto in Developer options, then retry Stop simulation."; completeStop() }
            catch (_: Exception) { recoveryRequired = true; stopError = "Google mock cleanup failed. Retry Stop simulation."; completeStop() }
        } else {
            if (fusedAttempted && googleAvailability != ConnectionResult.SERVICE_MISSING && googleAvailability != ConnectionResult.SERVICE_DISABLED) {
                recoveryRequired = true; stopError = "Google mock cleanup is unavailable. Update Play services, then retry Stop simulation."
            }
            completeStop()
        }
    }
    private fun completeStop() {
        if (completedStop) return
        completedStop = true; handler.removeCallbacks(stopTimeout)
        MockLocationStore(this).markRecovery(recoveryRequired)
        MockLocationRuntime.update(MockLocationSnapshot(if (stopError.isEmpty()) "Off" else "Error", fixes, lastFix, fusedEnabled, stopError, recoveryRequired))
        if (Build.VERSION.SDK_INT >= 24) stopForeground(STOP_FOREGROUND_REMOVE) else @Suppress("DEPRECATION") stopForeground(true)
        stopSelf()
    }
    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        if (!completedStop) {
            closing = true; running = false; removeProviders()
            completedStop = true
            if (fused != null && fusedAttempted) {
                recoveryRequired = true; MockLocationStore(this).markRecovery(true)
                stopError = "Service ended before Google mock cleanup was confirmed. Retry Stop simulation."
                try { fused?.setMockMode(false)?.addOnSuccessListener {
                    if (providers.isEmpty()) {
                        MockLocationStore(this).markRecovery(false)
                        MockLocationRuntime.update(MockLocationSnapshot("Off", fixes, lastFix))
                    }
                } } catch (_: SecurityException) { /* Recovery remains required. */ }
                catch (_: Exception) { /* Recovery remains required. */ }
            } else MockLocationStore(this).markRecovery(recoveryRequired)
            MockLocationRuntime.update(MockLocationSnapshot(if (stopError.isEmpty()) "Off" else "Error", fixes, lastFix, fusedEnabled, stopError, recoveryRequired))
        }
        super.onDestroy()
    }
    companion object {
        private const val CHANNEL = "virtual-location"
        private const val NOTIFICATION = 800
        private const val START = "com.pidal.sakamoto.LOCATION_START"
        private const val STOP = "com.pidal.sakamoto.LOCATION_STOP"
        fun start(context: Context, point: MockCoordinates) {
            val current = MockLocationRuntime.snapshot.value
            val active = current.phase == "Active" || current.phase == "Starting"
            check(!current.recoveryRequired && (active || !MockLocationStore(context).needsRecovery())) { "Retry Stop simulation to restore providers before starting again" }
            MockLocationStore(context).save(point)
            ContextCompat.startForegroundService(context, Intent(context, MockLocationService::class.java).setAction(START))
        }
        fun stop(context: Context) { context.startService(Intent(context, MockLocationService::class.java).setAction(STOP)) }
    }
}
