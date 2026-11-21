package com.pidal.sakamoto.runtime

import android.content.Context
import com.pidal.sakamoto.command.CommandClientRuntime
import io.nekohasekai.mobileexperiment.Mobileexperiment
import io.nekohasekai.mobilecore.Mobilecore
import io.nekohasekai.libbox.ConnectionEvents
import io.nekohasekai.libbox.Libbox
import kotlinx.coroutines.*
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject

/** Phone-owned experimentation while the VPN service runs. Host Tracker owns evidence. */
object ExperimentRuntime {
    data class Settings(val mode: String = "off", val threshold: Int = 3, val cfRegion: Boolean = false, val fallback: Boolean = false, val recoverAfter: Int = 2, val needsLearningConfirmation: Boolean = false, val needsFallbackConfirmation: Boolean = false, val showLegacyFinalGuidance: Boolean = false) {
        val learningEnabled: Boolean get() = mode == "auto" || cfRegion
        val requiresMonitoring: Boolean get() = learningEnabled || fallback
    }
    data class Status(val message: String = "Stopped", val pending: Boolean = false)
    private val _status = MutableStateFlow(Status())
    val status: StateFlow<Status> = _status
    private var job: Job? = null
    private var scope: CoroutineScope? = null
    @Volatile private var tracker: io.nekohasekai.mobileexperiment.ExperimentTracker? = null
    @Volatile private var activeSettings = Settings()
    private var recoveryAt = 0L
    @Volatile private var settling = false
    private var attempts = Channel<String>(Channel.RENDEZVOUS)
    private val recoveries = mutableMapOf<String, Int>()
    @Volatile private var prober: io.nekohasekai.mobileexperiment.LearningProber? = null
    @Volatile private var activeCheck: io.nekohasekai.mobileexperiment.LearningCheck? = null
    private val learningCooldown = mutableMapOf<String, Long>()
    private var lastVerificationAt = 0L
    private var persistVerified: ((ConfigRepository.StagedConfig, ConfigRepository.StagedConfig) -> Unit)? = null
    private var updateDomains: ((String) -> Unit)? = null
    private var context: Context? = null
    private val schedule = ExperimentProbeSchedule()
    private var wakeups = Channel<Unit>(Channel.CONFLATED)
    private var recoveryJob: Job? = null
    @Volatile private var verificationChange = "Network or policy changed"
    private val networkGeneration = java.util.concurrent.atomic.AtomicLong()
    @Volatile private var healthSince = 0L
    private var observedGroups: List<ConfigRepository.Group> = emptyList()
    private var monitoredSelectors = setOf("MainProxy")
    private var observedMode = ""

    fun networkChanged() {
        if (!activeSettings.requiresMonitoring || scope == null) return
        verificationChange = "Network changed"
        networkGeneration.incrementAndGet()
        activeCheck?.cancel()
        tracker = if (activeSettings.learningEnabled) Mobileexperiment.newExperimentTracker(activeSettings.threshold) else null
        healthSince = System.currentTimeMillis() / 1000 + 1
        schedule.reset()
        scope?.launch {
            recoveryJob?.cancelAndJoin()
            wakeups.trySend(Unit)
        }
    }

    fun modeChanged() {
        val next = MobilecoreRuntime.state.value.routingMode.lowercase()
        if (next != observedMode) {
            observedMode = next; verificationChange = "Routing mode changed"; networkGeneration.incrementAndGet(); activeCheck?.cancel()
        }
        if (scope != null) wakeups.trySend(Unit)
    }

