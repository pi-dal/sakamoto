package com.pidal.sakamoto.security

import org.json.JSONArray
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

    fun inject(authKey: String, configJSON: String): String {
        if (authKey.isEmpty()) return configJSON
        val root = try {
            JSONObject(configJSON)
        } catch (e: Exception) {
            throw ConfigInjectionError.InvalidConfig(e.message ?: "not JSON")
        }
        val endpoints = root.optJSONArray("endpoints")
            ?: throw ConfigInjectionError.NoTailscaleEndpoint
        if (endpoints.length() == 0) throw ConfigInjectionError.NoTailscaleEndpoint

        var found = false
        val next = JSONArray()
        for (i in 0 until endpoints.length()) {
            val item = endpoints.optJSONObject(i)
            if (item == null) {
                next.put(endpoints.get(i))
                continue
            }
            if (item.optString("type") == "tailscale") {
                // Stored key wins over any key already in the config text.
                item.put("auth_key", authKey)
                found = true
            }
            next.put(item)
        }
        if (!found) throw ConfigInjectionError.NoTailscaleEndpoint
        root.put("endpoints", next)
        return root.toString()
    }

    sealed class ConfigInjectionError(message: String?) : Exception(message) {
        class InvalidConfig(detail: String) : ConfigInjectionError(detail)
        class NoTailscaleEndpoint : ConfigInjectionError("no tailscale endpoint in config")
    }
}
