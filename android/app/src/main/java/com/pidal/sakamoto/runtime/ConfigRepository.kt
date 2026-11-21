package com.pidal.sakamoto.runtime

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * On-device staging storage for the Config tab, decoded/stored as one JSON
 * document in filesDir (the Android analogue of the iOS app's
 * ConfigStore-backed UserDefaults, NOT of any synced store).
 *
 * Fields (page boundary mirrors ios/App ConfigStore + docs/tui.md):
 *   generatedContent — the saved sing-box config the tunnel service loads
 *                      (host-generated on sakamoto; pasted or imported here).
 *                      This is runtime state and NEVER synced anywhere.
 *   sourceConf       — last imported Shadowrocket conf CONTENT (local file
 *                      import) or a URL reference. Metadata + content only;
 *                      remote include/RULE-SET bodies stay importer-managed.
 *   policy           — staged user policy rules (validated through
 *                      Mobilecore.normalizePolicyRule before they land here).
 *   nodes            — staged raw share links (nodes.txt-compatible). These
 *                      are secrets: they live in app-private storage and
 *                      never enter log/notice strings.
 *   subscriptions    — subscription METADATA (name + URL + format). Bodies
 *                      are fetched on the host; the device never pretends a
 *                      cached snapshot is the source.
 */
object ConfigRepository {

    private const val FILE_NAME = "sakamoto-config.json"
    private val _changes = kotlinx.coroutines.flow.MutableStateFlow(0L)
    val changes: kotlinx.coroutines.flow.StateFlow<Long> = _changes
    private val revision = java.util.concurrent.atomic.AtomicLong()

    data class PolicyRule(val match: String, val action: String)

    data class StagedConfig(
        val generatedContent: String = "",
        val sourceConfName: String = "",
        val sourceConfIsUrl: Boolean = false,
        val sourceConfContent: String = "",
        val sourceConfPath: String = "",
        val hostSnapshotAt: String = "",
        val hostMetadata: String = "{}",
        val sourceNeedsGenerate: Boolean = false,
        val policy: List<PolicyRule> = emptyList(),
        val nodes: List<String> = emptyList(),
        val subscriptions: List<Triple<String, String, String>> = emptyList(), // name, url, format
        val nodeAliases: Map<String, String> = emptyMap(),
        val automaticGroups: List<ProxyGroupEdit> = emptyList(),
        val deviceGroupExclusions: Map<String, List<String>>? = null,
        val regionalDNS: Map<String, String> = emptyMap(),
        val regionalDNSOrigin: String = "legacy",
        val proxyChain: ProxyChainSettings? = null,
        val modeCacheID: String = "",
        val packageRevision: String = "",
        val confirmedRevision: String = packageRevision,
        val scopedExperiment: Boolean = false,
        val learningCommit: String? = null,
        val learnedDomains: List<String>? = null, // Older installs keep the list in profile-scoped preferences.
        val legacyExperimentID: String = "",
        val s3Snapshot: String? = null,
        val editedNodeText: String? = null,
        val routeSourceVersion: Int = 0,
    ) {
        fun localExclusions(): Map<String, List<String>> = deviceGroupExclusions ?: if (generatedContent.isBlank()) emptyMap() else {
            // Older installs did not distinguish imported exclusions from edits. Retain their restrictions.
            ProxyGroupEditing.forImport(generatedContent, automaticGroups, nodeAliases)
                .filter(ProxyGroupEditing::followsGeneratedNodes).associate { it.tag to it.excludedMembers.orEmpty() }
        }

        val runtimeContent: String get() {
            TunnelPackage.validateRouteSource(generatedContent, routeSourceVersion)
            val content = proxyChain?.applying(generatedContent) ?: generatedContent
            if (content.isBlank()) return content
            val scoped = if (modeCacheID.isEmpty()) content else JSONObject(content).apply {
                val experimental = optJSONObject("experimental") ?: JSONObject().also { put("experimental", it) }
                val cache = experimental.optJSONObject("cache_file") ?: JSONObject().also { experimental.put("cache_file", it) }
                cache.put("enabled", true).put("path", "sakamoto-mode.db").put("cache_id", modeCacheID)
            }.toString()
            return io.nekohasekai.mobilecore.Mobilecore.applyRegionalDNS(scoped, regionalDNS["domains"].orEmpty(), regionalDNS["resolver_ip"].orEmpty(), regionalDNS["tls_name"].orEmpty(), regionalDNS["relay"].orEmpty(), regionalDNS["relay_fingerprint"].orEmpty())
        }
    }

