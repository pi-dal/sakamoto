package com.pidal.sakamoto.ui

import org.json.JSONArray
import org.json.JSONObject

/**
 * Pure rule construction for the policy editor. Builds the next rule JSON
 * from the untouched original so extra predicates and unrelated keys survive;
 * only the edited predicate, the action and the outbound leaf change.
 */
object EditingRules {

    val predicates = listOf(
        "domain", "domain_suffix", "domain_keyword", "ip_cidr", "rule_set",
        "protocol", "clash_mode", "network", "port", "ip_is_private",
    )

    val actions = listOf("route", "reject", "hijack-dns", "sniff", "resolve")

    /** Actions that are valid without any match value in this editor. */
    val noMatchActions = setOf("sniff", "resolve")

    fun entries(raw: String): List<String> = raw.split(',').map { it.trim() }.filter { it.isNotEmpty() }

    fun readValues(rule: JSONObject, predicate: String): String {
        val value = rule.opt(predicate) ?: return ""
        return if (value is JSONArray) (0 until value.length()).joinToString(", ") { value.get(it).toString() }
        else value.toString()
    }

    fun buildRule(original: JSONObject, oldPredicate: String?, predicate: String, rawValues: String, action: String, outbound: String): JSONObject {
        require(predicate in predicates) { "Unknown match type" }
        require(action in actions) { "Unknown action" }
        val entries = entries(rawValues)
        // Existing logical/unsupported predicates remain valid even when this
        // editor has no scalar match slot. Do not demand a fabricated domain.
        val unchangedMatch = (oldPredicate == predicate || oldPredicate == null && !original.has(predicate)) &&
            rawValues == readValues(original, predicate)
        val additionalPredicates = setOf("rules", "inbound", "source_ip_cidr", "source_port", "port_range",
            "source_port_range", "ip_version", "domain_regex", "process_name", "process_path", "package_name")
        val hasOtherMatch = original.keys().asSequence().any {
            (it in predicates || it in additionalPredicates) && it != oldPredicate && it != predicate
        }
        require(entries.isNotEmpty() || action in noMatchActions || unchangedMatch && hasOtherMatch) { "Add at least one match value" }
        val next = JSONObject(original.toString())
        // The editor exposes one predicate slot: switching the match type
        // retires the old key, while intentional extra predicates survive.
        if (!unchangedMatch) {
            if (oldPredicate != null && oldPredicate in predicates && oldPredicate != predicate) next.remove(oldPredicate)
            next.remove(predicate)
        }
        if (!unchangedMatch && entries.isNotEmpty()) {
            when (predicate) {
                "ip_is_private" -> {
                    require(entries.size == 1 && entries[0] in setOf("true", "false")) { "Use true or false" }
                    next.put(predicate, entries[0].toBoolean())
                }
                "clash_mode" -> {
                    require(entries.size == 1) { "Choose exactly one value" }
                    next.put(predicate, entries[0])
                }
                "port" -> next.put(predicate, JSONArray(entries.map { raw ->
                    // All validation failures here are IllegalArgumentException so the
                    // editor's inner catch can route every message to the match field.
                    val port = requireNotNull(raw.toIntOrNull()) { "Enter a port between 1 and 65535" }
                    require(port in 1..65535) { "Enter a port between 1 and 65535" }
                    port
                }))
                "network" -> {
                    require(entries.all { it in setOf("tcp", "udp") }) { "Network must be tcp or udp" }
                    next.put(predicate, JSONArray(entries))
                }
                else -> next.put(predicate, JSONArray(entries))
            }
        }
        if (action != original.optString("action", "route")) next.put("action", action)
        if (action == "route") {
            require(outbound.isNotBlank()) { "Choose an outbound" }
            if (outbound != original.optString("outbound")) next.put("outbound", outbound)
        } else next.remove("outbound")
        return next
    }
}
