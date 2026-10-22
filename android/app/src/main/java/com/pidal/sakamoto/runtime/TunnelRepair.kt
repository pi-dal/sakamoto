package com.pidal.sakamoto.runtime

import android.content.Context
import android.net.VpnService
import com.pidal.sakamoto.bg.TunnelBoxService
import io.nekohasekai.mobilecore.Mobilecore
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Platform operations around the shared Go repair decision, with confirmed teardown. */
object TunnelRepair {
    private val generation = AtomicLong()
    private val lock = Any()
    fun cancel() = synchronized(lock) { generation.incrementAndGet(); MobilecoreRuntime.recordRepair(null, false) }
    private fun perform(request: Long, operation: () -> Unit): Boolean = synchronized(lock) {
        if (generation.get() != request) false else { operation(); true }
    }
    suspend fun run(context: Context, checkOnly: Boolean = false, restart: Boolean = false) {
        val request = synchronized(lock) {
            if (MobilecoreRuntime.state.value.repairing) return
            val next = generation.incrementAndGet()
            MobilecoreRuntime.recordRepair(result("starting", "none", false, "Checking the VPN and core…"), true)
            next
        }
        fun current() = generation.get() == request
        fun publish(value: String, busy: Boolean = true) { synchronized(lock) { if (current()) MobilecoreRuntime.recordRepair(value, busy) } }
        try {
            withContext(Dispatchers.IO) {
                val observed = MobilecoreRuntime.state.value.serviceState
                val instance = TunnelBoxService.verifiedInstance()
                if (!current()) return@withContext
                if (instance > 0) MobilecoreRuntime.recordCoreIdentity(instance)
                val profileReady = checkOnly || (instance > 0 && !restart) || ConfigRepository.readGeneratedContent(context) != null || runCatching {
                    Mobilecore.newRecoverySession(context.filesDir.absolutePath).retainedConfig()
                }.isSuccess
                val decision = Mobilecore.repairRuntimeJSON(context.filesDir.absolutePath, observed, instance > 0, checkOnly, restart, profileReady)
                publish(decision)
                val action = JSONObject(decision).getString("action")
                if (action != "start" && action != "restart") return@withContext
                if (VpnService.prepare(context) != null) {
                    publish(result("failed", "none", false, "Allow VPN access with the Tunnel switch, then try repair again.")); return@withContext
                }
                if (action == "restart") {
                    TunnelBoxService.stopForRepair()
                    val deadline = System.nanoTime() + 10_000_000_000L
                    while (TunnelBoxService.hasOwner()) {
                        if (!current()) return@withContext
                        if (System.nanoTime() >= deadline) error("VPN teardown is unconfirmed. Disconnect in Android VPN settings before trying again.")
                        delay(100)
                    }
                }
                if (!current()) return@withContext
                check(!TunnelBoxService.hasOwner()) { "The previous core is still stopping. Wait before starting repair." }
                if (!perform(request) { TunnelBoxService.start(context, repair = true) }) return@withContext
                publish(result("starting", action, false, "Starting the saved VPN configuration…"))
                val deadline = System.nanoTime() + 20_000_000_000L
                while (System.nanoTime() < deadline) {
                    if (!current()) return@withContext
                    val confirmed = TunnelBoxService.verifiedInstance()
                    if (confirmed > 0 && MobilecoreRuntime.state.value.serviceState == "Running") {
                        MobilecoreRuntime.recordCoreIdentity(confirmed)
                        publish(result("repaired", if (action == "restart") "restarted" else "started", true, "Core verified. Use Check connection to test DNS and HTTPS."))
                        return@withContext
                    }
                    delay(200)
                }
                publish(result("degraded", action, false, "Core startup is unconfirmed. Disconnect to cancel, or check Android VPN settings."))
            }
        } catch (cancelled: kotlinx.coroutines.CancellationException) { throw cancelled }
        catch (error: Exception) {
            publish(result("failed", "none", false, "Repair could not confirm the core. Disconnect in Android VPN settings, then review the saved configuration."))
        } finally {
            synchronized(lock) { if (current()) MobilecoreRuntime.recordRepair(MobilecoreRuntime.state.value.repairResult, false) }
        }
    }
    private fun result(state: String, action: String, verified: Boolean, detail: String) = JSONObject()
        .put("state", state).put("action", action).put("verified", verified).put("detail", detail).toString()
}