    private fun file(context: Context): File = File(context.filesDir, FILE_NAME)

    // A compact receipt can cross activity recreation without putting the private config in a Bundle.
    fun manifestReceipt(context: Context): String {
        val manifest = file(context)
        if (!manifest.exists()) return "absent"
        return java.security.MessageDigest.getInstance("SHA-256").digest(manifest.readBytes())
            .joinToString("") { "%02x".format(it) }
    }

    @Synchronized fun loadAt(context: Context, receipt: String?): StagedConfig? =
        if (receipt != null && manifestReceipt(context) == receipt) load(context) else null

    fun load(context: Context): StagedConfig {
        val f = file(context)
        if (!f.exists()) return StagedConfig()
        return try {
            val bytes = f.readBytes()
            val root = JSONObject(String(bytes, Charsets.UTF_8))
            val content = root.optString("generatedContent")
            val sourceVersion = root.optInt("routeSourceVersion")
            // Adopt a legacy baseline in memory. The next successful edit persists the result.
            val checkedVersion = if (sourceVersion != 1 && content.isNotBlank()) {
                runCatching { TunnelPackage.validateRouteSource(content, sourceVersion) }.fold({ 1 }, { sourceVersion })
            } else sourceVersion
            StagedConfig(
                generatedContent = content,
                s3Snapshot = root.optJSONObject("s3Snapshot")?.toString(),
                editedNodeText = root.optString("editedNodeText").takeIf { root.has("editedNodeText") },
                modeCacheID = root.optString("modeCacheID"),
                packageRevision = root.optString("packageRevision"),
                confirmedRevision = root.optString("confirmedRevision", root.optString("packageRevision")),
                scopedExperiment = root.optBoolean("scopedExperiment"),
                learningCommit = root.optString("learningCommit").takeIf { it.isNotEmpty() },
                learnedDomains = root.optJSONArray("learnedDomains")?.let { names -> List(names.length()) { names.getString(it) } },
                // Bind an installed legacy profile to its initial manifest. Configuration edits
                // carry the identity forward without changing the selected routing-mode cache.
                legacyExperimentID = if (root.optBoolean("scopedExperiment") && root.optString("modeCacheID").isNotEmpty()) "" else
                    root.optString("legacyExperimentID").takeIf { runCatching { java.util.UUID.fromString(it).toString() == it }.getOrDefault(false) }
                        ?: java.util.UUID.nameUUIDFromBytes(bytes).toString(),
                routeSourceVersion = checkedVersion,
                sourceConfName = root.optString("sourceConfName"),
                sourceConfIsUrl = root.optBoolean("sourceConfIsUrl"),
                sourceConfContent = root.optString("sourceConfContent"),
                sourceConfPath = root.optString("sourceConfPath"),
                hostSnapshotAt = root.optString("hostSnapshotAt"),
                hostMetadata = root.optString("hostMetadata", "{}"),
                sourceNeedsGenerate = root.optBoolean("sourceNeedsGenerate"),
                policy = root.optJSONArray("policy")?.let { array ->
                    (0 until array.length()).mapNotNull { i ->
                        val o = array.optJSONObject(i) ?: return@mapNotNull null
                        PolicyRule(o.optString("match"), o.optString("action"))
                    }
                } ?: emptyList(),
                regionalDNSOrigin = root.optString("regionalDNSOrigin", "legacy"),
                regionalDNS = root.optJSONObject("regionalDNS")?.let { dns -> dns.keys().asSequence().associateWith { dns.getString(it) } } ?: emptyMap(),
                proxyChain = ProxyChainSettings.decode(root.optJSONObject("proxyChain")),
                deviceGroupExclusions = root.optJSONObject("deviceGroupExclusions")?.let { policy ->
                    policy.keys().asSequence().associateWith { tag -> policy.getJSONArray(tag).let { names -> List(names.length()) { names.getString(it) } } }
                },
                automaticGroups = ProxyGroupEditing.decodeSaved(root.optJSONArray("automaticGroups")),
                nodeAliases = root.optJSONObject("nodeAliases")?.let { aliases -> aliases.keys().asSequence().associateWith { aliases.getString(it) } } ?: emptyMap(),
                nodes = root.optJSONArray("nodes")?.let { a -> (0 until a.length()).map { a.optString(it) } } ?: emptyList(),
                subscriptions = root.optJSONArray("subscriptions")?.let { array ->
                    (0 until array.length()).mapNotNull { i ->
                        val o = array.optJSONObject(i) ?: return@mapNotNull null
                        Triple(o.optString("name"), o.optString("url"), o.optString("format"))
                    }
                } ?: emptyList(),
            )
        } catch (e: Exception) {
            // A corrupted staging file must not take the tunnel down: start
            // empty; the caller surfaces corruption through its own notice.
            StagedConfig()
        }
    }

