package com.pidal.sakamoto

import android.app.Instrumentation
import io.nekohasekai.libbox.*
import io.nekohasekai.mobilecore.Mobilecore
import java.io.File
import java.lang.reflect.Proxy
import org.json.JSONObject

/** Real JNI core + shared admission/repair contract, isolated from the VPN. */
object RecoveryObservationScenario {
    fun run(test: Instrumentation) {
        check(Libbox.version() == "1.14.2-sakamoto")
        check(Mobilecore.recoveryProtocol() == 1)
        fun <T> stub(type: Class<T>): T = type.cast(Proxy.newProxyInstance(type.classLoader, arrayOf(type)) { _, method, args ->
            if (method.name == "startDefaultInterfaceMonitor") (args?.firstOrNull() as? InterfaceUpdateListener)?.updateDefaultInterface("", -1, false, false)
            when {
                method.returnType == java.lang.Boolean.TYPE -> false
                method.returnType == java.lang.Integer.TYPE -> -1
                method.returnType == java.lang.Long.TYPE -> -1L
                method.returnType == String::class.java -> ""
                method.returnType.isInterface -> stub(method.returnType)
                else -> null
            }
        })
        val root = File(test.targetContext.cacheDir, "recovery-${java.util.UUID.randomUUID()}").apply { mkdirs() }
        val session = Mobilecore.newRecoverySession(root.absolutePath)
        val gate = Mobilecore.newRuntimeGate()
        val ticket = session.begin(true)
        val config = """{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}"""
        session.prepare(ticket, config, "{}", root.absolutePath)
        val server = Libbox.newCommandServer(stub(CommandServerHandler::class.java), stub(PlatformInterface::class.java))
        try {
            server.start(); server.startOrReloadService(config, OverrideOptions())
            val instance = server.runningInstanceID()
            check(server.isServiceRunning() && instance > 0)
            session.ready(ticket, instance.toString(), Libbox.version())
            val decision = JSONObject(Mobilecore.repairDecisionJSON("Running", server.isServiceRunning(), false, false, true))
            check(decision.getString("state") == "healthy" && decision.getString("action") == "none" && decision.getBoolean("verified"))
            check(server.runningInstanceID() == instance)
            check(runCatching { session.stable(ticket, instance.toString()) }.isFailure)
            val late = gate.begin(); gate.stop()
            check(!gate.ready(late) && !gate.current(late))
            session.stop()
            check(runCatching { session.ready(ticket, "late", Libbox.version()) }.isFailure)
            check(runCatching { session.begin(false) }.isFailure)
            check(runCatching { session.begin(true) }.isFailure)
            server.closeService()
            val replacement = Mobilecore.newRecoverySession(root.absolutePath)
            val successor = replacement.begin(true)
            check(successor != ticket)
            replacement.failed(successor)
            val fallback = replacement.beginFallback(); replacement.failed(fallback)
            check(runCatching { replacement.begin(false) }.isFailure)
            val evidence = JSONObject().put("healthy_retained_instance", instance).put("native_version", Libbox.version())
                .put("recovery_protocol", Mobilecore.recoveryProtocol()).put("late_start_rejected", true)
                .put("manual_stop_hold", true).put("failed_fallback_blocks", true)
            File(test.targetContext.cacheDir, "mobile-core-recovery.json").writeText(evidence.toString(2))
        } finally { runCatching { server.closeService() }; server.close(); root.deleteRecursively() }
    }

