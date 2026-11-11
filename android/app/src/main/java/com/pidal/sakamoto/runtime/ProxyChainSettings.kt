package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject

/** Profile-owned chain intent. The saved base keeps its original groups and credentials. */
data class ProxyChainSettings(val enabled: Boolean = false, val upstream: String = "MainProxy", val exit: String = "") {
    fun json(): JSONObject = JSONObject().put("enabled", enabled).put("upstream", upstream).put("exit", exit)

    fun applying(content: String): String {
        if (!enabled) return content
        val root = JSONObject(content)
        val rows = root.getJSONArray("outbounds").let { array -> (0 until array.length()).map { array.getJSONObject(it) } }
        val landing = rows.firstOrNull { it.optString("tag") == exit && exit in ConfigEdits.chainableOutboundTags(content) }
            ?: error("Choose an existing proxy exit node")
        require(upstream != exit && rows.any { it.optString("tag") == upstream && it.optString("type") !in setOf("direct", "block", "dns") }) { "Choose a separate upstream proxy or group" }
        landing.put("detour", upstream)
        val removed = mutableSetOf(exit)
        var changed = true
        while (changed) {
            changed = false
            for (row in rows) {
                val members = row.optJSONArray("outbounds") ?: continue
                val remaining = (0 until members.length()).map { members.getString(it) }.filterNot { it in removed }
                row.put("outbounds", JSONArray(remaining))
                if (remaining.isEmpty()) { if (removed.add(row.getString("tag"))) changed = true }
                else if (row.has("default") && row.optString("default") !in remaining) row.put("default", remaining.first())
            }
        }
        require(upstream !in removed) { "The chain needs an upstream node other than its exit" }
        val kept = rows.filter { it.optString("tag") == exit || it.optString("tag") !in removed }
        val endpoints = root.optJSONArray("endpoints") ?: JSONArray()
        val graph = mutableMapOf<String, List<String>>()
        for (row in kept + (0 until endpoints.length()).map { endpoints.getJSONObject(it) }) {
            val tag = row.optString("tag")
            require(tag.isNotEmpty() && tag !in graph) { "Proxy chain requires unique outbound tags" }
            val members = row.optJSONArray("outbounds") ?: JSONArray()
            graph[tag] = (0 until members.length()).map { members.getString(it) } + listOfNotNull(row.optString("detour").takeIf { it.isNotEmpty() })
        }
        val visiting = mutableSetOf<String>(); val visited = mutableSetOf<String>()
        fun visit(tag: String) {
            require(tag !in visiting) { "This proxy chain creates a routing cycle" }
            if (tag in visited) return
            val dependencies = graph[tag] ?: error("Proxy chain references a missing outbound")
            visiting.add(tag)
            dependencies.forEach { visit(it) }
            visiting.remove(tag); visited.add(tag)
        }
        graph.keys.forEach { visit(it) }
        root.put("outbounds", JSONArray(kept))
        root.optJSONObject("route")?.let { rewriteRoutes(it, setOf("MainProxy", upstream), exit) }
        rewriteDNS(root, setOf("MainProxy", upstream), exit)
        return root.toString()
    }

    companion object {
        fun decode(row: JSONObject?): ProxyChainSettings? = row?.let { ProxyChainSettings(it.optBoolean("enabled"), it.optString("upstream", "MainProxy"), it.optString("exit")) }

        fun imported(content: String): ProxyChainSettings? {
            val root = JSONObject(content)
            val route = root.optJSONObject("route") ?: return null
            val rules = route.optJSONArray("rules") ?: JSONArray()
            val proxy = (0 until rules.length()).map { rules.getJSONObject(it) }.firstOrNull {
                it.optString("clash_mode").equals("global", true) || it.optJSONArray("rule_set")?.toString() == "[\"rs-proxy\"]"
            }?.optString("outbound") ?: route.optString("final")
            val rows = root.optJSONArray("outbounds") ?: return null
            val landing = (0 until rows.length()).map { rows.getJSONObject(it) }.firstOrNull { it.optString("tag") == proxy && proxy in ConfigEdits.chainableOutboundTags(content) } ?: return null
            return landing.optString("detour").takeIf { it.isNotEmpty() }?.let { ProxyChainSettings(true, it, proxy) }
        }

        fun unchained(content: String): String {
            val chain = imported(content) ?: return content
            val root = JSONObject(content)
            val rows = root.getJSONArray("outbounds")
            for (i in 0 until rows.length()) if (rows.getJSONObject(i).optString("tag") == chain.exit) rows.getJSONObject(i).remove("detour")
            root.optJSONObject("route")?.let { rewriteRoutes(it, setOf(chain.exit), chain.upstream) }
            rewriteDNS(root, setOf(chain.exit), chain.upstream)
            return root.toString()
        }

        private fun rewriteRoutes(row: JSONObject, from: Set<String>, to: String) {
            for (key in listOf("outbound", "final")) if (row.optString(key) in from) row.put(key, to)
            row.optJSONArray("rules")?.let { rules -> for (i in 0 until rules.length()) rewriteRoutes(rules.getJSONObject(i), from, to) }
        }

        private fun rewriteDNS(root: JSONObject, from: Set<String>, to: String) {
            val servers = root.optJSONObject("dns")?.optJSONArray("servers") ?: return
            for (i in 0 until servers.length()) if (servers.getJSONObject(i).optString("detour") in from) servers.getJSONObject(i).put("detour", to)
        }
    }
}
