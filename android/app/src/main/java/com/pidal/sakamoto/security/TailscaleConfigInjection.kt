package com.pidal.sakamoto.security

import org.json.JSONObject

/**
 * Pure helper that injects the stored auth key into a generated sing-box
 * config's `endpoints[]` entry of type "tailscale" — the Kotlin port of
 * SakamotoKit.TailscaleConfigInjection, kept pure (String → String) so it is
 * unit-testable without Android and without the Keystore.
 *
 * The key travels to the tunnel process inside configContent at start/reload
 * time (same trust boundary as the upstream clients passing profile configs
 * into their tunnel extensions); it is never written to the repository or to
 * logs. Callers must NOT log the return value.
 */
object TailscaleConfigInjection {

    fun withoutAuthKey(configJSON: String): String {
        val root = JSONObject(configJSON)
        val endpoints = root.optJSONArray("endpoints") ?: return configJSON
        var changed = false
        for (i in 0 until endpoints.length()) {
            val endpoint = endpoints.optJSONObject(i) ?: continue
            if (endpoint.optString("type") == "tailscale" && endpoint.has("auth_key")) {
                endpoint.remove("auth_key")
                changed = true
            }
        }
        return if (changed) root.toString() else configJSON
    }

    fun forTunnel(configJSON: String, loadAuthKey: () -> String?): String {
        val root = try {
            JSONObject(configJSON)
        } catch (e: Exception) {
            throw ConfigInjectionError.InvalidConfig(e.message ?: "not JSON")
        }
        val endpoints = root.optJSONArray("endpoints") ?: return configJSON
        val tailscale = (0 until endpoints.length()).mapNotNull(endpoints::optJSONObject)
            .filter { it.optString("type") == "tailscale" }
        if (tailscale.isEmpty()) return configJSON
        val key = loadAuthKey()?.takeIf { it.isNotEmpty() } ?: return configJSON
        tailscale.forEach { it.put("auth_key", key) }
        return root.toString()
    }

    sealed class ConfigInjectionError(message: String?) : Exception(message) {
        class InvalidConfig(detail: String) : ConfigInjectionError(detail)
    }
}