    @Synchronized fun groupsChanged(groups: List<ConfigRepository.Group>) {
        if (!activeSettings.requiresMonitoring || scope == null) return
        val previous = FallbackDecision.selectedObservation(observedGroups)
        val current = FallbackDecision.selectedObservation(groups)
        val selectionChanged = groups.any { group -> group.tag in monitoredSelectors && observedGroups.firstOrNull { it.tag == group.tag }?.let { it.selected != group.selected } == true } || (previous != null && current != null && previous.tag != current.tag)
        if (selectionChanged) { verificationChange = "Proxy selection changed"; networkGeneration.incrementAndGet(); activeCheck?.cancel() }
        observedGroups = groups
        if (!settling && (selectionChanged || ((previous?.delay ?: 0) > 0 && (current?.delay ?: 0) <= 0))) {
            schedule.reset(); wakeups.trySend(Unit)
        } else if (!settling && !activeSettings.fallback && current != null && current.testedAt > (previous?.testedAt ?: 0)) {
            schedule.record(current.delay > 0 && System.currentTimeMillis() / 1000 - current.testedAt in 0..120)
        }
    }

    internal fun preferences(context: Context, config: ConfigRepository.StagedConfig = ConfigRepository.load(context)): android.content.SharedPreferences {
        val id = config.modeCacheID
        val name = when {
            config.scopedExperiment && id.isNotEmpty() -> "experiments-" + java.util.UUID.fromString(id)
            config.legacyExperimentID.isNotEmpty() -> "experiments-upgraded-" + java.util.UUID.fromString(config.legacyExperimentID)
            else -> "experiments-empty" // No installed profile. Startup requires a complete manifest.
        }
        return context.getSharedPreferences(name, Context.MODE_PRIVATE)
    }