    private fun manifest(config: StagedConfig): JSONObject {
        val root = JSONObject()
        root.put("generatedContent", config.generatedContent)
        config.editedNodeText?.let { root.put("editedNodeText", it) }
        config.s3Snapshot?.let { root.put("s3Snapshot", JSONObject(it)) }
        root.put("modeCacheID", config.modeCacheID)
        root.put("packageRevision", config.packageRevision)
        root.put("confirmedRevision", config.confirmedRevision)
        root.put("scopedExperiment", config.scopedExperiment)
        config.learningCommit?.let { root.put("learningCommit", it) }
        config.learnedDomains?.let { root.put("learnedDomains", JSONArray(it)) }
        if (config.legacyExperimentID.isNotEmpty()) root.put("legacyExperimentID", config.legacyExperimentID)
        root.put("routeSourceVersion", config.routeSourceVersion)
        root.put("sourceConfName", config.sourceConfName)
        root.put("sourceConfIsUrl", config.sourceConfIsUrl)
        root.put("sourceConfContent", config.sourceConfContent)
        root.put("sourceConfPath", config.sourceConfPath)
        root.put("hostSnapshotAt", config.hostSnapshotAt)
        root.put("hostMetadata", config.hostMetadata)
        root.put("sourceNeedsGenerate", config.sourceNeedsGenerate)
        root.put(
            "policy",
            JSONArray().apply {
                config.policy.forEach { put(JSONObject().put("match", it.match).put("action", it.action)) }
            },
        )
        root.put("regionalDNS", JSONObject(config.regionalDNS))
        root.put("regionalDNSOrigin", config.regionalDNSOrigin)
        root.put("proxyChain", config.proxyChain?.json())
        config.deviceGroupExclusions?.let { root.put("deviceGroupExclusions", JSONObject(it)) }
        root.put("automaticGroups", ProxyGroupEditing.encodeSaved(config.automaticGroups))
        root.put("nodeAliases", JSONObject(config.nodeAliases))
        root.put("nodes", JSONArray().apply { config.nodes.forEach { put(it) } })
        root.put(
            "subscriptions",
            JSONArray().apply {
                config.subscriptions.forEach {
                    put(JSONObject().put("name", it.first).put("url", it.second).put("format", it.third))
                }
            },
        )
        return root
    }

    fun saveLearned(context: Context, config: StagedConfig, expected: StagedConfig, prepareSnapshot: (String) -> Unit) {
        val root = manifest(config)
        val receipt = java.security.MessageDigest.getInstance("SHA-256").digest(root.toString().toByteArray(Charsets.UTF_8))
            .joinToString("") { "%02x".format(it) }
        publish(context, root) {
            check(load(context) == expected) { "Configuration changed during learning" }
            prepareSnapshot(receipt)
        }
    }

