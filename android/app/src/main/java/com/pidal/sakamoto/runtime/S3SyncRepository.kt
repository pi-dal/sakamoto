package com.pidal.sakamoto.runtime

import android.content.Context
import io.nekohasekai.mobilecore.Mobilecore
import com.pidal.sakamoto.security.S3CredentialStore
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Shared Go engine owns signing, source validation and conflict detection. */
class S3SyncRepository(private val context: Context) {
    private val prefs = context.getSharedPreferences("s3-sync", Context.MODE_PRIVATE)
    private val vault = S3CredentialStore(context)
    fun settings(): JSONObject = JSONObject(prefs.getString("settings", "{}") ?: "{}")
    fun credentials(): JSONObject = JSONObject(vault.readAuthKey() ?: "{}")
    fun save(settings: JSONObject, credentials: JSONObject) {
        Mobilecore.validateS3SettingsJSON(settings.toString())
        vault.storeAuthKey(credentials.toString())
        check(prefs.edit().putString("settings", settings.toString()).commit()) { "Could not save S3 settings" }
    }
    private fun payload(config: ConfigRepository.StagedConfig): JSONObject {
        val bundle = JSONObject(prefs.getString("bundle", "{\"version\":1,\"files\":{}}") ?: "{}")
        val files = bundle.optJSONObject("files") ?: JSONObject()
        if (config.nodes.isNotEmpty() || files.has("nodes.txt")) files.put("nodes.txt", config.nodes.joinToString("\n", postfix = if (config.nodes.isEmpty()) "" else "\n"))
        if (config.policy.isNotEmpty() || files.has("policy.json")) files.put("policy.json", JSONArray().apply {
            config.policy.forEach { put(JSONObject().put("match", it.match).put("action", it.action)) }
        }.toString())
        if (config.subscriptions.isNotEmpty() || files.has("subscriptions.json")) files.put("subscriptions.json", JSONArray().apply {
            config.subscriptions.forEach { put(JSONObject().put("name", it.first).put("url", it.second).put("format", it.third)) }
        }.toString())
        if (config.sourceConfContent.isNotEmpty() && !config.sourceConfIsUrl) {
            val name = "conf/" + File(config.sourceConfName).name
            bundle.put("main_conf", name)
            files.put(name, config.sourceConfContent)
        }
        bundle.put("version", 1).put("files", files)
        return bundle
    }
    fun syncNow(): String {
        val staged = ConfigRepository.load(context)
        val raw = Mobilecore.syncSourcesS3JSON(settings().toString(), credentials().toString(), payload(staged).toString(), prefs.getString("baseline", "") ?: "")
        check(ConfigRepository.load(context) == staged) { "Local sources changed during sync; retry without overwriting" }
        val response = JSONObject(raw)
        val bundle = response.getJSONObject("bundle")
        val files = bundle.getJSONObject("files")
        val array = response.optJSONArray("downloads") ?: JSONArray()
        val downloads = (0 until array.length()).map { array.getString(it) }.toSet()
        var next = staged
        if ("nodes.txt" in downloads) next = next.copy(nodes = files.getString("nodes.txt").lines().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }.onEach { Mobilecore.parseShareLink(it) })
        if ("policy.json" in downloads) {
            val list = JSONArray(files.getString("policy.json"))
            next = next.copy(policy = (0 until list.length()).map { i ->
                val r = list.getJSONObject(i)
                Mobilecore.normalizePolicyRule(r.getString("match"), r.getString("action"))
                ConfigRepository.PolicyRule(r.getString("match"), r.getString("action"))
            })
        }
        if ("subscriptions.json" in downloads) {
            val list = JSONArray(files.getString("subscriptions.json"))
            next = next.copy(subscriptions = (0 until list.length()).map { i ->
                val s = list.getJSONObject(i)
                Triple(s.getString("name"), s.getString("url"), s.optString("format", "auto"))
            })
        }
        val main = bundle.optString("main_conf")
        if (main.isNotEmpty()) {
            val content = files.getString(main)
            Mobilecore.parseConfContentJSON(content)
            next = next.copy(sourceConfName = main, sourceConfIsUrl = false, sourceConfContent = content)
        }
        if (next != staged) {
            ConfigRepository.save(context, next)
            MobilecoreRuntime.configEvent("modified")
        }
        check(prefs.edit().putString("bundle", bundle.toString()).putString("baseline", response.getJSONObject("baseline").toString()).commit()) { "Could not save sync baseline" }
        return "Synced ${downloads.size} source files" + if (response.optBoolean("uploaded")) "; uploaded changes" else ""
    }
}
