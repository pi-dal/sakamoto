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
    data class Settings(val mode: String = "off", val threshold: Int = 3, val cfRegion: Boolean = false, val fallback: Boolean = false, val recoverAfter: Int = 2) {
        val learningEnabled: Boolean get() = mode == "auto" || cfRegion
        val requiresMonitoring: Boolean get() = learningEnabled || fallback
    }
    data class Status(val message: String = "Stopped", val learned: Int = 0, val pending: Boolean = false)
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
    @Volatile private var updateDomains: ((String) -> Unit)? = null
    private var context: Context? = null
    private val schedule = ExperimentProbeSchedule()
    private var wakeups = Channel<Unit>(Channel.CONFLATED)
    private var recoveryJob: Job? = null
    private val networkGeneration = java.util.concurrent.atomic.AtomicLong()
    @Volatile private var healthSince = 0L
    private var observedGroups: List<ConfigRepository.Group> = emptyList()
    private var monitoredSelectors = setOf("MainProxy")

    fun networkChanged() {
        if (!activeSettings.requiresMonitoring || scope == null) return
        networkGeneration.incrementAndGet()
        healthSince = System.currentTimeMillis() / 1000 + 1
        schedule.reset()
        scope?.launch {
            recoveryJob?.cancelAndJoin()
            wakeups.trySend(Unit)
        }
    }

    fun modeChanged() { if (scope != null) wakeups.trySend(Unit) }

    @Synchronized fun groupsChanged(groups: List<ConfigRepository.Group>) {
        if (!activeSettings.requiresMonitoring || scope == null) return
        val previous = FallbackDecision.selectedObservation(observedGroups)
        val current = FallbackDecision.selectedObservation(groups)
        val selectionChanged = groups.any { group -> group.tag in monitoredSelectors && (observedGroups.firstOrNull { it.tag == group.tag }?.selected ?: "") != group.selected }
        observedGroups = groups
        if (!settling && (selectionChanged || ((previous?.delay ?: 0) > 0 && (current?.delay ?: 0) <= 0))) {
            schedule.reset(); wakeups.trySend(Unit)
        } else if (!settling && !activeSettings.fallback && current != null && current.testedAt > (previous?.testedAt ?: 0)) {
            schedule.record(current.delay > 0 && System.currentTimeMillis() / 1000 - current.testedAt in 0..120)
        }
    }

    fun settings(context: Context): Settings {
        val prefs = context.getSharedPreferences("experiments", Context.MODE_PRIVATE)
        val saved = prefs.getString("settings", null)
        val metadata = runCatching { JSONObject(ConfigRepository.load(context).hostMetadata) }.getOrDefault(JSONObject())
        val value = saved?.let { JSONObject(it) } ?: (metadata.optJSONObject("experiment") ?: JSONObject())
        return Settings(value.optString("mode", "off").takeIf { it in setOf("off", "on", "auto") } ?: "off", value.optInt("threshold", 3).coerceIn(1, 20), value.optBoolean("cf_region_block"),
            if (saved != null) value.optBoolean("fallback") else metadata.optBoolean("fallback_enabled"), value.optInt("recover_after", metadata.optInt("recover_after", 2)).coerceIn(1, 20))
    }
    fun learned(context: Context): List<String> {
        val raw = context.getSharedPreferences("experiments", Context.MODE_PRIVATE).getString("learned", "[]")!!
        val array = JSONArray(raw)
        return (0 until array.length()).map { array.getString(it) }
    }
    @Synchronized fun saveSettings(context: Context, next: Settings) {
        require(next.mode in setOf("off", "on", "auto") && next.threshold in 1..20 && next.recoverAfter in 1..20)
        val current = ConfigRepository.load(context)
        val root = JSONObject(current.generatedContent)
        val route = root.getJSONObject("route")
        route.put("final", if (next.mode == "on") Mobileexperiment.proxyOutbound(current.generatedContent) else "direct")
        check(!_status.value.pending && !settling) { "Experiment is busy" }
        val base = stripOwnedRule(root.toString(), learned(context))
        val adjusted = if ((next.mode == "auto" || next.cfRegion) && learned(context).isNotEmpty()) addOwnedRule(base, learned(context), emptyList()) else base
        ConfigRepository.saveGeneratedEdit(context, adjusted)
        val value = JSONObject().put("mode", next.mode).put("threshold", next.threshold).put("cf_region_block", next.cfRegion).put("fallback", next.fallback).put("recover_after", next.recoverAfter)
        check(context.getSharedPreferences("experiments", Context.MODE_PRIVATE).edit().putString("settings", value.toString()).commit())
        // Staged settings become runtime policy only after Apply/reconnect.
        _status.value = _status.value.copy(message = "Saved; apply configuration", learned = learned(context).size)
    }
    fun restorePending(context: Context) {
        val prefs = context.getSharedPreferences("experiments", Context.MODE_PRIVATE)
        val before = prefs.getString("pendingBefore", null) ?: return
        val candidate = prefs.getString("pendingCandidate", null) ?: return
        val current = ConfigRepository.load(context)
        if (current.generatedContent == candidate) {
            Libbox.checkConfig(before)
            ConfigRepository.save(context, current.copy(generatedContent = before))
        }
        check(prefs.edit().remove("pendingBefore").remove("pendingCandidate").commit())
    }

    fun prepareContent(context: Context, content: String): String {
        val connected = Mobilecore.prepareTailscaleConnectivityJSON(content)
        val settings = settings(context)
        if (settings.mode != "auto" && !settings.cfRegion) return connected
        val domains = JSONArray(learned(context)).toString()
        return Mobileexperiment.prepareLiveRulesJSON(connected, domains, domains)
    }

    @Synchronized fun start(context: Context, update: (String) -> Unit) {
        if (job != null) return
        updateDomains = update
        this.context = context.applicationContext
        activeSettings = settings(context)
        monitoredSelectors = setOf("MainProxy") + runCatching {
            JSONObject(ConfigRepository.load(context).hostMetadata).optJSONObject("fallbacks")?.keys()?.asSequence()?.toSet().orEmpty()
        }.getOrDefault(emptySet())
        tracker = if (activeSettings.learningEnabled) Mobileexperiment.newExperimentTracker(activeSettings.threshold) else null
        schedule.reset(); observedGroups = emptyList(); healthSince = System.currentTimeMillis() / 1000
        CommandClientRuntime.setLearningEnabled(activeSettings.learningEnabled)
        if (!activeSettings.requiresMonitoring) {
            _status.value = Status("Monitoring disabled", learned(context).size)
            return
        }
        val owner = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        scope = owner
        attempts = Channel(Channel.RENDEZVOUS)
        wakeups = Channel(Channel.CONFLATED)
        job = owner.launch {
            _status.value = Status("Monitoring while VPN runs", learned(context).size)
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
        scope?.cancel(); scope = null; job = null; recoveryJob = null
        wakeups.close(); schedule.reset(); observedGroups = emptyList()
        CommandClientRuntime.setLearningEnabled(false)
        attempts.close(); tracker = null; updateDomains = null; context = null; settling = false; recoveries.clear()
        _status.value = _status.value.copy(message = "Stopped", pending = false)
    }
    fun connection(events: ConnectionEvents) {
        if (activeSettings.mode != "auto" && !activeSettings.cfRegion) return
        val iterator = events.iterator()
        val now = System.currentTimeMillis()
        while (iterator.hasNext()) {
            val event = iterator.next()
            if (event.type.toLong() !in setOf(Libbox.ConnectionEventNew, Libbox.ConnectionEventClosed)) continue
            val c = event.connection ?: continue
            tracker?.connection(event.id, c.domain, c.destination, c.network, c.outbound, c.rule, event.type.toLong() == Libbox.ConnectionEventClosed, c.downlinkTotal, now)
        }
    }
    fun log(message: String) {
        if (_status.value.pending || MobilecoreRuntime.state.value.configState != "Clean" || MobilecoreRuntime.state.value.routingMode.lowercase() != "rule" || (activeSettings.mode != "auto" && !activeSettings.cfRegion)) return
        val groups = CommandClientRuntime.groups.value
        val fresh = maxOf(healthSince, System.currentTimeMillis() / 1000 - 120)
        val observation = FallbackDecision.selectedObservation(groups)
        val healthy = observation != null && observation.delay > 0 && observation.testedAt >= fresh
        val domain = tracker?.observeLog(message, System.currentTimeMillis(), healthy, activeSettings.mode == "auto", activeSettings.cfRegion).orEmpty()
        if (domain.isNotEmpty()) {
            val sent = attempts.trySend(domain)
            if (sent.isFailure) _status.value = _status.value.copy(message = "Learning busy; waiting for fresh evidence")
        }
    }
    private suspend fun promote(context: Context, domain: String) {
        val known = learned(context)
        val update = updateDomains ?: return
        if (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule") return
        if (domain in known || known.size >= 1000 || !CommandClientRuntime.channelConnected.value) return
        val before = ConfigRepository.load(context)
        if (before.sourceNeedsGenerate || MobilecoreRuntime.state.value.configState != "Clean") {
            _status.value = Status("Pending edits; automatic route update deferred", known.size)
            return
        }
        _status.value = Status("Validating learned route", known.size, true)
        var wrote = false
        var candidate = ""
        try {
            candidate = addOwnedRule(before.generatedContent, known + domain)
            Libbox.checkConfig(candidate)
            check(ConfigRepository.load(context) == before) { "Configuration changed during learning" }
            val prefs = context.getSharedPreferences("experiments", Context.MODE_PRIVATE)
            check(prefs.edit().putString("pendingBefore", before.generatedContent).putString("pendingCandidate", candidate).commit())
            ConfigRepository.save(context, before.copy(generatedContent = candidate)); wrote = true
            MobilecoreRuntime.configEvent("modified")
            // The live inline rule-set affects future connections only. Keep
            // the core/TUN/Tailscale instance and established SSH streams.
            update(JSONArray(known + domain).toString())
            currentCoroutineContext().ensureActive()
            check(ConfigRepository.load(context).generatedContent == candidate) { "Configuration changed during learning" }
            check(MobilecoreRuntime.state.value.serviceState == "Running" && CommandClientRuntime.channelConnected.value)
            MobilecoreRuntime.completeConfigApply()
            check(context.getSharedPreferences("experiments", Context.MODE_PRIVATE).edit().putString("learned", JSONArray(known + domain).toString()).remove("pendingBefore").remove("pendingCandidate").commit())
            _status.value = Status("Learned proxy route applied", known.size + 1)
        } catch (cancelled: CancellationException) {
            if (wrote && ConfigRepository.load(context).generatedContent == candidate) ConfigRepository.save(context, before)
            // Leave the journal for next-start recovery if cancellation
            // occurred during a native reload; never claim it was applied.
            throw cancelled
        } catch (_: Exception) {
            if (wrote && ConfigRepository.load(context).generatedContent == candidate) {
                ConfigRepository.save(context, before)
                try {
                    update(JSONArray(known).toString())
                    MobilecoreRuntime.completeConfigApply()
                } catch (_: Exception) {
                    _status.value = Status("Learning rollback failed; reconnect", known.size)
                    return // Keep the journal for next-start recovery.
                }
            }
            context.getSharedPreferences("experiments", Context.MODE_PRIVATE).edit().remove("pendingBefore").remove("pendingCandidate").commit()
            _status.value = Status("Learning failed; previous configuration restored", known.size)
        } finally { _status.value = _status.value.copy(pending = false) }
    }
    // Remove only an exact phone-owned learned domain set, not arbitrary user rules.
    private fun stripOwnedRule(content: String, domains: List<String>): String {
        if (domains.isEmpty()) return content
        val root = JSONObject(content); val route = root.getJSONObject("route"); val rules = route.getJSONArray("rules")
        val outbound = Mobileexperiment.proxyOutbound(content)
        val next = JSONArray()
        for (i in 0 until rules.length()) {
            val rule = rules.getJSONObject(i); val names = rule.optJSONArray("domain")
            val owned = rule.length() == 3 && rule.optString("action") == "route" && rule.optString("outbound") == outbound && names != null && (0 until names.length()).map { names.getString(it) }.toSet() == domains.toSet()
            if (!owned) next.put(rule)
        }
        route.put("rules", next); return root.toString()
    }
    private fun addOwnedRule(content: String, domains: List<String>, oldDomains: List<String> = context?.let { learned(it) }.orEmpty()): String {
        val base = stripOwnedRule(content, oldDomains)
        // The host insertion helper can replace an existing exact-domain proxy
        // rule. Refuse that ambiguity instead of deleting a user's rule.
        val proxy = Mobileexperiment.proxyOutbound(base)
        val rules = JSONObject(base).getJSONObject("route").getJSONArray("rules")
        for (i in 0 until rules.length()) {
            val rule = rules.getJSONObject(i)
            require(!(rule.has("domain") && rule.optString("outbound") == proxy && rule.optString("action") == "route")) { "User proxy rule overlaps learning insertion" }
        }
        return Mobileexperiment.rulesJSON(base, JSONArray(domains.distinct().sorted()).toString())
    }
    fun removeLearned(context: Context, domain: String) {
        check(!_status.value.pending && !settling) { "Experiment is busy" }
        val old = learned(context); val remaining = old.filterNot { it == domain }
        val current = ConfigRepository.load(context)
        var next = stripOwnedRule(current.generatedContent, old)
        if (remaining.isNotEmpty() && (activeSettings.mode == "auto" || activeSettings.cfRegion)) next = addOwnedRule(next, remaining)
        ConfigRepository.saveGeneratedEdit(context, next)
        context.getSharedPreferences("experiments", Context.MODE_PRIVATE).edit().putString("learned", JSONArray(remaining).toString()).apply()
        _status.value = Status("Learned route removed; apply configuration", remaining.size)
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