    fun save(context: Context, config: StagedConfig, expected: StagedConfig? = null, beforeCommit: () -> Unit = {}) {
        publish(context, manifest(config)) {
            check(expected == null || load(context) == expected) { "Configuration changed during save" }
            beforeCommit()
        }
    }

    private val sourceReaders = mutableSetOf<() -> Unit>()

    // Registration and publication use the same lock. A reader either observes
    // the replacement or registers in time to be cancelled before it.
    @Synchronized internal fun watchSources(context: Context, expected: StagedConfig, cancel: () -> Unit): AutoCloseable {
        check(load(context) == expected) { "Sources changed before sync started" }
        sourceReaders.add(cancel)
        return AutoCloseable { synchronized(this) { sourceReaders.remove(cancel) } }
    }

    @Synchronized internal fun publish(context: Context, state: JSONObject, beforeCommit: () -> Unit = {}) {
        val tmp = File.createTempFile("profile-", ".tmp", context.filesDir)
        try {
            tmp.outputStream().use { stream ->
                stream.write(state.toString().toByteArray(Charsets.UTF_8))
                stream.fd.sync()
            }
            beforeCommit()
            sourceReaders.forEach { it() }
            android.system.Os.rename(tmp.path, file(context).path)
        } finally {
            tmp.delete()
        }
        notifyInstalled()
    }

    internal fun notifyInstalled() { _changes.value = revision.incrementAndGet() }

    fun saveProxyChain(context: Context, settings: ProxyChainSettings, expected: StagedConfig) {
        val current = load(context)
        require(current == expected) { "Configuration changed. Reopen the chain editor." }
        val base = if (current.proxyChain == null) ProxyChainSettings.unchained(current.generatedContent) else current.generatedContent
        val candidate = settings.applying(base)
        validateRuntime(current.copy(proxyChain = null), candidate)
        save(context, current.copy(generatedContent = base, proxyChain = settings), expected)
        MobilecoreRuntime.configEvent("modified")
    }

    fun saveRegionalDNS(context: Context, expected: StagedConfig, values: Map<String, String>) {
        require(load(context) == expected) { "Configuration changed. Reopen Regional DNS." }
        val approved = if (values.getValue("domains").isEmpty()) emptyMap() else values +
            ("relay_fingerprint" to io.nekohasekai.mobilecore.Mobilecore.regionalRelayFingerprint(expected.generatedContent, values.getValue("relay")))
        val candidate = expected.copy(regionalDNS = approved, regionalDNSOrigin = "device")
        validateRuntime(candidate, candidate.generatedContent)
        save(context, candidate, expected)
        MobilecoreRuntime.configEvent("modified")
    }

    private fun validateRuntime(current: StagedConfig, content: String) {
        val runtime = current.copy(generatedContent = content).runtimeContent
        io.nekohasekai.mobilecore.Mobilecore.validateConfigJSON(runtime)
        io.nekohasekai.libbox.Libbox.checkConfig(runtime)
    }

    fun saveNodeEdit(context: Context, content: String, old: String, name: String, current: StagedConfig = load(context)) {
        val chain = current.proxyChain?.let { it.copy(upstream = if (it.upstream == old) name else it.upstream, exit = if (it.exit == old) name else it.exit) }
        val protected = io.nekohasekai.mobilecore.Mobilecore.prepareTailscaleConnectivityJSON(content)
        validateRuntime(current.copy(proxyChain = chain), protected)
        val aliases = current.nodeAliases.mapValues { if (it.value == old) name else it.value }.toMutableMap()
        if (old != name && current.nodeAliases.values.none { it == old }) aliases[old] = name
        val metadata = JSONObject(current.hostMetadata)
        metadata.optJSONObject("fallbacks")?.let { groups ->
            for (key in groups.keys()) {
                val members = groups.optJSONArray(key) ?: continue
                groups.put(key, JSONArray((0 until members.length()).map { members.getString(it).let { member -> if (member == old) name else member } }))
            }
        }
        val groups = current.automaticGroups.map { edit -> edit.copy(members = edit.members.map { if (it == old) name else it },
            excludedMembers = edit.excludedMembers?.map { if (it == old) name else it }, additionalMembers = edit.additionalMembers?.map { if (it == old) name else it }) }
        save(context, current.copy(generatedContent = protected, nodeAliases = aliases, hostMetadata = metadata.toString(), automaticGroups = groups, proxyChain = chain), current)
        MobilecoreRuntime.configEvent("modified")
    }

