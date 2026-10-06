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
    )

    private fun file(context: Context): File = File(context.filesDir, FILE_NAME)

    fun load(context: Context): StagedConfig {
        val f = file(context)
        if (!f.exists()) return StagedConfig()
        return try {
            val root = JSONObject(f.readText())
            StagedConfig(
                generatedContent = root.optString("generatedContent"),
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

    fun save(context: Context, config: StagedConfig) {
        val root = JSONObject()
        root.put("generatedContent", config.generatedContent)
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
        root.put("nodes", JSONArray().apply { config.nodes.forEach { put(it) } })
        root.put(
            "subscriptions",
            JSONArray().apply {
                config.subscriptions.forEach {
                    put(JSONObject().put("name", it.first).put("url", it.second).put("format", it.third))
                }
            },
        )
        val tmp = File(context.filesDir, "$FILE_NAME.tmp")
        tmp.writeText(root.toString())
        if (!tmp.renameTo(file(context))) {
            file(context).writeText(root.toString())
            tmp.delete()
        }
    }

    fun saveGeneratedEdit(context: Context, content: String) {
        io.nekohasekai.mobilecore.Mobilecore.validateConfigJSON(content)
        // libbox validates semantic options and outbound references as well.
        io.nekohasekai.libbox.Libbox.checkConfig(content)
        val current = load(context)
        save(context, current.copy(generatedContent = content))
        MobilecoreRuntime.configEvent("modified")
    }

    fun savePolicy(context: Context, original: PolicyRule?, replacement: PolicyRule?) {
        val current = load(context)
        val root = JSONObject(current.generatedContent)
        val route = root.getJSONObject("route")
        val existing = route.optJSONArray("rules") ?: JSONArray()
        val next = JSONArray()
        if (replacement != null) {
            val info = io.nekohasekai.mobilecore.Mobilecore.normalizePolicyRule(replacement.match, replacement.action)
            val generated = JSONObject().put(info.kind, JSONArray().put(info.value))
            if (info.action == "reject") generated.put("action", "reject")
            else {
                val destination = if (info.action == "direct") "direct" else RoutingSnapshot.parse(current.generatedContent).rules.firstOrNull { it.match.contains("rs-proxy") }?.outbound?.takeIf { it.isNotEmpty() }
                    ?: ConfigEdits.outboundTags(current.generatedContent).firstOrNull { it == "MainProxy" }
                    ?: error("No proxy outbound available")
                generated.put("action", "route").put("outbound", destination)
            }
            next.put(generated)
        }
        val originalInfo = original?.let { io.nekohasekai.mobilecore.Mobilecore.normalizePolicyRule(it.match, it.action) }
        for (i in 0 until existing.length()) {
            val rule = existing.getJSONObject(i)
            val matchesOriginal = originalInfo != null && rule.length() == (if (originalInfo.action == "reject") 2 else 3) && rule.optJSONArray(originalInfo.kind)?.let { it.length() == 1 && it.optString(0) == originalInfo.value } == true &&
                (if (originalInfo.action == "reject") rule.optString("action") == "reject" else rule.optString("action") == "route")
            if (!matchesOriginal) next.put(rule)
        }
        route.put("rules", next)
        io.nekohasekai.mobilecore.Mobilecore.validateConfigJSON(root.toString())
        io.nekohasekai.libbox.Libbox.checkConfig(root.toString())
        val policy = current.policy.filterNot { it == original }.let { if (replacement == null) it else it + replacement }
        save(context, current.copy(generatedContent = root.toString(), policy = policy))
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

    fun sourceFiles(context: Context): List<File> {
        val root = File(context.filesDir, "imports/local")
        if (!root.isDirectory) return emptyList()
        return root.walkTopDown().filter { it.isFile && it.canonicalPath.startsWith(root.canonicalPath + File.separator) &&
            (it.extension == "conf" || it.name in listOf("nodes.txt", "policy.json", "chain.json")) }.sortedBy { it.relativeTo(root).path }.toList()
    }

    /** The config content the tunnel service loads (BoxService start/reload). */
    fun readGeneratedContent(context: Context): String? {
        val content = load(context).generatedContent
        return content.ifBlank { null }
    }
}
