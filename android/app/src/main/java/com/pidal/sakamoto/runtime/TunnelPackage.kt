package com.pidal.sakamoto.runtime

import java.io.File
import java.io.InputStream
import java.util.zip.ZipInputStream
import org.json.JSONArray
import org.json.JSONObject

/** Transactional private import for official clients without run-as access. */
object TunnelPackage {
    data class Prepared(val state: JSONObject, val generated: String)

    fun extract(input: InputStream, staging: File) {
        var total = 0L
        var count = 0
        val names = mutableSetOf<String>()
        ZipInputStream(input).use { zip ->
            while (true) {
                val entry = zip.nextEntry ?: break
                require(++count <= 512) { "Package contains too many files" }
                val name = entry.name
                require(validPath(name) && names.add(name)) { "Invalid package path" }
                if (entry.isDirectory) { zip.closeEntry(); continue }
                require(name == "sakamoto-config.json" || name == "regional-dns.json" || name.startsWith("rules/") || name.startsWith("imports/local/")) { "Unexpected package file" }
                val file = File(staging, name)
                require(file.canonicalPath.startsWith(staging.canonicalPath + File.separator))
                file.parentFile?.mkdirs()
                file.outputStream().use { output ->
                    val buffer = ByteArray(8192)
                    var size = 0L
                    while (true) {
                        val read = zip.read(buffer)
                        if (read < 0) break
                        size += read; total += read
                        require(size <= 32L * 1024 * 1024 && total <= 64L * 1024 * 1024) { "Package exceeds import size limit" }
                        output.write(buffer, 0, read)
                    }
                }
                zip.closeEntry()
            }
        }
    }
    fun validPath(name: String): Boolean = name.isNotEmpty() && !name.startsWith('/') && !name.contains('\\') &&
        name.removeSuffix("/").split('/').none { it.isEmpty() || it == "." || it == ".." }

    internal class LegacyRouteSource : IllegalArgumentException("Re-export this older package with the current desktop app to verify the domain routes.")

    internal fun validateRouteSource(content: String, version: Int) {
        if (version == 1 || content.isBlank()) return
        val rules = JSONObject(content).optJSONObject("route")?.optJSONArray("rules") ?: return
        // Moving a rule or changing its exit does not establish its source.
        fun hasDomain(entries: JSONArray): Boolean = (0 until entries.length()).any { index ->
            val rule = entries.optJSONObject(index) ?: return@any false
            when (val domain = rule.opt("domain")) {
                is String -> domain.isNotEmpty()
                is JSONArray -> domain.length() > 0
                else -> false
            } || rule.optJSONArray("rules")?.let(::hasDomain) == true
        }
        if (hasDomain(rules)) throw LegacyRouteSource()
    }

    fun prepare(staging: File, destination: File, deviceExclusions: Map<String, List<String>> = emptyMap()): Prepared {
        val state = JSONObject(File(staging, "sakamoto-config.json").readText())
        state.remove("legacyExperimentID")
        state.remove("learningCommit")
        state.remove("learnedDomains") // Only this device can verify a learned route.
        state.remove("editedNodeText")
        state.remove("s3Snapshot") // Synchronization history belongs to the device that adopted it.
        val content = state.getString("generatedContent")
        validateRouteSource(content, state.optInt("routeSourceVersion"))
        val incomingAliases = state.optJSONObject("nodeAliases")?.let { aliases -> aliases.keys().asSequence().associateWith { aliases.getString(it) } }.orEmpty()
        val packaged = ProxyGroupEditing.groups(content).filter(ProxyGroupEditing::followsGeneratedNodes)
        val policy = state.optJSONObject("sourceGroupExclusions") ?: JSONObject().also {
            require(packaged.all { group -> group.members.containsAll(ProxyGroupEditing.automaticCandidates(content, group.tag)) }) {
                "Re-export this legacy package to preserve its automatic group exclusions."
            }
        }
        // Membership order, additional members and probe settings belong to the incoming group.
        val groups = ProxyGroupEditing.adoptingSource(policy, emptyList(), content, incomingAliases).map { group ->
            val local = deviceExclusions[group.tag].orEmpty().map { incomingAliases[it] ?: it }
            group.copy(excludedMembers = (group.excludedMembers.orEmpty() + local).distinct().sorted(),
                additionalMembers = packaged.firstOrNull { it.tag == group.tag }?.members.orEmpty() - ProxyGroupEditing.automaticCandidates(content, group.tag).toSet())
        }
        val adapted = ProxyGroupEditing.applySaved(content, groups)
        state.put("deviceGroupExclusions", JSONObject(deviceExclusions))
        val root = JSONObject(adapted)
        state.put("automaticGroups", ProxyGroupEditing.encodeSaved(groups))
        val sets = root.optJSONObject("route")?.optJSONArray("rule_set")
        if (sets != null) for (i in 0 until sets.length()) {
            val rule = sets.getJSONObject(i)
            if (rule.has("path")) {
                val old = rule.getString("path").replace('\\', '/')
                val relative = if (old.startsWith("rules/")) old else "rules/" + old.substringAfterLast('/')
                require(validPath(relative))
                val file = File(staging, relative)
                require(file.isFile) { "Package is missing a rule set" }
                rule.put("path", File(destination, relative).absolutePath)
            }
        }
        state.put("sourceNeedsGenerate", false)
        return Prepared(state, root.toString())
    }