    fun saveProxyGroup(context: Context, edit: ProxyGroupEdit, current: StagedConfig = load(context)) {
        val recorded = ProxyGroupEditing.recording(current.generatedContent, edit, current.automaticGroups.firstOrNull { it.tag == edit.tag })
        val content = ProxyGroupEditing.apply(current.generatedContent, recorded)
        validateRuntime(current, content)
        val groups = current.automaticGroups.filterNot { it.tag == edit.tag } + if (ProxyGroupEditing.followsGeneratedNodes(recorded)) listOf(recorded) else emptyList()
        val exclusions = current.localExclusions().toMutableMap()
        if (ProxyGroupEditing.followsGeneratedNodes(recorded)) {
            val sourceNames = current.nodeAliases.entries.associate { it.value to it.key }
            val before = ProxyGroupEditing.groups(current.generatedContent).firstOrNull { it.tag == edit.tag }?.members.orEmpty()
            val removed = (before - edit.members.toSet()).map { sourceNames[it] ?: it }
            val allowed = edit.members.map { sourceNames[it] ?: it }.toSet()
            exclusions[edit.tag] = ((exclusions[edit.tag].orEmpty() + removed).distinct() - allowed).sorted()
        }
        save(context, current.copy(generatedContent = content, automaticGroups = groups, deviceGroupExclusions = exclusions), current)
        MobilecoreRuntime.configEvent("modified")
    }

    fun saveGeneratedEdit(context: Context, content: String, hostMetadata: String? = null, expected: StagedConfig, learnedDomains: List<String>? = expected.learnedDomains) {
        check(load(context) == expected) { "Configuration changed. Reopen the editor." }
        val protected = io.nekohasekai.mobilecore.Mobilecore.prepareTailscaleConnectivityJSON(content)
        validateRuntime(expected, protected)
        save(context, expected.copy(generatedContent = protected, hostMetadata = hostMetadata ?: expected.hostMetadata, learnedDomains = learnedDomains), expected)
        MobilecoreRuntime.configEvent("modified")
    }

    /** Add several user-approved proxy rules as one validated config transaction. */
    fun savePolicyRules(context: Context, matches: List<String>, current: StagedConfig) {
        require(current.generatedContent.isNotBlank()) { "No saved configuration — import or generate one first." }
        val root = JSONObject(current.generatedContent)
        val route = root.getJSONObject("route")
        val existing = route.optJSONArray("rules") ?: JSONArray()
        val next = JSONArray()
        for (i in 0 until existing.length()) next.put(existing.getJSONObject(i))
        val destination = io.nekohasekai.mobileexperiment.Mobileexperiment.proxyOutbound(current.generatedContent)
            .takeIf { it.isNotBlank() && it != "direct" }
            ?: error("No proxy outbound available")
        val added = current.policy.toMutableList()
        val seen = added.map { it.match to it.action }.toMutableSet()
        // A learned route is temporary, even when it currently sends this host to the same exit.
        val learned = ExperimentRuntime.learned(context, current)
        val explicitRules = if (learned.isEmpty() || !ExperimentRuntime.settings(context, current).learningEnabled) existing else {
            val explicit = io.nekohasekai.mobileexperiment.Mobileexperiment.ownedRulesJSON(
                current.generatedContent, JSONArray(learned).toString(), "[]")
            JSONObject(explicit).getJSONObject("route").getJSONArray("rules")
        }
        val existingMatches = mutableSetOf<String>()
        for (i in 0 until explicitRules.length()) {
            val rule = explicitRules.optJSONObject(i) ?: continue
            val fields = if (rule.has("action")) 3 else 2
            if (rule.length() != fields || rule.optString("action", "route") != "route" || rule.optString("outbound") != destination) continue
            for (kind in listOf("domain", "domain_suffix", "domain_keyword", "ip_cidr")) {
                val values = rule.optJSONArray(kind) ?: continue
                for (j in 0 until values.length()) existingMatches += "$kind:${values.optString(j)}"
            }
        }
        for (raw in matches.map { it.trim() }.filter { it.isNotEmpty() }.distinct()) {
            val info = io.nekohasekai.mobilecore.Mobilecore.normalizePolicyRule(raw, "proxy")
            if (seen.add(info.match to info.action)) added += PolicyRule(info.match, info.action)
            if (existingMatches.add("${info.kind}:${info.value}")) {
                next.put(JSONObject().put(info.kind, JSONArray().put(info.value)).put("action", "route").put("outbound", destination))
            }
        }
        if (added.size == current.policy.size && next.length() == existing.length()) return
        route.put("rules", next)
        val content = root.toString()
        validateRuntime(current, content)
        save(context, current.copy(generatedContent = content, policy = added), current)
        MobilecoreRuntime.configEvent("modified")
    }

