package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject

/** Pure validated edits of the device's saved config, independent of host files. */
object ConfigEdits {
    fun outboundTags(content: String): List<String> {
        val array = JSONObject(content).optJSONArray("outbounds") ?: JSONArray()
        return (0 until array.length()).map { array.getJSONObject(it).getString("tag") }
    }

    fun logLevel(content: String, level: String): String {
        require(level in setOf("trace", "debug", "info", "warn", "error", "fatal", "panic"))
        val root = JSONObject(content)
        val log = root.optJSONObject("log") ?: JSONObject().also { root.put("log", it) }
        log.put("level", level)
        return root.toString()
    }

    fun tunSetting(content: String, field: String, value: Any): String {
        require(field in setOf("stack", "strict_route"))
        if (field == "stack") require(value in setOf("system", "gvisor", "mixed"))
        else require(value is Boolean)
        val root = JSONObject(content)
        val inbounds = root.getJSONArray("inbounds")
        var found = false
        for (i in 0 until inbounds.length()) {
            val inbound = inbounds.getJSONObject(i)
            if (inbound.optString("type") == "tun") { inbound.put(field, value); found = true }
        }
        require(found) { "No TUN inbound configured" }
        return root.toString()
    }

    fun groupDefault(content: String, groupTag: String, member: String): String {
        val root = JSONObject(content)
        val outbounds = root.getJSONArray("outbounds")
        val group = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }.first { it.optString("tag") == groupTag }
        require(group.optString("type") == "selector") { "Automatic groups choose by latency" }
        val members = group.getJSONArray("outbounds")
        require((0 until members.length()).any { members.getString(it) == member }) { "Node is not a member of this group" }
        group.put("default", member)
        return root.toString()
    }

    fun routeRule(content: String, index: Int, rule: JSONObject?): String {
        val root = JSONObject(content)
        val route = root.getJSONObject("route")
        val current = route.optJSONArray("rules") ?: JSONArray()
        require(index in 0..current.length()) { "Rule no longer exists" }
        if (rule != null) {
            require(rule.optString("action", "route") in setOf("route", "reject", "hijack-dns", "sniff", "resolve", "route-options", "bypass")) { "Unknown route action" }
            if (rule.has("outbound")) require(rule.getString("outbound") in outboundTags(content)) { "Unknown outbound" }
        }
        val next = JSONArray()
        for (i in 0 until current.length()) {
            if (i == index) { if (rule != null) next.put(rule) } else next.put(current.getJSONObject(i))
        }
        if (index == current.length() && rule != null) next.put(rule)
        route.put("rules", next)
        return root.toString()
    }

    fun finalOutbound(content: String, tag: String): String {
        require(tag in outboundTags(content)) { "Unknown outbound" }
        val root = JSONObject(content)
        root.getJSONObject("route").put("final", tag)
        return root.toString()
    }

    fun moveRule(content: String, from: Int, to: Int): String {
        val root = JSONObject(content)
        val route = root.getJSONObject("route")
        val rules = route.getJSONArray("rules")
        require(from in 0 until rules.length() && to in 0 until rules.length())
        val items = (0 until rules.length()).map { rules.getJSONObject(it) }.toMutableList()
        items.add(to, items.removeAt(from))
        route.put("rules", JSONArray(items))
        return root.toString()
    }

    fun validateDetour(content: String, tag: String, detour: String) {
        if (detour.isEmpty()) return
        require(detour != tag && detour in outboundTags(content)) { "Invalid detour" }
        val array = JSONObject(content).getJSONArray("outbounds")
        val outbounds = (0 until array.length()).map { array.getJSONObject(it) }
        fun reaches(current: String, visited: MutableSet<String>): Boolean {
            if (current == tag) return true
            if (!visited.add(current)) return false
            val item = outbounds.firstOrNull { it.optString("tag") == current } ?: return false
            val children = mutableListOf<String>()
            item.optString("detour").takeIf { it.isNotEmpty() }?.let { children.add(it) }
            item.optJSONArray("outbounds")?.let { members -> for (i in 0 until members.length()) children.add(members.getString(i)) }
            return children.any { reaches(it, visited) }
        }
        require(!reaches(detour, mutableSetOf())) { "Detour would create a proxy loop" }
    }

    fun updateExit(content: String, tag: String, server: String, port: Int, detour: String, username: String, password: String): String {
        require(server.isNotBlank() && !server.contains(Regex("\\s"))) { "Enter a server address" }
        require(port in 1..65535) { "Port must be 1–65535" }
        val root = JSONObject(content)
        val array = root.getJSONArray("outbounds")
        val outbounds = (0 until array.length()).map { array.getJSONObject(it) }
        val exit = outbounds.first { it.optString("tag") == tag }
        require(exit.optString("type") in setOf("socks", "http")) { "This outbound is not an exit proxy" }
        validateDetour(content, tag, detour)
        if (detour.isNotEmpty()) exit.put("detour", detour) else exit.remove("detour")
        exit.put("server", server.trim()).put("server_port", port)
        if (username.isEmpty()) exit.remove("username") else exit.put("username", username)
        if (password.isEmpty()) exit.remove("password") else exit.put("password", password)
        return root.toString()
    }
}