    fun settings(context: Context, config: ConfigRepository.StagedConfig = ConfigRepository.load(context)): Settings {
        val prefs = preferences(context, config)
        val saved = prefs.getString("settings", null)
        val metadata = runCatching { JSONObject(config.hostMetadata) }.getOrDefault(JSONObject())
        val local = metadata.optJSONObject("deviceExperiment") ?: saved?.let { JSONObject(it) }
        val value = local ?: metadata.optJSONObject("experiment") ?: JSONObject()
        val mode = value.optString("mode", "off").takeIf { it in setOf("off", "on", "auto") } ?: "off"
        val cfRegion = value.optBoolean("cf_region_block")
        // A legacy export describes desktop intent. The phone may have revoked learning
        // in its old shared preferences, which cannot establish profile-specific consent.
        val unconfirmedLegacy = config.legacyExperimentID.isNotEmpty() && local == null
        val needsConfirmation = unconfirmedLegacy && (mode == "auto" || cfRegion)
        val fallback = local?.optBoolean("fallback") ?: metadata.optBoolean("fallback_enabled")
        val effectiveMode = when {
            needsConfirmation && mode == "auto" -> "off"
            mode == "on" -> {
                val proxy = runCatching { Mobileexperiment.proxyOutbound(config.generatedContent) }.getOrNull()
                val final = JSONObject(config.generatedContent).getJSONObject("route").optString("final")
                if (final == proxy) "on" else "off"
            }
            else -> mode
        }
        return Settings(effectiveMode, value.optInt("threshold", 3).coerceIn(1, 20),
            cfRegion && !needsConfirmation, fallback && !unconfirmedLegacy,
            value.optInt("recover_after", metadata.optInt("recover_after", 2)).coerceIn(1, 20), needsConfirmation, unconfirmedLegacy && fallback,
            unconfirmedLegacy && metadata.optString("sourceFinal").isEmpty())
    }
    fun learned(context: Context, config: ConfigRepository.StagedConfig = ConfigRepository.load(context)): List<String> {
        config.learnedDomains?.let { return it }
        val raw = preferences(context, config).getString("learned", "[]")!!
        val array = JSONArray(raw)
        return (0 until array.length()).map { array.getString(it) }
    }
    fun history(context: Context, config: ConfigRepository.StagedConfig = ConfigRepository.load(context)): List<String> {
        val array = JSONArray(preferences(context, config).getString("history", "[]"))
        return (0 until array.length()).map { array.getJSONObject(it).toString() }
    }
    private fun record(context: Context, config: ConfigRepository.StagedConfig, check: io.nekohasekai.mobileexperiment.LearningCheck) {
        val message = try {
            val result = JSONObject(check.reportJSON())
            val items = history(context, config).takeLast(49).map { JSONObject(it) } + result
            val saved = preferences(context, config).edit().putString("history", JSONArray(items).toString()).commit()
            if (saved) result.getString("domain") + ": " + result.getString("detail") else "Could not save experiment history"
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            "Could not save experiment history"
        }
        if (ConfigRepository.load(context).modeCacheID != config.modeCacheID) return
        _status.value = _status.value.copy(message = if (scope == null) "Stopped" else message)
    }
    @Synchronized fun saveSettings(context: Context, next: Settings, current: ConfigRepository.StagedConfig) {
        require(next.mode in setOf("off", "on", "auto") && next.threshold in 1..20 && next.recoverAfter in 1..20)
        check(ConfigRepository.load(context) == current) { "Configuration changed. Reopen the experiment settings." }
        val root = JSONObject(current.generatedContent)
        detachDesktopLearning(root)
        val route = root.getJSONObject("route")
        val previous = settings(context, current)
        val metadata = JSONObject(current.hostMetadata)
        if (next.mode == "on") {
            val proxy = Mobileexperiment.proxyOutbound(current.generatedContent)
            if (previous.mode != "on") metadata.put("beforeOnFinal", route.getString("final"))
            route.put("final", proxy)
        } else if (previous.mode == "on" && next.mode != "on") {
            val final = metadata.optString("beforeOnFinal").ifEmpty { when (metadata.optString("sourceFinal")) {
                "DIRECT" -> "direct"
                "PROXY" -> Mobileexperiment.proxyOutbound(current.generatedContent)
                else -> error("Reimport the sources to restore their FINAL route.")
            } }
            require(final in ConfigEdits.outboundTags(current.generatedContent)) { "The saved FINAL outbound is missing. Reimport the sources to restore it." }
            metadata.remove("beforeOnFinal")
            route.put("final", final)
        }
        check(!_status.value.pending && !settling) { "Experiment is busy" }
        var adjusted = root.toString()
        if (previous.learningEnabled != next.learningEnabled) {
            val domains = learned(context, current)
            if (domains.isNotEmpty()) {
                val saved = JSONArray(domains).toString()
                adjusted = if (next.learningEnabled) Mobileexperiment.resumePausedRulesJSON(adjusted, saved)
                    else Mobileexperiment.prepareLiveRulesJSON(adjusted, saved, "[]")
            }
        }
        val value = JSONObject().put("mode", next.mode).put("threshold", next.threshold).put("cf_region_block", next.cfRegion).put("fallback", next.fallback).put("recover_after", next.recoverAfter)
        metadata.put("deviceExperiment", value)
        ConfigRepository.saveGeneratedEdit(context, adjusted, metadata.toString(), current)
        // Staged settings become runtime policy only after Apply/reconnect.
        _status.value = _status.value.copy(message = "Saved; apply configuration")
    }
    fun restorePending(context: Context) {
        val current = ConfigRepository.load(context)
        val prefs = preferences(context, current)
        val before = prefs.getString("pendingBefore", null) ?: return
        val candidate = prefs.getString("pendingCandidate", null) ?: return
        if (current.generatedContent == candidate) {
            Libbox.checkConfig(before)
            ConfigRepository.save(context, current.copy(generatedContent = before), current)
        }
        check(prefs.edit().remove("pendingBefore").remove("pendingCandidate").commit())
    }

