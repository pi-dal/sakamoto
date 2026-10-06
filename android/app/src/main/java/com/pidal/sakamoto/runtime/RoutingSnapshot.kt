package com.pidal.sakamoto.runtime

import org.json.JSONArray
import org.json.JSONObject

/** Safe display projection of the generated policy/exit graph. No credentials. */
object RoutingSnapshot {
    data class Rule(val order: Int, val match: String, val action: String, val outbound: String)
    data class Exit(val tag: String, val type: String, val server: String, val port: Int, val detour: String, val authenticated: Boolean)
    data class Snapshot(val rules: List<Rule>, val exits: List<Exit>, val final: String)

    fun parse(content: String): Snapshot {
        val config = JSONObject(content)
        val route = config.optJSONObject("route") ?: JSONObject()
        val rules = route.optJSONArray("rules") ?: JSONArray()
        val outbounds = config.optJSONArray("outbounds") ?: JSONArray()
        val exits = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }
            .filter { it.optString("type") in listOf("socks", "http") && it.optString("detour").isNotEmpty() }
            .map { Exit(it.optString("tag"), it.optString("type"), it.optString("server"), it.optInt("server_port"), it.optString("detour"), it.optString("username").isNotEmpty() || it.optString("password").isNotEmpty()) }
        return Snapshot((0 until rules.length()).map { index ->
            val rule = rules.getJSONObject(index)
            Rule(index + 1, match(rule), rule.optString("action", "route"), rule.optString("outbound"))
        }, exits, route.optString("final", "—"))
    }

    private fun match(rule: JSONObject): String {
        val predicates = rule.keys().asSequence().filter { it !in setOf("action", "outbound", "server", "timeout") }.map { key ->
            val value = rule.get(key)
            if (key == "rules" && value is JSONArray) {
                (0 until value.length()).joinToString("; ") { match(value.getJSONObject(it)) }
            } else {
                val text = if (value is JSONArray) (0 until value.length()).joinToString(", ") { value.get(it).toString() } else value.toString()
                "$key: $text"
            }
        }.toList()
        return predicates.joinToString(" · ").ifEmpty { "All traffic" }
    }
}
