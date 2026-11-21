package com.pidal.sakamoto.runtime

import android.content.Context
import io.nekohasekai.mobilecore.Mobilecore
import com.pidal.sakamoto.security.S3CredentialStore
import kotlinx.coroutines.suspendCancellableCoroutine
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/** Shared Go engine owns signing, source validation and conflict detection. */
class S3SyncRepository(private val context: Context) {
    private val prefs = context.getSharedPreferences("s3-sync", Context.MODE_PRIVATE)
    private val vault = S3CredentialStore(context)
    // Old installations stored only the credential fields here. Their first save
    // replaces the same encrypted file with the complete connection record.
    private fun connection(): JSONObject {
        val stored = vault.readAuthKey()?.let(::JSONObject) ?: JSONObject()
        if (stored.has("credentials")) return stored
        return JSONObject().put("settings", JSONObject(prefs.getString("settings", "{}") ?: "{}"))
            .put("credentials", stored)
    }

    fun settings(): JSONObject = connection().getJSONObject("settings")
    fun credentials(): JSONObject = connection().getJSONObject("credentials")
    fun save(settings: JSONObject, credentials: JSONObject) {
        Mobilecore.validateS3SettingsJSON(settings.toString())
        vault.storeAuthKey(JSONObject().put("settings", settings).put("credentials", credentials).toString())
    }
    // A downloaded include or an empty-list baseline belongs to one source and destination.
    // Legacy, unowned snapshots stay private. Rebuild the first upload from current sources.
    private fun scope(config: ConfigRepository.StagedConfig, target: JSONObject): String = JSONArray(listOf(
        config.modeCacheID, config.sourceConfIsUrl, config.sourceConfPath, config.sourceConfName,
        target.getString("endpoint"), target.getString("bucket"), target.optString("prefix"),
    )).toString()

    internal fun sourceSnapshot(config: ConfigRepository.StagedConfig, target: JSONObject? = null): JSONObject? {
        val raw = config.s3Snapshot ?: prefs.getString("snapshot", null) ?: return null
        val destination = target ?: settings()
        if (!destination.has("endpoint") || !destination.has("bucket")) return null
        return JSONObject(raw).takeIf { it.optString("scope") == scope(config, destination) }
    }

    private fun addPackageConfs(config: ConfigRepository.StagedConfig, files: JSONObject) {
        val root = File(ConfigRepository.packageDirectory(context, config.packageRevision), "imports/local").canonicalFile
        val visited = mutableSetOf<String>()
        fun visit(path: String, depth: Int, main: Boolean = false) {
            require(depth <= 8 && path.endsWith(".conf") && TunnelPackage.validPath(path) && path.split('/').none { it.startsWith('.') }) { "Invalid relative conf include" }
            if (!visited.add(path)) return
            require(visited.size <= 128) { "Too many conf includes" }
            val name = "conf/$path"
            val file = File(root, path)
            require(file.canonicalFile == file) { "Invalid conf include path" }
            val content = if (main) config.sourceConfContent else {
                require(file.isFile) { "Missing relative conf include. Restore the file or import the complete source package before syncing." }
                file.readText()
            }
            files.put(name, content)
            val includes = JSONObject(Mobilecore.parseConfContentJSON(content)).getJSONArray("includesPending")
            for (i in 0 until includes.length()) {
                val child = includes.getString(i)
                if (child.startsWith("http://", true) || child.startsWith("https://", true)) continue
                require(!child.startsWith('/') && !child.contains("..")) { "Unsafe relative conf include" }
                val relative = File(File(path).parentFile, child).normalize().path
                visit(relative, depth + 1)
            }
        }
        visit(config.sourceConfPath, 0, main = true)
    }

