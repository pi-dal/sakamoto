package com.pidal.sakamoto

import android.app.Instrumentation
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ExperimentRuntime
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import com.pidal.sakamoto.runtime.TunnelPackage
import io.nekohasekai.libbox.OverrideOptions
import io.nekohasekai.mobileexperiment.LearningProber
import io.nekohasekai.mobileexperiment.Mobileexperiment
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket

/** Run only on a disposable emulator with A.zip and B.zip in the test app cache. */
object ProfileIsolationScenario {
    fun run(test: Instrumentation, phase: String) {
        val ctx = test.targetContext
        val runtime = ExperimentRuntime
        val server = PowerObservationScenario.fixtureServer()
        val receipt = JSONObject()
        val baseline = phase == "baseline"
        fun install(name: String) {
            File(ctx.cacheDir, name).inputStream().use { TunnelPackage.import(ctx, it) }
        }
        fun sample(label: String): Int {
            val profile = ConfigRepository.load(ctx)
            // Exercise the command transport without creating a device VPN or sending health checks to an external server.
            val simple = """{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"final":"direct","rules":[{"clash_mode":"Global","action":"route","outbound":"MainProxy"},{"clash_mode":"Direct","action":"route","outbound":"direct"},{"rule_set":["rs-proxy"],"action":"route","outbound":"MainProxy"}],"rule_set":[{"type":"inline","tag":"rs-proxy","rules":[{"domain":["explicit.example"]}]}]},"experimental":{"clash_api":{"default_mode":"rule"}}}"""
            server.startOrReloadService(runtime.prepareContent(ctx, simple), OverrideOptions())
            MobilecoreRuntime.setServiceState("Running")
            MobilecoreRuntime.completeConfigApply()
            CommandClientRuntime.start()
            val deadline = System.nanoTime() + 6_000_000_000L
            while (!CommandClientRuntime.channelConnected.value && System.nanoTime() < deadline) Thread.sleep(25)
            CommandClientRuntime.setClashMode("rule")
            while (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule" && System.nanoTime() < deadline) Thread.sleep(25)
            check(CommandClientRuntime.channelConnected.value && MobilecoreRuntime.state.value.routingMode.equals("rule", true))
            val tracker = Mobileexperiment.newExperimentTracker(1)
            tracker.observeConnectionJSON("fixture", "origin.example", "192.0.2.2:80", "tcp", "direct", "", false, 0, 1000000)
            val candidate = tracker.observeFailureJSON("connection: open connection to origin.example:80 using outbound/direct[direct]: dial tcp 192.0.2.2:80: i/o timeout", 1000000, true, true, false)
            check(candidate.isNotEmpty())
            val direct = ServerSocket(0, 4, InetAddress.getLoopbackAddress()).use { listener ->
                listener.soTimeout = 1500
                val probe = object : LearningProber {
                    override fun probeOrigin(domain: String, port: Int, outbound: String, timeoutMillis: Int): Int {
                        if (outbound == "direct") Socket(listener.inetAddress, listener.localPort).close()
                        return 200
                    }
                }
                runtime.start(ctx, probe) { error("A reachable direct origin must not be promoted") }
                @Suppress("UNCHECKED_CAST")
                val attempts = runtime.javaClass.getDeclaredField("attempts").apply { isAccessible = true }.get(runtime) as Channel<String>
                runBlocking { withTimeoutOrNull(1000) { runCatching { attempts.send(candidate) } } }
                try { listener.accept().use { }; 1 } catch (_: java.net.SocketTimeoutException) { 0 }
            }
            runtime.quiesce()
            CommandClientRuntime.stop()
            server.closeService()
            MobilecoreRuntime.setServiceState("Stopped")
            receipt.put(label, JSONObject().put("direct_connections", direct).put("mode", runtime.settings(ctx).mode).put("cache_id", profile.modeCacheID))
            return direct
        }
        try {
            server.start()
            if (phase != "restart") {
                install("A.zip")
                ConfigRepository.load(ctx).let { profile -> runtime.saveSettings(ctx, runtime.settings(ctx, profile).copy(threshold = 1), profile) }
                check(sample("A") == 1)
                val a = ConfigRepository.load(ctx)
                if (!baseline) {
                    val preferences = ExperimentRuntime.preferences(ctx)
                    check(preferences.edit().putString("learned", "[\"old-device.fixture.example\"]")
                        .putString("pendingBefore", "old transaction").putString("pendingCandidate", a.generatedContent).commit())
                }
                check(runCatching { TunnelPackage.import(ctx, byteArrayOf(1,2,3).inputStream()) }.isFailure)
                check(ConfigRepository.load(ctx) == a && runtime.settings(ctx).mode == "auto")
                install("B.zip")
            }
            if (!baseline) check(!ExperimentRuntime.preferences(ctx).contains("pendingCandidate"))
            runtime.restorePending(ctx)
            val count = sample("B")
            File(ctx.cacheDir, "isolation-$phase.json").writeText(receipt.toString(2))
            check(count == if (baseline) 1 else 0) { receipt.toString() }
            if (!baseline) check(runtime.settings(ctx).mode == "off" && runtime.history(ctx).isEmpty() && runtime.learned(ctx).isEmpty())
        } finally {
            runtime.quiesce()
            CommandClientRuntime.stop()
            server.close()
        }
    }
}
