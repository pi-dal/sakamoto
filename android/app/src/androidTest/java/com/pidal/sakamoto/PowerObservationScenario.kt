package com.pidal.sakamoto

import android.app.Instrumentation
import com.pidal.sakamoto.command.CommandClientRuntime
import io.nekohasekai.libbox.*
import java.lang.reflect.Proxy

/** Real JNI server/client lifecycle with a synthetic core and no TUN/owner data. */
object PowerObservationScenario {
    fun run(test: Instrumentation) {
        fun <T> stub(type: Class<T>): T = type.cast(Proxy.newProxyInstance(type.classLoader, arrayOf(type)) { _, method, args ->
            if (method.name == "startDefaultInterfaceMonitor") {
                (args?.firstOrNull() as? InterfaceUpdateListener)?.updateDefaultInterface("", -1, false, false)
            }
            when {
                method.returnType == java.lang.Boolean.TYPE -> false
                method.returnType == java.lang.Integer.TYPE -> -1
                method.returnType == java.lang.Long.TYPE -> -1L
                method.returnType == String::class.java -> ""
                method.returnType.isInterface -> stub(method.returnType)
                else -> null
            }
        })
        check(!CommandClientRuntime.channelConnected.value) { "Power lane requires an idle isolated emulator" }
        val server = Libbox.newCommandServer(stub(CommandServerHandler::class.java), stub(PlatformInterface::class.java))
        var stage = "initial channel"
        fun eventually(check: () -> Boolean) {
            val end = System.nanoTime() + 5_000_000_000L
            while (!check() && System.nanoTime() < end) Thread.sleep(25)
            kotlin.check(check()) { "$stage: transition did not complete; notice=${com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.notice}" }
        }
        val samples = org.json.JSONArray()
        val previousConfig = com.pidal.sakamoto.runtime.ConfigRepository.load(test.targetContext)
        val experimentPrefs = test.targetContext.getSharedPreferences("experiments", 0)
        val previousSettings = experimentPrefs.getString("settings", null)
        check(experimentPrefs.edit().remove("settings").commit())
        var activity: MainActivity? = null
        try {
            server.start()
            val config = """{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"rules":[{"clash_mode":"global","action":"route","outbound":"MainProxy"},{"clash_mode":"direct","action":"route","outbound":"direct"}],"final":"MainProxy"},"experimental":{"clash_api":{"default_mode":"rule"}}}"""
            server.startOrReloadService(config, OverrideOptions())
            val instance = server.runningInstanceID()
            check(runCatching { server.probeAutomaticOrigin("invalid/host", 80, "direct", instance, 1000) }.isFailure) { "JNI origin probe accepted an invalid hostname" }
            CommandClientRuntime.setLearningEnabled(false)
            CommandClientRuntime.setDataVisible(false)
            CommandClientRuntime.start()
            eventually { CommandClientRuntime.channelConnected.value }
            stage = "initial Rule mode"
            CommandClientRuntime.setClashMode("rule")
            eventually { com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.routingMode.lowercase() == "rule" }
            com.pidal.sakamoto.runtime.MobilecoreRuntime.completeConfigApply()
            val field = CommandClientRuntime.javaClass.getDeclaredField("client").apply { isAccessible = true }
            val actionClient = field.get(CommandClientRuntime)
            Thread.sleep(1200)
            check(CommandClientRuntime.lastStatus == null)
            samples.put(org.json.JSONObject().put("stage", "hidden").put("status", false))
            activity = test.startActivitySync(android.content.Intent(test.targetContext, MainActivity::class.java).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            test.waitForIdleSync()
            repeat(3) {
                stage = "Data visible"
                test.runOnMainSync { activity!!.findViewById<android.view.View>(R.id.nav_data).performClick() }
                test.waitForIdleSync()
                eventually { CommandClientRuntime.lastStatus != null }
                check(field.get(CommandClientRuntime) === actionClient)
                check(server.runningInstanceID() == instance && server.isServiceRunning())
                samples.put(org.json.JSONObject().put("stage", "data").put("status", true))
                test.runOnMainSync { activity!!.findViewById<android.view.View>(R.id.nav_home).performClick() }
                test.waitForIdleSync()
                Thread.sleep(1200)
                check(CommandClientRuntime.lastStatus == null && CommandClientRuntime.connections.isEmpty())
                check(field.get(CommandClientRuntime) === actionClient)
            }
            val runtime = com.pidal.sakamoto.runtime.ExperimentRuntime
            fun settings(metadata: String) {
                com.pidal.sakamoto.runtime.ConfigRepository.save(test.targetContext, previousConfig.copy(generatedContent = config, hostMetadata = metadata))
            }
            val worker = runtime.javaClass.getDeclaredField("job").apply { isAccessible = true }
            val observer = CommandClientRuntime.javaClass.getDeclaredField("observer").apply { isAccessible = true }
            settings("""{"experiment":{"mode":"off"}}""")
            runtime.start(test.targetContext) { error("Disabled learning must not update routes") }
            check(worker.get(runtime) == null)
            samples.put(org.json.JSONObject().put("stage", "monitoring_disabled").put("worker", false))
            runtime.quiesce()
            settings("""{"fallback_enabled":true,"fallbacks":{"MainProxy":["direct"]},"experiment":{"mode":"off"}}""")
            stage = "fallback-only worker"
            runtime.start(test.targetContext) { error("Fallback-only policy must not learn routes") }
            eventually { runtime.status.value.message == "Recovery tests running" }
            check(observer.get(CommandClientRuntime) == null && CommandClientRuntime.lastStatus == null)
            val recovery = runtime.javaClass.getDeclaredField("recoveryJob").apply { isAccessible = true }
            val beforeNetwork = recovery.get(runtime)
            runtime.networkChanged()
            stage = "network-change recovery restart"
            eventually { recovery.get(runtime) !== beforeNetwork }
            check(server.runningInstanceID() == instance)
            samples.put(org.json.JSONObject().put("stage", "fallback_only_network_reset").put("observer", false).put("same_core", true))
            runtime.quiesce()
            settings("""{"experiment":{"mode":"auto"}}""")
            runtime.start(test.targetContext) { error("No flow evidence exists in this fixture") }
            stage = "learning observer"
            eventually { observer.get(CommandClientRuntime) != null }
            Thread.sleep(1200)
            check(CommandClientRuntime.lastStatus == null)
            check(field.get(CommandClientRuntime) === actionClient)
            samples.put(org.json.JSONObject().put("stage", "learning_only").put("status", false))
            runtime.quiesce()
            com.pidal.sakamoto.runtime.ConfigRepository.save(test.targetContext, previousConfig)
            // An in-flight attachment must not survive a rapid hide/stop.
            repeat(10) { CommandClientRuntime.setDataVisible(true); CommandClientRuntime.setDataVisible(false) }
            CommandClientRuntime.setLearningEnabled(false)
            CommandClientRuntime.stop()
            Thread.sleep(1200)
            check(!CommandClientRuntime.channelConnected.value && CommandClientRuntime.lastStatus == null)
            stage = "restarted channel"
            CommandClientRuntime.start()
            eventually { CommandClientRuntime.channelConnected.value }
            check(server.runningInstanceID() == instance)
            repeat(3) {
                CommandClientRuntime.stop(); CommandClientRuntime.start()
                eventually { CommandClientRuntime.channelConnected.value }
                Thread.sleep(200)
                check(CommandClientRuntime.channelConnected.value)
            }
            check(server.runningInstanceID() == instance)
            samples.put(org.json.JSONObject().put("stage", "rapid_stop_start").put("same_core", true))
            stage = "mode action"
            CommandClientRuntime.setClashMode("direct")
            eventually { com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.routingMode.lowercase() == "direct" }
            samples.put(org.json.JSONObject().put("stage", "restarted").put("action", "direct").put("same_core", true))
            java.io.File(test.targetContext.cacheDir, "power-observation.json").writeText(samples.toString(2))
            LearningObservationScenario.run(test, server, checkNotNull(activity))
        } finally {
            com.pidal.sakamoto.runtime.ExperimentRuntime.quiesce()
            com.pidal.sakamoto.runtime.ConfigRepository.save(test.targetContext, previousConfig)
            check(experimentPrefs.edit().apply { if (previousSettings == null) remove("settings") else putString("settings", previousSettings) }.commit())
            test.runOnMainSync { activity?.finish() }
            CommandClientRuntime.setDataVisible(false)
            CommandClientRuntime.setLearningEnabled(false)
            CommandClientRuntime.stop()
            runCatching { server.closeService() }
            server.close()
        }
    }
}
