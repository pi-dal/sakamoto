package com.pidal.sakamoto.runtime

import android.content.Context
import com.pidal.sakamoto.command.CommandClientRuntime
import io.nekohasekai.mobileexperiment.Mobileexperiment
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
    data class Settings(val mode: String = "off", val threshold: Int = 3, val cfRegion: Boolean = false, val fallback: Boolean = false, val recoverAfter: Int = 2)
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
    private var context: Context? = null

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
        activeSettings = next; tracker = Mobileexperiment.newExperimentTracker(next.threshold)
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

    fun start(context: Context) {
        if (job != null) return
        this.context = context.applicationContext
        activeSettings = settings(context)
        tracker = Mobileexperiment.newExperimentTracker(activeSettings.threshold)
        val owner = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        scope = owner
        attempts = Channel(Channel.RENDEZVOUS)
        job = owner.launch {
            _status.value = Status("Monitoring while VPN runs", learned(context).size)
            launch { for (domain in attempts) promote(context, domain) }
            while (isActive) {
                delay(30_000)
                if (activeSettings.fallback && !settling && !_status.value.pending) recover(false)
            }
        }
    }
    fun stop() {
        scope?.cancel(); scope = null; job = null
        attempts.close(); tracker = null; context = null; settling = false; recoveries.clear()
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
        val main = groups.firstOrNull { it.tag == "MainProxy" } ?: return
        val fresh = System.currentTimeMillis() / 1000 - 120
        val selected = groups.firstOrNull { it.tag == main.selected }
        val healthy = (selected?.items ?: groups.flatMap { it.items }.filter { it.tag == main.selected }).any { it.delay > 0 && it.testedAt >= fresh }
        val domain = tracker?.observeLog(message, System.currentTimeMillis(), healthy, activeSettings.mode == "auto", activeSettings.cfRegion).orEmpty()
        if (domain.isNotEmpty()) {
            val sent = attempts.trySend(domain)
            if (sent.isFailure) _status.value = _status.value.copy(message = "Learning busy; waiting for fresh evidence")
        }
    }
    private suspend fun promote(context: Context, domain: String) {
        val known = learned(context)
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
            check(CommandClientRuntime.reloadService()) { "Reload request failed" }
            withTimeout(15_000) {
                delay(250)
                while (MobilecoreRuntime.state.value.configApplyPending || MobilecoreRuntime.state.value.configState != "Clean") delay(100)
            }
            check(MobilecoreRuntime.state.value.serviceState == "Running" && CommandClientRuntime.channelConnected.value)
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
                CommandClientRuntime.reloadService()
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
        scope?.launch {
            try {
                val started = System.currentTimeMillis() / 1000
                eligible.forEach { tag -> val chain = priorities.getJSONArray(tag); for (i in 0 until chain.length()) CommandClientRuntime.urlTest(chain.getString(i)) }
                _status.value = _status.value.copy(message = "Recovery tests running")
                delay(10_000)
                val fresh = CommandClientRuntime.groups.value
                for (tag in eligible) {
                    val chain = priorities.getJSONArray(tag).let { array -> (0 until array.length()).map { array.getString(it) } }
                    if (MobilecoreRuntime.state.value.routingMode.lowercase() != "rule" || MobilecoreRuntime.state.value.configState != "Clean") break
                    val selected = fresh.firstOrNull { it.tag == tag }?.selected ?: continue
                    val current = chain.indexOf(selected); if (current < 0) continue
                    val desired = chain.indexOf(FallbackDecision.candidate(fresh, tag, chain, started))
                    if (desired < 0) { _status.value = _status.value.copy(message = "No freshly healthy fallback"); continue }
                    if (desired == current) { recoveries.clear(); continue }
                    val candidate = chain[desired]
                    val count = (recoveries[candidate] ?: 0) + 1
                    recoveries[candidate] = count
                    if (desired > current || count >= activeSettings.recoverAfter) {
                        if (CommandClientRuntime.selectOutbound(tag, candidate)) _status.value = _status.value.copy(message = "Fallback selection requested")
                        recoveries[candidate] = 0
                    }
                }
            } finally { settling = false }
        }
        return "queued"
    }
}
