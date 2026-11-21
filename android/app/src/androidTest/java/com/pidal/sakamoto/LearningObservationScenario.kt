package com.pidal.sakamoto

import android.app.Instrumentation
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.ConfigRepository
import com.pidal.sakamoto.runtime.ExperimentRuntime
import com.pidal.sakamoto.runtime.MobilecoreRuntime
import io.nekohasekai.libbox.CommandServer
import io.nekohasekai.libbox.OverrideOptions
import io.nekohasekai.mobileexperiment.LearningProber
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.runBlocking
import org.json.JSONArray
import org.json.JSONObject

/** Real JNI live updates plus controlled origins through the production transaction. */
object LearningObservationScenario {
    fun run(test: Instrumentation, server: CommandServer) = runBlocking {
        val context = test.targetContext
        val before = ConfigRepository.load(context)
        val fixture = before.copy(scopedExperiment = true, modeCacheID = java.util.UUID.randomUUID().toString(), hostMetadata = "{}", routeSourceVersion = 1, proxyChain = null, regionalDNS = emptyMap())
        val prefs = ExperimentRuntime.preferences(context, fixture)
        val report = JSONArray()
        try {
            for (outcome in listOf("verified", "direct_recovered", "not_improved", "rolled_back", "network_changed", "cancelled")) {
                ExperimentRuntime.quiesce()
                prefs.edit().clear().putString("settings", """{"mode":"auto","threshold":3}""").commit()
                val content = """{"log":{"level":"error"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"final":"direct","rules":[{"rule_set":["rs-proxy"],"action":"route","outbound":"MainProxy"}],"rule_set":[{"type":"inline","tag":"rs-proxy","rules":[{"domain":["explicit.example"]}]}]},"experimental":{"clash_api":{"default_mode":"rule"}}}"""
                ConfigRepository.save(context, fixture.copy(generatedContent = content, sourceNeedsGenerate = false))
                server.startOrReloadService(ExperimentRuntime.prepareContent(context, content), OverrideOptions())
                val id = server.runningInstanceID()
                MobilecoreRuntime.setServiceState("Running")
                MobilecoreRuntime.completeConfigApply()
                CommandClientRuntime.setClashMode("rule")
                val end = System.nanoTime() + 5_000_000_000L
                while ((!CommandClientRuntime.channelConnected.value || MobilecoreRuntime.state.value.routingMode.lowercase() != "rule") && System.nanoTime() < end) Thread.sleep(25)
                check(CommandClientRuntime.channelConnected.value && MobilecoreRuntime.state.value.routingMode.lowercase() == "rule") { "Fixture Rule mode did not settle" }
                val prober = object : LearningProber {
                    override fun probeOrigin(domain: String, port: Int, outbound: String, timeoutMillis: Int): Int {
                        check(domain == "origin.example" && port == 80 && timeoutMillis in 1..8000)
                        if (outcome == "network_changed") ExperimentRuntime.networkChanged()
                        return when (outbound) {
                            "direct" -> if (outcome == "direct_recovered") 200 else 502
                            "" -> if (outcome == "rolled_back") 502 else 200
                            else -> if (outcome == "not_improved") 403 else 200
                        }
                    }
                }
                var appliedDomains: List<String>? = null
                ExperimentRuntime.start(context, prober) { domains ->
                    server.updateAutomaticDomains(domains, id)
                    val applied = JSONArray(domains)
                    appliedDomains = List(applied.length()) { applied.getString(it) }
                }
                val channel = ExperimentRuntime.javaClass.getDeclaredField("attempts").apply { isAccessible = true }.get(ExperimentRuntime) as Channel<String>
                channel.send("""{"domain":"origin.example","port":"80","failures":1,"required":3,"reason":"observed unmatched direct TCP dial failure"}""")
                if (outcome == "cancelled") ExperimentRuntime.quiesce()
                val deadline = System.nanoTime() + 5_000_000_000L
                while (ExperimentRuntime.history(context).isEmpty() && System.nanoTime() < deadline) Thread.sleep(25)
                val last = JSONObject(checkNotNull(ExperimentRuntime.history(context).lastOrNull()) {
                    "Missing $outcome decision: config=${MobilecoreRuntime.state.value.configState}, mode=${MobilecoreRuntime.state.value.routingMode}, channel=${CommandClientRuntime.channelConnected.value}, monitor=${ExperimentRuntime.status.value.message}"
                })
                java.io.File(context.cacheDir, "learning-case.json").writeText(JSONObject().put("case", outcome).put("result", last).put("configState", MobilecoreRuntime.state.value.configState).put("mode", MobilecoreRuntime.state.value.routingMode).put("connected", CommandClientRuntime.channelConnected.value).toString(2))
                check(last.getString("state") == if (outcome in listOf("network_changed", "cancelled")) "skipped" else outcome) { "$outcome: $last" }
                check(ExperimentRuntime.learned(context) == if (outcome == "verified") listOf("origin.example") else emptyList<String>())
                when (outcome) {
                    "verified" -> check(appliedDomains == listOf("origin.example"))
                    "rolled_back" -> check(appliedDomains == emptyList<String>())
                    else -> check(appliedDomains == null)
                }
                check(server.runningInstanceID() == id)
                report.put(last.put("same_core", true))
            }
            java.io.File(context.cacheDir, "mobile-learning-transactions.json").writeText(report.toString(2))
        } finally {
            ExperimentRuntime.quiesce()
            ConfigRepository.save(context, before)
            check(prefs.edit().clear().commit())
        }
    }
}
