package com.pidal.sakamoto

import android.app.Instrumentation
import android.content.Intent
import android.net.VpnService
import com.pidal.sakamoto.bg.TunnelBoxService
import com.pidal.sakamoto.command.CommandClientRuntime
import com.pidal.sakamoto.runtime.*
import io.nekohasekai.libbox.CommandServer
import kotlinx.coroutines.ensureActive
import org.json.JSONObject
import java.io.File

/** Host runs the loopback VMess, DNS and packet-count fixtures. No synthetic learning events. */
object VpnProfileScenario {
    fun run(test: Instrumentation, phase: String) {
        val ctx = test.targetContext
        check(android.os.Build.MODEL.contains("sdk") && VpnService.prepare(ctx) == null)
        check(!TunnelBoxService.hasOwner())
        fun install(name: String) = File(ctx.cacheDir, name).inputStream().use { TunnelPackage.import(ctx, it) }
        fun waitFor(label: String, condition: () -> Boolean) {
            val until = System.nanoTime() + 20_000_000_000L
            while (!condition() && System.nanoTime() < until) Thread.sleep(50)
            check(condition()) { "$label: ${MobilecoreRuntime.state.value}, ${ExperimentRuntime.status.value}, ${CommandClientRuntime.lastGroupsSummary}" }
        }
        fun installTailnetA() {
            install("A.zip")
            val profile = ConfigRepository.load(ctx)
            val config = JSONObject(profile.generatedContent)
            val endpoint = JSONObject().put("type", "tailscale").put("tag", "fixture-tailnet")
                .put("control_url", "http://127.0.0.1:1")
            config.put("endpoints", org.json.JSONArray().put(endpoint))
            ConfigRepository.saveGeneratedEdit(ctx, config.toString(), expected = profile)
        }
        val keyFixture = "local-only-unused-fixture-key"
        if (phase == "vpn-tskey-apply") install("B.zip")
        if (phase == "vpn-tskey-save") {
            installTailnetA()
            val vault = com.pidal.sakamoto.security.TailscaleAuthKeyStore(ctx)
            check(!vault.hasAuthKey()) { "Run on a fresh fixture installation" }
            vault.storeAuthKey(keyFixture)
        }
        if (phase == "vpn-tskey-return") installTailnetA()
        if (phase.startsWith("vpn-import-")) {
            install("A.zip")
            DeferredImportScenario.run(test, phase.removePrefix("vpn-import-"))
        }
        val writeFault = phase in setOf("vpn-snapshot-create", "vpn-snapshot-partial", "vpn-snapshot-source")
        if (phase == "vpn-policy-manual-edit") {
            val prior = ConfigRepository.load(ctx)
            if (prior.regionalDNSOrigin == "device" && prior.regionalDNS["domains"] == "regional.fixture.example") {
                ConfigRepository.saveRegionalDNS(ctx, prior, mapOf("domains" to ""))
            }
            install("manual-old.zip")
        }
        if (phase in setOf("vpn-screen-A", "vpn-repair-A", "vpn-forget-edit", "vpn-forget-denied", "vpn-history-denied", "vpn-learn-kill", "vpn-learn-rollback")) {
            install("A.zip")
            val profile = ConfigRepository.load(ctx)
            ExperimentRuntime.saveSettings(ctx, ExperimentRuntime.settings(ctx, profile).copy(threshold = 1), profile)
        }
        if (phase == "vpn-final-edit") install("B.zip")
        if (phase in setOf("vpn-priority-edit", "vpn-reorder-edit", "vpn-promote-edit", "vpn-policy-edit")) install("A.zip")
        val pendingEdit = phase == "vpn-pending-edit"
        val pendingReconnect = phase == "vpn-pending-reconnect"
        val reconnectFault = phase == "vpn-save-fault-reconnect"
        val stagedDisconnect = phase in setOf("vpn-save-disconnect", "vpn-save-fault-edit")
        if (phase == "vpn-reload-expiry" || writeFault || stagedDisconnect || pendingEdit) install("B.zip")
        if (phase.startsWith("vpn-stale-") && !phase.endsWith("restart")) {
            install("A.zip")
            StaleThresholdScenario.run(test, phase.removePrefix("vpn-stale-"))
        }
        if (phase in setOf("vpn-delete-race", "vpn-undo-race")) {
            install("A.zip")
            SourceSaveRace.run(test, phase == "vpn-undo-race")
        }
        val chainPhase = phase.startsWith("vpn-chain-")
        if (phase == "vpn-chain-A") {
            install("regional-B.zip")
            val staged = ConfigRepository.load(ctx)
            ConfigRepository.saveProxyChain(ctx, ProxyChainSettings(true, "MainProxy", "Excluded"), staged)
            check(ConfigRepository.load(ctx).generatedContent == staged.generatedContent)
        }
        val regionalPhase = phase.startsWith("vpn-regional-")
        if (phase == "vpn-regional-local") {
            // A prior run of this fixture may have persisted the device's approval.
            val prior = ConfigRepository.load(ctx)
            if (prior.regionalDNSOrigin == "device" && prior.regionalDNS["domains"] == "regional.fixture.example") {
                ConfigRepository.saveRegionalDNS(ctx, prior, mapOf("domains" to ""))
            }
        }
        if (regionalPhase && !phase.endsWith("restart")) install(phase.removePrefix("vpn-") + ".zip")
        if (phase == "vpn-A") {
            install("A.zip")
            SourceDownloadScenario.run(test)
            install("A.zip")
            SourceRevisionScenario.run(test)
            val group = ProxyGroupEditing.groups(ConfigRepository.load(ctx).generatedContent).first { it.tag == "OthersAuto" }
            ConfigRepository.saveProxyGroup(ctx, group.copy(members = listOf("Allowed", "Excluded")))
            val before = ConfigRepository.load(ctx)
            val manifest = File(ctx.filesDir, "sakamoto-config.json")
            val originalBytes = manifest.readBytes()
            val originalSources = ConfigRepository.sourceFiles(ctx).associateWith { it.readBytes() }
            val revisionDir = File(ctx.filesDir, "package-revisions")
            val revisionsBefore = revisionDir.list()!!.toSet()
            val permissions = android.system.Os.stat(ctx.filesDir.path).st_mode and 511
            android.system.Os.chmod(ctx.filesDir.path, 365) // 0555: resources can stage, manifest cannot publish
            try {
                for (source in originalSources.keys.filter { it.extension == "conf" || it.name == "nodes.txt" }) {
                    check(runCatching { ConfigRepository.editSource(ctx, before, source, source.readText() + "\n# fixture edit\n") }.isFailure)
                }
                check(runCatching { ConfigRepository.saveProxyGroup(ctx, group) }.isFailure)
                check(runCatching { install("B.zip") }.isFailure)
            } finally { android.system.Os.chmod(ctx.filesDir.path, permissions) }
            val importJob = kotlinx.coroutines.Job()
            val cancelled = runCatching {
                File(ctx.cacheDir, "B.zip").inputStream().use { stream ->
                    TunnelPackage.import(ctx, stream) {
                        if (ctx.filesDir.list().orEmpty().any { it.startsWith("profile-") && it.endsWith(".tmp") }) importJob.cancel()
                        importJob.ensureActive()
                    }
                }
            }.exceptionOrNull()
            check(cancelled is kotlinx.coroutines.CancellationException)
            check(manifest.readBytes().contentEquals(originalBytes) && ConfigRepository.load(ctx) == before)
            check(originalSources.isNotEmpty() && originalSources.all { (file, bytes) -> file.readBytes().contentEquals(bytes) })
            check(revisionDir.list()!!.toSet() == revisionsBefore)
            io.nekohasekai.libbox.Libbox.checkConfig(before.runtimeContent)
            val failure = runCatching { install("legacy-B.zip") }.exceptionOrNull()
            check(failure?.message?.contains("Re-export") == true && ConfigRepository.load(ctx) == before)
            install("A.zip")
            ConfigRepository.load(ctx).let { profile -> ExperimentRuntime.saveSettings(ctx, ExperimentRuntime.settings(ctx, profile).copy(threshold = 1), profile) }
        } else if (phase == "vpn-B" || phase == "vpn-repair-B") install("B.zip")
        val activity = test.startActivitySync(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        var faultDirectory: File? = null
        var originalMode = 0
        val savedReceipt = ConfigRepository.manifestReceipt(ctx)
        try {
            fun toggleHome(connected: Boolean) {
                test.waitForIdleSync()
                test.runOnMainSync {
                    val toggle = activity.findViewById<android.widget.CompoundButton>(R.id.connection_switch)
                    check(toggle.isShown && toggle.isEnabled && toggle.isChecked == connected)
                    toggle.performClick()
                }
            }
            if (reconnectFault) {
                val staged = ConfigRepository.load(ctx)
                val dir = ConfigRepository.packageDirectory(ctx, staged.confirmedRevision)
                val selection = JSONObject(File(dir, ".confirmed-selection").readText())
                check(selection.getBoolean("connectStaged"))
                val inactive = File(dir, ".confirmed-" + (1 - selection.getInt("slot")))
                check(inactive.mkdir())
                originalMode = android.system.Os.stat(inactive.path).st_mode and 511
                faultDirectory = inactive
                android.system.Os.chmod(inactive.path, 365)
                check(!File(ctx.filesDir, "recovery/mobile-profiles/last-good.json").exists())
            }
            if (phase == "vpn-repair-B") {
                val retained = io.nekohasekai.mobilecore.Mobilecore.newRecoverySession(ctx.filesDir.absolutePath).retainedConfig()
                check(retained.contains("health-a.fixture.example"))
                test.runOnMainSync {
                    val repair = activity.findViewById<android.view.View>(R.id.repair_vpn)
                    check(repair.isShown && repair.isEnabled && repair.performClick())
                }
            } else if (pendingEdit || pendingReconnect || reconnectFault) toggleHome(false) else TunnelBoxService.start(ctx)
            waitFor("VPN apply") { MobilecoreRuntime.state.value.serviceState == "Running" && CommandClientRuntime.channelConnected.value }
            waitFor("initial core mode") { MobilecoreRuntime.state.value.routingMode.isNotBlank() }
            CommandClientRuntime.setClashMode("rule")
            val coldRollback = phase == "vpn-reload-restart" || (phase.startsWith("vpn-snapshot-") && phase.endsWith("-restart"))
            waitFor("Rule mode") { MobilecoreRuntime.state.value.routingMode.equals("rule", true) && (reconnectFault || pendingReconnect || coldRollback || phase == "vpn-learn-restart" || MobilecoreRuntime.state.value.configState == "Clean") }
            var instance = TunnelBoxService.verifiedInstance()
            check(instance > 0 && VpnDiagnostics.snapshot(ctx).vpn)
            val fallbackUpgrade = phase.startsWith("vpn-fallback-")
            if (!regionalPhase && !chainPhase && !fallbackUpgrade) CommandClientRuntime.urlTest("OthersAuto")
            if (!regionalPhase && !chainPhase && !fallbackUpgrade) waitFor("local proxy health") { FallbackDecision.selectedObservation(CommandClientRuntime.groups.value)?.delay?.let { it > 0 } == true }
            // Submit requests to the live core's ordinary router by hostname. The JNI transport
            // emits real NEW, dial-failure and CLOSED events; it does not enqueue learning work.
            val owner = TunnelBoxService::class.java.getDeclaredField("active").apply { isAccessible = true }.get(null)
            val server = TunnelBoxService::class.java.getDeclaredField("commandServer").apply { isAccessible = true }.get(owner) as CommandServer
            fun request(host: String) = server.probeAutomaticOrigin(host, 80, "", instance, 4000)
            if (phase.startsWith("vpn-tskey-")) {
                if (phase == "vpn-tskey-apply") {
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    val replacement = TunnelBoxService.verifiedInstance()
                    check(replacement > 0 && replacement != instance)
                    instance = replacement
                }
                if (phase == "vpn-tskey-save") {
                    TailscaleKeySaveFailure.run(test, activity)
                    val file = ctx.getFileStreamPath("tailscale-authkey.bin")
                    val permissions = android.system.Os.stat(file.path).st_mode and 511
                    android.system.Os.chmod(file.path, 0)
                    try {
                        check(!CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                        check(TunnelBoxService.verifiedInstance() == instance)
                        check(request("explicit-direct.fixture.example") == 200 && request("explicit-proxy.fixture.example") == 204)
                    } finally { android.system.Os.chmod(file.path, permissions) }
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                val deleted = phase in setOf("vpn-tskey-delete", "vpn-tskey-deleted-restart")
                if (phase == "vpn-tskey-delete") TailscaleKeySaveFailure.deleteAndRetry(test, activity)
                val key = com.pidal.sakamoto.security.TailscaleAuthKeyStore(ctx).readAuthKey()
                check(key == if (deleted) null else keyFixture)
                val saved = ConfigRepository.load(ctx).generatedContent
                val returned = deleted || phase in setOf("vpn-tskey-save", "vpn-tskey-return")
                var reads = 0
                val prepared = com.pidal.sakamoto.security.TailscaleConfigInjection.forTunnel(saved) { reads++; key }
                if (deleted) {
                    check(reads == 1 && !JSONObject(prepared).getJSONArray("endpoints").getJSONObject(0).has("auth_key"))
                } else if (returned) {
                    check(reads == 1 && JSONObject(prepared).getJSONArray("endpoints").getJSONObject(0).getString("auth_key") == key)
                } else check(reads == 0 && prepared == saved)
                val direct = request("explicit-direct.fixture.example")
                val proxy = request("explicit-proxy.fixture.example")
                check(direct == 200 && proxy == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("key_retained", key != null).put("deletion_retried", phase == "vpn-tskey-delete")
                    .put("replacement_failure_exercised", phase == "vpn-tskey-save")
                    .put("tailscale_endpoint", returned).put("explicit_direct_status", direct).put("explicit_proxy_status", proxy).toString())
                return
            }
            if (phase.startsWith("vpn-oldfinal-")) {
                val mode = if (phase.endsWith("enable") || phase.endsWith("restart")) "on" else "off"
                if (phase.endsWith("enable") || phase.endsWith("restore")) {
                    UpgradeConsentScenario.chooseMode(test, activity, mode)
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                val saved = ConfigRepository.load(ctx)
                val final = JSONObject(saved.generatedContent).getJSONObject("route").getString("final")
                val status = request("unmatched.fixture.example")
                val shownMode = ExperimentRuntime.settings(ctx, saved).mode
                check(shownMode == mode && status == if (mode == "on") 204 else 200) { "$phase mode=$shownMode final=$final HTTP=$status" }
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("mode", shownMode).put("final", final)
                    .put("unmatched_http_status", status).put("domain", "unmatched.fixture.example").toString())
                return
            }
            if (fallbackUpgrade) {
                val confirmed = phase in setOf("vpn-fallback-enable", "vpn-fallback-enabled-restart")
                if (phase == "vpn-fallback-enable") {
                    UpgradeConsentScenario.enableFallback(test, activity)
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                val settings = ExperimentRuntime.settings(ctx)
                check(settings.fallback == confirmed && !settings.learningEnabled)
                if (confirmed) {
                    val deadline = System.nanoTime() + 65_000_000_000L
                    while (CommandClientRuntime.groups.value.none { it.tag == "MainProxy" && it.selected == "OthersAuto" } && System.nanoTime() < deadline) Thread.sleep(100)
                } else {
                    val screen = com.pidal.sakamoto.ui.ExperimentsFragment()
                    test.runOnMainSync {
                        activity.openChild(screen, ctx.getString(R.string.experiments_title))
                        activity.supportFragmentManager.executePendingTransactions()
                        val matches = arrayListOf<android.view.View>()
                        screen.requireView().findViewsWithText(matches, ctx.getString(R.string.fallback_upgrade_consent), android.view.View.FIND_VIEWS_WITH_TEXT)
                        check(matches.any { it.isShown })
                    }
                    Thread.sleep(23000) // Observe one complete recovery round.
                }
                val selected = CommandClientRuntime.groups.value.single { it.tag == "MainProxy" }.selected
                val observations = CommandClientRuntime.groups.value.joinToString { group ->
                    "${group.tag}=${group.selected} ${group.items.map { "${it.tag}/${it.delay}/${it.testedAt}" }}"
                }
                check(selected == if (confirmed) "OthersAuto" else "ManualPick") {
                    "Unexpected fallback selection: $selected, ${ExperimentRuntime.status.value.message}, ${MobilecoreRuntime.state.value.routingMode}, $observations"
                }
                val status = request("explicit-proxy.fixture.example")
                check(status == if (confirmed) 204 else 202)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("fallback_enabled", settings.fallback)
                    .put("upgrade_guidance", settings.needsFallbackConfirmation).put("selected", selected).put("proxy_status", status).toString())
                return
            }
            if (phase.startsWith("vpn-s3-")) {
                val profile = ConfigRepository.load(ctx)
                check(File(ctx.cacheDir, "s3-original").readText() == ConfigRepository.manifestReceipt(ctx))
                check(!profile.sourceNeedsGenerate && profile.policy.isEmpty())
                val direct = request("explicit-direct.fixture.example")
                val proxy = request("explicit-proxy.fixture.example")
                check(direct == 200 && proxy == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("same_manifest", true)
                    .put("pending", profile.sourceNeedsGenerate).put("direct_status", direct).put("proxy_status", proxy).toString())
                return
            }
            if (phase.startsWith("vpn-priority-")) {
                val host = "origin.fixture.example"
                if (phase.endsWith("edit")) {
                    check(runCatching { request(host) }.isFailure)
                    waitFor("verified learning") { ExperimentRuntime.learned(ctx) == listOf(host) && !ExperimentRuntime.status.value.pending }
                    check(request(host) == 204)
                    val form = RoutingLifecycleForm(test, activity)
                    form.rejectBeforeLearning(host)
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                    check(runCatching { request(host) }.isFailure)
                    form.setLearning(false)
                    check(!ExperimentRuntime.settings(ctx).learningEnabled && ExperimentRuntime.learned(ctx) == listOf(host))
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                    check(runCatching { request(host) }.isFailure && request("explicit-proxy.fixture.example") == 204)
                    form.setLearning(true)
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                check(ExperimentRuntime.settings(ctx).learningEnabled && ExperimentRuntime.learned(ctx) == listOf(host))
                check(runCatching { request(host) }.isFailure) { "Learned route bypassed saved rejection" }
                check(request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("mode_round_trip", "auto-off-auto").put("learned_count", 1).put("rejected", true).put("proxy_control", 204).toString())
                return
            }
            if (phase.startsWith("vpn-final-") || phase.startsWith("vpn-reorder-")) {
                val isFinal = phase.startsWith("vpn-final-")
                val host = "origin.fixture.example"
                if (phase.endsWith("edit")) {
                    val form = RoutingLifecycleForm(test, activity)
                    if (isFinal) form.finalRoundTrip() else {
                        check(runCatching { request(host) }.isFailure)
                        waitFor("verified learning") { ExperimentRuntime.learned(ctx) == listOf(host) && !ExperimentRuntime.status.value.pending }
                        form.moveAndForget(host)
                    }
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                check(ExperimentRuntime.settings(ctx).mode == "off" && ExperimentRuntime.learned(ctx).isEmpty())
                val outcome = runCatching { request(host) }.getOrNull()
                check(if (isFinal) outcome == 204 else outcome == null) { "Unexpected unmatched request response $outcome" }
                check(request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("mode", "off").put("learned_count", 0).put("unmatched", outcome?.toString() ?: "direct_refused").put("proxy_control", 204).toString())
                return
            }
            if (phase.startsWith("vpn-learn-")) {
                val host = "origin.fixture.example"
                if (!phase.endsWith("restart")) {
                    check(runCatching { request(host) }.isFailure)
                    waitFor("verified origin") { ExperimentRuntime.learned(ctx) == listOf(host) && !ExperimentRuntime.status.value.pending }
                    check(request(host) == 204)
                    if (phase.endsWith("kill")) {
                        File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("live_proxy", 204).toString())
                        android.os.Process.killProcess(android.os.Process.myPid())
                        error("Expected forced termination")
                    }
                    File(ctx.cacheDir, "learned-before-rollback").writeText("verified")
                    waitFor("host packet counter reset") { File(ctx.cacheDir, "measure-learning-rollback").exists() }
                    val clock = TunnelBoxService::class.java.getDeclaredField("elapsedMillis").apply { isAccessible = true }
                    val original = clock.get(owner)
                    val calls = java.util.concurrent.atomic.AtomicInteger()
                    val expired = { android.os.SystemClock.elapsedRealtime() + if (calls.incrementAndGet() >= 4) 31_000L else 0L }
                    try {
                        clock.set(owner, expired)
                        check(!CommandClientRuntime.reloadService(ConfigRepository.load(ctx)) && calls.get() == 4)
                    } finally { clock.set(owner, original) }
                    val restored = TunnelBoxService.verifiedInstance()
                    check(restored > 0 && restored != instance)
                    instance = restored
                }
                check(ExperimentRuntime.learned(ctx) == listOf(host))
                val response = request(host)
                check(response == 204 && request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("learned_count", 1).put("restored_proxy", response).toString())
                return
            }
            if (phase.startsWith("vpn-history-")) {
                val host = "origin.fixture.example"
                val report = JSONObject()
                if (phase == "vpn-history-denied") {
                    android.system.Os.chmod(File(ctx.applicationInfo.dataDir, "shared_prefs").path, 365)
                    check(runCatching { request(host) }.isFailure)
                    waitFor("history save completed") { ExperimentRuntime.learned(ctx) == listOf(host) && !ExperimentRuntime.status.value.pending }
                    check(ExperimentRuntime.status.value.message == "Could not save experiment history")
                    report.put("history_save_failed", true)
                } else {
                    check(ExperimentRuntime.history(ctx).isEmpty())
                    report.put("persisted_history", 0)
                }
                check(ExperimentRuntime.learned(ctx) == listOf(host))
                val origin = request(host)
                val control = request("explicit-proxy.fixture.example")
                check(origin == 204 && control == 204 && TunnelBoxService.verifiedInstance() == instance)
                File(ctx.cacheDir, "$phase.json").writeText(report.put("learned_count", 1).put("same_instance", true)
                    .put("learned_origin", origin).put("proxy_control", control).toString())
                return
            }
            if (phase.startsWith("vpn-forget-")) {
                val host = "origin.fixture.example"
                val denied = phase.startsWith("vpn-forget-denied")
                if (!phase.endsWith("-restart")) {
                    check(runCatching { request(host) }.isFailure)
                    waitFor("verified route") { ExperimentRuntime.learned(ctx) == listOf(host) && !ExperimentRuntime.status.value.pending }
                    check(request(host) == 204)
                    // The harness restores permissions after force-stop.
                    val target = if (denied) ctx.filesDir else File(ctx.applicationInfo.dataDir, "shared_prefs")
                    val permissions = android.system.Os.stat(target.path).st_mode and 511
                    val beforeDelete = ConfigRepository.manifestReceipt(ctx)
                    android.system.Os.chmod(target.path, 365)
                    try { RoutingLifecycleForm(test, activity).forget(host) }
                    finally { if (denied) android.system.Os.chmod(target.path, permissions) }
                    if (denied) check(ConfigRepository.manifestReceipt(ctx) == beforeDelete)
                    check(ExperimentRuntime.learned(ctx) == if (denied) listOf(host) else emptyList())
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                val learned = ExperimentRuntime.learned(ctx)
                check(learned == if (denied) listOf(host) else emptyList())
                val response = runCatching { request(host) }.getOrNull()
                check(if (denied) response == 204 else response == null)
                check(request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("learned_count", learned.size)
                    .put("origin_response", response?.toString() ?: "direct_refused").put("proxy_control", 204).toString())
                return
            }
            if (phase.startsWith("vpn-promote-")) {
                val host = "origin.fixture.example"
                val report = JSONObject()
                if (phase.endsWith("edit")) {
                    check(runCatching { request(host) }.isFailure)
                    waitFor("verified native learning") { ExperimentRuntime.history(ctx).any { JSONObject(it).optString("state") == "verified" } && !ExperimentRuntime.status.value.pending }
                    check(ExperimentRuntime.learned(ctx) == listOf(host))
                    PromoteFailureScenario.run(test, activity, host) {
                        check(ConfigRepository.load(ctx).policy.any { it.match == host && it.action == "proxy" }) { "Approved domain missing from explicit policy" }
                    }
                    check(ExperimentRuntime.learned(ctx).isEmpty())
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                    report.put("verified_before_promotion", true)
                }
                check(ConfigRepository.load(ctx).policy.any { it.match == host && it.action == "proxy" })
                check(ExperimentRuntime.learned(ctx).isEmpty())
                check(request(host) == 204)
                File(ctx.cacheDir, "$phase.json").writeText(report.put("explicit_proxy_saved", true).put("learned_count", 0).put("proxy_status", 204).toString())
                return
            }
            if (phase.startsWith("vpn-policy-manual-")) {
                val host = "manual.fixture.example"
                if (phase.endsWith("edit")) {
                    PolicyFormScenario.add(test, activity, "proxy", host)
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                }
                check(ConfigRepository.load(ctx).policy.any { it.match == host && it.action == "proxy" })
                val status = request(host)
                check(status == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("explicit_proxy_status", status).toString())
                return
            }
            if (phase.startsWith("vpn-policy-")) {
                val host = PolicyFormScenario.domain
                check(ExperimentRuntime.settings(ctx).mode == "auto" && ExperimentRuntime.learned(ctx).isEmpty())
                val report = JSONObject()
                fun applyAndProbe(expected: Int, label: String) {
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                    val actual = request(host)
                    check(actual == expected) { "$label got $actual" }
                    report.put(label, actual)
                }
                if (phase.endsWith("edit")) {
                    check(request(host) == 200)
                    PolicyFormScenario.add(test, activity, "proxy")
                    applyAndProbe(204, "explicit_proxy")
                    PolicyFormScenario.add(test, activity, "direct")
                    applyAndProbe(200, "higher_priority_direct")
                    val current = ConfigRepository.load(ctx)
                    ConfigRepository.savePolicy(ctx, current.policy.single { it.action == "direct" }, null, current)
                    check(ConfigRepository.load(ctx).policy.single().action == "proxy")
                    applyAndProbe(204, "after_removing_direct")
                    PolicyFormScenario.addFromRulesPage(test, activity, "origin.fixture.example")
                    check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                    instance = TunnelBoxService.verifiedInstance()
                    check(request("origin.fixture.example") == 204)
                    report.put("rules_page_proxy", 204)
                } else {
                    val actual = request(host)
                    check(actual == 204)
                    check(request("origin.fixture.example") == 204)
                    report.put("proxy_after_restart", actual).put("rules_page_proxy_after_restart", 204)
                }
                File(ctx.cacheDir, "$phase.json").writeText(report.toString())
                return
            }
            if (pendingEdit || pendingReconnect) {
                val saved = File(ctx.cacheDir, "pending-source-receipt")
                if (pendingEdit) {
                    SourceRevisionScenario.run(test, changeRoute = true, existing = activity)
                    test.runOnMainSync { check(activity.supportFragmentManager.popBackStackImmediate()) }
                    saved.writeText(ConfigRepository.manifestReceipt(ctx))
                }
                val config = checkNotNull(ConfigRepository.loadAt(ctx, saved.readText()))
                check(config.sourceNeedsGenerate && config.sourceConfContent.contains("explicit-direct.fixture.example,PROXY"))
                check(request("explicit-direct.fixture.example") == 200)
                check(request("explicit-proxy.fixture.example") == 204)
                toggleHome(true)
                waitFor("Home disconnect") { !TunnelBoxService.hasOwner() }
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("pending_source_preserved", true)
                    .put("direct_status", 200).put("proxy_status", 204).put("home_connect_clicks", 1).toString())
                return
            }
            if (reconnectFault) {
                val candidate = checkNotNull(ConfigRepository.loadAt(ctx, savedReceipt))
                check(request("explicit-direct.fixture.example") == 200)
                android.system.Os.chmod(checkNotNull(faultDirectory).path, originalMode)
                faultDirectory = null
                check(CommandClientRuntime.reloadService(candidate))
                instance = TunnelBoxService.verifiedInstance()
                check(runCatching { request("explicit-direct.fixture.example") }.isFailure)
                check(request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("home_connect_clicks", 1)
                    .put("global_stable_snapshot", false).put("confirmed_direct_status", 200)
                    .put("candidate_manifest_intact", true).put("explicit_retry_rejected_request", true).put("proxy_control_status", 204).toString())
                return
            }
            if (phase == "vpn-save-reconnect") {
                check(runCatching { request("explicit-direct.fixture.example") }.isFailure && request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("saved_rule_rejected_request", true).put("proxy_status", 204).toString())
                return
            }
            if (phase == "vpn-reload-expiry" || writeFault || stagedDisconnect) {
                val sourceRevision = phase == "vpn-snapshot-source"
                if (sourceRevision) SourceRevisionScenario.run(test, changeRoute = true, existing = activity)
                val original = ConfigRepository.load(ctx)
                val json = JSONObject(original.generatedContent)
                val route = json.getJSONObject("route")
                val rules = route.getJSONArray("rules")
                val replaced = org.json.JSONArray().put(JSONObject().put("domain", org.json.JSONArray().put("explicit-direct.fixture.example")).put("action", "reject"))
                for (i in 0 until rules.length()) replaced.put(rules.get(i))
                route.put("rules", replaced)
                val candidate = if (sourceRevision) {
                    check(original.sourceNeedsGenerate && original.confirmedRevision != original.packageRevision)
                    val ready = File(ctx.cacheDir, "regenerated.zip")
                    File(ctx.cacheDir, "source-ready").writeText("ready")
                    val end = System.nanoTime() + 45_000_000_000L
                    while (!ready.isFile && System.nanoTime() < end) Thread.sleep(50)
                    val inputs = File(ctx.filesDir, "test-generation-" + java.util.UUID.randomUUID()).also { check(it.mkdir()) }
                    ready.inputStream().use { TunnelPackage.extract(it, inputs) }
                    val prepared = TunnelPackage.prepare(inputs, inputs, original.localExclusions())
                    check(prepared.state.getString("sourceConfContent") == original.sourceConfContent)
                    original.copy(generatedContent = prepared.generated, sourceNeedsGenerate = false)
                } else original.copy(generatedContent = json.toString())
                fun stage(next: ConfigRepository.StagedConfig) = ConfigRepository.save(ctx, next, ConfigRepository.load(ctx))
                if (stagedDisconnect) {
                    stage(candidate)
                    check(request("explicit-direct.fixture.example") == 200)
                    File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("live_response_before_disconnect", 200).toString())
                    return
                }
                if (writeFault) {
                    stage(candidate)
                    val root = ConfigRepository.packageDirectory(ctx, original.confirmedRevision)
                    val selection = File(root, ".confirmed-selection")
                    val beforeBytes = selection.readBytes()
                    val inactive = File(root, ".confirmed-" + (1 - JSONObject(String(beforeBytes)).getInt("slot")))
                    check(!inactive.exists())
                    val partial = phase.endsWith("partial")
                    val parent = if (partial) File(inactive, "recovery/mobile-profiles").also { check(it.mkdirs()) } else root
                    val mode = android.system.Os.stat(parent.path).st_mode and 511
                    android.system.Os.chmod(parent.path, 365)
                    try {
                        check(!CommandClientRuntime.reloadService(candidate))
                        check(selection.readBytes().contentEquals(beforeBytes) && inactive.exists() == partial)
                        check(request("explicit-direct.fixture.example") == 200)
                    } finally { android.system.Os.chmod(parent.path, mode) }
                    File(ctx.cacheDir, "expiry-manifest").writeText(ConfigRepository.manifestReceipt(ctx))
                    File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("fault", phase.removePrefix("vpn-snapshot-"))
                        .put("selection_intact", true).put("inactive_directory_exists", partial).put("live_response", 200).toString())
                    return
                }
                stage(candidate)
                check(CommandClientRuntime.reloadService(candidate))
                instance = TunnelBoxService.verifiedInstance()
                check(runCatching { request("explicit-direct.fixture.example") }.isFailure)
                stage(original)
                check(CommandClientRuntime.reloadService(original))
                instance = TunnelBoxService.verifiedInstance()
                check(request("explicit-direct.fixture.example") == 200)
                stage(candidate)
                val field = TunnelBoxService::class.java.getDeclaredField("elapsedMillis").apply { isAccessible = true }
                val clock = field.get(owner)
                val calls = java.util.concurrent.atomic.AtomicInteger()
                val delayedClock = {
                    // Delay confirmation of a successful native reload past its real production deadline.
                    if (calls.incrementAndGet() == 4) Thread.sleep(31_000)
                    android.os.SystemClock.elapsedRealtime()
                }
                try {
                    field.set(owner, delayedClock)
                    check(!CommandClientRuntime.reloadService(candidate) && calls.get() == 4)
                    instance = TunnelBoxService.verifiedInstance()
                    repeat(3) { check(request("explicit-direct.fixture.example") == 200); Thread.sleep(100) }
                } finally { field.set(owner, clock) }
                check(ConfigRepository.load(ctx) == candidate)
                File(ctx.cacheDir, "expiry-manifest").writeText(ConfigRepository.manifestReceipt(ctx))
                File(ctx.cacheDir, "reload-expiry.json").writeText(JSONObject().put("native_confirmation_delay_ms", 31000).put("candidate_rejected_control_request", true).put("responses_after_failure", org.json.JSONArray(listOf(200, 200, 200))).toString())
            }
            if (coldRollback) {
                val staged = ConfigRepository.load(ctx)
                val receipt = File(ctx.cacheDir, "expiry-manifest").readText()
                check(ConfigRepository.loadAt(ctx, receipt) == staged)
                val regenerated = phase == "vpn-snapshot-source-restart"
                if (!regenerated) check(JSONObject(staged.generatedContent).getJSONObject("route").getJSONArray("rules").getJSONObject(0).getString("action") == "reject")
                check(request("explicit-direct.fixture.example") == 200 && MobilecoreRuntime.state.value.configState != "Clean")
                check(CommandClientRuntime.reloadService(staged))
                instance = TunnelBoxService.verifiedInstance()
                if (regenerated) check(request("explicit-direct.fixture.example") == 204)
                else check(runCatching { request("explicit-direct.fixture.example") }.isFailure && TunnelBoxService.verifiedInstance() == instance)
                check(request("explicit-proxy.fixture.example") == 204)
                File(ctx.cacheDir, "$phase.json").writeText(JSONObject().put("candidate_manifest_intact", true).put("ordinary_reconnect_status", 200)
                    .put("explicit_retry_rejected_request", !regenerated).put("regenerated_proxy_response", if (regenerated) 204 else null).put("proxy_status_after_retry", 204).toString())
                return
            }
            if (regionalPhase || chainPhase) {
                if (phase == "vpn-regional-local") {
                    val old = ConfigRepository.load(ctx)
                    check(old.routeSourceVersion == 1 && old.regionalDNS.isEmpty())
                    ConfigRepository.saveRegionalDNS(ctx, old, mapOf("domains" to "regional.fixture.example", "resolver_ip" to "192.0.2.53", "tls_name" to "allowed.fixture.example", "relay" to "Excluded"))
                    TunnelBoxService.applySaved(ConfigRepository.load(ctx))
                }
                val staged = ConfigRepository.load(ctx)
                val report = JSONObject()
                if (chainPhase) {
                    val chained = phase == "vpn-chain-A"
                    check((staged.proxyChain?.enabled == true) == chained)
                    val status = request("explicit-proxy.fixture.example")
                    check(status == if (chained) 202 else 204) { "Unexpected proxy status $status" }
                    report.put("chain_enabled", chained).put("proxy_status", status)
                } else if (phase.startsWith("vpn-regional-local")) {
                    check(staged.regionalDNSOrigin == "device" && staged.regionalDNS.getValue("domains") == "regional.fixture.example" && staged.routeSourceVersion == 1)
                } else check(staged.regionalDNSOrigin == "package" && staged.regionalDNS.isNotEmpty() == phase.endsWith("-A"))
                CommandClientRuntime.setClashMode("direct")
                waitFor("Direct mode") { MobilecoreRuntime.state.value.routingMode.equals("direct", true) }
                val query = byteArrayOf(0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0) +
                    "\u0008regional\u0007fixture\u0007example\u0000".toByteArray() + byteArrayOf(0, 1, 0, 1)
                java.net.DatagramSocket().use { socket ->
                    socket.soTimeout = 12000
                    socket.connect(java.net.InetAddress.getByName("192.0.2.99"), 53)
                    socket.send(java.net.DatagramPacket(query, query.size))
                    val answer = java.net.DatagramPacket(ByteArray(512), 512)
                    socket.receive(answer)
                    check(answer.length >= 16 && answer.data[0] == query[0] && answer.data[1] == query[1] &&
                        answer.data[7] == 1.toByte() && answer.data.sliceArray(answer.length - 4 until answer.length).contentEquals(byteArrayOf(203.toByte(), 0, 113, 34))) { "Unexpected VPN DNS answer (rcode=${answer.data[3].toInt() and 15}, answers=${answer.data[7]}, bytes=${answer.length})" }
                }
                File(ctx.cacheDir, "$phase.json").writeText(report.put("regional_enabled", staged.regionalDNS.isNotEmpty()).put("dns_answer", "203.0.113.34").toString())
                return
            }
            val reenable = phase == "vpn-upgrade-confirm"
            if (reenable) {
                check(ExperimentRuntime.settings(ctx).needsLearningConfirmation)
                UpgradeConsentScenario.confirmAuto(test, activity)
                check(CommandClientRuntime.reloadService(ConfigRepository.load(ctx)))
                instance = TunnelBoxService.verifiedInstance()
                CommandClientRuntime.urlTest("OthersAuto")
                waitFor("proxy health after Apply") { FallbackDecision.selectedObservation(CommandClientRuntime.groups.value)?.delay?.let { it > 0 } == true }
            }
            check(request("explicit-direct.fixture.example") == 200)
            check(request("explicit-proxy.fixture.example") == 204)
            val profile = if (phase.endsWith("-A")) "A" else "B"
            val group = ProxyGroupEditing.groups(ConfigRepository.load(ctx).runtimeContent).single { it.tag == "OthersAuto" }
            check(group.url == "http://health-${profile.lowercase()}.fixture.example/health/$profile" && group.interval == if (profile == "A") "4m" else "7m") { "Unexpected health target: $group" }
            val previous = ExperimentRuntime.history(ctx).size
            check(runCatching { request("origin.fixture.example") }.isFailure)
            val auto = profile == "A" || reenable
            if (auto) waitFor("native learning comparison") { ExperimentRuntime.history(ctx).size > previous }
            else Thread.sleep(6000)
            val settings = ExperimentRuntime.settings(ctx)
            val history = ExperimentRuntime.history(ctx)
            check(settings.mode == if (auto) "auto" else "off")
            if (!auto) check(history.isEmpty() && ExperimentRuntime.learned(ctx).isEmpty())
            val result = JSONObject().put("mode", settings.mode).put("running_tun", true).put("explicit_direct_status", 200)
                .put("health_url", group.url).put("health_interval", group.interval)
                .put("explicit_proxy_status", 204).put("failed_requests", 1).put("learning_records", history.size)
            if (phase == "vpn-repair-A") {
                val session = io.nekohasekai.mobilecore.Mobilecore.newRecoverySession(ctx.filesDir.absolutePath)
                val id = ConfigRepository.load(ctx).modeCacheID
                val deadline = System.nanoTime() + 330_000_000_000L
                while (runCatching { session.retainedConfig().contains(id) }.getOrDefault(false).not()) {
                    check(System.nanoTime() < deadline) { "A did not publish its stable snapshot" }
                    Thread.sleep(500)
                }
                result.put("stable_snapshot_verified", true)
            }
            if (phase == "vpn-repair-B") {
                waitFor("Home Repair confirmed") { !MobilecoreRuntime.state.value.repairing }
                check(JSONObject(MobilecoreRuntime.state.value.repairResult!!).getBoolean("verified"))
                result.put("repair_verified", true)
            }
            if (phase == "vpn-screen-A") {
                val domain = "origin.fixture.example"
                waitFor("A learned the fixture origin") { ExperimentRuntime.learned(ctx) == listOf(domain) && !ExperimentRuntime.status.value.pending }
                TunnelBoxService.stop(ctx)
                waitFor("A disconnected") { !TunnelBoxService.hasOwner() }
                install("B.zip")
                check(!ExperimentRuntime.settings(ctx).learningEnabled && !TunnelBoxService.hasOwner())
                val screen = com.pidal.sakamoto.ui.ExperimentsFragment()
                test.runOnMainSync {
                    activity.openChild(screen, ctx.getString(R.string.experiments_title))
                    activity.supportFragmentManager.executePendingTransactions()
                }
                test.waitForIdleSync()
                test.runOnMainSync {
                    fun texts(query: String): List<String> {
                        val views = ArrayList<android.view.View>()
                        screen.requireView().findViewsWithText(views, query, android.view.View.FIND_VIEWS_WITH_TEXT)
                        return views.filterIsInstance<android.widget.TextView>().filter { it.isShown }.map { it.text.toString() }
                    }
                    val status = texts("Stopped").single()
                    val empty = ctx.getString(R.string.experiment_no_learned)
                    check(empty in texts(empty) && texts(domain).isEmpty())
                    check(status == "Stopped · 0 learned") { status }
                    result.put("before_connect_status", status).put("empty_list_shown", true).put("previous_domain_rows", 0)
                }
            }
            if (phase.startsWith("vpn-upgrade-")) {
                val legacy = ctx.getSharedPreferences("experiments", 0)
                val globalSettings = JSONObject(legacy.getString("settings", "{}")!!)
                check(globalSettings.getInt("threshold") == 1)
                val exportMode = JSONObject(ConfigRepository.load(ctx).hostMetadata).getJSONObject("experiment").getString("mode")
                check(settings.threshold == 1 && settings.learningEnabled == reenable)
                check(settings.needsLearningConfirmation == (exportMode == "auto" && !reenable))
                check(globalSettings.getString("mode") == if (exportMode == "auto") "off" else "auto")
                if (settings.needsLearningConfirmation) {
                    test.runOnMainSync {
                        val screen = com.pidal.sakamoto.ui.ExperimentsFragment()
                        activity.openChild(screen, ctx.getString(R.string.experiments_title))
                        activity.supportFragmentManager.executePendingTransactions()
                        val views = ArrayList<android.view.View>()
                        screen.requireView().findViewsWithText(views, ctx.getString(R.string.experiment_upgrade_consent), android.view.View.FIND_VIEWS_WITH_TEXT)
                        check(views.any { it.isShown })
                    }
                    result.put("upgrade_guidance_displayed", true)
                }
                result.put("export_mode", exportMode).put("legacy_device_mode", globalSettings.getString("mode"))
                    .put("requires_learning_confirmation", settings.needsLearningConfirmation)
                check(legacy.getString("learned", "") == "[\"old-device.fixture.example\"]")
                if (!reenable) check(legacy.getString("pendingCandidate", null) == ConfigRepository.load(ctx).generatedContent)
                else check(legacy.contains("pendingCandidate"))
                check(legacy.contains("pendingBefore"))
                result.put("global_pending_preserved", true).put("visible_learned_domains", ExperimentRuntime.learned(ctx).size)
            }
            File(ctx.cacheDir, "$phase.json").writeText(result.toString(2))
        } finally {
            faultDirectory?.let { android.system.Os.chmod(it.path, originalMode) }
            TunnelBoxService.stop(ctx)
            waitFor("VPN stopped") { !TunnelBoxService.hasOwner() }
            test.runOnMainSync { activity.finish() }
        }
    }
}