    fun savePolicy(context: Context, original: PolicyRule?, replacement: PolicyRule?, current: StagedConfig = load(context)) {
        val root = JSONObject(current.generatedContent)
        val route = root.getJSONObject("route")
        val existing = route.optJSONArray("rules") ?: JSONArray()
        val next = JSONArray()
        fun destination(action: String) = if (action == "direct") "direct"
            else io.nekohasekai.mobileexperiment.Mobileexperiment.proxyOutbound(current.generatedContent)
        if (replacement != null) {
            val info = io.nekohasekai.mobilecore.Mobilecore.normalizePolicyRule(replacement.match, replacement.action)
            val generated = JSONObject().put(info.kind, JSONArray().put(info.value))
            if (info.action == "reject") generated.put("action", "reject")
            else {
                generated.put("action", "route").put("outbound", destination(info.action))
            }
            next.put(generated)
        }
        val originalInfo = original?.let { io.nekohasekai.mobilecore.Mobilecore.normalizePolicyRule(it.match, it.action) }
        for (i in 0 until existing.length()) {
            val rule = existing.getJSONObject(i)
            val matchesOriginal = originalInfo != null && rule.length() == (if (originalInfo.action == "reject") 2 else 3) && rule.optJSONArray(originalInfo.kind)?.let { it.length() == 1 && it.optString(0) == originalInfo.value } == true &&
                (if (originalInfo.action == "reject") rule.optString("action") == "reject" else rule.optString("action") == "route" && rule.optString("outbound") == destination(originalInfo.action))
            if (!matchesOriginal) next.put(rule)
        }
        route.put("rules", next)
        validateRuntime(current, root.toString())
        val policy = current.policy.filterNot { it == original }.let { if (replacement == null) it else it + replacement }
        save(context, current.copy(generatedContent = root.toString(), policy = policy), current)
        MobilecoreRuntime.configEvent("modified")
    }

    data class GroupItem(val tag: String, val type: String, val delay: Int = 0, val testedAt: Long = 0L)
    data class Group(val tag: String, val type: String, val selectable: Boolean, val selected: String, val items: List<GroupItem>)

    fun savedGroups(context: Context): List<Group> = runCatching {
        val root = JSONObject(readGeneratedContent(context) ?: return emptyList())
        val array = root.optJSONArray("outbounds") ?: return emptyList()
        val outbounds = (0 until array.length()).map { array.getJSONObject(it) }
        val types = outbounds.associate { it.optString("tag") to it.optString("type") }
        outbounds.filter { it.optString("type") in listOf("selector", "urltest") }.map { group ->
            val items = group.optJSONArray("outbounds") ?: JSONArray()
            Group(group.getString("tag"), group.getString("type"), group.getString("type") == "selector", group.optString("default"),
                (0 until items.length()).map { GroupItem(items.getString(it), types[items.getString(it)].orEmpty()) })
        }
    }.getOrDefault(emptyList())

    fun packageDirectory(context: Context, revision: String = load(context).packageRevision): File {
        if (revision.isEmpty()) return context.filesDir
        require(java.util.UUID.fromString(revision).toString() == revision) { "Invalid package revision" }
        return File(context.filesDir, "package-revisions/$revision")
    }

