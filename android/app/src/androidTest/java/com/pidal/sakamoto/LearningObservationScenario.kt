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
    fun run(test: Instrumentation, server: CommandServer, activity: MainActivity) = runBlocking {
        val context = test.targetContext
        val before = ConfigRepository.load(context)
        val prefs = context.getSharedPreferences("experiments", 0)
        val saved = prefs.all.toMap()
        val report = JSONArray()
        try {
            for (outcome in listOf("verified", "direct_recovered", "not_improved", "rolled_back", "network_changed", "cancelled")) {
                ExperimentRuntime.quiesce()
                prefs.edit().clear().putString("settings", """{"mode":"auto","threshold":3}""").commit()
                val content = """{"log":{"level":"error"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"final":"direct","rules":[{"rule_set":["rs-proxy"],"action":"route","outbound":"MainProxy"}],"rule_set":[{"type":"inline","tag":"rs-proxy","rules":[{"domain":["explicit.example"]}]}]},"experimental":{"clash_api":{"default_mode":"rule"}}}"""
                ConfigRepository.save(context, before.copy(generatedContent = content, sourceNeedsGenerate = false))
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
                var updates = 0
                ExperimentRuntime.start(context, prober) { domains ->
                    server.updateAutomaticDomains(domains, id); updates++
                }
                val channel = ExperimentRuntime.javaClass.getDeclaredField("attempts").apply { isAccessible = true }.get(ExperimentRuntime) as Channel<String>
                channel.send("""{"domain":"origin.example","port":"80","failures":1,"required":3,"reason":"observed unmatched direct TCP dial failure"}""")
                if (outcome == "cancelled") ExperimentRuntime.quiesce()
                val deadline = System.nanoTime() + 5_000_000_000L
                while (ExperimentRuntime.history(context).isEmpty() && System.nanoTime() < deadline) Thread.sleep(25)
                val last = JSONObject(ExperimentRuntime.history(context).last())
                java.io.File(context.cacheDir, "learning-case.json").writeText(JSONObject().put("case", outcome).put("result", last).put("configState", MobilecoreRuntime.state.value.configState).put("mode", MobilecoreRuntime.state.value.routingMode).put("connected", CommandClientRuntime.channelConnected.value).toString(2))
                check(last.getString("state") == if (outcome in listOf("network_changed", "cancelled")) "skipped" else outcome) { "$outcome: $last" }
                check(ExperimentRuntime.learned(context) == if (outcome == "verified") listOf("origin.example") else emptyList<String>())
                check(updates == if (outcome == "verified") 1 else if (outcome == "rolled_back") 2 else 0)
                check(server.runningInstanceID() == id)
                check(!prefs.contains("pendingBefore") && !prefs.contains("pendingCandidate"))
                report.put(last.put("same_core", true).put("updates", updates))
            }
            test.runOnMainSync { activity.openChild(com.pidal.sakamoto.ui.ExperimentsFragment(), "Experiment") }
            test.waitForIdleSync()
            test.runOnMainSync {
                fun descendants(view: android.view.View): List<android.view.View> = listOf(view) + if (view is android.view.ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
                val views = descendants(activity.window.decorView)
                check(views.filterIsInstance<android.widget.TextView>().any { it.text.toString() == "Recent checks" })
                views.filterIsInstance<androidx.core.widget.NestedScrollView>().forEach { it.fullScroll(android.view.View.FOCUS_DOWN) }
            }
            test.waitForIdleSync()
            test.runOnMainSync {
                val root = activity.window.decorView
                val bitmap = android.graphics.Bitmap.createBitmap(root.width, root.height, android.graphics.Bitmap.Config.ARGB_8888)
                root.draw(android.graphics.Canvas(bitmap))
                java.io.File(context.cacheDir, "experiment-recent-checks.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
                bitmap.recycle()
            }
            java.io.File(context.cacheDir, "mobile-learning-transactions.json").writeText(report.toString(2))
        } finally {
            ExperimentRuntime.quiesce()
            ConfigRepository.save(context, before)
            val editor = prefs.edit().clear()
            saved.forEach { (key, value) -> if (value is String) editor.putString(key, value) }
            editor.commit()
        }
    }
}