    // Old desktop auto exports reference a host-owned source file and loopback probe listeners.
    // Only the device's own verified domains may populate its native learning slot.
    private fun detachDesktopLearning(root: JSONObject): Boolean {
        val route = root.optJSONObject("route") ?: return false
        val sets = route.optJSONArray("rule_set") ?: return false
        val tag = "sakamoto-auto-proxy"
        val index = (0 until sets.length()).firstOrNull { i ->
            val set = sets.optJSONObject(i)
            set?.optString("tag") == tag && set.optString("type") == "local" && set.optString("format") == "source" &&
                set.optString("path").substringAfterLast('/') == "auto-proxy-rules.json"
        } ?: return false
        sets.remove(index)
        val listeners = setOf("learning-direct", "learning-proxy", "learning-route")
        fun single(rule: JSONObject, key: String): String? = when (val match = rule.opt(key)) {
            is String -> match
            is JSONArray -> match.takeIf { it.length() == 1 }?.optString(0)
            else -> null
        }
        val rules = route.getJSONArray("rules")
        for (i in rules.length() - 1 downTo 0) {
            val rule = rules.getJSONObject(i)
            if (single(rule, "rule_set") == tag || single(rule, "inbound") in listeners) rules.remove(i)
        }
        val inbounds = root.optJSONArray("inbounds") ?: return true
        for (i in inbounds.length() - 1 downTo 0) {
            if (inbounds.getJSONObject(i).optString("tag") in listeners) inbounds.remove(i)
        }
        return true
    }

    fun prepareContent(context: Context, content: String): String {
        val root = JSONObject(content)
        val connected = Mobilecore.prepareTailscaleConnectivityJSON(if (detachDesktopLearning(root)) root.toString() else content)
        val settings = settings(context)
        if (settings.mode != "auto" && !settings.cfRegion) return connected
        val domains = JSONArray(learned(context)).toString()
        return Mobileexperiment.prepareLiveRulesJSON(connected, domains, domains)
    }

