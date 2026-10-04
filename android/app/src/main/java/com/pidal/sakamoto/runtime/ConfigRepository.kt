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

    /** The config content the tunnel service loads (BoxService start/reload). */
    fun readGeneratedContent(context: Context): String? {
        val content = load(context).generatedContent
        return content.ifBlank { null }
    }
}