    /** Dedicated consented emulator only: real foreground VPN and TUN lifecycle. */
    fun runVpn(test: Instrumentation) {
        val context = test.targetContext
        check(android.os.Build.FINGERPRINT.contains("generic") || android.os.Build.MODEL.contains("sdk")) { "VPN recovery drill requires an isolated emulator" }
        check(android.net.VpnService.prepare(context) == null) { "Grant ACTIVATE_VPN on the isolated emulator first" }
        check(!com.pidal.sakamoto.bg.TunnelBoxService.hasOwner()) { "An existing VPN must not be interrupted" }
        val previous = com.pidal.sakamoto.runtime.ConfigRepository.load(context)
        val recovery = File(context.filesDir, "recovery")
        val savedRecovery = File(context.filesDir, "recovery-test-backup-${java.util.UUID.randomUUID()}")
        if (recovery.exists()) check(recovery.renameTo(savedRecovery))
        val samples = org.json.JSONArray()
        var activity: MainActivity? = null
        fun eventually(label: String, condition: () -> Boolean) {
            val end = System.nanoTime() + 15_000_000_000L
            while (!condition() && System.nanoTime() < end) Thread.sleep(25)
            check(condition()) { "$label: ${com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value}" }
        }
        try {
            val ruleSource = File(context.cacheDir, "mobile-recovery-rules.json")
            val ruleBytes = """{"version":3,"rules":[{"domain_suffix":["fixture.invalid"]}]}"""
            ruleSource.writeText(ruleBytes)
            val config = """{"log":{"level":"warn"},"dns":{"servers":[{"type":"local","tag":"local"}]},"inbounds":[{"type":"tun","tag":"tun","address":["172.31.20.1/30"],"auto_route":true}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","auto_detect_interface":true,"rule_set":[{"type":"local","tag":"fixture","format":"source","path":"${ruleSource.absolutePath}"}],"rules":[{"rule_set":"fixture","action":"route","outbound":"direct"}]}}"""
            Libbox.checkConfig(config)
            com.pidal.sakamoto.runtime.ConfigRepository.save(context, previous.copy(generatedContent = config, sourceNeedsGenerate = false, hostMetadata = "{}"))
            activity = test.startActivitySync(android.content.Intent(context, MainActivity::class.java).addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
            test.waitForIdleSync()
            for (wait in listOf(0L, 5L, 20L, 50L, 150L)) {
                com.pidal.sakamoto.runtime.MobilecoreRuntime.setServiceState("Starting")
                com.pidal.sakamoto.bg.TunnelBoxService.start(context)
                if (wait > 0) Thread.sleep(wait)
                com.pidal.sakamoto.bg.TunnelBoxService.stop(context)
                eventually("stop during start") { !com.pidal.sakamoto.bg.TunnelBoxService.hasOwner() && com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.serviceState == "Stopped" }
                Thread.sleep(250)
                check(!com.pidal.sakamoto.bg.TunnelBoxService.hasOwner() && com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.serviceState == "Stopped")
                samples.put(JSONObject().put("stage", "stop_during_start").put("delay_ms", wait).put("late_running", false))
            }
            com.pidal.sakamoto.bg.TunnelBoxService.start(context)
            eventually("native VPN startup") { com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.serviceState == "Running" }
            val instance = com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance()
            check(instance > 0 && com.pidal.sakamoto.runtime.VpnDiagnostics.snapshot(context).vpn)
            test.runOnMainSync { activity!!.findViewById<android.view.View>(R.id.repair_vpn).performClick() }
            eventually("healthy repair UI") {
                val state = com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value
                !state.repairing && state.repairResult?.let { JSONObject(it).optString("state") == "healthy" } == true
            }
            check(com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance() == instance)
            samples.put(JSONObject().put("stage", "healthy_repair_ui").put("same_instance", true).put("real_tun", true))
            eventually("command channel") { com.pidal.sakamoto.command.CommandClientRuntime.channelConnected.value }
            ruleSource.delete()
            check(!com.pidal.sakamoto.command.CommandClientRuntime.reloadService())
            check(com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance() == instance)
            samples.put(JSONObject().put("stage", "missing_staged_asset").put("same_instance", true))
            ruleSource.writeText(ruleBytes)
            java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { occupied ->
                val bad = JSONObject(config)
                bad.getJSONArray("inbounds").put(JSONObject().put("type", "mixed").put("tag", "conflict").put("listen", "127.0.0.1").put("listen_port", occupied.localPort))
                com.pidal.sakamoto.runtime.ConfigRepository.save(context, previous.copy(generatedContent = bad.toString(), sourceNeedsGenerate = false, hostMetadata = "{}"))
                check(!com.pidal.sakamoto.command.CommandClientRuntime.reloadService())
                check(com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.serviceState == "Running")
                val rolledBack = com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance()
                check(rolledBack > 0 && rolledBack != instance)
                samples.put(JSONObject().put("stage", "failed_native_update").put("previous_config_restored", true).put("new_instance", true))
            }
            com.pidal.sakamoto.runtime.ConfigRepository.save(context, previous.copy(generatedContent = config, sourceNeedsGenerate = false, hostMetadata = "{}"))
            val beforeReconnect = com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance()
            kotlinx.coroutines.runBlocking { com.pidal.sakamoto.runtime.TunnelRepair.run(context, restart = true) }
            val result = JSONObject(checkNotNull(com.pidal.sakamoto.runtime.MobilecoreRuntime.state.value.repairResult))
            check(result.getBoolean("verified") && result.getString("state") == "repaired")
            val replaced = com.pidal.sakamoto.bg.TunnelBoxService.verifiedInstance()
            check(replaced > 0 && replaced != beforeReconnect)
            samples.put(JSONObject().put("stage", "explicit_reconnect").put("verified", true).put("new_instance", true))
            test.waitForIdleSync()
            test.uiAutomation.takeScreenshot()?.let { bitmap ->
                File(context.cacheDir, "mobile-recovery-home.png").outputStream().use { bitmap.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }; bitmap.recycle()
            }
            File(context.cacheDir, "mobile-vpn-recovery.json").writeText(samples.toString(2))
        } finally {
            com.pidal.sakamoto.bg.TunnelBoxService.stop(context)
            eventually("fixture teardown") { !com.pidal.sakamoto.bg.TunnelBoxService.hasOwner() }
            com.pidal.sakamoto.runtime.ConfigRepository.save(context, previous)
            recovery.deleteRecursively()
            if (savedRecovery.exists()) check(savedRecovery.renameTo(recovery))
            test.runOnMainSync { activity?.finish() }
        }
    }
}
