package com.pidal.sakamoto

import com.pidal.sakamoto.runtime.ConfigEdits
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ConfigEditsTest {
    private val config = """{"outbounds":[{"tag":"MainProxy","type":"selector","outbounds":["entry"],"default":"entry"},{"tag":"entry","type":"direct"},{"tag":"exit","type":"socks","server":"example.org","server_port":1080,"detour":"MainProxy","password":"existing"},{"tag":"bad-path","type":"selector","outbounds":["exit"]}],"route":{"rules":[{"domain":["old.example"],"action":"route","outbound":"exit"},{"action":"reject","protocol":"stun"}],"final":"entry"}}"""

    @Test fun ruleReorderRetainsPredicatesAndActions() {
        val rules = JSONObject(ConfigEdits.moveRule(config, 0, 1)).getJSONObject("route").getJSONArray("rules")
        assertEquals("stun", rules.getJSONObject(0).getString("protocol"))
        assertEquals("old.example", rules.getJSONObject(1).getJSONArray("domain").getString(0))
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.moveRule(config, 0, 99) }
    }

    @Test fun selectorRejectsNonMember() {
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.groupDefault(config, "MainProxy", "exit") }
        val saved = JSONObject(ConfigEdits.groupDefault(config, "MainProxy", "entry"))
        assertEquals("entry", saved.getJSONArray("outbounds").getJSONObject(0).getString("default"))
    }

    @Test fun ruleEditPreservesOrderAndUnrelatedRules() {
        val saved = JSONObject(ConfigEdits.routeRule(config, 0, JSONObject("""{"domain":["new.example"],"action":"route","outbound":"entry"}""")))
        val rules = saved.getJSONObject("route").getJSONArray("rules")
        assertEquals("new.example", rules.getJSONObject(0).getJSONArray("domain").getString(0))
        assertEquals("stun", rules.getJSONObject(1).getString("protocol"))
        assertEquals(1, JSONObject(ConfigEdits.routeRule(config, 0, null)).getJSONObject("route").getJSONArray("rules").length())
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.finalOutbound(config, "missing") }
    }

    @Test fun exitCredentialsAndDetourChangeWithoutTouchingOtherOutbounds() {
        val saved = JSONObject(ConfigEdits.updateExit(config, "exit", "new.example", 443, "MainProxy", "user", "new-secret"))
        val exit = saved.getJSONArray("outbounds").getJSONObject(2)
        assertEquals("new-secret", exit.getString("password"))
        assertEquals(443, exit.getInt("server_port"))
        assertEquals("entry", saved.getJSONArray("outbounds").getJSONObject(1).getString("tag"))
    }

    @Test fun tunnelSettingsPreserveAddressAndOtherInbounds() {
        val source = """{"log":{"level":"info","timestamp":true},"inbounds":[{"type":"tun","address":["172.18.0.1/30"],"auto_route":true},{"type":"mixed","listen_port":2334}]}"""
        val next = JSONObject(ConfigEdits.tunSetting(ConfigEdits.logLevel(source, "debug"), "stack", "gvisor"))
        assertTrue(next.getJSONObject("log").getBoolean("timestamp"))
        assertEquals("debug", next.getJSONObject("log").getString("level"))
        assertEquals("172.18.0.1/30", next.getJSONArray("inbounds").getJSONObject(0).getJSONArray("address").getString(0))
        assertEquals(2334, next.getJSONArray("inbounds").getJSONObject(1).getInt("listen_port"))
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.tunSetting(source, "stack", "invalid") }
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.tunSetting(source, "strict_route", "true") }
    }

    @Test fun rejectsDialLoopAndInvalidPort() {
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.updateExit(config, "exit", "example.org", 1080, "bad-path", "", "") }
        assertThrows(IllegalArgumentException::class.java) { ConfigEdits.updateExit(config, "exit", "example.org", 0, "MainProxy", "", "") }
    }
}