    /** Keep comments and line endings whenever the saved file represents the active nodes. */
    internal fun nodeSourceText(context: Context, staged: StagedConfig,
        snapshot: JSONObject? = S3SyncRepository(context).sourceSnapshot(staged)): String? {
        val cached = snapshot?.getJSONObject("bundle")?.getJSONObject("files")?.let { if (it.has("nodes.txt")) it.getString("nodes.txt") else null }
        val legacySnapshot = snapshot != null && !snapshot.has("nodeTextVersion")
        val root = packageDirectory(context, staged.packageRevision).canonicalFile
        val file = File(root, "imports/local/nodes.txt")
        require(file.canonicalFile == file) { "Invalid node source path" }
        val disk = file.takeIf { it.isFile }?.readText()
        val saved = staged.editedNodeText ?: (if (legacySnapshot && cached != null) cached else disk ?: cached)
        if (saved == null) return staged.nodes.takeIf { it.isNotEmpty() }?.joinToString("\n", postfix = "\n")
        return reconcileNodeLines(saved, staged.nodes)
    }

    private val sourceLine = Regex("[^\\r\\n]*(?:\\r\\n|\\r|\\n|$)")

    internal fun editNodeRow(context: Context, current: StagedConfig, index: Int, replacement: String?): StagedConfig {
        val original = current.nodes[index]
        val text = checkNotNull(nodeSourceText(context, current))
        val selected = sourceLine.findAll(text).filter {
            val value = it.value.trim()
            value.isNotEmpty() && !value.startsWith('#')
        }.elementAt(index)
        check(selected.value.trim() == original) { "The node source changed. Reopen the list." }
        val line = replacement?.let { selected.value.replace(original, it) }.orEmpty()
        val nodes = current.nodes.toMutableList().apply {
            if (replacement == null) removeAt(index) else set(index, replacement)
        }
        return current.copy(nodes = nodes, editedNodeText = text.replaceRange(selected.range, line), sourceNeedsGenerate = true)
    }

