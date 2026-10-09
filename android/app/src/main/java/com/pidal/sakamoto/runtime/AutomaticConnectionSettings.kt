package com.pidal.sakamoto.runtime

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.net.IDN

/** Device-only automation. It survives source regeneration and is never synced. */
data class AutomaticConnectionSettings(
    val startOnBoot: Boolean = false,
    val domains: List<String> = emptyList(),
    val packages: List<String> = emptyList(),
    val proxyOutbound: String = "",
    val onlySelectedApps: Boolean = false,
) {
    companion object {
        fun load(context: Context): AutomaticConnectionSettings {
            val prefs = context.getSharedPreferences("automatic-connection", Context.MODE_PRIVATE)
            return AutomaticConnectionSettings(
                prefs.getBoolean("startOnBoot", false),
                prefs.getStringSet("domains", emptySet()).orEmpty().sorted(),
                prefs.getStringSet("packages", emptySet()).orEmpty().sorted(),
                prefs.getString("proxyOutbound", "").orEmpty(),
                prefs.getBoolean("onlySelectedApps", false),
            )
        }

        fun normalizeDomains(input: String): List<String> = input.split(Regex("[\\s,]+")).filter { it.isNotEmpty() }.map { token ->
            val url = try { URI(if (token.contains("://")) token else "https://$token") } catch (_: Exception) { throw IllegalArgumentException("Enter a domain or HTTP/HTTPS URL") }
            require(url.scheme.lowercase() in setOf("http", "https") && url.userInfo == null) { "Enter an HTTP/HTTPS URL without credentials" }
            val host = url.host?.lowercase()?.trimEnd('.') ?: throw IllegalArgumentException("Enter a valid domain")
            val ascii = IDN.toASCII(host)
            require(ascii.length <= 253 && ascii.split('.').all { it.isNotEmpty() && it.length <= 63 && it.first() != '-' && it.last() != '-' && it.matches(Regex("[a-z0-9-]+")) }) { "Enter a valid domain" }
            ascii
        }.distinct()
    }

    fun save(context: Context) {
        val previous = load(context)
        require(!onlySelectedApps || packages.isNotEmpty()) { "Select at least one app before restricting VPN access" }
        val normalized = normalizeDomains(domains.joinToString("\n"))
        val base = ConfigRepository.readGeneratedContent(context)
        if (normalized.isNotEmpty() || packages.isNotEmpty()) {
            require(base != null) { "Generate a configuration first" }
            val candidate = copy(domains = normalized).applying(base)
            io.nekohasekai.libbox.Libbox.checkConfig(candidate)
            packages.forEach { context.packageManager.getApplicationInfo(it, 0) }
        }
        if (startOnBoot) {
            require(base != null && !ConfigRepository.load(context).sourceNeedsGenerate) { "Generate and apply the configuration first" }
            require(android.net.VpnService.prepare(context) == null) { "Connect once in Home to authorize the VPN" }
        }
        check(context.getSharedPreferences("automatic-connection", Context.MODE_PRIVATE).edit()
            .putBoolean("startOnBoot", startOnBoot)
            .putStringSet("domains", normalized.toSet())
            .putStringSet("packages", packages.toSet())
            .putString("proxyOutbound", proxyOutbound)
            .putBoolean("onlySelectedApps", onlySelectedApps)
            .commit()) { "Could not save automatic connection" }
        if (previous.domains != normalized || previous.packages != packages || previous.proxyOutbound != proxyOutbound || previous.onlySelectedApps != onlySelectedApps) MobilecoreRuntime.configEvent("modified")
    }

    fun applying(content: String): String {
        if (domains.isEmpty() && packages.isEmpty()) return content
        val root = JSONObject(content)
        val outbounds = root.optJSONArray("outbounds") ?: JSONArray()
        val proxies = (0 until outbounds.length()).map { outbounds.getJSONObject(it) }
            .filter { it.optString("type") !in setOf("direct", "block", "dns") }.map { it.optString("tag") }
        require(proxyOutbound.isNotEmpty() && proxyOutbound in proxies) { "Choose an existing proxy outbound in Automatic connection" }
        val route = root.optJSONObject("route") ?: error("No route configuration")
        val rules = route.optJSONArray("rules") ?: JSONArray()
        val next = JSONArray()
        // DNS interception and sniffing must precede host-based routing.
        var index = 0
        while (index < rules.length() && rules.getJSONObject(index).optString("action") in setOf("sniff", "hijack-dns", "resolve")) {
            next.put(rules.getJSONObject(index++))
        }
        if (domains.isNotEmpty()) next.put(JSONObject().put("domain_suffix", JSONArray(domains)).put("action", "route").put("outbound", proxyOutbound))
        if (packages.isNotEmpty()) next.put(JSONObject().put("package_name", JSONArray(packages)).put("action", "route").put("outbound", proxyOutbound))
        while (index < rules.length()) next.put(rules.getJSONObject(index++))
        route.put("rules", next)
        return root.toString()
    }
}