    @Synchronized fun import(context: android.content.Context, input: InputStream, expected: ConfigRepository.StagedConfig = ConfigRepository.load(context), ensureActive: () -> Unit = {}) {
        ensureActive()
        val current = expected
        check(ConfigRepository.load(context) == current) { "Configuration changed during import. Reopen the package picker." }
        check(MobilecoreRuntime.state.value.serviceState !in setOf("Running", "Starting", "Stopping")) { "Disconnect before importing a tunnel package" }
        val revision = java.util.UUID.randomUUID().toString()
        val staging = ConfigRepository.packageDirectory(context, revision)
        check(staging.mkdirs()) { "Unable to stage the package" }
        var published = false
        try {
            extract(input, staging)
            check(!com.pidal.sakamoto.bg.TunnelBoxService.hasOwner()) { "Disconnect before importing a tunnel package" }
            ExperimentRuntime.quiesce()
            ensureActive()
            val check = prepare(staging, staging, current.localExclusions())
            io.nekohasekai.mobilecore.Mobilecore.validateConfigJSON(check.generated)
            val modeCacheID = java.util.UUID.randomUUID().toString()
            val regionalFile = File(staging, "regional-dns.json")
            val incoming = check.state.optJSONObject("regionalDNS")?.let { obj -> obj.keys().asSequence().associateWith { obj.getString(it) } }.orEmpty()
            if (regionalFile.exists() || incoming.isNotEmpty()) {
                val policy = JSONObject(regionalFile.readText())
                require(incoming.isNotEmpty() && policy.keys().asSequence().associateWith { policy.getString(it) } == incoming) { "Regional DNS package settings are inconsistent" }
            }
            val retainDeviceDNS = current.regionalDNSOrigin == "device" && current.regionalDNS.isNotEmpty()
            if (retainDeviceDNS) {
                require(incoming.isEmpty() || current.regionalDNS == incoming) { "Review the conflicting device and package regional DNS settings before importing" }
            } else if (current.regionalDNSOrigin != "package" && current.regionalDNS.isNotEmpty()) {
                require(current.regionalDNS == incoming) { "Open Regional DNS and save or clear the existing settings before importing a different policy." }
            }
            val regional = if (retainDeviceDNS) current.regionalDNS else incoming
            val origin = if (retainDeviceDNS || (current.regionalDNS.isNotEmpty() && current.regionalDNSOrigin != "package")) current.regionalDNSOrigin else "package"
            val runtime = current.copy(routeSourceVersion = check.state.optInt("routeSourceVersion"), regionalDNS = regional, modeCacheID = modeCacheID, generatedContent = check.generated, proxyChain = ProxyChainSettings.decode(check.state.optJSONObject("proxyChain"))).runtimeContent
            io.nekohasekai.libbox.Libbox.checkConfig(runtime)
            val prepared = check
            prepared.state.put("packageRevision", revision)
            prepared.state.put("confirmedRevision", revision)
            prepared.state.put("modeCacheID", modeCacheID)
            prepared.state.put("scopedExperiment", true)
            prepared.state.put("regionalDNS", JSONObject(regional))
            prepared.state.put("regionalDNSOrigin", origin)
            prepared.state.put("generatedContent", prepared.generated)
            check(File(staging, "sakamoto-config.json").delete())
            File(staging, "imports/local/config.json").apply { parentFile?.mkdirs(); writeText(JSONObject(prepared.generated).toString(2)) }
            ConfigRepository.publish(context, prepared.state) {
                check(ConfigRepository.load(context) == current && !com.pidal.sakamoto.bg.TunnelBoxService.hasOwner()) { "Configuration changed during import. Retry after disconnecting." }
                ensureActive()
                for (old in setOf(current.packageRevision, current.confirmedRevision)) RecoveryFiles.preserve(context, old)
                ensureActive()
            }
            published = true
            MobilecoreRuntime.configEvent("modified")
            for (old in setOf(current.packageRevision, current.confirmedRevision).filter(String::isNotEmpty)) {
                ConfigRepository.packageDirectory(context, old).deleteRecursively()
            }
        } finally { if (!published) staging.deleteRecursively() }
    }
}
