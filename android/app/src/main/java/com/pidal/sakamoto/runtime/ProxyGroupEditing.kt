package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI

data class ProxyGroupEdit(val tag: String, val type: String = "selector", val members: List<String> = emptyList(), val defaultMember: String = "", val url: String = "https://www.gstatic.com/generate_204", val interval: String = "3m")

object ProxyGroupEditing {
    fun rows(content: String): List<JSONObject> {
        val array = JSONObject(content).optJSONArray("outbounds") ?: JSONArray()
        return (0 until array.length()).map { array.getJSONObject(it) }
    }
    fun groups(content: String): List<ProxyGroupEdit> = rows(content).filter { it.optString("type") in setOf("selector", "urltest") }.map { row ->
        val members = row.optJSONArray("outbounds") ?: JSONArray()
        ProxyGroupEdit(row.getString("tag"), row.getString("type"), (0 until members.length()).map { members.getString(it) }, row.optString("default"), row.optString("url", "https://www.gstatic.com/generate_204"), row.optString("interval", "3m"))
    }
    fun apply(content: String, edit: ProxyGroupEdit): String {
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
