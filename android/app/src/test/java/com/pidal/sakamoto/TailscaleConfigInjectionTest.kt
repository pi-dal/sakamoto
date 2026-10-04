package com.pidal.sakamoto

import com.pidal.sakamoto.security.TailscaleConfigInjection
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * TailscaleConfigInjection — the auth-key merge into the generated sing-box
 * config at start time. Pure JVM tests (org.json real artifact); the key
 * itself never appears in an assertion message.
 */
class TailscaleConfigInjectionTest {

    private val config = """
    {
      "inbounds": [{"type": "tun"}],
      "endpoints": [
        {"type": "tailscale", "state_directory": "ts", "auth_key": "stale-key"},
        {"type": "wireguard", "name": "wg"}
      ],
      "outbounds": [{"type": "direct"}]
    }
    """.trimIndent()

    @Test
    fun injectsIntoTailscaleEndpointAndOverridesExistingKey() {
        val injected = TailscaleConfigInjection.inject("secret-ts-key", config)
        val root = JSONObject(injected)
        val endpoints = root.getJSONArray("endpoints")
        assertEquals(2, endpoints.length())
        val ts = endpoints.getJSONObject(0)
        assertEquals("tailscale", ts.getString("type"))
        // Stored key wins over whatever was in the config text.
        assertEquals("secret-ts-key", ts.getString("auth_key"))
        // Non-tailscale endpoints are untouched and no key leaks onto them.
        val wg = endpoints.getJSONObject(1)
        assertEquals("wireguard", wg.getString("type"))
        assertTrue(!wg.has("auth_key"))
        // The rest of the config survives.
        assertEquals("tun", root.getJSONArray("inbounds").getJSONObject(0).getString("type"))
        assertEquals(1, root.getJSONArray("outbounds").length())
    }

    @Test
    fun emptyKeyLeavesConfigUntouched() {
        assertEquals(config, TailscaleConfigInjection.inject("", config))
    }

    @Test
    fun configWithoutTailscaleEndpointThrows() {
        val noTs = """
        {"endpoints": [{"type": "wireguard"}]}
        """.trimIndent()
        assertThrows(TailscaleConfigInjection.ConfigInjectionError.NoTailscaleEndpoint::class.java) {
            TailscaleConfigInjection.inject("key", noTs)
        }
        assertThrows(TailscaleConfigInjection.ConfigInjectionError.NoTailscaleEndpoint::class.java) {
            TailscaleConfigInjection.inject("key", "{\"inbounds\": []}")
        }
    }

    @Test
    fun invalidJSONThrowsInvalidConfig() {
        assertThrows(TailscaleConfigInjection.ConfigInjectionError.InvalidConfig::class.java) {
            TailscaleConfigInjection.inject("key", "not json at all")
        }
    }

    @Test
    fun addsKeyWhenEndpointHadNone() {
        val bare = """
        {"endpoints": [{"type": "tailscale"}]}
        """.trimIndent()
        val injected = TailscaleConfigInjection.inject("fresh-key", bare)
        assertEquals("fresh-key", JSONObject(injected).getJSONArray("endpoints").getJSONObject(0).getString("auth_key"))
    }
}
