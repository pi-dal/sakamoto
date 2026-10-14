package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI

data class ProxyGroupEdit(val tag: String, val type: String = "selector", val members: List<String> = emptyList(), val defaultMember: String = "", val url: String = "https://www.gstatic.com/generate_204", val interval: String = "3m", val excludedMembers: List<String>? = null, val additionalMembers: List<String>? = null)

object ProxyGroupEditing {
    fun followsGeneratedNodes(edit: ProxyGroupEdit): Boolean = edit.type == "urltest" && edit.tag in setOf("RealityAuto", "OthersAuto")
    fun automaticCandidates(content: String, tag: String): List<String> = rows(content).filter { row ->
        row.optString("type") !in setOf("selector", "urltest", "direct", "block", "dns", "socks") && row.has("tag")
    }.filter { row ->
        val reality = row.optJSONObject("tls")?.optJSONObject("reality")?.optBoolean("enabled") == true || row.getString("tag").contains("reality", true)
        (tag == "RealityAuto" && reality) || (tag == "OthersAuto" && !reality)
    }.map { it.getString("tag") }
    fun recording(content: String, edit: ProxyGroupEdit, previous: ProxyGroupEdit? = null): ProxyGroupEdit {
        if (!followsGeneratedNodes(edit)) return edit
        val candidates = automaticCandidates(content, edit.tag)
        val current = groups(content).firstOrNull { it.tag == edit.tag }?.members.orEmpty()
        val removable = (candidates + current + previous?.additionalMembers.orEmpty()).distinct()
        val excluded = ((edit.excludedMembers ?: previous?.excludedMembers).orEmpty() + (removable - edit.members.toSet())).distinct() - edit.members.toSet()
        return edit.copy(excludedMembers = excluded.sorted(), additionalMembers = edit.members - candidates.toSet())
    }
    fun resolving(content: String, edit: ProxyGroupEdit): ProxyGroupEdit {
        if (!followsGeneratedNodes(edit) || edit.excludedMembers == null) return edit
        val candidates = automaticCandidates(content, edit.tag) - edit.excludedMembers.toSet()
        val root = JSONObject(content)
        val endpoints = root.optJSONArray("endpoints") ?: JSONArray()
        val known = (rows(content).map { it.getString("tag") } + (0 until endpoints.length()).map { endpoints.getJSONObject(it).getString("tag") }).toSet()
        val extras = edit.additionalMembers.orEmpty().filter { it in known && it !in edit.excludedMembers }
        val ordered = edit.members.filter { it in candidates || it in extras }
        return edit.copy(members = (ordered + candidates + extras).distinct())
    }
    fun encodeSaved(edits: List<ProxyGroupEdit>): JSONArray = JSONArray(edits.map { edit ->
        JSONObject().put("tag", edit.tag).put("type", edit.type).put("members", JSONArray(edit.members))
            .put("defaultMember", edit.defaultMember).put("url", edit.url).put("interval", edit.interval).apply {
                edit.excludedMembers?.let { put("excludedMembers", JSONArray(it)) }
                edit.additionalMembers?.let { put("additionalMembers", JSONArray(it)) }
            }
    })
    private fun strings(row: JSONObject, key: String): List<String>? = row.optJSONArray(key)?.let { array -> (0 until array.length()).map { array.getString(it) } }
    fun decodeSaved(array: JSONArray?): List<ProxyGroupEdit> = if (array == null) emptyList() else (0 until array.length()).map { index ->
        val row = array.getJSONObject(index)
        ProxyGroupEdit(row.getString("tag"), row.optString("type", "urltest"), strings(row, "members").orEmpty(), row.optString("defaultMember"),
            row.optString("url", "https://www.gstatic.com/generate_204"), row.optString("interval", "3m"), strings(row, "excludedMembers"), strings(row, "additionalMembers"))
    }
    fun renamingSaved(edits: List<ProxyGroupEdit>, names: Map<String, String>): List<ProxyGroupEdit> = edits.map { edit ->
        edit.copy(members = edit.members.map { names[it] ?: it }, excludedMembers = edit.excludedMembers?.map { names[it] ?: it },
            additionalMembers = edit.additionalMembers?.map { names[it] ?: it })
    }
    fun sourceExclusions(content: String, saved: List<ProxyGroupEdit>, aliases: Map<String, String>): JSONObject? {
        val edits = saved.filter(::followsGeneratedNodes)
        if (edits.isEmpty()) return null
        val sources = aliases.entries.associate { it.value to it.key }
        return JSONObject().apply {
            for (savedEdit in edits) {
                val edit = if (savedEdit.excludedMembers == null && content.isNotBlank()) recording(content, savedEdit) else savedEdit
                val excluded = edit.excludedMembers.orEmpty().map { sources[it] ?: it }.distinct().sorted()
                if (excluded.isNotEmpty()) put(edit.tag, JSONArray(excluded))
            }
        }
    }
    fun adoptingSource(source: JSONObject, saved: List<ProxyGroupEdit>, content: String, aliases: Map<String, String>): List<ProxyGroupEdit> {
        val excluded = source.keys().asSequence().associateWith { tag ->
            require(tag in setOf("RealityAuto", "OthersAuto")) { "Unknown automatic group" }
            val values = source.getJSONArray(tag)
            val names = (0 until values.length()).map { index ->
                val name = values.get(index)
                require(name is String && name.isNotBlank() && name.none { it == '\u0000' || it == '\r' || it == '\n' }) { "Invalid group exclusion" }
                name
            }
            require(names.distinct().size == names.size) { "Duplicate group exclusion" }
            names
        }
        val current = if (content.isBlank()) emptyList() else groups(content)
        val names = (excluded.keys + saved.filter(::followsGeneratedNodes).map { it.tag } + current.filter(::followsGeneratedNodes).map { it.tag }).toSet()
        return saved.filterNot { it.tag in names } + names.sorted().map { tag ->
            val edit = saved.firstOrNull { it.tag == tag } ?: current.firstOrNull { it.tag == tag } ?: ProxyGroupEdit(tag, type = "urltest")
            val adopted = edit.copy(type = "urltest", excludedMembers = excluded[tag].orEmpty().map { aliases[it] ?: it }.sorted())
            if (content.isBlank()) adopted else resolving(content, adopted)
        }
    }
    fun forImport(content: String, saved: List<ProxyGroupEdit>, aliases: Map<String, String>): List<ProxyGroupEdit> {
        val edits = saved + groups(content).filter { followsGeneratedNodes(it) && saved.none { edit -> edit.tag == it.tag } }.map { recording(content, it) }
        val sources = aliases.entries.associate { it.value to it.key }
        return renamingSaved(edits, sources)
    }
    fun applySaved(content: String, edits: List<ProxyGroupEdit>): String {
        var result = content
        for (edit in edits) {
            val resolved = resolving(result, edit)
            if (followsGeneratedNodes(edit) && edit.excludedMembers != null && resolved.members.isEmpty() && rows(result).none { it.optString("tag") == edit.tag }) continue
            result = apply(result, resolved)
        }
        return result
    }
    fun rows(content: String): List<JSONObject> {
        val array = JSONObject(content).optJSONArray("outbounds") ?: JSONArray()
        return (0 until array.length()).map { array.getJSONObject(it) }
    }
    fun groups(content: String): List<ProxyGroupEdit> = rows(content).filter { it.optString("type") in setOf("selector", "urltest") }.map { row ->
        val members = row.optJSONArray("outbounds") ?: JSONArray()
        ProxyGroupEdit(row.getString("tag"), row.getString("type"), (0 until members.length()).map { members.getString(it) }, row.optString("default"), row.optString("url", "https://www.gstatic.com/generate_204"), row.optString("interval", "3m"))
    }
    fun apply(content: String, saved: ProxyGroupEdit): String {
        val edit = resolving(content, saved)
        val root = JSONObject(content)
        val rows = rows(content).toMutableList()
        val endpoints = root.optJSONArray("endpoints") ?: JSONArray()
        val endpointRows = (0 until endpoints.length()).map { endpoints.getJSONObject(it) }
        val tags = (rows + endpointRows).map { it.getString("tag") }
        require(tags.distinct().size == tags.size) { "Configuration contains duplicate names." }
        require(edit.tag.isNotBlank() && edit.tag.length <= 128 && !edit.tag.contains('\n') && edit.type in setOf("selector", "urltest")) { "Enter a group name and choose Manual or Automatic." }
        require(edit.members.isNotEmpty() && edit.members.distinct().size == edit.members.size && edit.tag !in edit.members && edit.members.all { it in tags }) { "Choose at least one existing member. A group cannot contain itself or missing nodes." }
        val index = rows.indexOfFirst { it.optString("tag") == edit.tag }
        require(endpointRows.none { it.optString("tag") == edit.tag } && (index < 0 || rows[index].optString("type") in setOf("selector", "urltest"))) { "That name belongs to a node. Choose another group name." }
        val group = if (index < 0) JSONObject().put("tag", edit.tag) else rows[index]
        group.put("type", edit.type).put("outbounds", JSONArray(edit.members))
        if (edit.type == "selector") {
            require(edit.defaultMember.isEmpty() || edit.defaultMember in edit.members) { "The default node must be a selected member." }
            if (edit.defaultMember.isEmpty()) group.remove("default") else group.put("default", edit.defaultMember)
            for (key in listOf("url", "interval", "tolerance", "idle_timeout")) group.remove(key)
        } else {
            val uri = runCatching { URI(edit.url) }.getOrNull()
            require(uri?.scheme in listOf("http", "https") && !uri?.host.isNullOrBlank()) { "Enter an HTTP(S) latency test URL." }
            require(edit.interval.matches(Regex("(?:[0-9]+(?:\\.[0-9]+)?(?:ms|s|m|h))+")) && edit.interval.any { it in '1'..'9' }) { "Enter a positive test interval, such as 30s, 3m or 1h." }
            group.remove("default"); group.put("url", edit.url).put("interval", edit.interval)
        }
        if (index < 0) rows.add(group)
        val graph = (rows + endpointRows).associate { row ->
            val members = row.optJSONArray("outbounds") ?: JSONArray()
            row.getString("tag") to ((0 until members.length()).map { members.getString(it) } + listOfNotNull(row.optString("detour").takeIf { it.isNotEmpty() }))
        }
        val visited = mutableSetOf<String>(); val active = mutableSetOf<String>()
        fun visit(tag: String) {
            require(tag !in active) { "Group membership would create a proxy loop. Remove the circular member." }
            if (!visited.add(tag)) return
            active.add(tag)
            graph[tag].orEmpty().forEach { visit(it) }
            active.remove(tag)
        }
        graph.keys.forEach { visit(it) }
        root.put("outbounds", JSONArray(rows))
        return root.toString()
    }
}