    @Synchronized fun start(context: Context, prober: io.nekohasekai.mobileexperiment.LearningProber? = null,
                            persist: (ConfigRepository.StagedConfig, ConfigRepository.StagedConfig) -> Unit = { before, next -> ConfigRepository.save(context, next, before) },
                            update: (String) -> Unit) {
        if (job != null) return
        persistVerified = persist
        this.prober = prober
        updateDomains = update
        this.context = context.applicationContext
        activeSettings = settings(context)
        monitoredSelectors = setOf("MainProxy") + runCatching {
            JSONObject(ConfigRepository.load(context).hostMetadata).optJSONObject("fallbacks")?.keys()?.asSequence()?.toSet().orEmpty()
        }.getOrDefault(emptySet())
        tracker = if (activeSettings.learningEnabled) Mobileexperiment.newExperimentTracker(activeSettings.threshold) else null
        schedule.reset(); observedGroups = CommandClientRuntime.groups.value
        observedMode = MobilecoreRuntime.state.value.routingMode.lowercase()
        healthSince = System.currentTimeMillis() / 1000
        CommandClientRuntime.setLearningEnabled(activeSettings.learningEnabled)
        if (!activeSettings.requiresMonitoring) {
            _status.value = Status("Monitoring disabled")
            return
        }
        val owner = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        scope = owner
        attempts = Channel(Channel.RENDEZVOUS)
        wakeups = Channel(Channel.CONFLATED)
        job = owner.launch {
            _status.value = Status("Monitoring while VPN runs")
            if (activeSettings.learningEnabled) launch { for (domain in attempts) promote(context, domain) }
            while (isActive) {
                var recoveryState = "unavailable"
                if (!settling && !_status.value.pending && CommandClientRuntime.channelConnected.value) {
                    recoveryState = if (activeSettings.fallback) recover(false) else "disabled"
                    if (recoveryState == "queued") recoveryJob?.join()
                    if (activeSettings.learningEnabled && recoveryState in setOf("disabled", "manual") && MobilecoreRuntime.state.value.configState == "Clean" && MobilecoreRuntime.state.value.routingMode.lowercase() == "rule") {
                        val wait = healthSince * 1000 + 50 - System.currentTimeMillis()
                        if (wait > 0) delay(wait)
                        CommandClientRuntime.groups.value.firstOrNull { it.tag == "MainProxy" }?.selected?.let { CommandClientRuntime.urlTest(it) }
                    }
                }
                val mode = MobilecoreRuntime.state.value.routingMode.lowercase()
                if (recoveryState == "mode" || (mode.isNotEmpty() && mode != "rule") || (!activeSettings.learningEnabled && recoveryState in setOf("disabled", "manual"))) wakeups.receive()
                else withTimeoutOrNull(schedule.delayMillis(activeSettings.learningEnabled)) { wakeups.receive() }
            }
        }
    }
    fun quiesce() {
        val running = scope?.coroutineContext?.get(Job)
        stop()
        runBlocking { running?.join() }
    }
    @Synchronized fun stop() {
        activeCheck?.cancel(); networkGeneration.incrementAndGet()
        scope?.cancel(); scope = null; job = null; recoveryJob = null
        wakeups.close(); schedule.reset(); observedGroups = emptyList()
        CommandClientRuntime.setLearningEnabled(false)
        learningCooldown.clear(); lastVerificationAt = 0; recoveryAt = 0
        attempts.close(); persistVerified = null; tracker = null; updateDomains = null; prober = null; context = null; settling = false; recoveries.clear()
        _status.value = _status.value.copy(message = "Stopped", pending = false)
    }
    fun connection(events: ConnectionEvents) {
        if (_status.value.pending || (activeSettings.mode != "auto" && !activeSettings.cfRegion)) return
        val iterator = events.iterator()
        val now = System.currentTimeMillis()
        while (iterator.hasNext()) {
            val event = iterator.next()
            if (event.type.toLong() !in setOf(Libbox.ConnectionEventNew, Libbox.ConnectionEventClosed)) continue
            val c = event.connection ?: continue
            val candidate = tracker?.observeConnectionJSON(event.id, c.domain, c.destination, c.network, c.outbound, c.rule, event.type.toLong() == Libbox.ConnectionEventClosed, c.downlinkTotal, now).orEmpty()
            if (activeSettings.mode == "auto") queueCandidate(candidate)
        }
    }
    fun log(message: String) {
        if (_status.value.pending || MobilecoreRuntime.state.value.configState != "Clean" || MobilecoreRuntime.state.value.routingMode.lowercase() != "rule" || (activeSettings.mode != "auto" && !activeSettings.cfRegion)) return
        val groups = CommandClientRuntime.groups.value
        val fresh = maxOf(healthSince, System.currentTimeMillis() / 1000 - 120)
        val observation = FallbackDecision.selectedObservation(groups)
        val healthy = observation != null && observation.delay > 0 && observation.testedAt >= fresh
        queueCandidate(tracker?.observeFailureJSON(message, System.currentTimeMillis(), healthy, activeSettings.mode == "auto", activeSettings.cfRegion).orEmpty())
    }
    @Synchronized private fun queueCandidate(candidate: String) {
        if (candidate.isEmpty() || _status.value.pending || settling || MobilecoreRuntime.state.value.configState != "Clean" || MobilecoreRuntime.state.value.routingMode.lowercase() != "rule") return
        val observation = FallbackDecision.selectedObservation(CommandClientRuntime.groups.value) ?: return
        val now = System.currentTimeMillis()
        if (observation.delay <= 0 || observation.testedAt < maxOf(healthSince, now / 1000 - 120)) return
        learningCooldown.entries.removeAll { it.value <= now }
        val domain = JSONObject(candidate).getString("domain")
        if (domain in learned(context ?: return) || domain in learningCooldown || learningCooldown.size >= 1024 || now - lastVerificationAt < 30_000) return
        if (attempts.trySend(candidate).isSuccess) {
            learningCooldown[domain] = now + 300_000; lastVerificationAt = now
        }
    }
    private suspend fun promote(context: Context, candidateJSON: String) {
        val check = runCatching { Mobileexperiment.newLearningCheck(candidateJSON) }.getOrNull() ?: return
        val domain = JSONObject(candidateJSON).getString("domain")
        val before = ConfigRepository.load(context)
        val known = learned(context, before)
        val update = updateDomains ?: return
        val persist = persistVerified ?: return
        val probe = prober ?: return
        if (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule") return
        if (domain in known || known.size >= 1000 || !CommandClientRuntime.channelConnected.value) return
        if (before.sourceNeedsGenerate || MobilecoreRuntime.state.value.configState != "Clean") return
        val generation = networkGeneration.get()
        fun unchanged() {
            check(generation == networkGeneration.get() && CommandClientRuntime.channelConnected.value && MobilecoreRuntime.state.value.routingMode.lowercase() == "rule") { "Network, selection or policy changed during verification" }
        }
        _status.value = Status("Checking direct and proxy origin", pending = true)
        activeCheck = check
        var liveAttempted = false
        var committed = false
        try {
            currentCoroutineContext().ensureActive()
            check.compare(probe, Mobileexperiment.proxyOutbound(before.runtimeContent))
            currentCoroutineContext().ensureActive(); unchanged()
            if (!check.ready()) return
            check(ConfigRepository.load(context) == before && MobilecoreRuntime.state.value.configState == "Clean") { "Configuration changed during verification" }
            val candidate = addOwnedRule(before.generatedContent, known + domain, known)
            Libbox.checkConfig(before.proxyChain?.applying(candidate) ?: candidate)
            // The trial belongs to the running instance. Persist only after it verifies.
            liveAttempted = true
            update(JSONArray(known + domain).toString())
            check(check.verify(probe)) { "Installed route failed target verification" }
            currentCoroutineContext().ensureActive(); unchanged()
            check(MobilecoreRuntime.state.value.serviceState == "Running")
            persist(before, before.copy(generatedContent = candidate, learnedDomains = known + domain))
            committed = true
            MobilecoreRuntime.completeConfigApply()
            _status.value = Status("Verified proxy route applied", pending = true)
        } catch (error: Exception) {
            if (liveAttempted && !committed) {
                try { update(JSONArray(known).toString()) }
                catch (_: Exception) {
                    check.finish("failed", "Learning rollback failed; reconnect to restore previous rules")
                    return // The previous rules remain on disk for reconnect.
                }
            }
            val interrupted = error is CancellationException
            val changed = generation != networkGeneration.get()
            check.finish(if (liveAttempted) "rolled_back" else "skipped", when {
                liveAttempted -> "Learning verification failed or was interrupted; previous rules restored"
                interrupted -> "Verification canceled; no route saved"
                changed -> "$verificationChange during verification; no route saved"
                ConfigRepository.load(context) != before -> "Configuration changed during verification; no route saved"
                else -> "Verification could not complete; no route saved"
            })
            if (error is CancellationException) throw error
        } finally {
            activeCheck = null; check.cancel()
            try { record(context, before, check) }
            finally { _status.value = _status.value.copy(pending = false) }
        }
    }
    private fun stripOwnedRule(content: String, domains: List<String>): String =
        if (domains.isEmpty()) content else Mobileexperiment.ownedRulesJSON(content, JSONArray(domains).toString(), "[]")

    private fun addOwnedRule(content: String, domains: List<String>, oldDomains: List<String> = context?.let { learned(it) }.orEmpty()): String =
        Mobileexperiment.ownedRulesJSON(content, JSONArray(oldDomains).toString(), JSONArray(domains.distinct().sorted()).toString())
    fun removeLearned(context: Context, domain: String, current: ConfigRepository.StagedConfig = ConfigRepository.load(context)) {
        check(!_status.value.pending && !settling) { "Experiment is busy" }
        val old = learned(context, current); val remaining = old.filterNot { it == domain }
        val next = if (!settings(context, current).learningEnabled) {
            if (remaining.isEmpty()) Mobileexperiment.resumePausedRulesJSON(current.generatedContent, "[]") else current.generatedContent
        }
            else if (remaining.isEmpty()) stripOwnedRule(current.generatedContent, old)
            else addOwnedRule(current.generatedContent, remaining, old)
        ConfigRepository.saveGeneratedEdit(context, next, expected = current, learnedDomains = remaining)
        _status.value = Status("Learned route removed; apply configuration")
    }
    @Synchronized fun recover(manual: Boolean = true): String {
        val context = context ?: return "unavailable"
        if (!activeSettings.fallback) return "disabled"
        if (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule") return "mode"
        if (_status.value.pending || MobilecoreRuntime.state.value.configApplyPending || MobilecoreRuntime.state.value.configState != "Clean") return "busy"
        if (settling) return "busy"
        if (manual && System.currentTimeMillis() - recoveryAt < 90_000) return "cooldown"
        val metadata = JSONObject(ConfigRepository.load(context).hostMetadata)
        val priorities = metadata.optJSONObject("fallbacks") ?: return "disabled"
        val groups = CommandClientRuntime.groups.value
        if (groups.isEmpty()) return "unavailable"
        val eligible = priorities.keys().asSequence().filter { tag ->
            val chain = priorities.getJSONArray(tag)
            val selected = groups.firstOrNull { it.tag == tag }?.selected
            (0 until chain.length()).any { chain.getString(it) == selected }
        }.toList()
        if (eligible.isEmpty()) return "manual"
        settling = true; recoveryAt = System.currentTimeMillis()
        val selectedBefore = groups.associate { it.tag to it.selected }
        val generation = networkGeneration.get()
        val targets = eligible.flatMap { tag -> priorities.getJSONArray(tag).let { array -> (0 until array.length()).map { array.getString(it) } } }.toSet()
        val waitMillis = FallbackDecision.resultWaitMillis(targets, groups)
        recoveryJob = scope?.launch {
            var healthyRound = true
            try {
                // Native test timestamps have whole-second resolution.
                delay(1000 - System.currentTimeMillis() % 1000 + 50)
                val started = System.currentTimeMillis() / 1000
                for (target in targets) { ensureActive(); CommandClientRuntime.urlTest(target) }
                _status.value = _status.value.copy(message = "Recovery tests running")
                delay(waitMillis)
                ensureActive()
                if (networkGeneration.get() != generation) return@launch
                val fresh = CommandClientRuntime.groups.value
                for (tag in eligible) {
                    val chain = priorities.getJSONArray(tag).let { array -> (0 until array.length()).map { array.getString(it) } }
                    if (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule" || MobilecoreRuntime.state.value.configState != "Clean" || !CommandClientRuntime.channelConnected.value) { healthyRound = false; break }
                    val selected = fresh.firstOrNull { it.tag == tag }?.selected ?: continue
                    if (selected != selectedBefore[tag]) { recoveries.clear(); healthyRound = false; continue }
                    val current = chain.indexOf(selected); if (current < 0) continue
                    val desired = chain.indexOf(FallbackDecision.candidate(fresh, tag, chain, started))
                    if (desired < 0) { healthyRound = false; _status.value = _status.value.copy(message = "No freshly healthy fallback"); continue }
                    if (desired == current) { recoveries.clear(); continue }
                    healthyRound = false
                    val candidate = chain[desired]
                    val count = (recoveries[candidate] ?: 0) + 1
                    recoveries[candidate] = count
                    if (desired > current || count >= activeSettings.recoverAfter) {
                        if (CommandClientRuntime.selectOutbound(tag, candidate)) _status.value = _status.value.copy(message = "Fallback selection requested")
                        recoveries[candidate] = 0
                    }
                }
                schedule.record(healthyRound)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (_: Exception) {
                schedule.reset()
                _status.value = _status.value.copy(message = "Recovery failed; selection retained")
            } finally { settling = false }
        }
        return "queued"
    }
}