    /** Keep each unchanged line, including its own terminator. List actions remove,
     * append, or replace a node in place. Comments have no node index. */
    private fun reconcileNodeLines(text: String, nodes: List<String>): String {
        val lines = sourceLine.findAll(text).map { it.value }.filter { it.isNotEmpty() }.toList()
        val original = lines.map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith('#') }
        if (original == nodes) return text
        val wanted = nodes.toHashSet()
        val existing = original.toHashSet()
        val ending = Regex("\\r\\n|\\r|\\n").find(text)?.value ?: "\n"
        var index = 0
        return buildString {
            for (line in lines) {
                val key = line.trim()
                when {
                    key.isEmpty() || key.startsWith('#') -> append(line)
                    key == nodes.getOrNull(index) -> { append(line); index++ }
                    key !in wanted && index < nodes.size && nodes[index] !in existing -> append(line.replace(key, nodes[index++]))
                }
            }
            while (index < nodes.size) {
                if (isNotEmpty() && last() != '\r' && last() != '\n') append(ending)
                append(nodes[index++]).append(ending)
            }
        }
    }

    @Synchronized fun editSource(context: Context, expected: StagedConfig, file: File, edited: String) {
        require(load(context) == expected) { "Configuration changed. Reopen the source editor." }
        val previous = packageDirectory(context, expected.packageRevision)
        val sources = File(previous, "imports/local")
        require(file in sourceFiles(context) && (file.extension == "conf" || file.name == "nodes.txt")) { "Invalid source file" }
        val relative = file.relativeTo(sources).path
        val nodes = if (file.name == "nodes.txt") edited.lineSequence().map(String::trim)
            .filter { it.isNotEmpty() && !it.startsWith("#") }.onEach { io.nekohasekai.mobilecore.Mobilecore.parseShareLink(it) }.toList()
        else expected.nodes.also { io.nekohasekai.mobilecore.Mobilecore.parseConfContentJSON(edited) }
        val updates = mutableMapOf(relative to edited)
        var syncState = expected.s3Snapshot
        if (relative == "nodes.txt") {
            val snapshot = S3SyncRepository(context).sourceSnapshot(expected)
            if (snapshot != null && !snapshot.has("nodeTextVersion")) {
                val prior = file.readText()
                val recovery = File(sources, "nodes-before-sync.txt")
                check(!recovery.exists() || recovery.readText() == prior) { context.getString(com.pidal.sakamoto.R.string.source_recovery_conflict) }
                updates[recovery.name] = prior
                syncState = snapshot.put("nodeTextVersion", 1).toString()
            }
        }
        saveSourceRevision(context, expected, expected.copy(sourceNeedsGenerate = true, s3Snapshot = syncState,
            sourceConfContent = if (expected.sourceConfPath == relative) edited else expected.sourceConfContent, nodes = nodes,
            editedNodeText = if (relative == "nodes.txt") null else expected.editedNodeText), updates)
        MobilecoreRuntime.configEvent("modified")
    }

    @Synchronized internal fun saveSourceRevision(context: Context, expected: StagedConfig, candidate: StagedConfig,
        sources: Map<String, String>, beforeCommit: () -> Unit = {}, archiveRecovery: Boolean = false) {
        check(load(context) == expected) { "Sources changed during save" }
        val previous = packageDirectory(context, expected.packageRevision)
        val id = java.util.UUID.randomUUID().toString()
        val next = packageDirectory(context, id)
        check(next.mkdirs()) { "Unable to stage source edit" }
        var committed = false
        try {
            for (name in listOf("imports", "rules")) {
                val from = File(previous, name)
                if (from.exists()) check(from.copyRecursively(File(next, name)))
            }
            val root = File(next.canonicalFile, "imports/local")
            if (archiveRecovery) {
                val recovery = File(root, "nodes-before-sync.txt")
                check(recovery.isFile && recovery.canonicalFile == recovery) { "Reopen Source Files and select the current recovery copy." }
                val archived = File(root, "nodes-before-sync-archived-$id.txt")
                check(!archived.exists() && recovery.renameTo(archived)) { "Could not archive the recovery copy." }
            }
            for ((path, content) in sources) {
                require(TunnelPackage.validPath(path) && (path.endsWith(".conf") || path in setOf("nodes.txt", "nodes-before-sync.txt"))) { "Invalid source path" }
                val target = File(root, path)
                require(target.canonicalFile == target.absoluteFile) { "Invalid source destination" }
                target.parentFile?.mkdirs()
                target.writeText(content)
            }
            val config = candidate.generatedContent.takeIf { it.isNotBlank() }?.let(::JSONObject)
            val sets = config?.optJSONObject("route")?.optJSONArray("rule_set")
            if (sets != null) for (index in 0 until sets.length()) {
                val row = sets.getJSONObject(index)
                val path = row.optString("path")
                val prefix = File(previous, "rules").absolutePath + File.separator
                if (path.startsWith(prefix)) row.put("path", File(next, "rules/" + path.removePrefix(prefix)).absolutePath)
            }
            save(context, candidate.copy(packageRevision = id, generatedContent = config?.toString().orEmpty()), expected, beforeCommit)
            committed = true
        } finally { if (!committed) next.deleteRecursively() }
        if (expected.packageRevision.isNotEmpty() && expected.packageRevision != expected.confirmedRevision) previous.deleteRecursively()
    }

    fun sourceFiles(context: Context): List<File> {
        val root = File(packageDirectory(context), "imports/local")
        if (!root.isDirectory) return emptyList()
        return root.walkTopDown().filter { it.isFile && it.canonicalPath.startsWith(root.canonicalPath + File.separator) &&
            (it.extension == "conf" || (it.name.startsWith("nodes-before-sync-archived-") && it.extension == "txt") || it.name in listOf("nodes.txt", "nodes-before-sync.txt", "policy.json", "chain.json")) }.sortedBy { it.relativeTo(root).path }.toList()
    }

    /** The config content the tunnel service loads (BoxService start/reload). */
    fun readGeneratedContent(context: Context): String? {
        val content = load(context).runtimeContent
        return content.ifBlank { null }
    }
}
