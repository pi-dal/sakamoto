package com.pidal.sakamoto.runtime

import java.io.File
import java.io.InputStream
import java.util.zip.ZipInputStream
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
                require(name == "sakamoto-config.json" || name.startsWith("rules/") || name.startsWith("imports/local/")) { "Unexpected package file" }
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

    fun prepare(staging: File, destination: File): Prepared {
        val state = JSONObject(File(staging, "sakamoto-config.json").readText())
        val root = JSONObject(state.getString("generatedContent"))
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

    fun import(context: android.content.Context, input: InputStream) {
        check(MobilecoreRuntime.state.value.serviceState !in setOf("Running", "Starting", "Stopping")) { "Disconnect before importing a tunnel package" }
        val staging = File(context.cacheDir, "tunnel-import-${java.util.UUID.randomUUID()}").apply { mkdirs() }
        val backup = File(context.filesDir, "tunnel-import-backup-${java.util.UUID.randomUUID()}").apply { mkdirs() }
        val moved = mutableListOf<String>()
        try {
            extract(input, staging)
            val check = prepare(staging, staging)
            io.nekohasekai.mobilecore.Mobilecore.validateConfigJSON(check.generated)
            io.nekohasekai.libbox.Libbox.checkConfig(check.generated)
            val prepared = prepare(staging, context.filesDir)
            prepared.state.put("generatedContent", prepared.generated)
            File(staging, "sakamoto-config.json").writeText(prepared.state.toString())
            File(staging, "imports/local/config.json").apply { parentFile?.mkdirs(); writeText(JSONObject(prepared.generated).toString(2)) }
            for (name in listOf("rules", "imports", "sakamoto-config.json")) {
                val incoming = File(staging, name)
                if (!incoming.exists()) continue
                val target = File(context.filesDir, name)
                if (target.exists()) check(target.renameTo(File(backup, name))) { "Unable to preserve previous package" }
                moved.add(name)
                check(incoming.renameTo(target)) { "Unable to install package" }
            }
            MobilecoreRuntime.configEvent("modified")
        } catch (error: Exception) {
            for (name in moved.asReversed()) {
                File(context.filesDir, name).deleteRecursively()
                File(backup, name).takeIf { it.exists() }?.renameTo(File(context.filesDir, name))
            }
            throw error
        } finally { staging.deleteRecursively(); backup.deleteRecursively() }
    }
}