    private fun payload(config: ConfigRepository.StagedConfig, snapshot: JSONObject?): JSONObject {
        val bundle = snapshot?.getJSONObject("bundle") ?: JSONObject()
        val files = bundle.optJSONObject("files") ?: JSONObject()
        ConfigRepository.nodeSourceText(context, config, snapshot)?.let { files.put("nodes.txt", it) }
        if (config.policy.isNotEmpty() || files.has("policy.json")) files.put("policy.json", JSONArray().apply {
            config.policy.forEach { put(JSONObject().put("match", it.match).put("action", it.action)) }
        }.toString())
        if (config.subscriptions.isNotEmpty() || files.has("subscriptions.json")) files.put("subscriptions.json", JSONArray().apply {
            config.subscriptions.forEach { put(JSONObject().put("name", it.first).put("url", it.second).put("format", it.third)) }
        }.toString())
        val groups = ProxyGroupEditing.sourceExclusions(config.generatedContent, config.automaticGroups, config.nodeAliases)
        if (groups != null || files.has("groups.json")) files.put("groups.json", (groups ?: JSONObject()).toString())
        if (config.sourceConfContent.isNotEmpty() && !config.sourceConfIsUrl) {
            if (config.sourceConfPath.isNotEmpty()) {
                addPackageConfs(config, files)
                bundle.put("main_conf", "conf/" + config.sourceConfPath)
            } else {
                // A pasted or scanned source has a localized display label, not a filename.
                val name = config.sourceConfName.takeIf { it.startsWith("conf/") && it.endsWith(".conf") } ?: "conf/imported.conf"
                bundle.put("main_conf", name)
                files.put(name, config.sourceConfContent)
            }
        }
        bundle.put("version", 1).put("files", files)
        return bundle
    }
    suspend fun syncNow(ensureActive: () -> Unit): String {
        ensureActive()
        val staged = ConfigRepository.load(context)
        val record = connection()
        val target = record.getJSONObject("settings")
        val snapshot = sourceSnapshot(staged, target)
        val local = payload(staged, snapshot)
        val nodeFile = File(ConfigRepository.packageDirectory(context, staged.packageRevision), "imports/local/nodes.txt")
        val oldText = nodeFile.takeIf { it.isFile }?.readText()
        val migratingNodes = snapshot != null && !snapshot.has("nodeTextVersion") && oldText != null
        if (migratingNodes) {
            // A GET can introduce a new node body even when the local list has no edits.
            // Reserve the recovery slot before the sync engine can issue a conditional PUT.
            val recovery = File(nodeFile.parentFile, "nodes-before-sync.txt")
            check(!recovery.exists() || recovery.readText() == oldText) { context.getString(com.pidal.sakamoto.R.string.source_recovery_conflict) }
        }
        val raw = suspendCancellableCoroutine<String> { continuation ->
            val request = Mobilecore.newS3SyncRequest()
            continuation.invokeOnCancellation { request.cancel() }
            continuation.resumeWith(runCatching {
                ConfigRepository.watchSources(context, staged, request::cancel).use {
                    request.run(target.toString(), record.getJSONObject("credentials").toString(), local.toString(),
                        snapshot?.getJSONObject("baseline")?.toString().orEmpty())
                }
            })
        }
        ensureActive()
        check(ConfigRepository.load(context) == staged && connection().toString() == record.toString()) { "Local sources or S3 settings changed during sync; retry without overwriting" }
        val response = JSONObject(raw)
        val bundle = response.getJSONObject("bundle")
        val files = bundle.getJSONObject("files")
        val array = response.optJSONArray("downloads") ?: JSONArray()
        val downloads = (0 until array.length()).map { array.getString(it) }.toSet()
        var next = staged
        val sourceUpdates = mutableMapOf<String, String>()
        if ("nodes.txt" in downloads) {
            val text = files.getString("nodes.txt")
            next = next.copy(nodes = text.lineSequence().map(String::trim).filter { it.isNotEmpty() && !it.startsWith("#") }
                .onEach { Mobilecore.parseShareLink(it) }.toList())
            sourceUpdates["nodes.txt"] = text
        }
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
        if ("groups.json" in downloads) {
            val groups = ProxyGroupEditing.adoptingSource(JSONObject(files.getString("groups.json")), next.automaticGroups, next.generatedContent, next.nodeAliases)
            next = next.copy(automaticGroups = groups, sourceNeedsGenerate = true)
        }
        val main = bundle.optString("main_conf")
        val downloadedConf = downloads.any { it.startsWith("conf/") }
        if (!staged.sourceConfIsUrl && main.isNotEmpty() && (downloadedConf || main != local.optString("main_conf"))) {
            // Publish the source tree and its manifest together. The editor and the next
            // sync must see the same main and include bodies, even after process death.
            for (name in files.keys()) if (name.startsWith("conf/")) {
                sourceUpdates[name.removePrefix("conf/")] = files.getString(name)
            }
            val path = main.removePrefix("conf/")
            next = next.copy(sourceConfName = File(path).name, sourceConfPath = path, sourceConfContent = sourceUpdates.getValue(path))
        }
        val sourcesChanged = next != staged || downloadedConf && !staged.sourceConfIsUrl
        if (files.has("nodes.txt")) {
            val syncedText = files.getString("nodes.txt")
            if (oldText != syncedText) {
                if (migratingNodes && oldText != null) sourceUpdates["nodes-before-sync.txt"] = oldText
                sourceUpdates["nodes.txt"] = syncedText
            }
        }
        val adopted = JSONObject().put("nodeTextVersion", 1).put("scope", scope(next, target)).put("bundle", bundle).put("baseline", response.getJSONObject("baseline"))
        val candidate = next.copy(editedNodeText = null, s3Snapshot = adopted.toString(), sourceNeedsGenerate = next.sourceNeedsGenerate || sourcesChanged)
        val beforeCommit = {
            check(connection().toString() == record.toString()) { "S3 settings changed during sync" }
            ensureActive()
        }
        if (sourceUpdates.isEmpty()) ConfigRepository.save(context, candidate, staged, beforeCommit)
        else ConfigRepository.saveSourceRevision(context, staged, candidate, sourceUpdates, beforeCommit)
        if (sourcesChanged) MobilecoreRuntime.configEvent("modified")
        return "Synced ${downloads.size} source files" + if (response.optBoolean("uploaded")) "; uploaded changes" else ""
    }
}
